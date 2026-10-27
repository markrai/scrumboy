package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

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

func TestLogin2FAJSONDoesNotLeakSecrets(t *testing.T) {
	raw, err := json.Marshal(userToJSON(store.User{ID: 1, Email: "a@b.com", Name: "A"}))
	if err != nil {
		t.Fatal(err)
	}
	assertNoRecoverySecretLeak(t, raw, "ABCD-EFGH")
}
