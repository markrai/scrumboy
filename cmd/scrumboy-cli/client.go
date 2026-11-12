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
	"time"
)

// maxResponseBytes bounds how much of a response body the client will read.
const maxResponseBytes = 32 << 20

// Client talks to a Scrumboy server over the legacy POST /mcp tool interface
// (see API.md). It deliberately depends on nothing under internal/: the wire
// protocol is documented JSON, and the CLI should keep working across server
// refactors.
type Client struct {
	BaseURL   string
	Token     string
	UserAgent string
	HTTP      *http.Client
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

// NewClient builds a client for the server at baseURL.
func NewClient(baseURL, token, userAgent string) *Client {
	return &Client{
		BaseURL:   strings.TrimRight(baseURL, "/"),
		Token:     token,
		UserAgent: userAgent,
		HTTP:      &http.Client{Timeout: 30 * time.Second},
	}
}

// Call invokes a tool. A nil input is sent as an empty object, which the
// server requires for tools that decode structured input.
func (c *Client) Call(ctx context.Context, tool string, input any) (*Result, error) {
	if input == nil {
		input = map[string]any{}
	}
	body, err := json.Marshal(struct {
		Tool  string `json:"tool"`
		Input any    `json:"input"`
	}{tool, input})
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/mcp", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", c.BaseURL, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
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
		return nil, env.Error
	}
	return &Result{Data: env.Data, Meta: env.Meta}, nil
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
