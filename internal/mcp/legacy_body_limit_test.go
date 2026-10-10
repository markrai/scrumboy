package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"scrumboy/internal/mcp"
)

const legacyMCPBodyLimit = 1 << 20

type countingReader struct {
	reader *bytes.Reader
	read   int
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.read += n
	return n, err
}

func legacyMCPBodyOfSize(t *testing.T, size int) []byte {
	t.Helper()
	const prefix = `{"tool":"system_getCapabilities","input":{"padding":"`
	const suffix = `"}}`
	padding := size - len(prefix) - len(suffix)
	if padding < 0 {
		t.Fatalf("requested legacy MCP body size %d is too small", size)
	}
	return []byte(prefix + strings.Repeat("x", padding) + suffix)
}

func TestLegacyMCPRequestBodyLimitPrecedesAuthentication(t *testing.T) {
	_, st, handler := newPhase6MCPFixture(t)
	ctx := context.Background()
	user, err := st.CreateUser(ctx, "legacy-limit@example.com", "password123", "Legacy Limit")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	sessionToken, _, err := st.CreateSession(ctx, user.ID, time.Hour)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	_, apiToken, _, err := st.CreateUserAPIToken(ctx, user.ID, nil, false)
	if err != nil {
		t.Fatalf("create API token: %v", err)
	}

	exact := legacyMCPBodyOfSize(t, legacyMCPBodyLimit)
	exactRequest := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(exact))
	exactRequest.Header.Set("Content-Type", "application/json")
	exactResponse := httptest.NewRecorder()
	handler.ServeHTTP(exactResponse, exactRequest)
	if exactResponse.Code != http.StatusOK {
		t.Fatalf("exact-limit request returned %d, want 200: %s", exactResponse.Code, exactResponse.Body.String())
	}

	oversized := legacyMCPBodyOfSize(t, 2*legacyMCPBodyLimit)
	knownLengthReader := &countingReader{reader: bytes.NewReader(oversized)}
	knownLengthRequest := httptest.NewRequest(http.MethodPost, "/mcp", knownLengthReader)
	knownLengthRequest.ContentLength = int64(len(oversized))
	knownLengthResponse := httptest.NewRecorder()
	handler.ServeHTTP(knownLengthResponse, knownLengthRequest)
	if knownLengthResponse.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("known oversized request returned %d, want 413: %s", knownLengthResponse.Code, knownLengthResponse.Body.String())
	}
	if knownLengthReader.read != 0 {
		t.Fatalf("known oversized request read %d body bytes, want 0", knownLengthReader.read)
	}

	tests := []struct {
		name      string
		configure func(*http.Request)
	}{
		{name: "unauthenticated", configure: func(*http.Request) {}},
		{name: "session cookie", configure: func(req *http.Request) {
			req.AddCookie(&http.Cookie{Name: "scrumboy_session", Value: sessionToken})
			req.Header.Set("X-Scrumboy", "1")
		}},
		{name: "API bearer token", configure: func(req *http.Request) {
			req.Header.Set("Authorization", "Bearer "+apiToken)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := &countingReader{reader: bytes.NewReader(oversized)}
			req := httptest.NewRequest(http.MethodPost, "/mcp", reader)
			req.Header.Set("Content-Type", "application/json")
			tt.configure(req)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("oversized request returned %d, want 413: %s", rec.Code, rec.Body.String())
			}
			var response struct {
				OK    bool `json:"ok"`
				Error struct {
					Code    string         `json:"code"`
					Message string         `json:"message"`
					Details map[string]any `json:"details"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode error envelope: %v", err)
			}
			if response.OK || response.Error.Code != mcp.CodeValidationError || response.Error.Message != "request body too large" || len(response.Error.Details) != 0 {
				t.Fatalf("unexpected error envelope: %#v", response)
			}
			if reader.read > legacyMCPBodyLimit+1 {
				t.Fatalf("server read %d bytes, want at most %d", reader.read, legacyMCPBodyLimit+1)
			}
			if reader.read >= len(oversized) {
				t.Fatalf("server consumed the entire oversized body (%d bytes)", reader.read)
			}
		})
	}
}
