package crypto

import (
	"fmt"
	"time"

	"github.com/pquerna/otp/totp"
)

const (
	totpPeriod = 30
	totpSkew   = 1
	totpDigits = 6
	issuer     = "Scrumboy"
)

// GenerateTOTPSecret creates a new TOTP key for the given account (email).
// Returns the raw secret string, otpauth URI, and manual entry key.
func GenerateTOTPSecret(accountName string) (secret string, otpauthURI string, manualEntryKey string, err error) {
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: accountName,
		Period:      totpPeriod,
		Digits:      totpDigits,
	})
	if err != nil {
		return "", "", "", fmt.Errorf("totp generate: %w", err)
	}
	return key.Secret(), key.URL(), key.Secret(), nil
}

// ValidateTOTPCode validates a 6-digit code against the secret with ±1 step skew.
func ValidateTOTPCode(secret string, code string) bool {
	valid, _ := totp.ValidateCustom(code, secret, time.Now(), totp.ValidateOpts{
		Period: totpPeriod,
		Skew:   totpSkew,
		Digits: totpDigits,
	})
	return valid
}

// ValidateTOTPCodeCustom validates with explicit skew (for testing).
func ValidateTOTPCodeCustom(secret string, code string, t time.Time, skew uint) bool {
	valid, _ := totp.ValidateCustom(code, secret, t, totp.ValidateOpts{
		Period: totpPeriod,
		Skew:   skew,
		Digits: totpDigits,
	})
	return valid
}
