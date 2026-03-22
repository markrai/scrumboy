package crypto

import (
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

func TestValidateTOTPCode(t *testing.T) {
	secret, otpauthURI, manualKey, err := GenerateTOTPSecret("test@example.com")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if secret == "" || otpauthURI == "" || manualKey == "" {
		t.Fatal("expected non-empty secret, uri, manualKey")
	}
	if secret != manualKey {
		t.Fatalf("secret %q != manualKey %q", secret, manualKey)
	}

	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	if !ValidateTOTPCode(secret, code) {
		t.Fatalf("expected valid code %q to pass", code)
	}

	if ValidateTOTPCode(secret, "000000") {
		t.Fatal("expected wrong code to fail")
	}
}

func TestValidateTOTPCodeCustomSkew(t *testing.T) {
	secret, _, _, err := GenerateTOTPSecret("test@example.com")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	now := time.Now()
	code, err := totp.GenerateCode(secret, now)
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	if !ValidateTOTPCodeCustom(secret, code, now, 1) {
		t.Fatal("expected code to pass with skew 1")
	}
}
