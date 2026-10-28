package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"scrumboy/internal/store"
)

func loginRequires2FA(t *testing.T, client *http.Client, baseURL, email, password string) (tempToken string, body []byte) {
	t.Helper()
	var out map[string]any
	resp, raw := doJSON(t, client, http.MethodPost, baseURL+"/api/auth/login", map[string]any{
		"email":    email,
		"password": password,
	}, &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status=%d body=%s", resp.StatusCode, raw)
	}
	if out["requires2fa"] != true {
		t.Fatalf("expected requires2fa: %+v", out)
	}
	token, _ := out["tempToken"].(string)
	if token == "" {
		t.Fatalf("missing tempToken: %+v", out)
	}
	assertNoRecoverySecretLeak(t, raw, "")
	return token, raw
}

func assertNoRecoverySecretLeak(t *testing.T, body []byte, recoveryCode string) {
	t.Helper()
	text := string(body)
	if recoveryCode != "" && (strings.Contains(text, recoveryCode) || strings.Contains(text, strings.ReplaceAll(recoveryCode, "-", ""))) {
		t.Fatalf("response leaked recovery code: %s", text)
	}
	if strings.Contains(text, "$2a$") || strings.Contains(text, "$2b$") {
		t.Fatalf("response leaked bcrypt hash: %s", text)
	}
}

func TestLogin2FARecoveryCodeSucceedsOnce(t *testing.T) {
	ts, _, st, owner, _ := new2FAHTTPFixture(t)
	setupToken, secret := setupTwoFactor(t, owner, ts.URL)
	codes := enableTwoFactor(t, owner, ts.URL, setupToken, secret)

	client := newCookieClient(t)
	tempToken, _ := loginRequires2FA(t, client, ts.URL, "2fa-enable@example.com", "password123")
	if clientHasSessionCookie(t, client, ts.URL) {
		t.Fatal("password step established a session before 2FA")
	}

	var verified map[string]any
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/login/2fa", map[string]any{
		"tempToken": tempToken,
		"code":      codes[0],
	}, &verified)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login 2FA status=%d body=%s", resp.StatusCode, body)
	}
	assertNoRecoverySecretLeak(t, body, codes[0])
	if !clientHasSessionCookie(t, client, ts.URL) {
		t.Fatal("successful recovery login did not establish a session")
	}
	consumed, err := st.ConsumeRecoveryCode(context.Background(), verifiedUserID(t, verified), codes[0])
	if err != nil {
		t.Fatal(err)
	}
	if consumed {
		t.Fatal("successful login left the recovery code unused")
	}

	replayClient := newCookieClient(t)
	replayToken, _ := loginRequires2FA(t, replayClient, ts.URL, "2fa-enable@example.com", "password123")
	replay, replayBody := doJSON(t, replayClient, http.MethodPost, ts.URL+"/api/auth/login/2fa", map[string]any{
		"tempToken": replayToken,
		"code":      codes[0],
	}, nil)
	if replay.StatusCode != http.StatusUnauthorized || clientHasSessionCookie(t, replayClient, ts.URL) {
		t.Fatalf("reused recovery code status=%d session=%v body=%s", replay.StatusCode, clientHasSessionCookie(t, replayClient, ts.URL), replayBody)
	}
}

func verifiedUserID(t *testing.T, verified map[string]any) int64 {
	t.Helper()
	id, ok := verified["id"].(float64)
	if !ok || id <= 0 {
		t.Fatalf("login 2FA user id: %+v", verified)
	}
	return int64(id)
}

func TestLogin2FATOTPDoesNotConsumeRecoveryCodes(t *testing.T) {
	ts, _, st, owner, userID := new2FAHTTPFixture(t)
	setupToken, secret := setupTwoFactor(t, owner, ts.URL)
	codes := enableTwoFactor(t, owner, ts.URL, setupToken, secret)

	client := newCookieClient(t)
	tempToken, _ := loginRequires2FA(t, client, ts.URL, "2fa-enable@example.com", "password123")
	var verified map[string]any
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/login/2fa", map[string]any{
		"tempToken": tempToken,
		"code":      currentTOTP(t, secret),
	}, &verified)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("TOTP login 2FA status=%d body=%s", resp.StatusCode, body)
	}
	assertNoRecoverySecretLeak(t, body, codes[0])
	if !clientHasSessionCookie(t, client, ts.URL) {
		t.Fatal("successful TOTP login did not establish a session")
	}
	consumed, err := st.ConsumeRecoveryCode(context.Background(), userID, codes[0])
	if err != nil || !consumed {
		t.Fatalf("TOTP login consumed recovery: consumed=%v err=%v", consumed, err)
	}
}

func TestLogin2FAExpiredPendingCannotEstablishSession(t *testing.T) {
	ts, sqlDB, _, owner, _ := new2FAHTTPFixture(t)
	setupToken, secret := setupTwoFactor(t, owner, ts.URL)
	codes := enableTwoFactor(t, owner, ts.URL, setupToken, secret)

	client := newCookieClient(t)
	tempToken, _ := loginRequires2FA(t, client, ts.URL, "2fa-enable@example.com", "password123")
	if _, err := sqlDB.Exec(`UPDATE login_2fa_pending SET expires_at = 0`); err != nil {
		t.Fatalf("expire pending login: %v", err)
	}
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/login/2fa", map[string]any{
		"tempToken": tempToken,
		"code":      codes[0],
	}, nil)
	if resp.StatusCode != http.StatusUnauthorized || clientHasSessionCookie(t, client, ts.URL) {
		t.Fatalf("expired pending login status=%d session=%v body=%s", resp.StatusCode, clientHasSessionCookie(t, client, ts.URL), body)
	}
}

func TestLogin2FAOldPlaintextAfterRegenDoesNotEstablishSession(t *testing.T) {
	ts, _, st, owner, userID := new2FAHTTPFixture(t)
	setupToken, secret := setupTwoFactor(t, owner, ts.URL)
	original := enableTwoFactor(t, owner, ts.URL, setupToken, secret)

	client := newCookieClient(t)
	tempToken, _ := loginRequires2FA(t, client, ts.URL, "2fa-enable@example.com", "password123")
	replacement := store.GenerateRecoveryCodes(store.RecoveryCodeCount)
	if err := st.ReplaceRecoveryCodes(context.Background(), userID, replacement, store.RecoveryCodeProof{}); err != nil {
		t.Fatalf("ReplaceRecoveryCodes: %v", err)
	}

	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/login/2fa", map[string]any{
		"tempToken": tempToken,
		"code":      original[1],
	}, nil)
	if resp.StatusCode != http.StatusUnauthorized || clientHasSessionCookie(t, client, ts.URL) {
		t.Fatalf("old recovery plaintext status=%d session=%v body=%s", resp.StatusCode, clientHasSessionCookie(t, client, ts.URL), body)
	}
	consumed, err := st.ConsumeRecoveryCode(context.Background(), userID, replacement[0])
	if err != nil || !consumed {
		t.Fatalf("replacement code consumed=%v err=%v", consumed, err)
	}
}

func TestLogin2FASixthAttemptInvalidatesChallenge(t *testing.T) {
	ts, _, st, _, userID := new2FAHTTPFixture(t)
	ctx := context.Background()
	encrypted, err := st.EncryptTOTPSecret([]byte(testTOTPSecret))
	if err != nil {
		t.Fatalf("encrypt TOTP secret: %v", err)
	}
	if err := st.SetUserTwoFactor(ctx, userID, encrypted); err != nil {
		t.Fatalf("SetUserTwoFactor: %v", err)
	}
	if err := st.AddRecoveryCodes(ctx, userID, []string{"ABCD-EFGH"}); err != nil {
		t.Fatalf("AddRecoveryCodes: %v", err)
	}

	client := newCookieClient(t)
	tempToken, _ := loginRequires2FA(t, client, ts.URL, "2fa-enable@example.com", "password123")
	for i := 0; i < 5; i++ {
		resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/login/2fa", map[string]any{
			"tempToken": tempToken,
			"code":      "not-a-valid-code",
		}, nil)
		if resp.StatusCode != http.StatusUnauthorized || clientHasSessionCookie(t, client, ts.URL) {
			t.Fatalf("attempt %d status=%d session=%v body=%s", i+1, resp.StatusCode, clientHasSessionCookie(t, client, ts.URL), body)
		}
		if !strings.Contains(string(body), `"message":"invalid code"`) {
			t.Fatalf("attempt %d body=%s, want invalid code", i+1, body)
		}
	}
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/login/2fa", map[string]any{
		"tempToken": tempToken,
		"code":      "not-a-valid-code",
	}, nil)
	if resp.StatusCode != http.StatusUnauthorized || clientHasSessionCookie(t, client, ts.URL) {
		t.Fatalf("sixth attempt status=%d session=%v body=%s", resp.StatusCode, clientHasSessionCookie(t, client, ts.URL), body)
	}
	if !strings.Contains(string(body), "too many attempts; please sign in again") {
		t.Fatalf("sixth attempt body=%s, want too many attempts; please sign in again", body)
	}
}

func TestLogin2FAJSONDoesNotLeakSecrets(t *testing.T) {
	raw, err := json.Marshal(userToJSON(store.User{ID: 1, Email: "a@b.com", Name: "A"}))
	if err != nil {
		t.Fatal(err)
	}
	assertNoRecoverySecretLeak(t, raw, "ABCD-EFGH")
}

func sessionCountForUserID(t *testing.T, sqlDB *sql.DB, userID int64) int {
	t.Helper()
	var n int
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM sessions WHERE user_id = ?`, userID).Scan(&n); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	return n
}

func TestLogin2FAAssignmentFailureStillEstablishesSession(t *testing.T) {
	ts, sqlDB, st, owner, userID := new2FAHTTPFixture(t)
	setupToken, secret := setupTwoFactor(t, owner, ts.URL)
	codes := enableTwoFactor(t, owner, ts.URL, setupToken, secret)

	nowMs := time.Now().UTC().UnixMilli()
	if _, err := sqlDB.Exec(`INSERT INTO projects(name, image, slug, last_activity_at, expires_at, created_at, updated_at) VALUES (?, ?, ?, ?, NULL, ?, ?)`,
		"unowned", "/scrumboy.png", "unowned-login2fa", nowMs, nowMs, nowMs); err != nil {
		t.Fatalf("insert unowned project: %v", err)
	}
	if _, err := sqlDB.Exec(`CREATE TRIGGER fail_login_2fa_assign BEFORE UPDATE ON projects BEGIN SELECT RAISE(ABORT, 'injected assignment failure'); END`); err != nil {
		t.Fatalf("install assignment trigger: %v", err)
	}

	before := sessionCountForUserID(t, sqlDB, userID)
	client := newCookieClient(t)
	tempToken, _ := loginRequires2FA(t, client, ts.URL, "2fa-enable@example.com", "password123")
	var verified map[string]any
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/login/2fa", map[string]any{
		"tempToken": tempToken,
		"code":      codes[0],
	}, &verified)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("assignment failure status=%d body=%s", resp.StatusCode, body)
	}
	if !clientHasSessionCookie(t, client, ts.URL) {
		t.Fatal("assignment failure withheld the session cookie")
	}
	if sessionCountForUserID(t, sqlDB, userID) != before+1 {
		t.Fatalf("sessions=%d, want %d", sessionCountForUserID(t, sqlDB, userID), before+1)
	}
	consumed, err := st.ConsumeRecoveryCode(context.Background(), userID, codes[0])
	if err != nil || consumed {
		t.Fatalf("assignment failure left recovery unused: consumed=%v err=%v", consumed, err)
	}
	var ownerID sql.NullInt64
	if err := sqlDB.QueryRow(`SELECT owner_user_id FROM projects WHERE slug = 'unowned-login2fa'`).Scan(&ownerID); err != nil {
		t.Fatalf("read project owner: %v", err)
	}
	if ownerID.Valid {
		t.Fatalf("assignment trigger still assigned owner=%d", ownerID.Int64)
	}
}

func TestLogin2FAConcurrentRecoveryCreatesOneSession(t *testing.T) {
	ts, sqlDB, st, owner, userID := new2FAHTTPFixture(t)
	setupToken, secret := setupTwoFactor(t, owner, ts.URL)
	codes := enableTwoFactor(t, owner, ts.URL, setupToken, secret)

	loginClient := newCookieClient(t)
	tempToken, _ := loginRequires2FA(t, loginClient, ts.URL, "2fa-enable@example.com", "password123")
	before := sessionCountForUserID(t, sqlDB, userID)

	type result struct {
		status    int
		body      []byte
		hasCookie bool
		err       error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := newCookieClient(t)
			payload, err := json.Marshal(map[string]any{"tempToken": tempToken, "code": codes[0]})
			if err != nil {
				results <- result{err: err}
				return
			}
			req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/auth/login/2fa", bytes.NewReader(payload))
			if err != nil {
				results <- result{err: err}
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Scrumboy", "1")
			<-start
			resp, err := client.Do(req)
			if err != nil {
				results <- result{err: err}
				return
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			results <- result{
				status:    resp.StatusCode,
				body:      body,
				hasCookie: clientHasSessionCookie(t, client, ts.URL),
			}
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	var okCount, unauthorized int
	var loserBody []byte
	for res := range results {
		if res.err != nil {
			t.Fatalf("concurrent login 2FA: %v", res.err)
		}
		switch res.status {
		case http.StatusOK:
			okCount++
			if !res.hasCookie {
				t.Fatalf("winning callback missing session cookie: %s", res.body)
			}
		case http.StatusUnauthorized:
			unauthorized++
			if res.hasCookie {
				t.Fatalf("losing callback established a session: %s", res.body)
			}
			loserBody = res.body
		default:
			t.Fatalf("unexpected concurrent status=%d body=%s", res.status, res.body)
		}
	}
	if okCount != 1 || unauthorized != 1 {
		t.Fatalf("concurrent login 2FA ok=%d unauthorized=%d, want 1/1", okCount, unauthorized)
	}
	loserMsg := string(loserBody)
	if !strings.Contains(loserMsg, "invalid or expired code") && !strings.Contains(loserMsg, `"message":"invalid code"`) {
		t.Fatalf("losing callback body=%s, want existing 401 contract", loserBody)
	}
	if sessionCountForUserID(t, sqlDB, userID) != before+1 {
		t.Fatalf("concurrent sessions=%d, want %d", sessionCountForUserID(t, sqlDB, userID), before+1)
	}
	consumed, err := st.ConsumeRecoveryCode(context.Background(), userID, codes[0])
	if err != nil || consumed {
		t.Fatalf("losing concurrent callback consumed or left proof unused: consumed=%v err=%v", consumed, err)
	}
}
