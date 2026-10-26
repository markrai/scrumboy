package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scrumboy/internal/auth/tokens"
	"scrumboy/internal/db"
	"scrumboy/internal/migrate"
	"scrumboy/internal/oidc"
	"scrumboy/internal/store"
)

type adminPasswordResetStore struct {
	*store.Store
	getUserCalls     map[int64]int
	failGetUserAfter map[int64]int
	failGetUserErr   error
	hashErr          error
	hashCalls        int
	updatePasswordN  int
	resetPasswordN   int
}

func (s *adminPasswordResetStore) GetUser(ctx context.Context, userID int64) (store.User, error) {
	if s.getUserCalls == nil {
		s.getUserCalls = map[int64]int{}
	}
	s.getUserCalls[userID]++
	if s.failGetUserErr != nil {
		if after, ok := s.failGetUserAfter[userID]; ok && s.getUserCalls[userID] > after {
			return store.User{}, s.failGetUserErr
		}
	}
	return s.Store.GetUser(ctx, userID)
}

func (s *adminPasswordResetStore) GetUserPasswordHash(ctx context.Context, userID int64) (string, error) {
	s.hashCalls++
	if s.hashErr != nil {
		return "", s.hashErr
	}
	return s.Store.GetUserPasswordHash(ctx, userID)
}

func (s *adminPasswordResetStore) UpdateUserPassword(ctx context.Context, userID int64, newPassword string) error {
	s.updatePasswordN++
	return s.Store.UpdateUserPassword(ctx, userID, newPassword)
}

func (s *adminPasswordResetStore) ResetLocalPassword(ctx context.Context, userID int64, expectedHash, password string) error {
	s.resetPasswordN++
	return s.Store.ResetLocalPassword(ctx, userID, expectedHash, password)
}

type adminPasswordResetFixture struct {
	ts          *httptest.Server
	db          *sql.DB
	st          *store.Store
	spy         *adminPasswordResetStore
	owner       store.User
	admin       store.User
	user        store.User
	peerOwner   store.User
	ownerClient *http.Client
	adminClient *http.Client
	userClient  *http.Client
}

func newAdminPasswordResetFixture(t *testing.T, opts Options) *adminPasswordResetFixture {
	t.Helper()
	if opts.MaxRequestBody == 0 {
		opts.MaxRequestBody = 1 << 20
	}
	if opts.ScrumboyMode == "" {
		opts.ScrumboyMode = "full"
	}
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "app.db"), db.Options{
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
	realStore := store.New(sqlDB, nil)
	ctx := context.Background()
	owner, err := realStore.BootstrapUser(ctx, "owner-reset@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	admin, err := realStore.CreateUser(ctx, "admin-reset@example.com", "password123", "Admin")
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	if err := realStore.UpdateUserRole(ctx, owner.ID, admin.ID, store.SystemRoleAdmin); err != nil {
		t.Fatalf("promote admin: %v", err)
	}
	user, err := realStore.CreateUser(ctx, "user-reset@example.com", "password123", "User")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	peer, err := realStore.CreateUser(ctx, "peer-owner-reset@example.com", "password123", "Peer")
	if err != nil {
		t.Fatalf("create peer: %v", err)
	}
	if err := realStore.UpdateUserRole(ctx, owner.ID, peer.ID, store.SystemRoleOwner); err != nil {
		t.Fatalf("promote peer owner: %v", err)
	}
	spy := &adminPasswordResetStore{Store: realStore, getUserCalls: map[int64]int{}}
	srv := NewServer(spy, opts)
	ts := httptest.NewServer(srv)
	t.Cleanup(func() {
		ts.Close()
		_ = sqlDB.Close()
	})
	fx := &adminPasswordResetFixture{
		ts: ts, db: sqlDB, st: realStore, spy: spy,
		owner: owner, admin: admin, user: user, peerOwner: peer,
	}
	if opts.OIDCService != nil && opts.OIDCService.Config().LocalAuthDisabled {
		return fx
	}
	login := func(email string) *http.Client {
		jar, err := cookiejar.New(nil)
		if err != nil {
			t.Fatalf("cookie jar: %v", err)
		}
		client := &http.Client{Transport: ts.Client().Transport, Jar: jar}
		loginUserClient(t, client, ts.URL, email, "password123")
		return client
	}
	fx.ownerClient = login(owner.Email)
	fx.adminClient = login(admin.Email)
	fx.userClient = login(user.Email)
	return fx
}

func adminPasswordResetPath(userID int64) string {
	return fmt.Sprintf("/api/admin/users/%d/password-reset", userID)
}

func (f *adminPasswordResetFixture) sessionCount(t *testing.T, userID int64) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id = ?`, userID).Scan(&n); err != nil {
		t.Fatalf("session count: %v", err)
	}
	return n
}

func TestAdminPasswordResetRESTContracts(t *testing.T) {
	fx := newAdminPasswordResetFixture(t, Options{EncryptionKey: testEncryptionKey, ScrumboyMode: "full"})
	missingID := int64(999999)

	t.Run("authorization and validation precedence", func(t *testing.T) {
		cases := []struct {
			name        string
			client      *http.Client
			method      string
			path        string
			wantStatus  int
			wantCode    string
			wantMessage string
		}{
			{"unauthenticated precedes invalid id", fx.ts.Client(), http.MethodPost, "/api/admin/users/not-an-id/password-reset", 401, "UNAUTHORIZED", "unauthorized"},
			{"regular user precedes invalid id", fx.userClient, http.MethodPost, "/api/admin/users/not-an-id/password-reset", 403, "FORBIDDEN", "admin or owner role required"},
			{"admin reaches invalid id before owner check", fx.adminClient, http.MethodPost, "/api/admin/users/not-an-id/password-reset", 400, "BAD_REQUEST", "invalid user id"},
			{"owner invalid id", fx.ownerClient, http.MethodPost, "/api/admin/users/not-an-id/password-reset", 400, "BAD_REQUEST", "invalid user id"},
			{"zero id", fx.ownerClient, http.MethodPost, "/api/admin/users/0/password-reset", 400, "BAD_REQUEST", "invalid user id"},
			{"negative id", fx.ownerClient, http.MethodPost, "/api/admin/users/-1/password-reset", 400, "BAD_REQUEST", "invalid user id"},
			{"admin owner check precedes missing target", fx.adminClient, http.MethodPost, adminPasswordResetPath(missingID), 403, "FORBIDDEN", "owner role required"},
			{"owner missing target", fx.ownerClient, http.MethodPost, adminPasswordResetPath(missingID), 404, "NOT_FOUND", "not found"},
			{"self reset", fx.ownerClient, http.MethodPost, adminPasswordResetPath(fx.owner.ID), 400, "BAD_REQUEST", "cannot generate reset link for yourself"},
			{"peer owner", fx.ownerClient, http.MethodPost, adminPasswordResetPath(fx.peerOwner.ID), 403, "FORBIDDEN", "cannot reset password for another owner"},
			{"unsupported method", fx.ownerClient, http.MethodGet, adminPasswordResetPath(fx.user.ID), 405, "METHOD_NOT_ALLOWED", "method not allowed"},
		}
		for _, tt := range cases {
			t.Run(tt.name, func(t *testing.T) {
				resp, body := doJSON(t, tt.client, tt.method, fx.ts.URL+tt.path, map[string]any{}, nil)
				assertAdminRESTError(t, resp, body, tt.wantStatus, tt.wantCode, tt.wantMessage)
			})
		}
	})

	t.Run("owner generates link for user", func(t *testing.T) {
		hashBefore, err := fx.st.GetUserPasswordHash(context.Background(), fx.user.ID)
		if err != nil {
			t.Fatal(err)
		}
		ownerSessions := fx.sessionCount(t, fx.owner.ID)
		userSessions := fx.sessionCount(t, fx.user.ID)
		var out map[string]any
		resp, body := doJSON(t, fx.ownerClient, http.MethodPost, fx.ts.URL+adminPasswordResetPath(fx.user.ID), map[string]any{}, &out)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d %s", resp.StatusCode, body)
		}
		resetURL, _ := out["reset_url"].(string)
		expiresAt, _ := out["expires_at"].(string)
		if !strings.HasPrefix(resetURL, fx.ts.URL+"/auth/reset-password?token=") {
			t.Fatalf("reset_url=%q", resetURL)
		}
		parsedExp, err := time.Parse(time.RFC3339, expiresAt)
		if err != nil {
			t.Fatalf("expires_at=%q: %v", expiresAt, err)
		}
		if parsedExp.Location() != time.UTC {
			t.Fatalf("expires_at location=%v", parsedExp.Location())
		}
		until := time.Until(parsedExp)
		if until < 29*time.Minute || until > 31*time.Minute {
			t.Fatalf("expires_at delta=%s", until)
		}
		parsedURL, err := url.Parse(resetURL)
		if err != nil {
			t.Fatal(err)
		}
		token := parsedURL.Query().Get("token")
		userID, ts, sig, err := tokens.ParsePasswordResetToken(token)
		if err != nil {
			t.Fatalf("parse token: %v", err)
		}
		if userID != fx.user.ID {
			t.Fatalf("token user=%d want=%d", userID, fx.user.ID)
		}
		if err := tokens.VerifyPasswordResetToken(testEncryptionKey, userID, ts, sig, hashBefore); err != nil {
			t.Fatalf("verify: %v", err)
		}
		hashAfter, err := fx.st.GetUserPasswordHash(context.Background(), fx.user.ID)
		if err != nil || hashAfter != hashBefore {
			t.Fatalf("password hash changed: %v %q %q", err, hashBefore, hashAfter)
		}
		if fx.spy.updatePasswordN != 0 || fx.spy.resetPasswordN != 0 {
			t.Fatalf("password mutation calls update=%d reset=%d", fx.spy.updatePasswordN, fx.spy.resetPasswordN)
		}
		if fx.sessionCount(t, fx.owner.ID) != ownerSessions || fx.sessionCount(t, fx.user.ID) != userSessions {
			t.Fatal("session rows changed")
		}
		resp, body = doJSON(t, fx.ownerClient, http.MethodGet, fx.ts.URL+"/api/me", nil, nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("owner session after link: %d %s", resp.StatusCode, body)
		}
	})

	t.Run("owner generates link for admin", func(t *testing.T) {
		var out map[string]any
		resp, body := doJSON(t, fx.ownerClient, http.MethodPost, fx.ts.URL+adminPasswordResetPath(fx.admin.ID), map[string]any{}, &out)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d %s", resp.StatusCode, body)
		}
		resetURL, _ := out["reset_url"].(string)
		parsedURL, err := url.Parse(resetURL)
		if err != nil {
			t.Fatal(err)
		}
		token := parsedURL.Query().Get("token")
		userID, ts, sig, err := tokens.ParsePasswordResetToken(token)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		hash, err := fx.st.GetUserPasswordHash(context.Background(), fx.admin.ID)
		if err != nil {
			t.Fatal(err)
		}
		if userID != fx.admin.ID {
			t.Fatalf("token user=%d", userID)
		}
		if err := tokens.VerifyPasswordResetToken(testEncryptionKey, userID, ts, sig, hash); err != nil {
			t.Fatalf("verify: %v", err)
		}
	})

	t.Run("self-reset precedes rate limit", func(t *testing.T) {
		resp, body := doJSON(t, fx.ownerClient, http.MethodPost, fx.ts.URL+adminPasswordResetPath(fx.owner.ID), map[string]any{}, nil)
		assertAdminRESTError(t, resp, body, 400, "BAD_REQUEST", "cannot generate reset link for yourself")
	})
}

func TestAdminPasswordResetRESTEncryptionAndLocalAuth(t *testing.T) {
	t.Run("missing encryption key", func(t *testing.T) {
		fx := newAdminPasswordResetFixture(t, Options{ScrumboyMode: "full"})
		resp, body := doJSON(t, fx.ownerClient, http.MethodPost, fx.ts.URL+adminPasswordResetPath(fx.user.ID), map[string]any{}, nil)
		assertAdminRESTError(t, resp, body, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "Password reset is not configured. Set SCRUMBOY_ENCRYPTION_KEY (e.g. openssl rand -base64 32) and restart.")
	})

	t.Run("encryption key precedes invalid id", func(t *testing.T) {
		fx := newAdminPasswordResetFixture(t, Options{ScrumboyMode: "full"})
		resp, body := doJSON(t, fx.ownerClient, http.MethodPost, fx.ts.URL+"/api/admin/users/not-an-id/password-reset", map[string]any{}, nil)
		assertAdminRESTError(t, resp, body, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "Password reset is not configured. Set SCRUMBOY_ENCRYPTION_KEY (e.g. openssl rand -base64 32) and restart.")
	})

	t.Run("local auth disabled precedes method", func(t *testing.T) {
		service := oidc.New(oidc.Config{
			IssuerCanonical:   "https://idp.example",
			ClientID:          "client",
			ClientSecret:      "secret",
			RedirectURL:       "https://scrumboy.example/api/auth/oidc/callback",
			LocalAuthDisabled: true,
		})
		fx := newAdminPasswordResetFixture(t, Options{
			EncryptionKey: testEncryptionKey,
			ScrumboyMode:  "full",
			OIDCService:   service,
		})
		ownerClient := authenticatedOIDCTestClient(t, fx.ts, fx.st, fx.owner.ID)
		resp, body := doJSON(t, ownerClient, http.MethodGet, fx.ts.URL+adminPasswordResetPath(fx.user.ID), nil, nil)
		assertAdminRESTError(t, resp, body, http.StatusNotFound, "NOT_FOUND", "not found")
		resp, body = doJSON(t, ownerClient, http.MethodPost, fx.ts.URL+adminPasswordResetPath(fx.user.ID), map[string]any{}, nil)
		assertAdminRESTError(t, resp, body, http.StatusNotFound, "NOT_FOUND", "not found")
	})

	t.Run("public base url", func(t *testing.T) {
		fx := newAdminPasswordResetFixture(t, Options{
			EncryptionKey: testEncryptionKey,
			ScrumboyMode:  "full",
			PublicBaseURL: "https://boards.example",
		})
		var out map[string]any
		resp, body := doJSON(t, fx.ownerClient, http.MethodPost, fx.ts.URL+adminPasswordResetPath(fx.user.ID), map[string]any{}, &out)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status=%d %s", resp.StatusCode, body)
		}
		resetURL, _ := out["reset_url"].(string)
		if !strings.HasPrefix(resetURL, "https://boards.example/auth/reset-password?token=") {
			t.Fatalf("reset_url=%q", resetURL)
		}
	})
}

func TestAdminPasswordResetRESTStoreFailuresAndRateLimit(t *testing.T) {
	t.Run("requester read after gate", func(t *testing.T) {
		fx := newAdminPasswordResetFixture(t, Options{EncryptionKey: testEncryptionKey, ScrumboyMode: "full"})
		forced := errors.New("forced requester read")
		fx.spy.failGetUserErr = forced
		fx.spy.failGetUserAfter = map[int64]int{fx.owner.ID: 1}
		resp, body := doJSON(t, fx.ownerClient, http.MethodPost, fx.ts.URL+adminPasswordResetPath(fx.user.ID), map[string]any{}, nil)
		got := assertAdminRESTError(t, resp, body, 500, "INTERNAL", "internal error")
		if got.Error.Details["detail"] != forced.Error() {
			t.Fatalf("details=%v", got.Error.Details)
		}
		if fx.spy.hashCalls != 0 {
			t.Fatalf("hash calls=%d", fx.spy.hashCalls)
		}
	})

	t.Run("target read failure", func(t *testing.T) {
		fx := newAdminPasswordResetFixture(t, Options{EncryptionKey: testEncryptionKey, ScrumboyMode: "full"})
		forced := errors.New("forced target read")
		fx.spy.failGetUserErr = forced
		fx.spy.failGetUserAfter = map[int64]int{fx.user.ID: 0}
		resp, body := doJSON(t, fx.ownerClient, http.MethodPost, fx.ts.URL+adminPasswordResetPath(fx.user.ID), map[string]any{}, nil)
		got := assertAdminRESTError(t, resp, body, 500, "INTERNAL", "internal error")
		if got.Error.Details["detail"] != forced.Error() {
			t.Fatalf("details=%v", got.Error.Details)
		}
		if fx.spy.hashCalls != 0 {
			t.Fatalf("hash calls=%d", fx.spy.hashCalls)
		}
	})

	t.Run("password hash failure", func(t *testing.T) {
		fx := newAdminPasswordResetFixture(t, Options{EncryptionKey: testEncryptionKey, ScrumboyMode: "full"})
		forced := errors.New("forced hash read")
		fx.spy.hashErr = forced
		resp, body := doJSON(t, fx.ownerClient, http.MethodPost, fx.ts.URL+adminPasswordResetPath(fx.user.ID), map[string]any{}, nil)
		got := assertAdminRESTError(t, resp, body, 500, "INTERNAL", "internal error")
		if got.Error.Details["detail"] != forced.Error() {
			t.Fatalf("details=%v", got.Error.Details)
		}
		if fx.spy.hashCalls != 1 {
			t.Fatalf("hash calls=%d", fx.spy.hashCalls)
		}
	})

	t.Run("peer owner precedes rate limit", func(t *testing.T) {
		fx := newAdminPasswordResetFixture(t, Options{EncryptionKey: testEncryptionKey, ScrumboyMode: "full"})
		for i := 0; i < 10; i++ {
			resp, body := doJSON(t, fx.ownerClient, http.MethodPost, fx.ts.URL+adminPasswordResetPath(fx.user.ID), map[string]any{}, nil)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("warmup %d: %d %s", i, resp.StatusCode, body)
			}
		}
		resp, body := doJSON(t, fx.ownerClient, http.MethodPost, fx.ts.URL+adminPasswordResetPath(fx.peerOwner.ID), map[string]any{}, nil)
		assertAdminRESTError(t, resp, body, 403, "FORBIDDEN", "cannot reset password for another owner")
		resp, body = doJSON(t, fx.ownerClient, http.MethodPost, fx.ts.URL+adminPasswordResetPath(fx.user.ID), map[string]any{}, nil)
		assertAdminRESTError(t, resp, body, http.StatusTooManyRequests, "RATE_LIMITED", "too many reset links; try again later")
	})
}

func TestAdminPasswordResetJSONShape(t *testing.T) {
	fx := newAdminPasswordResetFixture(t, Options{EncryptionKey: testEncryptionKey, ScrumboyMode: "full"})
	resp, body := doJSON(t, fx.ownerClient, http.MethodPost, fx.ts.URL+adminPasswordResetPath(fx.user.ID), map[string]any{}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d %s", resp.StatusCode, body)
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["reset_url"].(string); !ok {
		t.Fatalf("body=%s", body)
	}
	if _, ok := raw["expires_at"].(string); !ok {
		t.Fatalf("body=%s", body)
	}
	if len(raw) != 2 {
		t.Fatalf("keys=%v", raw)
	}
}
