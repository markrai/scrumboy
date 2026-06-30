package tokens

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"
)

func encodeTokenPayload(payload string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(payload))
}

func mustParseGeneratedToken(t *testing.T, token string) (int64, int64, []byte) {
	t.Helper()

	userID, timestamp, signature, err := ParsePasswordResetToken(token)
	if err != nil {
		t.Fatalf("ParsePasswordResetToken(%q): %v", token, err)
	}
	return userID, timestamp, signature
}

func signPasswordResetToken(secret []byte, userID int64, timestamp int64, passwordHash string) []byte {
	payload := fmt.Sprintf("%d|%d", userID, timestamp)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	mac.Write([]byte("|"))
	mac.Write([]byte(passwordHash))
	return mac.Sum(nil)
}

func TestGeneratePasswordResetToken_EmptySecret(t *testing.T) {
	token, expiresAt, err := GeneratePasswordResetToken(nil, 42, "hash")
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "secret required" {
		t.Fatalf("error = %q, want secret required", err.Error())
	}
	if token != "" {
		t.Fatalf("token = %q, want empty", token)
	}
	if !expiresAt.IsZero() {
		t.Fatalf("expiresAt = %v, want zero", expiresAt)
	}
}

func TestGeneratePasswordResetToken_ValidToken(t *testing.T) {
	secret := []byte("test-secret")
	passwordHash := "current-password-hash"
	const userID int64 = 42

	before := time.Now().UTC()
	token, expiresAt, err := GeneratePasswordResetToken(secret, userID, passwordHash)
	after := time.Now().UTC()
	if err != nil {
		t.Fatalf("GeneratePasswordResetToken: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}

	minExpires := before.Add(TokenExpiry)
	maxExpires := after.Add(TokenExpiry)
	if expiresAt.Before(minExpires) || expiresAt.After(maxExpires) {
		t.Fatalf("expiresAt = %v, want between %v and %v", expiresAt, minExpires, maxExpires)
	}

	gotUserID, timestamp, signature := mustParseGeneratedToken(t, token)
	if gotUserID != userID {
		t.Fatalf("userID = %d, want %d", gotUserID, userID)
	}
	if timestamp == 0 {
		t.Fatal("timestamp = 0, want nonzero")
	}
	if timestamp < before.Add(-time.Second).Unix() || timestamp > after.Add(time.Second).Unix() {
		t.Fatalf("timestamp = %d, want current between %d and %d", timestamp, before.Add(-time.Second).Unix(), after.Add(time.Second).Unix())
	}
	if len(signature) != 32 {
		t.Fatalf("signature length = %d, want 32", len(signature))
	}
}

func TestParsePasswordResetToken_MalformedInputs(t *testing.T) {
	validSig := strings.Repeat("ab", 32)
	cases := []struct {
		name  string
		token string
	}{
		{name: "empty", token: ""},
		{name: "whitespace", token: " \t\n "},
		{name: "invalid base64", token: "!not-base64!"},
		{name: "too few parts", token: encodeTokenPayload("42|123")},
		{name: "nonnumeric user id", token: encodeTokenPayload("abc|123|" + validSig)},
		{name: "zero user id", token: encodeTokenPayload("0|123|" + validSig)},
		{name: "negative user id", token: encodeTokenPayload("-1|123|" + validSig)},
		{name: "nonnumeric timestamp", token: encodeTokenPayload("42|soon|" + validSig)},
		{name: "non-hex signature", token: encodeTokenPayload("42|123|" + strings.Repeat("z", 64))},
		{name: "wrong signature length", token: encodeTokenPayload("42|123|" + hex.EncodeToString([]byte("short")))},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			userID, timestamp, signature, err := ParsePasswordResetToken(tc.token)
			if err == nil {
				t.Fatalf("expected error, got userID=%d timestamp=%d signature=%x", userID, timestamp, signature)
			}
		})
	}
}

func TestVerifyPasswordResetToken_ValidGeneratedToken(t *testing.T) {
	secret := []byte("test-secret")
	passwordHash := "current-password-hash"
	const userID int64 = 42

	token, _, err := GeneratePasswordResetToken(secret, userID, passwordHash)
	if err != nil {
		t.Fatalf("GeneratePasswordResetToken: %v", err)
	}
	gotUserID, timestamp, signature := mustParseGeneratedToken(t, token)

	if err := VerifyPasswordResetToken(secret, gotUserID, timestamp, signature, passwordHash); err != nil {
		t.Fatalf("VerifyPasswordResetToken: %v", err)
	}
}

func TestVerifyPasswordResetToken_RejectsInvalidInputs(t *testing.T) {
	secret := []byte("test-secret")
	passwordHash := "current-password-hash"
	const userID int64 = 42
	now := time.Now().UTC().Unix()
	validSignature := signPasswordResetToken(secret, userID, now, passwordHash)

	mutatedSignature := append([]byte(nil), validSignature...)
	mutatedSignature[0] ^= 0xff

	expiredTimestamp := time.Now().UTC().Unix() - int64(TokenExpiry.Seconds()) - 60
	futureTimestamp := time.Now().UTC().Unix() + int64(ClockSkew.Seconds()) + 60

	cases := []struct {
		name         string
		secret       []byte
		userID       int64
		timestamp    int64
		signature    []byte
		passwordHash string
		wantMessage  string
	}{
		{
			name:         "empty secret",
			secret:       nil,
			userID:       userID,
			timestamp:    now,
			signature:    validSignature,
			passwordHash: passwordHash,
			wantMessage:  "secret required",
		},
		{
			name:         "wrong secret",
			secret:       []byte("wrong-secret"),
			userID:       userID,
			timestamp:    now,
			signature:    validSignature,
			passwordHash: passwordHash,
		},
		{
			name:         "wrong password hash",
			secret:       secret,
			userID:       userID,
			timestamp:    now,
			signature:    validSignature,
			passwordHash: "old-password-hash",
		},
		{
			name:         "wrong user id",
			secret:       secret,
			userID:       userID + 1,
			timestamp:    now,
			signature:    validSignature,
			passwordHash: passwordHash,
		},
		{
			name:         "mutated signature",
			secret:       secret,
			userID:       userID,
			timestamp:    now,
			signature:    mutatedSignature,
			passwordHash: passwordHash,
		},
		{
			name:         "expired timestamp",
			secret:       secret,
			userID:       userID,
			timestamp:    expiredTimestamp,
			signature:    signPasswordResetToken(secret, userID, expiredTimestamp, passwordHash),
			passwordHash: passwordHash,
		},
		{
			name:         "future timestamp beyond skew",
			secret:       secret,
			userID:       userID,
			timestamp:    futureTimestamp,
			signature:    signPasswordResetToken(secret, userID, futureTimestamp, passwordHash),
			passwordHash: passwordHash,
		},
		{
			name:         "non-32-byte signature",
			secret:       secret,
			userID:       userID,
			timestamp:    now,
			signature:    []byte("short"),
			passwordHash: passwordHash,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := VerifyPasswordResetToken(tc.secret, tc.userID, tc.timestamp, tc.signature, tc.passwordHash)
			if err == nil {
				t.Fatal("expected error")
			}
			if tc.wantMessage != "" && err.Error() != tc.wantMessage {
				t.Fatalf("error = %q, want %q", err.Error(), tc.wantMessage)
			}
		})
	}
}
