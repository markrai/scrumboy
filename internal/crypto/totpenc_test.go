package crypto

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func TestEncryptDecryptRoundtrip(t *testing.T) {
	key, err := DecodeKey("YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXoxMjM0NTY=") // 32 bytes base64
	if err != nil {
		t.Fatalf("decode key: %v", err)
	}
	plaintext := []byte("secret-totp-key-12345")
	encrypted, err := EncryptSecret(key, plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if encrypted == "" || encrypted[:3] != "v1:" {
		t.Fatalf("expected v1: prefix, got %q", encrypted)
	}
	decrypted, err := DecryptSecret(key, encrypted)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("decrypted %q != plaintext %q", decrypted, plaintext)
	}

	wrapped, err := EncryptTOTPSecret(key, plaintext)
	if err != nil {
		t.Fatalf("EncryptTOTPSecret: %v", err)
	}
	viaWrapper, err := DecryptTOTPSecret(key, wrapped)
	if err != nil {
		t.Fatalf("DecryptTOTPSecret: %v", err)
	}
	if !bytes.Equal(viaWrapper, plaintext) {
		t.Fatalf("wrapper decrypted %q != plaintext %q", viaWrapper, plaintext)
	}
}

func TestDecryptWrongKey(t *testing.T) {
	key, _ := DecodeKey("YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXoxMjM0NTY=")
	plaintext := []byte("secret")
	encrypted, err := EncryptTOTPSecret(key, plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	wrongKey, _ := DecodeKey("eHl6YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXo=")
	_, err = DecryptTOTPSecret(wrongKey, encrypted)
	if err == nil {
		t.Fatal("expected decrypt to fail with wrong key")
	}
}

func TestDecryptTampered(t *testing.T) {
	key, err := DecodeKey("YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXoxMjM0NTY=")
	if err != nil {
		t.Fatalf("decode key: %v", err)
	}
	plaintext := []byte("secret")
	encrypted, err := EncryptTOTPSecret(key, plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if !strings.HasPrefix(encrypted, "v1:") {
		t.Fatalf("expected v1: prefix, got %q", encrypted)
	}
	combined, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(encrypted, "v1:"))
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if len(combined) <= nonceLen {
		t.Fatalf("payload too short for nonce and ciphertext: %d bytes", len(combined))
	}
	original := append([]byte(nil), combined...)
	combined[nonceLen] ^= 0x01
	if bytes.Equal(combined, original) {
		t.Fatal("tampering did not change the encrypted bytes")
	}
	tampered := "v1:" + base64.RawURLEncoding.EncodeToString(combined)
	_, err = DecryptTOTPSecret(key, tampered)
	if !errors.Is(err, ErrDecrypt) {
		t.Fatalf("expected ErrDecrypt, got %v", err)
	}
}
