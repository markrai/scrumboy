package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"scrumboy/internal/oidc"
	"scrumboy/internal/store"
)

func enableOIDCLinkTwoFactor(t *testing.T, st *store.Store, userID int64, codes []string) {
	t.Helper()
	enc, err := st.EncryptTOTPSecret([]byte(testTOTPSecret))
	if err != nil {
		t.Fatalf("EncryptTOTPSecret: %v", err)
	}
	if err := st.SetUserTwoFactor(context.Background(), userID, enc); err != nil {
		t.Fatalf("SetUserTwoFactor: %v", err)
	}
	if err := st.AddRecoveryCodes(context.Background(), userID, codes); err != nil {
		t.Fatalf("AddRecoveryCodes: %v", err)
	}
}

func startOIDCLink(t *testing.T, client *http.Client, baseURL, password, twoFactor string) (map[string]any, int) {
	t.Helper()
	var start map[string]any
	resp, body := doJSON(t, client, http.MethodPost, baseURL+"/api/auth/oidc/link/start", map[string]any{
		"currentPassword": password,
		"twoFactorCode":   twoFactor,
	}, &start)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("link start status=%d body=%s", resp.StatusCode, body)
	}
	return start, resp.StatusCode
}

func oidcAuthorizationState(t *testing.T, start map[string]any) string {
	t.Helper()
	params, ok := start["authorizationParameters"].(map[string]any)
	if !ok {
		t.Fatalf("missing authorization parameters: %#v", start)
	}
	state, _ := params["state"].(string)
	if state == "" {
		t.Fatalf("missing state: %#v", start)
	}
	return state
}

func assertAuthorizationDoesNotLeakProof(t *testing.T, start map[string]any, recoveryCode string) {
	t.Helper()
	raw, err := json.Marshal(start)
	if err != nil {
		t.Fatalf("marshal start: %v", err)
	}
	body := string(raw)
	if strings.Contains(body, recoveryCode) || strings.Contains(body, strings.ReplaceAll(recoveryCode, "-", "")) {
		t.Fatalf("authorization response leaked recovery code: %s", body)
	}
	if strings.Contains(body, "$2a$") || strings.Contains(body, "$2b$") {
		t.Fatalf("authorization response leaked bcrypt hash: %s", body)
	}
}

func testOIDCService(t *testing.T, ts *httptest.Server) *oidc.Service {
	t.Helper()
	srv, ok := ts.Config.Handler.(*Server)
	if !ok {
		t.Fatalf("handler type %T", ts.Config.Handler)
	}
	if srv.oidcService == nil {
		t.Fatal("missing OIDC service")
	}
	return srv.oidcService
}

func assertRecoveryConsumable(t *testing.T, st *store.Store, userID int64, code string) {
	t.Helper()
	consumed, err := st.ConsumeRecoveryCode(context.Background(), userID, code)
	if err != nil {
		t.Fatalf("ConsumeRecoveryCode: %v", err)
	}
	if !consumed {
		t.Fatal("expected recovery code to remain unused")
	}
}

func assertRecoveryConsumed(t *testing.T, st *store.Store, userID int64, code string) {
	t.Helper()
	consumed, err := st.ConsumeRecoveryCode(context.Background(), userID, code)
	if err != nil {
		t.Fatalf("ConsumeRecoveryCode: %v", err)
	}
	if consumed {
		t.Fatal("expected recovery code to be consumed")
	}
}

func assertOIDCNotLinked(t *testing.T, st *store.Store, issuer, subject string) {
	t.Helper()
	if _, err := st.GetUserByOIDCIdentity(context.Background(), issuer, subject); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("identity linked unexpectedly: %v", err)
	}
}

func replaceOIDCSessionCookie(t *testing.T, client *http.Client, ts *httptest.Server, st *store.Store, userID int64) {
	t.Helper()
	token, expires, err := st.CreateSession(context.Background(), userID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	base, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	client.Jar.SetCookies(base, []*http.Cookie{{Name: "scrumboy_session", Value: token, Path: "/", Expires: expires}})
}

func recoveryRowIDs(t *testing.T, sqlDB *sql.DB, userID int64) []int64 {
	t.Helper()
	rows, err := sqlDB.Query(`SELECT id FROM user_recovery_codes WHERE user_id = ? ORDER BY id`, userID)
	if err != nil {
		t.Fatalf("list recovery ids: %v", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan recovery id: %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate recovery ids: %v", err)
	}
	return ids
}

func containsInt64(ids []int64, want int64) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func TestOIDCLinkAbandonedAuthorizationLeavesRecoveryCodeUnused(t *testing.T) {
	idp := newFakeIdP(t)
	defer idp.close()
	ts, st, cleanup := newTestOIDCServerWithStore(t, idp)
	defer cleanup()
	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, idp.email, "Password123!", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	codes := store.GenerateRecoveryCodes(store.RecoveryCodeCount)
	enableOIDCLinkTwoFactor(t, st, owner.ID, codes)
	client := authenticatedOIDCTestClient(t, ts, st, owner.ID)
	start, _ := startOIDCLink(t, client, ts.URL, "Password123!", codes[0])
	assertAuthorizationDoesNotLeakProof(t, start, codes[0])
	assertOIDCNotLinked(t, st, idp.issuer, idp.subject)
	assertRecoveryConsumable(t, st, owner.ID, codes[0])
}

func TestOIDCLinkInvalidCallbackStateLeavesRecoveryCodeUnused(t *testing.T) {
	idp := newFakeIdP(t)
	defer idp.close()
	ts, st, cleanup := newTestOIDCServerWithStore(t, idp)
	defer cleanup()
	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, idp.email, "Password123!", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	codes := store.GenerateRecoveryCodes(store.RecoveryCodeCount)
	enableOIDCLinkTwoFactor(t, st, owner.ID, codes)
	client := authenticatedOIDCTestClient(t, ts, st, owner.ID)
	startOIDCLink(t, client, ts.URL, "Password123!", codes[0])

	resp, err := client.Get(ts.URL + "/api/auth/oidc/callback?state=not-a-real-state&code=not-a-real-code")
	if err != nil {
		t.Fatalf("invalid callback: %v", err)
	}
	resp.Body.Close()
	if !strings.Contains(resp.Request.URL.String(), "oidc_error=state_invalid") {
		t.Fatalf("invalid state error=%s", resp.Request.URL)
	}
	assertOIDCNotLinked(t, st, idp.issuer, idp.subject)
	assertRecoveryConsumable(t, st, owner.ID, codes[0])
}

func TestOIDCLinkExpiredStateCannotLinkOrConsume(t *testing.T) {
	idp := newFakeIdP(t)
	defer idp.close()
	ts, st, cleanup := newTestOIDCServerWithStore(t, idp)
	defer cleanup()
	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, idp.email, "Password123!", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	codes := store.GenerateRecoveryCodes(store.RecoveryCodeCount)
	enableOIDCLinkTwoFactor(t, st, owner.ID, codes)
	client := authenticatedOIDCTestClient(t, ts, st, owner.ID)
	start, _ := startOIDCLink(t, client, ts.URL, "Password123!", codes[0])
	if !testOIDCService(t, ts).ExpireStoredStateForTest(oidcAuthorizationState(t, start)) {
		t.Fatal("failed to expire stored state")
	}
	callback := submitOIDCAuthorizationRequest(t, client, start)
	callback.Body.Close()
	if !strings.Contains(callback.Request.URL.String(), "oidc_error=state_invalid") {
		t.Fatalf("expired state error=%s", callback.Request.URL)
	}
	assertOIDCNotLinked(t, st, idp.issuer, idp.subject)
	assertRecoveryConsumable(t, st, owner.ID, codes[0])
}

func TestOIDCLinkSessionChangePreventsLinkAndConsume(t *testing.T) {
	idp := newFakeIdP(t)
	defer idp.close()
	ts, st, cleanup := newTestOIDCServerWithStore(t, idp)
	defer cleanup()
	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, idp.email, "Password123!", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	codes := store.GenerateRecoveryCodes(store.RecoveryCodeCount)
	enableOIDCLinkTwoFactor(t, st, owner.ID, codes)
	client := authenticatedOIDCTestClient(t, ts, st, owner.ID)
	start, _ := startOIDCLink(t, client, ts.URL, "Password123!", codes[0])
	replaceOIDCSessionCookie(t, client, ts, st, owner.ID)
	callback := submitOIDCAuthorizationRequest(t, client, start)
	callback.Body.Close()
	if !strings.Contains(callback.Request.URL.String(), "oidc_error=session_changed") {
		t.Fatalf("session change error=%s", callback.Request.URL)
	}
	assertOIDCNotLinked(t, st, idp.issuer, idp.subject)
	assertRecoveryConsumable(t, st, owner.ID, codes[0])
}

func TestOIDCLinkProviderEmailMismatchLeavesRecoveryCodeUnused(t *testing.T) {
	idp := newFakeIdP(t)
	defer idp.close()
	ts, st, cleanup := newTestOIDCServerWithStore(t, idp)
	defer cleanup()
	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, idp.email, "Password123!", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	codes := store.GenerateRecoveryCodes(store.RecoveryCodeCount)
	enableOIDCLinkTwoFactor(t, st, owner.ID, codes)
	client := authenticatedOIDCTestClient(t, ts, st, owner.ID)
	start, _ := startOIDCLink(t, client, ts.URL, "Password123!", codes[0])
	idp.email = "other@example.com"
	callback := submitOIDCAuthorizationRequest(t, client, start)
	callback.Body.Close()
	if !strings.Contains(callback.Request.URL.String(), "oidc_error=link_rejected") {
		t.Fatalf("email mismatch error=%s", callback.Request.URL)
	}
	assertOIDCNotLinked(t, st, idp.issuer, idp.subject)
	assertRecoveryConsumable(t, st, owner.ID, codes[0])
}

func TestOIDCLinkStoreFailureRollsBackRecoveryConsume(t *testing.T) {
	idp := newFakeIdP(t)
	defer idp.close()
	ts, st, sqlDB, cleanup := newTestOIDCServerWithDB(t, idp)
	defer cleanup()
	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, idp.email, "Password123!", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	codes := store.GenerateRecoveryCodes(store.RecoveryCodeCount)
	enableOIDCLinkTwoFactor(t, st, owner.ID, codes)
	client := authenticatedOIDCTestClient(t, ts, st, owner.ID)
	start, _ := startOIDCLink(t, client, ts.URL, "Password123!", codes[0])
	if _, err := sqlDB.Exec(`CREATE TRIGGER fail_oidc_identity BEFORE INSERT ON user_oidc_identities BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
		t.Fatalf("install trigger: %v", err)
	}
	callback := submitOIDCAuthorizationRequest(t, client, start)
	callback.Body.Close()
	if !strings.Contains(callback.Request.URL.String(), "oidc_error=token") {
		t.Fatalf("store failure error=%s", callback.Request.URL)
	}
	assertOIDCNotLinked(t, st, idp.issuer, idp.subject)
	assertRecoveryConsumable(t, st, owner.ID, codes[0])
}

func TestOIDCLinkSuccessConsumesRecoveryProofOnceAndRejectsReplay(t *testing.T) {
	idp := newFakeIdP(t)
	defer idp.close()
	ts, st, cleanup := newTestOIDCServerWithStore(t, idp)
	defer cleanup()
	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, idp.email, "Password123!", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	codes := store.GenerateRecoveryCodes(store.RecoveryCodeCount)
	enableOIDCLinkTwoFactor(t, st, owner.ID, codes)
	client := authenticatedOIDCTestClient(t, ts, st, owner.ID)
	start, _ := startOIDCLink(t, client, ts.URL, "Password123!", codes[0])
	assertAuthorizationDoesNotLeakProof(t, start, codes[0])

	var callbackURL string
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if strings.Contains(req.URL.Path, "/api/auth/oidc/callback") {
			callbackURL = req.URL.String()
		}
		return nil
	}
	callback := submitOIDCAuthorizationRequest(t, client, start)
	callback.Body.Close()
	if strings.Contains(callback.Request.URL.String(), "oidc_error=") {
		t.Fatalf("successful link failed: %s", callback.Request.URL)
	}
	linked, err := st.GetUserByOIDCIdentity(ctx, idp.issuer, idp.subject)
	if err != nil || linked.ID != owner.ID {
		t.Fatalf("successful link missing: linked=%+v err=%v", linked, err)
	}
	assertRecoveryConsumed(t, st, owner.ID, codes[0])
	sibling, err := st.ConsumeRecoveryCode(ctx, owner.ID, codes[1])
	if err != nil {
		t.Fatal(err)
	}
	if !sibling {
		t.Fatal("successful link consumed an unused sibling recovery code")
	}

	if callbackURL == "" {
		t.Fatal("did not capture callback URL for replay")
	}
	replay, err := client.Get(callbackURL)
	if err != nil {
		t.Fatalf("replay callback: %v", err)
	}
	replay.Body.Close()
	if !strings.Contains(replay.Request.URL.String(), "oidc_error=state_invalid") {
		t.Fatalf("callback replay error=%s", replay.Request.URL)
	}
}

func TestOIDCLinkStaleProofAfterRecoveryRegenerationFailsEvenIfRowIDReused(t *testing.T) {
	idp := newFakeIdP(t)
	defer idp.close()
	ts, st, sqlDB, cleanup := newTestOIDCServerWithDB(t, idp)
	defer cleanup()
	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, idp.email, "Password123!", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	original := store.GenerateRecoveryCodes(store.RecoveryCodeCount)
	enableOIDCLinkTwoFactor(t, st, owner.ID, original)
	staleID, staleHash, err := st.MatchRecoveryCodeProof(ctx, owner.ID, original[1])
	if err != nil || staleID == 0 || staleHash == "" {
		t.Fatalf("MatchRecoveryCodeProof stale: id=%d err=%v", staleID, err)
	}
	currentID, currentHash, err := st.MatchRecoveryCodeProof(ctx, owner.ID, original[0])
	if err != nil || currentID == 0 || currentHash == "" {
		t.Fatalf("MatchRecoveryCodeProof current: id=%d err=%v", currentID, err)
	}

	client := authenticatedOIDCTestClient(t, ts, st, owner.ID)
	start, _ := startOIDCLink(t, client, ts.URL, "Password123!", original[1])
	replacement := store.GenerateRecoveryCodes(store.RecoveryCodeCount)
	if err := st.ReplaceRecoveryCodes(ctx, owner.ID, replacement, store.RecoveryCodeProof{ID: currentID, Hash: currentHash}); err != nil {
		t.Fatalf("ReplaceRecoveryCodes: %v", err)
	}
	ids := recoveryRowIDs(t, sqlDB, owner.ID)
	if !containsInt64(ids, staleID) {
		t.Fatalf("replacement ids %v did not reuse stale proof id %d", ids, staleID)
	}

	callback := submitOIDCAuthorizationRequest(t, client, start)
	callback.Body.Close()
	if !strings.Contains(callback.Request.URL.String(), "oidc_error=link_rejected") {
		t.Fatalf("stale proof error=%s", callback.Request.URL)
	}
	assertOIDCNotLinked(t, st, idp.issuer, idp.subject)
	assertRecoveryConsumable(t, st, owner.ID, replacement[0])
}

func TestOIDCLinkConcurrentSameRecoveryProofCannotBothSucceed(t *testing.T) {
	idp := newFakeIdP(t)
	defer idp.close()
	ts, st, cleanup := newTestOIDCServerWithStore(t, idp)
	defer cleanup()
	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, idp.email, "Password123!", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	codes := store.GenerateRecoveryCodes(store.RecoveryCodeCount)
	enableOIDCLinkTwoFactor(t, st, owner.ID, codes)

	clients := []*http.Client{
		authenticatedOIDCTestClient(t, ts, st, owner.ID),
		authenticatedOIDCTestClient(t, ts, st, owner.ID),
	}
	starts := make([]map[string]any, 2)
	for i, client := range clients {
		starts[i], _ = startOIDCLink(t, client, ts.URL, "Password123!", codes[0])
	}

	start := make(chan struct{})
	urls := make([]string, 2)
	var wg sync.WaitGroup
	for i := range clients {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			callback := submitOIDCAuthorizationRequest(t, clients[i], starts[i])
			callback.Body.Close()
			urls[i] = callback.Request.URL.String()
		}(i)
	}
	close(start)
	wg.Wait()

	var linked, rejected int
	for _, raw := range urls {
		switch {
		case strings.Contains(raw, "oidc_error=link_rejected"):
			rejected++
		case strings.Contains(raw, "oidc_error="):
			t.Fatalf("unexpected concurrent callback error: %s", raw)
		default:
			linked++
		}
	}
	if linked != 1 || rejected != 1 {
		t.Fatalf("concurrent callbacks linked=%d rejected=%d urls=%v", linked, rejected, urls)
	}
	got, err := st.GetUserByOIDCIdentity(ctx, idp.issuer, idp.subject)
	if err != nil || got.ID != owner.ID {
		t.Fatalf("expected one linked identity: %+v err=%v", got, err)
	}
	assertRecoveryConsumed(t, st, owner.ID, codes[0])
}

func TestOIDCLinkTOTPDoesNotConsumeRecoveryCodes(t *testing.T) {
	idp := newFakeIdP(t)
	defer idp.close()
	ts, st, cleanup := newTestOIDCServerWithStore(t, idp)
	defer cleanup()
	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, idp.email, "Password123!", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	codes := store.GenerateRecoveryCodes(store.RecoveryCodeCount)
	enableOIDCLinkTwoFactor(t, st, owner.ID, codes)
	totpCode, err := totp.GenerateCode(testTOTPSecret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	client := authenticatedOIDCTestClient(t, ts, st, owner.ID)
	start, _ := startOIDCLink(t, client, ts.URL, "Password123!", totpCode)
	assertAuthorizationDoesNotLeakProof(t, start, codes[0])
	callback := submitOIDCAuthorizationRequest(t, client, start)
	callback.Body.Close()
	if strings.Contains(callback.Request.URL.String(), "oidc_error=") {
		t.Fatalf("TOTP link failed: %s", callback.Request.URL)
	}
	linked, err := st.GetUserByOIDCIdentity(ctx, idp.issuer, idp.subject)
	if err != nil || linked.ID != owner.ID {
		t.Fatalf("TOTP link missing: %+v err=%v", linked, err)
	}
	assertRecoveryConsumable(t, st, owner.ID, codes[0])
}
