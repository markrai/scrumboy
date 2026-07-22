package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"scrumboy/internal/store"
)

func rawJSONRPC(t *testing.T, client *http.Client, baseURL, bearer string) (*http.Response, []byte) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-11-25",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "auth-test", "version": "1"},
		},
	})
	if err != nil {
		t.Fatalf("marshal JSON-RPC request: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/mcp/rpc", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new JSON-RPC request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do JSON-RPC request: %v", err)
	}
	raw, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read JSON-RPC response: %v", err)
	}
	return resp, raw
}

func TestJSONRPCAuthenticationChallenge(t *testing.T) {
	ts, _, cleanup := newTestServer(t, "full")
	defer cleanup()

	for name, bearer := range map[string]string{"missing": "", "invalid bearer": "not-a-token"} {
		t.Run(name, func(t *testing.T) {
			resp, body := rawJSONRPC(t, newStatelessClient(ts), ts.URL, bearer)
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", resp.StatusCode)
			}
			want := `Bearer resource_metadata="` + ts.URL + `/.well-known/oauth-protected-resource/mcp/rpc"`
			if bearer != "" {
				want = `Bearer error="invalid_token", resource_metadata="` + ts.URL + `/.well-known/oauth-protected-resource/mcp/rpc"`
			}
			if got := resp.Header.Get("WWW-Authenticate"); got != want {
				t.Fatalf("WWW-Authenticate = %q, want %q", got, want)
			}
			if len(body) != 0 {
				t.Fatalf("401 body = %q, want empty", body)
			}
			if got := resp.Header.Get("Content-Type"); got != "" {
				t.Fatalf("401 Content-Type = %q, want empty", got)
			}
		})
	}
}

func TestJSONRPCAuthenticationMethods(t *testing.T) {
	ts, sqlDB, cleanup := newTestServer(t, "full")
	defer cleanup()
	cookieClient := newCookieClient(t, ts)
	bootstrapUser(t, cookieClient, ts.URL)

	t.Run("cookie", func(t *testing.T) {
		resp, body := rawJSONRPC(t, cookieClient, ts.URL, "")
		if resp.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(`"result"`)) {
			t.Fatalf("cookie response status=%d body=%s", resp.StatusCode, body)
		}
	})

	st := store.New(sqlDB, nil)
	user, err := st.GetUserByEmail(context.Background(), "owner@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	_, token, _, err := st.CreateUserAPIToken(context.Background(), user.ID, nil)
	if err != nil {
		t.Fatalf("CreateUserAPIToken: %v", err)
	}
	t.Run("static bearer", func(t *testing.T) {
		resp, body := rawJSONRPC(t, newStatelessClient(ts), ts.URL, token)
		if resp.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(`"result"`)) {
			t.Fatalf("static bearer response status=%d body=%s", resp.StatusCode, body)
		}
	})

	oauthClient, err := st.CreateOAuthClient(context.Background(), "wrong-resource-client", "Wrong Resource", "http://127.0.0.1/callback")
	if err != nil {
		t.Fatalf("CreateOAuthClient: %v", err)
	}
	wrongResourcePair, err := st.IssueOAuthTokenPair(context.Background(), oauthClient.ID, user.ID, "https://other.example/mcp/rpc")
	if err != nil {
		t.Fatalf("IssueOAuthTokenPair: %v", err)
	}
	t.Run("wrong-resource OAuth bearer", func(t *testing.T) {
		resp, body := rawJSONRPC(t, newStatelessClient(ts), ts.URL, wrongResourcePair.AccessToken)
		if resp.StatusCode != http.StatusUnauthorized || len(body) != 0 {
			t.Fatalf("status=%d body=%q, want empty 401", resp.StatusCode, body)
		}
		if got := resp.Header.Get("WWW-Authenticate"); got != `Bearer error="invalid_token", resource_metadata="`+ts.URL+`/.well-known/oauth-protected-resource/mcp/rpc"` {
			t.Fatalf("WWW-Authenticate = %q", got)
		}
	})

	t.Run("invalid bearer does not fall back to cookie", func(t *testing.T) {
		resp, body := rawJSONRPC(t, cookieClient, ts.URL, "invalid-token")
		if resp.StatusCode != http.StatusUnauthorized || len(body) != 0 {
			t.Fatalf("status=%d body=%q, want empty 401", resp.StatusCode, body)
		}
		if got := resp.Header.Get("WWW-Authenticate"); got != `Bearer error="invalid_token", resource_metadata="`+ts.URL+`/.well-known/oauth-protected-resource/mcp/rpc"` {
			t.Fatalf("WWW-Authenticate = %q", got)
		}
	})
}
