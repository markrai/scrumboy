package tokens

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	// TokenExpiry is how long a password reset token is valid.
	TokenExpiry = 30 * time.Minute
	// ClockSkew allows tokens up to 5 minutes in the future (server time drift).
	ClockSkew = 5 * time.Minute
)

// GeneratePasswordResetToken creates a one-time reset token for the user.
// Token format: base64url(userID | timestamp | signature).
// Signature = HMAC-SHA256(secret, userID | timestamp | password_hash).
// Expected token length: ~90-110 characters. Normal — do not attempt to shorten.
func GeneratePasswordResetToken(secret []byte, userID int64, passwordHash string) (token string, expiresAt time.Time, err error) {
	if len(secret) == 0 {
		return "", time.Time{}, fmt.Errorf("secret required")
	}
	now := time.Now().UTC()
	ts := now.Unix()
	expiresAt = now.Add(TokenExpiry)

	payload := fmt.Sprintf("%d|%d", userID, ts)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	mac.Write([]byte("|"))
	mac.Write([]byte(passwordHash))
	sig := mac.Sum(nil)
	sigHex := fmt.Sprintf("%x", sig)

	tokenPayload := payload + "|" + sigHex
	token = base64.RawURLEncoding.EncodeToString([]byte(tokenPayload))
	return token, expiresAt, nil
}

// ParsePasswordResetToken decodes the token and returns userID, timestamp, and signature.
// No store interaction. Returns error if token format is invalid.
func ParsePasswordResetToken(token string) (userID int64, timestamp int64, signature []byte, err error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return 0, 0, nil, fmt.Errorf("empty token")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return 0, 0, nil, fmt.Errorf("invalid token encoding")
	}
	parts := strings.SplitN(string(decoded), "|", 3)
	if len(parts) != 3 {
		return 0, 0, nil, fmt.Errorf("invalid token format")
	}
	userID, err = strconv.ParseInt(parts[0], 10, 64)
	if err != nil || userID <= 0 {
		return 0, 0, nil, fmt.Errorf("invalid user id in token")
	}
	timestamp, err = strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, 0, nil, fmt.Errorf("invalid timestamp in token")
	}
	// Signature is hex-encoded (64 hex chars = 32 bytes)
	signature, err = hex.DecodeString(parts[2])
	if err != nil || len(signature) != 32 {
		return 0, 0, nil, fmt.Errorf("invalid signature")
	}
	return userID, timestamp, signature, nil
}

// VerifyPasswordResetToken verifies the signature and checks expiry.
// Uses timing-safe comparison for signature. Caller fetches passwordHash from store.
func VerifyPasswordResetToken(secret []byte, userID int64, timestamp int64, signature []byte, passwordHash string) error {
	if len(secret) == 0 {
		return fmt.Errorf("secret required")
	}
	now := time.Now().UTC().Unix()
	// Expired: token timestamp + 30 min < now (with clock skew: allow up to 5 min in future)
	if timestamp+int64(TokenExpiry.Seconds()) < now {
		return fmt.Errorf("token expired")
	}
	if timestamp > now+int64(ClockSkew.Seconds()) {
		return fmt.Errorf("token not yet valid")
	}
	payload := fmt.Sprintf("%d|%d", userID, timestamp)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(payload))
	mac.Write([]byte("|"))
	mac.Write([]byte(passwordHash))
	expectedSig := mac.Sum(nil)
	if len(signature) != 32 {
		return fmt.Errorf("invalid signature")
	}
	if !hmac.Equal(expectedSig, signature) {
		return fmt.Errorf("signature mismatch")
	}
	return nil
}
