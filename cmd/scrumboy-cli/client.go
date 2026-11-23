package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// maxResponseBytes bounds how much of a response body the client will read.
const maxResponseBytes = 32 << 20

// mcpProtocolVersion is sent on /mcp/rpc requests.
const mcpProtocolVersion = "2025-11-25"

// Client talks to a Scrumboy server (see API.md). It deliberately depends on
// nothing under internal/: the wire protocol is documented JSON, and the CLI
// should keep working across server refactors.
//
// Two transports are supported:
//   - legacy POST /mcp ({tool, input} envelope): static sb_ API tokens.
//   - JSON-RPC POST /mcp/rpc (tools/call): static tokens and OAuth access
//     tokens. OAuth tokens are only accepted here.
type Client struct {
	BaseURL   string
	Token     string
	UserAgent string
	HTTP      *http.Client

	// RPC selects the JSON-RPC transport (required for OAuth tokens).
	RPC bool

	// Expiry, when set, makes the client refresh proactively shortly before it.
	Expiry time.Time
	// Refresh obtains a new access token (OAuth). It is called before a request
	// when Expiry is near, and once after a 401.
	Refresh func(ctx context.Context) (token string, expiry time.Time, err error)

	rpcID atomic.Int64
}

// Result is the successful payload of a tool call.
type Result struct {
	Data json.RawMessage
	Meta json.RawMessage
}

// Decode unmarshals Data into v.
func (r *Result) Decode(v any) error {
	if len(r.Data) == 0 {
		return errors.New("empty response data")
	}
	return json.Unmarshal(r.Data, v)
}

// DecodeMeta unmarshals Meta into v; absent meta is not an error.
func (r *Result) DecodeMeta(v any) error {
	if len(r.Meta) == 0 {
		return nil
	}
	return json.Unmarshal(r.Meta, v)
}

type envelope struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Meta  json.RawMessage `json:"meta"`
	Error *APIError       `json:"error"`
}

// APIError is a structured error returned by the server.
type APIError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details"`
	Status  int            `json:"-"`
}

func (e *APIError) Error() string {
	var b strings.Builder
	if e.Code != "" {
		b.WriteString(e.Code)
		b.WriteString(": ")
	}
	b.WriteString(e.Message)
	if len(e.Details) > 0 {
		if raw, err := json.Marshal(e.Details); err == nil {
			b.WriteString(" ")
			b.Write(raw)
		}
	}
	return b.String()
}

// errUnauthorized marks a rejected credential so a refresh can be attempted.
type errUnauthorized struct{ msg string }

func (e *errUnauthorized) Error() string { return e.msg }

// noRedirectHTTP is the HTTP client for every request that carries a
// credential. It never follows redirects: Go keeps the Authorization header
// when the target has the same hostname, whatever its port or scheme (so also
// https -> http), and re-sends a POST body on 307/308, which would replay a
// tool call (a mutation) or a refresh token at the redirect target.
func noRedirectHTTP() *http.Client {
	return &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// NewClient builds a legacy-transport client for the server at baseURL.
func NewClient(baseURL, token, userAgent string) *Client {
	return &Client{
		BaseURL:   strings.TrimRight(baseURL, "/"),
		Token:     token,
		UserAgent: userAgent,
		HTTP:      noRedirectHTTP(),
	}
}

// Call invokes a tool. A nil input is sent as an empty object, which the
// server requires for tools that decode structured input.
func (c *Client) Call(ctx context.Context, tool string, input any) (*Result, error) {
	if input == nil {
		input = map[string]any{}
	}
	if c.Refresh != nil && !c.Expiry.IsZero() && time.Until(c.Expiry) < time.Minute {
		if err := c.refresh(ctx); err != nil {
			return nil, err
		}
	}
	res, err := c.once(ctx, tool, input)
	var ua *errUnauthorized
	if errors.As(err, &ua) && c.Refresh != nil {
		if rerr := c.refresh(ctx); rerr != nil {
			return nil, fmt.Errorf("%v (and refreshing the session failed: %w)", ua.msg, rerr)
		}
		return c.once(ctx, tool, input)
	}
	return res, err
}

func (c *Client) refresh(ctx context.Context) error {
	tok, exp, err := c.Refresh(ctx)
	if err != nil {
		return err
	}
	c.Token, c.Expiry = tok, exp
	return nil
}

func (c *Client) once(ctx context.Context, tool string, input any) (*Result, error) {
	if c.RPC {
		return c.callRPC(ctx, tool, input)
	}
	return c.callLegacy(ctx, tool, input)
}

func (c *Client) post(ctx context.Context, path string, body any, extra map[string]string) (*http.Response, []byte, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, nil, fmt.Errorf("encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(raw))
	if err != nil {
		return nil, nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("request %s: %w", c.BaseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return nil, nil, fmt.Errorf("%s answered with a redirect (HTTP %d to %s); credentials are never sent to a redirect target, so pass the server's final URL with --server",
			c.BaseURL, resp.StatusCode, safeText(resp.Header.Get("Location")))
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, nil, fmt.Errorf("read response: %w", err)
	}
	return resp, b, nil
}

func (c *Client) callLegacy(ctx context.Context, tool string, input any) (*Result, error) {
	resp, raw, err := c.post(ctx, "/mcp", struct {
		Tool  string `json:"tool"`
		Input any    `json:"input"`
	}{tool, input}, nil)
	if err != nil {
		return nil, err
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		// Proxies, auth gateways and wrong URLs answer with HTML or nothing.
		return nil, fmt.Errorf("unexpected response from %s (HTTP %d): %s", c.BaseURL, resp.StatusCode, snippet(raw))
	}
	if !env.OK {
		if env.Error == nil {
			return nil, fmt.Errorf("server reported failure without an error (HTTP %d)", resp.StatusCode)
		}
		env.Error.Status = resp.StatusCode
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, &errUnauthorized{msg: env.Error.Error()}
		}
		return nil, env.Error
	}
	return &Result{Data: env.Data, Meta: env.Meta}, nil
}

type rpcResponse struct {
	Result *struct {
		Content           []struct{ Type, Text string } `json:"content"`
		StructuredContent json.RawMessage               `json:"structuredContent"`
		IsError           bool                          `json:"isError"`
	} `json:"result"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// callRPC invokes a tool over JSON-RPC tools/call on /mcp/rpc. The tool's
// structuredContent is the same payload the legacy transport returns as
// `data`; the handful of tools with pagination metadata copy it beside the
// data fields, so the same object serves as both Data and Meta.
func (c *Client) callRPC(ctx context.Context, tool string, input any) (*Result, error) {
	resp, raw, err := c.post(ctx, "/mcp/rpc", map[string]any{
		"jsonrpc": "2.0",
		"id":      c.rpcID.Add(1),
		"method":  "tools/call",
		"params":  map[string]any{"name": tool, "arguments": input},
	}, map[string]string{
		"Accept":               "application/json, text/event-stream",
		"MCP-Protocol-Version": mcpProtocolVersion,
	})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, &errUnauthorized{msg: "AUTH_REQUIRED: the server rejected the credentials (HTTP 401)"}
	}
	var rr rpcResponse
	if err := json.Unmarshal(raw, &rr); err != nil || (rr.Result == nil && rr.Error == nil) {
		return nil, fmt.Errorf("unexpected response from %s (HTTP %d): %s", c.BaseURL, resp.StatusCode, snippet(raw))
	}
	if rr.Error != nil {
		return nil, &APIError{Code: fmt.Sprintf("RPC_%d", rr.Error.Code), Message: rr.Error.Message, Status: resp.StatusCode}
	}
	sc := rr.Result.StructuredContent
	if len(sc) == 0 && len(rr.Result.Content) > 0 {
		sc = json.RawMessage(rr.Result.Content[0].Text)
	}
	if rr.Result.IsError {
		ae := &APIError{Status: resp.StatusCode}
		if json.Unmarshal(sc, ae) != nil || ae.Message == "" {
			ae.Message = snippet([]byte(firstText(rr)))
		}
		return nil, ae
	}
	if !json.Valid(sc) {
		return nil, fmt.Errorf("tool %s returned no structured content", tool)
	}
	return &Result{Data: sc, Meta: sc}, nil
}

func firstText(r rpcResponse) string {
	if r.Result != nil && len(r.Result.Content) > 0 {
		return r.Result.Content[0].Text
	}
	return ""
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if s == "" {
		return "(empty body)"
	}
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}

// Raw sends one request to a bearer-authenticated /mcp/assets/* route (inline
// image download and upload, which the JSON-RPC tool transport cannot carry).
// It uses the same credential, redirect policy and refresh-on-401 as Call. A
// non-2xx answer is returned as an *APIError.
func (c *Client) Raw(ctx context.Context, method, path, contentType string, body []byte) (http.Header, []byte, error) {
	if c.Refresh != nil && !c.Expiry.IsZero() && time.Until(c.Expiry) < time.Minute {
		if err := c.refresh(ctx); err != nil {
			return nil, nil, err
		}
	}
	hdr, data, err := c.rawOnce(ctx, method, path, contentType, body)
	var ua *errUnauthorized
	if errors.As(err, &ua) && c.Refresh != nil {
		if rerr := c.refresh(ctx); rerr != nil {
			return nil, nil, fmt.Errorf("%v (and refreshing the session failed: %w)", ua.msg, rerr)
		}
		return c.rawOnce(ctx, method, path, contentType, body)
	}
	return hdr, data, err
}

func (c *Client) rawOnce(ctx context.Context, method, path, contentType string, body []byte) (http.Header, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, nil, fmt.Errorf("build request: %w", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	// Image transfers can outlast the 30s tool-call timeout on a slow link.
	hc := *c.HTTP
	hc.Timeout = 5 * time.Minute
	resp, err := hc.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("request %s: %w", c.BaseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return nil, nil, fmt.Errorf("%s answered with a redirect (HTTP %d to %s); credentials are never sent to a redirect target, so pass the server's final URL with --server",
			c.BaseURL, resp.StatusCode, safeText(resp.Header.Get("Location")))
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp.Header, b, nil
	}
	var env struct {
		Error *APIError `json:"error"`
	}
	if json.Unmarshal(b, &env) != nil || env.Error == nil {
		return nil, nil, fmt.Errorf("unexpected response from %s (HTTP %d): %s", c.BaseURL, resp.StatusCode, snippet(b))
	}
	env.Error.Status = resp.StatusCode
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, nil, &errUnauthorized{msg: env.Error.Error()}
	}
	return nil, nil, env.Error
}
