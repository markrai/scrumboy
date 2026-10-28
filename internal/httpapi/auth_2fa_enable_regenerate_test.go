package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"scrumboy/internal/store"
)

const testTOTPSecret = "JBSWY3DPEHPK3PXP"

func new2FAHTTPFixture(t *testing.T) (*httptest.Server, *sql.DB, *store.Store, *http.Client, int64) {
	t.Helper()
	ts, sqlDB, cleanup := newTestHTTPServerWithOptions(t, Options{
		EncryptionKey: testEncryptionKey,
		ScrumboyMode:  "full",
	})
	t.Cleanup(cleanup)
	st := store.New(sqlDB, &store.StoreOptions{EncryptionKey: testEncryptionKey})
	client := newCookieClient(t)
	user := bootstrapUserClient(t, client, ts.URL, "TwoFA", "2fa-enable@example.com", "password123")
	id, ok := user["id"].(float64)
	if !ok || id <= 0 {
		t.Fatalf("bootstrap user id: %+v", user)
	}
	return ts, sqlDB, st, client, int64(id)
}

func currentTOTP(t *testing.T, secret string) string {
	t.Helper()
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("GenerateCode: %v", err)
	}
	return code
}

func setupTwoFactor(t *testing.T, client *http.Client, baseURL string) (setupToken, secret string) {
	t.Helper()
	var out map[string]any
	resp, body := doJSON(t, client, http.MethodPost, baseURL+"/api/auth/2fa/setup", nil, &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setup status=%d body=%s", resp.StatusCode, body)
	}
	token, _ := out["setupToken"].(string)
	secret, _ = out["manualEntryKey"].(string)
	if token == "" || secret == "" {
		t.Fatalf("setup response missing token/secret: %+v", out)
	}
	return token, secret
}

func enableTwoFactor(t *testing.T, client *http.Client, baseURL, setupToken, secret string) []string {
	t.Helper()
	var out map[string]any
	resp, body := doJSON(t, client, http.MethodPost, baseURL+"/api/auth/2fa/enable", map[string]any{
		"setupToken": setupToken,
		"code":       currentTOTP(t, secret),
	}, &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("enable status=%d body=%s", resp.StatusCode, body)
	}
	return recoveryCodesFrom(t, out)
}

func recoveryCodesFrom(t *testing.T, out map[string]any) []string {
	t.Helper()
	raw, ok := out["recoveryCodes"].([]any)
	if !ok || len(raw) != store.RecoveryCodeCount {
		t.Fatalf("recoveryCodes = %+v", out["recoveryCodes"])
	}
	codes := make([]string, len(raw))
	for i, v := range raw {
		code, ok := v.(string)
		if !ok || code == "" {
			t.Fatalf("recovery code %d = %#v", i, v)
		}
		codes[i] = code
	}
	return codes
}

func countUserRecoveryCodes(t *testing.T, sqlDB *sql.DB, userID int64) (total, unused int) {
	t.Helper()
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM user_recovery_codes WHERE user_id = ?`, userID).Scan(&total); err != nil {
		t.Fatalf("count recovery codes: %v", err)
	}
	if err := sqlDB.QueryRow(`SELECT COUNT(*) FROM user_recovery_codes WHERE user_id = ? AND used_at IS NULL`, userID).Scan(&unused); err != nil {
		t.Fatalf("count unused recovery codes: %v", err)
	}
	return total, unused
}

func userTwoFactorFlag(t *testing.T, sqlDB *sql.DB, userID int64) bool {
	t.Helper()
	var enabled int
	if err := sqlDB.QueryRow(`SELECT two_factor_enabled FROM users WHERE id = ?`, userID).Scan(&enabled); err != nil {
		t.Fatalf("two_factor_enabled: %v", err)
	}
	return enabled == 1
}

func TestAuth2FAEnableIsAtomicAndRejectsReplay(t *testing.T) {
	ts, sqlDB, _, client, userID := new2FAHTTPFixture(t)
	setupToken, secret := setupTwoFactor(t, client, ts.URL)
	codes := enableTwoFactor(t, client, ts.URL, setupToken, secret)
	if !userTwoFactorFlag(t, sqlDB, userID) {
		t.Fatal("expected 2FA enabled")
	}
	total, unused := countUserRecoveryCodes(t, sqlDB, userID)
	if total != store.RecoveryCodeCount || unused != store.RecoveryCodeCount {
		t.Fatalf("recovery codes total=%d unused=%d", total, unused)
	}

	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/2fa/enable", map[string]any{
		"setupToken": setupToken,
		"code":       currentTOTP(t, secret),
	}, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("replay status=%d body=%s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), "recoveryCodes") {
		t.Fatalf("replay leaked recovery codes: %s", body)
	}
	total, unused = countUserRecoveryCodes(t, sqlDB, userID)
	if total != store.RecoveryCodeCount || unused != store.RecoveryCodeCount {
		t.Fatalf("replay mutated codes total=%d unused=%d", total, unused)
	}
	if len(codes) != store.RecoveryCodeCount {
		t.Fatalf("issued %d codes", len(codes))
	}
}

func TestAuth2FAEnableRollsBackAndDoesNotReturnCodes(t *testing.T) {
	ts, sqlDB, _, client, userID := new2FAHTTPFixture(t)
	setupToken, secret := setupTwoFactor(t, client, ts.URL)
	if _, err := sqlDB.Exec(`CREATE TRIGGER fail_enable_recovery BEFORE INSERT ON user_recovery_codes BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
		t.Fatalf("install trigger: %v", err)
	}

	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/2fa/enable", map[string]any{
		"setupToken": setupToken,
		"code":       currentTOTP(t, secret),
	}, nil)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("failed enable status=%d body=%s", resp.StatusCode, body)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, body)
	}
	if _, ok := payload["recoveryCodes"]; ok {
		t.Fatalf("failed enable leaked recovery codes: %s", body)
	}
	if userTwoFactorFlag(t, sqlDB, userID) {
		t.Fatal("2FA enabled after failed insert")
	}
	total, _ := countUserRecoveryCodes(t, sqlDB, userID)
	if total != 0 {
		t.Fatalf("recovery codes after failed enable = %d", total)
	}
}

func TestAuth2FARecoveryRegenerateRequiresProof(t *testing.T) {
	ts, sqlDB, _, client, userID := new2FAHTTPFixture(t)
	setupToken, secret := setupTwoFactor(t, client, ts.URL)
	original := enableTwoFactor(t, client, ts.URL, setupToken, secret)

	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/2fa/recovery/regenerate", map[string]any{}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty code status=%d body=%s", resp.StatusCode, body)
	}

	resp, body = doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/2fa/recovery/regenerate", map[string]any{
		"code": "000000",
	}, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid totp status=%d body=%s", resp.StatusCode, body)
	}
	total, unused := countUserRecoveryCodes(t, sqlDB, userID)
	if total != store.RecoveryCodeCount || unused != store.RecoveryCodeCount {
		t.Fatalf("invalid totp mutated codes total=%d unused=%d", total, unused)
	}

	var out map[string]any
	resp, body = doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/2fa/recovery/regenerate", map[string]any{
		"code": currentTOTP(t, secret),
	}, &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("totp regen status=%d body=%s", resp.StatusCode, body)
	}
	replaced := recoveryCodesFrom(t, out)
	if len(replaced) != store.RecoveryCodeCount {
		t.Fatalf("replaced %d codes", len(replaced))
	}
	if replaced[0] == original[0] && replaced[1] == original[1] {
		t.Fatal("regenerate returned the original recovery-code set")
	}
}

func TestAuth2FARecoveryRegenerateAcceptsUnusedRecoveryCode(t *testing.T) {
	ts, sqlDB, _, client, userID := new2FAHTTPFixture(t)
	setupToken, secret := setupTwoFactor(t, client, ts.URL)
	original := enableTwoFactor(t, client, ts.URL, setupToken, secret)

	var out map[string]any
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/2fa/recovery/regenerate", map[string]any{
		"code": original[0],
	}, &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("recovery regen status=%d body=%s", resp.StatusCode, body)
	}
	replaced := recoveryCodesFrom(t, out)
	total, unused := countUserRecoveryCodes(t, sqlDB, userID)
	if total != store.RecoveryCodeCount || unused != store.RecoveryCodeCount {
		t.Fatalf("after recovery regen total=%d unused=%d", total, unused)
	}

	resp, body = doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/2fa/recovery/regenerate", map[string]any{
		"code": original[0],
	}, nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("reused recovery status=%d body=%s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), "recoveryCodes") {
		t.Fatalf("reused recovery leaked codes: %s", body)
	}
	if replaced[0] == original[0] {
		t.Fatal("replacement set reused the consumed proof code")
	}
}

func TestAuth2FARecoveryRegenerateRequiresActive2FA(t *testing.T) {
	ts, _, _, client, _ := new2FAHTTPFixture(t)
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/2fa/recovery/regenerate", map[string]any{
		"code": "123456",
	}, nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing 2FA status=%d body=%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "two_factor_not_enabled") && !strings.Contains(string(body), "not enabled") {
		t.Fatalf("expected two-factor not enabled error, body=%s", body)
	}
}

func TestAuth2FARecoveryRegenerateWorksForSSOOnlyUser(t *testing.T) {
	ts, sqlDB, _, client, userID := new2FAHTTPFixture(t)
	setupToken, secret := setupTwoFactor(t, client, ts.URL)
	_ = enableTwoFactor(t, client, ts.URL, setupToken, secret)
	if _, err := sqlDB.Exec(`UPDATE users SET password_hash = NULL WHERE id = ?`, userID); err != nil {
		t.Fatalf("clear local password: %v", err)
	}

	var out map[string]any
	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/2fa/recovery/regenerate", map[string]any{
		"code": currentTOTP(t, secret),
	}, &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sso-only regen status=%d body=%s", resp.StatusCode, body)
	}
	_ = recoveryCodesFrom(t, out)
}

func TestAuth2FARecoveryRegenerateRollsBackAndDoesNotReturnCodes(t *testing.T) {
	ts, sqlDB, _, client, userID := new2FAHTTPFixture(t)
	setupToken, secret := setupTwoFactor(t, client, ts.URL)
	original := enableTwoFactor(t, client, ts.URL, setupToken, secret)
	if _, err := sqlDB.Exec(`CREATE TRIGGER fail_regen_recovery BEFORE INSERT ON user_recovery_codes BEGIN SELECT RAISE(ABORT, 'injected failure'); END`); err != nil {
		t.Fatalf("install trigger: %v", err)
	}

	resp, body := doJSON(t, client, http.MethodPost, ts.URL+"/api/auth/2fa/recovery/regenerate", map[string]any{
		"code": currentTOTP(t, secret),
	}, nil)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("failed regen status=%d body=%s", resp.StatusCode, body)
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, body)
	}
	if _, ok := payload["recoveryCodes"]; ok {
		t.Fatalf("failed regen leaked recovery codes: %s", body)
	}
	total, unused := countUserRecoveryCodes(t, sqlDB, userID)
	if total != store.RecoveryCodeCount || unused != store.RecoveryCodeCount {
		t.Fatalf("codes after failed regen total=%d unused=%d", total, unused)
	}

	if _, err := sqlDB.Exec(`DROP TRIGGER fail_regen_recovery`); err != nil {
		t.Fatalf("drop trigger: %v", err)
	}
	consumed, err := store.New(sqlDB, &store.StoreOptions{EncryptionKey: testEncryptionKey}).ConsumeRecoveryCode(context.Background(), userID, original[0])
	if err != nil {
		t.Fatalf("ConsumeRecoveryCode: %v", err)
	}
	if !consumed {
		t.Fatal("original recovery codes were replaced despite rollback")
	}
}
