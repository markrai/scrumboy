package crypto

import (
	"bytes"
	"testing"
)

func TestEncryptDecryptRoundtrip(t *testing.T) {
	key, err := DecodeKey("YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXoxMjM0NTY=") // 32 bytes base64
	if err != nil {
		t.Fatalf("decode key: %v", err)
	}
	plaintext := []byte("secret-totp-key-12345")
	encrypted, err := EncryptTOTPSecret(key, plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if encrypted == "" || encrypted[:3] != "v1:" {
		t.Fatalf("expected v1: prefix, got %q", encrypted)
	}
	decrypted, err := DecryptTOTPSecret(key, encrypted)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("decrypted %q != plaintext %q", decrypted, plaintext)
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
	key, _ := DecodeKey("YWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXoxMjM0NTY=")
	plaintext := []byte("secret")
	encrypted, _ := EncryptTOTPSecret(key, plaintext)
	tampered := encrypted[:len(encrypted)-2] + "xx"
	_, err := DecryptTOTPSecret(key, tampered)
	if err == nil {
		t.Fatal("expected decrypt to fail with tampered ciphertext")
	}
}
