package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

const (
	keyLenBytes = 32 // AES-256
	nonceLen    = 12 // GCM standard nonce size
	version     = "v1"
)

var (
	ErrInvalidKey   = errors.New("encryption key must be 32 bytes (base64 decoded)")
	ErrDecrypt      = errors.New("decryption failed")
	ErrInvalidInput = errors.New("invalid encrypted input format")
)

// EncryptTOTPSecret encrypts plaintext with AES-256-GCM. Key must be 32 bytes.
// Returns format: v1:<base64url(nonce|ciphertext)> (no padding).
func EncryptTOTPSecret(key []byte, plaintext []byte) (string, error) {
	if len(key) != keyLenBytes {
		return "", ErrInvalidKey
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("aes new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("gcm: %w", err)
	}
	nonce := make([]byte, nonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("rand nonce: %w", err)
	}
	ciphertext := aead.Seal(nil, nonce, plaintext, nil)
	combined := append(nonce, ciphertext...)
	encoded := base64.RawURLEncoding.EncodeToString(combined)
	return version + ":" + encoded, nil
}

// DecryptTOTPSecret decrypts input in format v1:<base64url(nonce|ciphertext)>.
func DecryptTOTPSecret(key []byte, encrypted string) ([]byte, error) {
	if len(key) != keyLenBytes {
		return nil, ErrInvalidKey
	}
	parts := strings.SplitN(encrypted, ":", 2)
	if len(parts) != 2 || parts[0] != version {
		return nil, ErrInvalidInput
	}
	combined, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDecrypt, err)
	}
	if len(combined) < nonceLen {
		return nil, ErrDecrypt
	}
	nonce := combined[:nonceLen]
	ciphertext := combined[nonceLen:]
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes new cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrDecrypt
	}
	return plaintext, nil
}

// DecodeKey decodes a base64-encoded 32-byte key. Returns error if wrong length.
func DecodeKey(b64 string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		key, err = base64.RawURLEncoding.DecodeString(b64)
		if err != nil {
			return nil, fmt.Errorf("decode key: %w", err)
		}
	}
	if len(key) != keyLenBytes {
		return nil, ErrInvalidKey
	}
	return key, nil
}
