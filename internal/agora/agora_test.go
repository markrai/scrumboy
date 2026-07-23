package agora_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"scrumboy/internal/agora"
	"scrumboy/internal/db"
	"scrumboy/internal/httpapi"
	"scrumboy/internal/mcp"
	"scrumboy/internal/migrate"
	"scrumboy/internal/store"
)

var agoraTestBearerTokens sync.Map

func newAgoraTestServer(t *testing.T, mode string, withAgora bool) (*httptest.Server, *sql.DB, func()) {
	t.Helper()
	dir := t.TempDir()
	sqlDB, err := db.Open(filepath.Join(dir, "app.db"), db.Options{
		BusyTimeout: 5000,
		JournalMode: "WAL",
		Synchronous: "FULL",
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := migrate.Apply(context.Background(), sqlDB); err != nil {
		_ = sqlDB.Close()
		t.Fatalf("migrate: %v", err)
	}
	st := store.New(sqlDB, nil)
	maxB := int64(1 << 20)
	mcpH := mcp.New(st, mcp.Options{Mode: mode})
	opts := httpapi.Options{
		MaxRequestBody: maxB,
		ScrumboyMode:   mode,
		MCPHandler:     mcpH,
	}
	if withAgora {
		opts.AgoraHandler = agora.New(mcpH, agora.Options{MaxRequestBytes: maxB})
	}
	srv := httpapi.NewServer(st, opts)
	ts := httptest.NewServer(srv)
	if mode == "full" {
		registerAgoraTestStaticToken(t, ts.URL, st)
	}
	return ts, sqlDB, func() {
		ts.Close()
		_ = sqlDB.Close()
	}
}

func registerAgoraTestStaticToken(t *testing.T, baseURL string, st *store.Store) {
	t.Helper()
	user, err := st.BootstrapUser(context.Background(), "agora@example.com", "Password123!", "Agora Test")
	if err != nil {
		t.Fatalf("bootstrap Agora test user: %v", err)
	}
	_, token, _, err := st.CreateUserAPIToken(context.Background(), user.ID, nil)
	if err != nil {
		t.Fatalf("create Agora test token: %v", err)
	}
	agoraTestBearerTokens.Store(baseURL, token)
	t.Cleanup(func() { agoraTestBearerTokens.Delete(baseURL) })
}

func authorizeAgoraTestRequest(req *http.Request) {
	if req.Header.Get("Authorization") != "" {
		return
	}
	baseURL := (&url.URL{Scheme: req.URL.Scheme, Host: req.URL.Host}).String()
	if token, ok := agoraTestBearerTokens.Load(baseURL); ok {
		req.Header.Set("Authorization", "Bearer "+token.(string))
	}
}

func postRaw(t *testing.T, client *http.Client, targetURL string, contentType string, body []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	authorizeAgoraTestRequest(req)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	return resp
}

func TestAgora_MCPJSONRPCUnchangedWithAgoraWired(t *testing.T) {
	t.Parallel()
	const listBody = `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	const callBody = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"system_getCapabilities","arguments":{}}}`

	ts1, _, c1 := newAgoraTestServer(t, "full", false)
	defer c1()
	ts2, _, c2 := newAgoraTestServer(t, "full", true)
	defer c2()
	cl := &http.Client{}

	for _, tc := range []struct {
		name string
		raw  string
	}{
		{"tools_list", listBody},
		{"tools_call", callBody},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u1 := ts1.URL + "/mcp/rpc"
			u2 := ts2.URL + "/mcp/rpc"
			r1 := postRaw(t, cl, u1, "application/json", []byte(tc.raw))
			b1, _ := io.ReadAll(r1.Body)
			_ = r1.Body.Close()
			r2 := postRaw(t, cl, u2, "application/json", []byte(tc.raw))
			b2, _ := io.ReadAll(r2.Body)
			_ = r2.Body.Close()
			if r1.StatusCode != r2.StatusCode {
				t.Fatalf("status mismatch: %d vs %d", r1.StatusCode, r2.StatusCode)
			}
			if !bytes.Equal(b1, b2) {
				t.Fatalf("mcp/rpc body mismatch without vs with agora: %q vs %q", b1, b2)
			}
		})
	}
}

func TestAgora_DiscoverSuccess(t *testing.T) {
	t.Parallel()
	ts, _, cleanup := newAgoraTestServer(t, "full", true)
	defer cleanup()
	cl := &http.Client{}
	resp := postRaw(t, cl, ts.URL+"/agora/v1/discover", "application/json", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out["ok"] != true {
		t.Fatalf("ok: %v", out["ok"])
	}
	if out["error"] != nil {
		t.Fatalf("error: %v", out["error"])
	}
	res, ok := out["result"].(map[string]any)
	if !ok {
		t.Fatalf("result: %v", out["result"])
	}
	tools, ok := res["tools"].([]any)
	if !ok || len(tools) < 1 {
		t.Fatalf("tools: %v", res["tools"])
	}
	first, ok := tools[0].(map[string]any)
	if !ok {
		t.Fatal("first tool not object")
	}
	if first["name"] == nil {
		t.Fatal("no name on tool")
	}
	if first["inputSchema"] == nil {
		t.Fatal("no inputSchema on tool")
	}
}

func TestAgora_InvokeSuccessStructured(t *testing.T) {
	t.Parallel()
	ts, _, cleanup := newAgoraTestServer(t, "full", true)
	defer cleanup()
	cl := &http.Client{}
	body := []byte(`{"tool":"system_getCapabilities","arguments":{}}`)
	resp := postRaw(t, cl, ts.URL+"/agora/v1/invoke", "application/json", body)
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out["ok"] != true {
		t.Fatalf("ok: %v body=%s", out["ok"], b)
	}
	if out["error"] != nil {
		t.Fatalf("error: %v", out["error"])
	}
	_, ok := out["result"].(map[string]any)
	if !ok {
		t.Fatalf("result: %T %v", out["result"], out["result"])
	}
}

func TestAgora_BearerPassthroughInvoke(t *testing.T) {
	t.Parallel()
	ts, _, cleanup := newAgoraTestServer(t, "full", true)
	defer cleanup()
	cl := &http.Client{}
	body := []byte(`{"tool":"projects_list","arguments":{}}`)
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/agora/v1/invoke", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer sb_invalidtoken_for_apitest_xxx")
	rA, _ := cl.Do(req)
	bA, _ := io.ReadAll(rA.Body)
	_ = rA.Body.Close()

	req2, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp/rpc", bytes.NewReader([]byte(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"projects_list","arguments":{}}}`,
	)))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", "Bearer sb_invalidtoken_for_apitest_xxx")
	rB, _ := cl.Do(req2)
	bB, _ := io.ReadAll(rB.Body)
	_ = rB.Body.Close()

	var aMap map[string]any
	if err := json.Unmarshal(bA, &aMap); err != nil {
		t.Fatalf("adapter body: %v", err)
	}
	if rA.StatusCode != http.StatusUnauthorized || rB.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status adapter=%d mcp=%d, want 401", rA.StatusCode, rB.StatusCode)
	}
	if aMap["ok"] != false {
		t.Fatalf("adapter expected ok false, got %v", aMap["ok"])
	}
	if len(bB) != 0 {
		t.Fatalf("MCP 401 body = %q, want empty", bB)
	}
	ae, _ := aMap["error"].(map[string]any)
	if ae == nil || ae["message"] != "authentication required" {
		t.Fatalf("adapter error shape: %v", aMap)
	}
	if aChallenge, mcpChallenge := rA.Header.Get("WWW-Authenticate"), rB.Header.Get("WWW-Authenticate"); aChallenge != "" || mcpChallenge == "" {
		t.Fatalf("challenge adapter=%q mcp=%q", aChallenge, mcpChallenge)
	}
}

func TestAgora_DoesNotAcceptMCPResourceOAuthToken(t *testing.T) {
	t.Parallel()
	ts, sqlDB, cleanup := newAgoraTestServer(t, "full", true)
	defer cleanup()
	st := store.New(sqlDB, nil)
	user, err := st.GetUserByEmail(context.Background(), "agora@example.com")
	if err != nil {
		t.Fatal(err)
	}
	client, err := st.CreateOAuthClient(context.Background(), "agora-boundary", "Agora Boundary", "http://127.0.0.1/callback")
	if err != nil {
		t.Fatal(err)
	}
	pair, err := st.IssueOAuthTokenPair(context.Background(), client.ID, user.ID, ts.URL+"/mcp/rpc")
	if err != nil {
		t.Fatal(err)
	}

	rpcBody := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	rpcReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp/rpc", bytes.NewReader(rpcBody))
	rpcReq.Header.Set("Content-Type", "application/json")
	rpcReq.Header.Set("Accept", "application/json, text/event-stream")
	rpcReq.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	rpcResp, err := http.DefaultClient.Do(rpcReq)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, rpcResp.Body)
	_ = rpcResp.Body.Close()
	if rpcResp.StatusCode != http.StatusOK {
		t.Fatalf("direct MCP OAuth status = %d, want 200", rpcResp.StatusCode)
	}

	agoraBody := []byte(`{"tool":"projects_list","arguments":{}}`)
	agoraReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/agora/v1/invoke", bytes.NewReader(agoraBody))
	agoraReq.Header.Set("Content-Type", "application/json")
	agoraReq.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	agoraResp, err := http.DefaultClient.Do(agoraReq)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(agoraResp.Body)
	_ = agoraResp.Body.Close()
	if agoraResp.StatusCode != http.StatusUnauthorized || agoraResp.Header.Get("WWW-Authenticate") != "" {
		t.Fatalf("Agora OAuth status=%d challenge=%q body=%s", agoraResp.StatusCode, agoraResp.Header.Get("WWW-Authenticate"), raw)
	}
}

func TestAgora_InvokeArgumentsNonObjectRejected(t *testing.T) {
	t.Parallel()
	sqlDB := setupTestDB(t)
	defer func() { _ = sqlDB.Close() }()
	var mcpCalls int
	mcpH := mcp.New(store.New(sqlDB, nil), mcp.Options{Mode: "full"})
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mcpCalls++
		mcpH.ServeHTTP(w, r)
	})
	h := agora.New(wrapped, agora.Options{MaxRequestBytes: 1 << 20})
	ts := httptest.NewServer(h)
	defer ts.Close()
	cl := &http.Client{}
	cases := []string{
		`{"tool":"system_getCapabilities","arguments":[]}`,
		`{"tool":"system_getCapabilities","arguments":"x"}`,
		`{"tool":"system_getCapabilities","arguments":123}`,
		`{"tool":"system_getCapabilities","arguments":true}`,
	}
	for _, raw := range cases {
		resp := postRaw(t, cl, ts.URL+"/agora/v1/invoke", "application/json", []byte(raw))
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400 for %q, got %d: %s", raw, resp.StatusCode, b)
		}
	}
	if mcpCalls != 0 {
		t.Fatalf("mcp should not be called, calls=%d", mcpCalls)
	}
}

func TestAgora_UnknownAgoraPathJSON404(t *testing.T) {
	t.Parallel()
	ts, _, cleanup := newAgoraTestServer(t, "full", true)
	defer cleanup()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/agora/v1/unknown", nil)
	req.Header.Set("Content-Type", "application/json")
	cl := &http.Client{}
	resp, _ := cl.Do(req)
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %q", resp.StatusCode, b)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content-type: %q", ct)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("body not json: %q", b)
	}
	if out["ok"] != false {
		t.Fatalf("ok: %v", out)
	}
}

func TestAgora_InvokeTopLevelUnknownRejected(t *testing.T) {
	t.Parallel()
	sqlDB := setupTestDB(t)
	defer func() { _ = sqlDB.Close() }()
	var mcpCalls int
	mcpH := mcp.New(store.New(sqlDB, nil), mcp.Options{Mode: "full"})
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mcpCalls++
		mcpH.ServeHTTP(w, r)
	})
	h := agora.New(wrapped, agora.Options{MaxRequestBytes: 1 << 20})
	ts := httptest.NewServer(h)
	defer ts.Close()
	cl := &http.Client{}
	bad := []byte(`{"tool":"system_getCapabilities","arguments":{},"metadata":{"x":1}}`)
	resp := postRaw(t, cl, ts.URL+"/agora/v1/invoke", "application/json", bad)
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", resp.StatusCode, b)
	}
	if mcpCalls != 0 {
		t.Fatalf("mcp should not be called, calls=%d", mcpCalls)
	}
}

func TestAgora_InvokeArgumentsPassThrough(t *testing.T) {
	t.Parallel()
	var lastMCP []byte
	sqlDB := setupTestDB(t)
	defer func() { _ = sqlDB.Close() }()
	st := store.New(sqlDB, nil)
	core := mcp.New(st, mcp.Options{Mode: "full"})
	wrap := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		lastMCP = append([]byte(nil), b...)
		r2 := r.Clone(r.Context())
		r2.Body = io.NopCloser(bytes.NewReader(b))
		r2.ContentLength = int64(len(b))
		r2.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(b)), nil
		}
		core.ServeHTTP(w, r2)
	})
	h := agora.New(wrap, agora.Options{MaxRequestBytes: 1 << 20})
	ts := httptest.NewServer(h)
	defer ts.Close()
	registerAgoraTestStaticToken(t, ts.URL, st)
	cl := &http.Client{}
	invokeBody := `{"tool":"system_getCapabilities","arguments":{"unusedKey":1}}`
	resp := postRaw(t, cl, ts.URL+"/agora/v1/invoke", "application/json", []byte(invokeBody))
	_, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if len(lastMCP) == 0 {
		t.Fatal("mcp not invoked")
	}
	var rpc map[string]any
	if err := json.Unmarshal(lastMCP, &rpc); err != nil {
		t.Fatal(err)
	}
	p, _ := json.Marshal(rpc["params"])
	if !strings.Contains(string(p), "unusedKey") {
		t.Fatalf("arguments not passed through: %s", p)
	}
}

func TestAgora_InvokeToolError(t *testing.T) {
	t.Parallel()
	ts, _, cleanup := newAgoraTestServer(t, "full", true)
	defer cleanup()
	cl := &http.Client{}
	body := []byte(`{"tool":"__no_such_tool__","arguments":{}}`)
	resp := postRaw(t, cl, ts.URL+"/agora/v1/invoke", "application/json", body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out["ok"] != false {
		t.Fatalf("ok: %v", out)
	}
	if out["result"] != nil {
		t.Fatalf("result: %v", out["result"])
	}
	er, _ := out["error"].(map[string]any)
	if er == nil || er["message"] == nil {
		t.Fatalf("error: %v", out)
	}
}

func TestAgora_ProtocolErrorFromMCP(t *testing.T) {
	t.Parallel()
	mcpH := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"invalid request"}}`))
	})
	h := agora.New(mcpH, agora.Options{MaxRequestBytes: 1 << 20})
	ts := httptest.NewServer(h)
	defer ts.Close()
	cl := &http.Client{}
	resp := postRaw(t, cl, ts.URL+"/agora/v1/discover", "application/json", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out["ok"] != false {
		t.Fatalf("ok: %v", out)
	}
	er, _ := out["error"].(map[string]any)
	if er == nil {
		t.Fatalf("error: %v", out)
	}
	if int(er["code"].(float64)) != -32600 {
		t.Fatalf("code: %v", er)
	}
	if out["result"] != nil {
		t.Fatalf("result: %v", out["result"])
	}
}

func TestAgora_JSONRPCErrorDataPassthrough(t *testing.T) {
	t.Parallel()
	mcpH := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"invalid request","data":{"foo":1}}}`))
	})
	h := agora.New(mcpH, agora.Options{MaxRequestBytes: 1 << 20})
	ts := httptest.NewServer(h)
	defer ts.Close()
	cl := &http.Client{}
	resp := postRaw(t, cl, ts.URL+"/agora/v1/discover", "application/json", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	er, _ := out["error"].(map[string]any)
	if er == nil {
		t.Fatal("missing error")
	}
	data, _ := er["data"].(map[string]any)
	if data == nil || data["foo"].(float64) != 1 {
		t.Fatalf("data: %v", er)
	}
}

func TestAgora_InvokeMissingArgumentsKey(t *testing.T) {
	t.Parallel()
	ts, _, cleanup := newAgoraTestServer(t, "full", true)
	defer cleanup()
	cl := &http.Client{}
	resp := postRaw(t, cl, ts.URL+"/agora/v1/invoke", "application/json", []byte(`{"tool":"system_getCapabilities"}`))
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: %d body=%s", resp.StatusCode, b)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	er, _ := out["error"].(map[string]any)
	if er["message"] != "missing arguments" {
		t.Fatalf("error: %v", out)
	}
	if out["result"] != nil {
		t.Fatalf("result: %v", out["result"])
	}
}

func TestAgora_InvokeArgumentsJSONNullNormalizes(t *testing.T) {
	t.Parallel()
	ts, _, cleanup := newAgoraTestServer(t, "full", true)
	defer cleanup()
	cl := &http.Client{}
	resp := postRaw(t, cl, ts.URL+"/agora/v1/invoke", "application/json", []byte(`{"tool":"system_getCapabilities","arguments":null}`))
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out["ok"] != true {
		t.Fatalf("ok: %v", out)
	}
}

func TestAgora_InvokeResultAllowsArrayFromStructuredContent(t *testing.T) {
	t.Parallel()
	mcpH := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[],"structuredContent":[1,2,3],"isError":false}}`))
	})
	h := agora.New(mcpH, agora.Options{MaxRequestBytes: 1 << 20})
	ts := httptest.NewServer(h)
	defer ts.Close()
	cl := &http.Client{}
	resp := postRaw(t, cl, ts.URL+"/agora/v1/invoke", "application/json", []byte(`{"tool":"any.name","arguments":{}}`))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out["ok"] != true {
		t.Fatalf("ok: %v", out)
	}
	arr, ok := out["result"].([]any)
	if !ok || len(arr) != 3 {
		t.Fatalf("want array result, got %T %v", out["result"], out["result"])
	}
}

func TestAgora_DiscoverEnvelopeHasRequiredTopLevelKeys(t *testing.T) {
	t.Parallel()
	ts, _, cleanup := newAgoraTestServer(t, "full", true)
	defer cleanup()
	cl := &http.Client{}
	resp := postRaw(t, cl, ts.URL+"/agora/v1/discover", "application/json", []byte(`{}`))
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"ok", "result", "error"} {
		if _, ok := raw[k]; !ok {
			t.Fatalf("missing key %q", k)
		}
	}
}

func TestAgora_AgoraRouteNotUnderMCP(t *testing.T) {
	t.Parallel()
	ts, _, cleanup := newAgoraTestServer(t, "full", true)
	defer cleanup()
	cl := &http.Client{}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/agora/v1/discover", nil)
	resp, _ := cl.Do(req)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", resp.StatusCode)
	}
}

func TestAgora_AgoraV1RootJSON404(t *testing.T) {
	t.Parallel()
	ts, _, cleanup := newAgoraTestServer(t, "full", true)
	defer cleanup()
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		req, _ := http.NewRequest(method, ts.URL+"/agora/v1", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s /agora/v1: status %d, body %q", method, resp.StatusCode, b)
		}
		ct := resp.Header.Get("Content-Type")
		if !strings.HasPrefix(ct, "application/json") {
			t.Fatalf("%s: content-type %q", method, ct)
		}
		if strings.Contains(string(b), "<!DOCTYPE") || strings.Contains(string(b), "<html") {
			t.Fatalf("%s: got HTML, want JSON", method)
		}
		var out map[string]any
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatalf("%s: %v body=%q", method, err, b)
		}
		if out["ok"] != false {
			t.Fatalf("%s: ok: %v", method, out)
		}
	}
}

func TestAgora_DiscoverWhitespaceBodyOK(t *testing.T) {
	t.Parallel()
	ts, _, cleanup := newAgoraTestServer(t, "full", true)
	defer cleanup()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/agora/v1/discover", bytes.NewBufferString("   \n\t  "))
	req.Header.Set("Content-Type", "application/json")
	authorizeAgoraTestRequest(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, b)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out["ok"] != true {
		t.Fatalf("ok: %v", out)
	}
}

func TestAgora_CookiePassthroughParity(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sqlDB, err := db.Open(filepath.Join(dir, "cookie.db"), db.Options{
		BusyTimeout: 5000, JournalMode: "WAL", Synchronous: "FULL",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate.Apply(context.Background(), sqlDB); err != nil {
		_ = sqlDB.Close()
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	st := store.New(sqlDB, nil)
	user, err := st.BootstrapUser(context.Background(), "cookie@example.com", "Password123!", "Cookie Test")
	if err != nil {
		t.Fatal(err)
	}
	sessionToken, _, err := st.CreateSession(context.Background(), user.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	mcpH := mcp.New(st, mcp.Options{Mode: "full"})
	var seen []string
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Cookie"))
		mcpH.ServeHTTP(w, r)
	})
	srv := httpapi.NewServer(st, httpapi.Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "full",
		MCPHandler:     wrapped,
		AgoraHandler:   agora.New(wrapped, agora.Options{MaxRequestBytes: 1 << 20}),
	})
	ts := httptest.NewServer(srv)
	defer ts.Close()
	cl := &http.Client{}
	want := "scrumboy_session=" + sessionToken
	r1, _ := http.NewRequest(http.MethodPost, ts.URL+"/agora/v1/invoke", bytes.NewBufferString(`{"tool":"system_getCapabilities","arguments":{}}`))
	r1.Header.Set("Content-Type", "application/json")
	r1.Header.Set("Cookie", want)
	resp1, err := cl.Do(r1)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp1.Body)
	_ = resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("Agora cookie status = %d, want 200", resp1.StatusCode)
	}
	r2, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp/rpc", bytes.NewBufferString(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"system_getCapabilities","arguments":{}}}`,
	))
	r2.Header.Set("Content-Type", "application/json")
	r2.Header.Set("Cookie", want)
	resp2, err := cl.Do(r2)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp2.Body)
	_ = resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("MCP cookie status = %d, want 200", resp2.StatusCode)
	}
	if len(seen) != 2 {
		t.Fatalf("expected 2 handler calls, got %d %v", len(seen), seen)
	}
	if seen[0] != want || seen[1] != want {
		t.Fatalf("cookie mismatch: %v", seen)
	}
}

func TestAgora_DiscoverToolsPayloadMatchesMCPToolsList(t *testing.T) {
	t.Parallel()
	ts, _, cleanup := newAgoraTestServer(t, "full", true)
	defer cleanup()
	cl := &http.Client{}
	rpcBody := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	rMCP := postRaw(t, cl, ts.URL+"/mcp/rpc", "application/json", rpcBody)
	bMCP, _ := io.ReadAll(rMCP.Body)
	_ = rMCP.Body.Close()
	rDis := postRaw(t, cl, ts.URL+"/agora/v1/discover", "application/json", nil)
	bDis, _ := io.ReadAll(rDis.Body)
	_ = rDis.Body.Close()
	var wMCP struct {
		Result struct {
			Tools json.RawMessage `json:"tools"`
		} `json:"result"`
	}
	var wDis struct {
		Result struct {
			Tools json.RawMessage `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(bMCP, &wMCP); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(bDis, &wDis); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(wMCP.Result.Tools, wDis.Result.Tools) {
		t.Fatalf("tools JSON mismatch: mcp len %d vs agora len %d", len(wMCP.Result.Tools), len(wDis.Result.Tools))
	}
}

func TestAgora_InvokeEmptyOrWhitespaceBodyMissingTool(t *testing.T) {
	t.Parallel()
	ts, _, cleanup := newAgoraTestServer(t, "full", true)
	defer cleanup()
	cl := &http.Client{}
	for _, body := range []string{"", "   \n\t  "} {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/agora/v1/invoke", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := cl.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("body %q: status %d", body, resp.StatusCode)
		}
		var out map[string]any
		if err := json.Unmarshal(b, &out); err != nil {
			t.Fatal(err)
		}
		if out["ok"] != false {
			t.Fatalf("body %q: ok", body)
		}
		er, _ := out["error"].(map[string]any)
		if er == nil || er["message"] != "missing tool" {
			t.Fatalf("body %q: error: %v body=%s", body, out, b)
		}
	}
}

func TestAgora_StandaloneHandlerNonAgoraPathJSON404(t *testing.T) {
	t.Parallel()
	sqlDB := setupTestDB(t)
	defer func() { _ = sqlDB.Close() }()
	h := agora.New(mcp.New(store.New(sqlDB, nil), mcp.Options{Mode: "full"}), agora.Options{MaxRequestBytes: 1 << 20})
	ts := httptest.NewServer(h)
	defer ts.Close()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/other", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		t.Fatalf("content-type: %s", resp.Header.Get("Content-Type"))
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("not json: %q", b)
	}
	if out["ok"] != false {
		t.Fatalf("out: %v", out)
	}
}

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	sqlDB, err := db.Open(filepath.Join(dir, "spy.db"), db.Options{BusyTimeout: 5000, JournalMode: "WAL", Synchronous: "FULL"})
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate.Apply(context.Background(), sqlDB); err != nil {
		_ = sqlDB.Close()
		t.Fatal(err)
	}
	return sqlDB
}
