package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"scrumboy/internal/db"
	"scrumboy/internal/migrate"
	"scrumboy/internal/store"
)

// The transport records the account type it read, so the store can act on it without asking again.
func TestResolveRequestAuth_RecordsAccountType(t *testing.T) {
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "app.db"), db.Options{BusyTimeout: 5000, JournalMode: "WAL", Synchronous: "FULL"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	ctx := context.Background()
	if err := migrate.Apply(ctx, sqlDB); err != nil {
		t.Fatal(err)
	}
	st := store.New(sqlDB, nil)
	owner, err := st.BootstrapUser(ctx, "owner@example.com", "CorrectHorse123!", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := st.CreateServiceAccount(ctx, owner.ID, "Bot")
	if err != nil {
		t.Fatal(err)
	}
	const serviceToken = store.APITokenPrefix + "principal-test"
	sum := sha256.Sum256([]byte(serviceToken))
	if _, err := sqlDB.Exec(`INSERT INTO api_tokens(user_id, token_hash, created_at) VALUES (?, ?, 1)`, svc.ID, hex.EncodeToString(sum[:])); err != nil {
		t.Fatal(err)
	}
	_, humanFlagged, _, err := st.CreateUserAPIToken(ctx, owner.ID, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	session, _, err := st.CreateSession(ctx, owner.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	a := New(st, Options{Mode: "full"})

	for name, tc := range map[string]struct {
		bearer, cookie string
		wantUser       int64
		wantService    bool
	}{
		"service account token":       {bearer: serviceToken, wantUser: svc.ID, wantService: true},
		"human token flagged service": {bearer: humanFlagged, wantUser: owner.ID, wantService: false},
		"human session":               {cookie: session, wantUser: owner.ID, wantService: false},
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			if tc.bearer != "" {
				req.Header.Set("Authorization", "Bearer "+tc.bearer)
			}
			if tc.cookie != "" {
				req.AddCookie(&http.Cookie{Name: "scrumboy_session", Value: tc.cookie})
			}
			got := a.resolveRequestAuth(req, false)
			if !got.Authenticated {
				t.Fatalf("not authenticated: %+v", got)
			}
			if id, ok := store.UserIDFromContext(got.Ctx); !ok || id != tc.wantUser {
				t.Fatalf("actor = %d, %v, want %d", id, ok, tc.wantUser)
			}
			isService, ok := store.ServiceIdentityFromContext(got.Ctx, tc.wantUser)
			if !ok || isService != tc.wantService {
				t.Fatalf("recorded identity = %v, %v, want %v", isService, ok, tc.wantService)
			}
		})
	}
}
