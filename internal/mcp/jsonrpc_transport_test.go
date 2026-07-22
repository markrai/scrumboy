package mcp_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func rpcHTTP(t *testing.T, client *http.Client, method, target string, body []byte, headers http.Header) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, target, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new RPC request: %v", err)
	}
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("RPC request: %v", err)
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read RPC response: %v", err)
	}
	return resp, raw
}

func standardRPCHeaders() http.Header {
	return http.Header{
		"Content-Type": {"application/json"},
		"Accept":       {"application/json, text/event-stream"},
	}
}

func initializeBody(version string) []byte {
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      "init",
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "transport-test", "version": "1"},
		},
	})
	return body
}

func TestJSONRPCTransportMethodOriginAndCanonicalPath(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()
	cookieClient := newCookieClient(t, ts)
	bootstrapUser(t, cookieClient, ts.URL)

	for _, method := range []string{http.MethodGet, http.MethodDelete, http.MethodPut} {
		resp, body := rpcHTTP(t, cookieClient, method, ts.URL+"/mcp/rpc", nil, nil)
		if resp.StatusCode != http.StatusMethodNotAllowed || resp.Header.Get("Allow") != http.MethodPost || len(body) != 0 {
			t.Errorf("%s status=%d Allow=%q body=%q", method, resp.StatusCode, resp.Header.Get("Allow"), body)
		}
	}

	badOrigin := standardRPCHeaders()
	badOrigin.Set("Origin", "https://attacker.example")
	resp, body := rpcHTTP(t, newStatelessClient(ts), http.MethodPost, ts.URL+"/mcp/rpc", initializeBody("2025-11-25"), badOrigin)
	if resp.StatusCode != http.StatusForbidden || len(body) != 0 || resp.Header.Get("WWW-Authenticate") != "" {
		t.Fatalf("invalid Origin status=%d challenge=%q body=%q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"), body)
	}

	goodOrigin := standardRPCHeaders()
	goodOrigin.Set("Origin", ts.URL)
	resp, body = rpcHTTP(t, cookieClient, http.MethodPost, ts.URL+"/mcp/rpc", initializeBody("2025-11-25"), goodOrigin)
	if resp.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(`"result"`)) {
		t.Fatalf("same-origin request status=%d body=%s", resp.StatusCode, body)
	}

	noRedirect := newStatelessClient(ts)
	noRedirect.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
	resp, body = rpcHTTP(t, noRedirect, http.MethodPost, ts.URL+"/mcp/rpc/", initializeBody("2025-11-25"), standardRPCHeaders())
	if resp.StatusCode != http.StatusPermanentRedirect || resp.Header.Get("Location") != "/mcp/rpc" || len(body) != 0 {
		t.Fatalf("trailing-slash status=%d location=%q body=%q", resp.StatusCode, resp.Header.Get("Location"), body)
	}
}

func TestJSONRPCTransportMediaAndMessageClassification(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()
	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	body := initializeBody("2025-11-25")

	for name, headers := range map[string]http.Header{
		"wrong content type": {"Content-Type": {"text/plain"}, "Accept": {"application/json, text/event-stream"}},
		"wrong charset":      {"Content-Type": {"application/json; charset=iso-8859-1"}, "Accept": {"application/json, text/event-stream"}},
	} {
		t.Run(name, func(t *testing.T) {
			resp, raw := rpcHTTP(t, client, http.MethodPost, ts.URL+"/mcp/rpc", body, headers)
			if resp.StatusCode != http.StatusUnsupportedMediaType || len(raw) != 0 {
				t.Fatalf("status=%d body=%q", resp.StatusCode, raw)
			}
		})
	}
	for name, accept := range map[string]string{"JSON only": "application/json", "SSE only": "text/event-stream", "quality zero": "application/json, text/event-stream;q=0"} {
		t.Run(name, func(t *testing.T) {
			headers := http.Header{"Content-Type": {"application/json"}, "Accept": {accept}}
			resp, raw := rpcHTTP(t, client, http.MethodPost, ts.URL+"/mcp/rpc", body, headers)
			if resp.StatusCode != http.StatusNotAcceptable || len(raw) != 0 {
				t.Fatalf("status=%d body=%q", resp.StatusCode, raw)
			}
		})
	}

	for name, message := range map[string]string{
		"initialized": `{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		"unknown":     `{"jsonrpc":"2.0","method":"notifications/example","params":{}}`,
	} {
		t.Run(name+" notification", func(t *testing.T) {
			resp, raw := rpcHTTP(t, client, http.MethodPost, ts.URL+"/mcp/rpc", []byte(message), standardRPCHeaders())
			if resp.StatusCode != http.StatusAccepted || len(raw) != 0 || resp.Header.Get("Content-Type") != "" {
				t.Fatalf("status=%d content-type=%q body=%q", resp.StatusCode, resp.Header.Get("Content-Type"), raw)
			}
		})
	}

	for name, message := range map[string]string{
		"unsolicited response": `{"jsonrpc":"2.0","id":1,"result":{}}`,
		"batch":                `[{"jsonrpc":"2.0","id":1,"method":"tools/list"}]`,
	} {
		t.Run(name, func(t *testing.T) {
			resp, raw := rpcHTTP(t, client, http.MethodPost, ts.URL+"/mcp/rpc", []byte(message), standardRPCHeaders())
			if resp.StatusCode != http.StatusBadRequest || len(raw) != 0 {
				t.Fatalf("status=%d body=%q", resp.StatusCode, raw)
			}
		})
	}

	oversized := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","padding":"` + strings.Repeat("x", (1<<20)+1) + `"}`)
	resp, raw := rpcHTTP(t, client, http.MethodPost, ts.URL+"/mcp/rpc", oversized, standardRPCHeaders())
	if resp.StatusCode != http.StatusRequestEntityTooLarge || len(raw) != 0 {
		t.Fatalf("oversized status=%d body=%q", resp.StatusCode, raw)
	}

	resp, raw = rpcHTTP(t, client, http.MethodPost, ts.URL+"/mcp/rpc", []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"} {}`), standardRPCHeaders())
	if resp.StatusCode != http.StatusOK || !bytes.Contains(raw, []byte(`"code":-32700`)) {
		t.Fatalf("trailing JSON status=%d body=%s", resp.StatusCode, raw)
	}
}

func TestJSONRPCProtocolVersionNegotiation(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()
	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)

	for requested, want := range map[string]string{
		"2025-03-26": "2025-03-26",
		"2025-06-18": "2025-06-18",
		"2025-11-25": "2025-11-25",
		"2024-11-05": "2025-11-25",
		"future":     "2025-11-25",
	} {
		t.Run(requested, func(t *testing.T) {
			resp, raw := rpcHTTP(t, client, http.MethodPost, ts.URL+"/mcp/rpc", initializeBody(requested), standardRPCHeaders())
			if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/json" {
				t.Fatalf("status=%d content-type=%q body=%s", resp.StatusCode, resp.Header.Get("Content-Type"), raw)
			}
			var out struct {
				Result struct {
					ProtocolVersion string `json:"protocolVersion"`
				} `json:"result"`
			}
			if err := json.Unmarshal(raw, &out); err != nil || out.Result.ProtocolVersion != want {
				t.Fatalf("response=%s err=%v want protocolVersion=%s", raw, err, want)
			}
			if resp.Header.Get("Mcp-Session-Id") != "" {
				t.Fatalf("stateless transport emitted session id %q", resp.Header.Get("Mcp-Session-Id"))
			}
		})
	}

	request := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	for name, values := range map[string][]string{
		"unsupported": {"2024-11-05"},
		"malformed":   {"not-a-version"},
		"duplicate":   {"2025-11-25", "2025-06-18"},
	} {
		t.Run(name, func(t *testing.T) {
			headers := standardRPCHeaders()
			for _, value := range values {
				headers.Add("MCP-Protocol-Version", value)
			}
			resp, raw := rpcHTTP(t, client, http.MethodPost, ts.URL+"/mcp/rpc", request, headers)
			if resp.StatusCode != http.StatusBadRequest || len(raw) != 0 {
				t.Fatalf("status=%d body=%q", resp.StatusCode, raw)
			}
		})
	}

	resp, raw := rpcHTTP(t, client, http.MethodPost, ts.URL+"/mcp/rpc", request, standardRPCHeaders())
	if resp.StatusCode != http.StatusOK || !bytes.Contains(raw, []byte(`"result"`)) {
		t.Fatalf("missing protocol header fallback status=%d body=%s", resp.StatusCode, raw)
	}
}

func TestJSONRPCPreservesLargeNumericID(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()
	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	const id = "9007199254740993123456789"
	request := []byte(`{"jsonrpc":"2.0","id":` + id + `,"method":"tools/list"}`)
	resp, raw := rpcHTTP(t, client, http.MethodPost, ts.URL+"/mcp/rpc", request, standardRPCHeaders())
	if resp.StatusCode != http.StatusOK || !bytes.Contains(raw, []byte(`"id":`+id)) {
		t.Fatalf("status=%d response=%s", resp.StatusCode, raw)
	}
}
