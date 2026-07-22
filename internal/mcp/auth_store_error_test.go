package mcp

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"scrumboy/internal/store"
)

type authFaultStore struct {
	storeAPI
	sessionUser  store.User
	sessionErr   error
	apiErr       error
	oauthErr     error
	sessionCalls int
}

func (s *authFaultStore) GetUserBySessionToken(context.Context, string) (store.User, error) {
	s.sessionCalls++
	return s.sessionUser, s.sessionErr
}

func (s *authFaultStore) GetUserByAPIToken(context.Context, string) (store.User, error) {
	return store.User{}, s.apiErr
}

func (s *authFaultStore) GetUserByOAuthAccessToken(context.Context, string, string) (store.User, error) {
	return store.User{}, s.oauthErr
}

func authFaultRequest(t *testing.T, st storeAPI, bearer string, withCookie bool) *httptest.ResponseRecorder {
	t.Helper()
	adapter := New(st, Options{Mode: "full"})
	body := `{"jsonrpc":"2.0","id":1,"method":"ping"}`
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/mcp/rpc", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if withCookie {
		req.AddCookie(&http.Cookie{Name: "scrumboy_session", Value: "session-value"})
	}
	rec := httptest.NewRecorder()
	adapter.ServeHTTP(rec, req)
	return rec
}

func TestJSONRPCAuthenticationStoreFailures(t *testing.T) {
	backendErr := errors.New("authentication store unavailable")

	t.Run("session backend failure", func(t *testing.T) {
		st := &authFaultStore{sessionErr: backendErr}
		rec := authFaultRequest(t, st, "", true)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status=%d, want 500", rec.Code)
		}
		if challenge := rec.Header().Get("WWW-Authenticate"); challenge != "" {
			t.Fatalf("500 challenge=%q, want empty", challenge)
		}
	})

	t.Run("missing session", func(t *testing.T) {
		st := &authFaultStore{sessionErr: store.ErrNotFound}
		rec := authFaultRequest(t, st, "", true)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status=%d, want 401", rec.Code)
		}
		if challenge := rec.Header().Get("WWW-Authenticate"); challenge == "" {
			t.Fatal("missing session must follow the normal unauthenticated challenge path")
		}
	})

	t.Run("static token backend failure", func(t *testing.T) {
		st := &authFaultStore{apiErr: backendErr}
		rec := authFaultRequest(t, st, "static-token", false)
		if rec.Code != http.StatusInternalServerError || rec.Header().Get("WWW-Authenticate") != "" {
			t.Fatalf("status=%d challenge=%q, want 500 without challenge", rec.Code, rec.Header().Get("WWW-Authenticate"))
		}
	})

	t.Run("OAuth token backend failure", func(t *testing.T) {
		st := &authFaultStore{apiErr: store.ErrNotFound, oauthErr: backendErr}
		rec := authFaultRequest(t, st, "oauth-token", false)
		if rec.Code != http.StatusInternalServerError || rec.Header().Get("WWW-Authenticate") != "" {
			t.Fatalf("status=%d challenge=%q, want 500 without challenge", rec.Code, rec.Header().Get("WWW-Authenticate"))
		}
	})

	t.Run("invalid bearer does not reach valid cookie", func(t *testing.T) {
		st := &authFaultStore{
			sessionUser: store.User{ID: 1, Email: "user@example.com", Name: "User"},
			apiErr:      store.ErrNotFound,
			oauthErr:    store.ErrNotFound,
		}
		rec := authFaultRequest(t, st, "invalid-token", true)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status=%d, want 401", rec.Code)
		}
		if st.sessionCalls != 0 {
			t.Fatalf("session lookup calls=%d, want 0", st.sessionCalls)
		}
	})
}
