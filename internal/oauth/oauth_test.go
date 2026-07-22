package oauth

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func challengeFor(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func TestVerifyPKCE_S256Match(t *testing.T) {
	verifier := "test-verifier-value-1234567890"
	challenge := challengeFor(verifier)
	if !VerifyPKCE("S256", verifier, challenge) {
		t.Fatal("expected matching S256 verifier/challenge to verify")
	}
}

func TestVerifyPKCE_S256Mismatch(t *testing.T) {
	challenge := challengeFor("correct-verifier")
	if VerifyPKCE("S256", "wrong-verifier", challenge) {
		t.Fatal("expected mismatched verifier to fail")
	}
}

func TestVerifyPKCE_EmptyValues(t *testing.T) {
	if VerifyPKCE("S256", "", "somechallenge") {
		t.Fatal("expected empty verifier to fail")
	}
	if VerifyPKCE("S256", "someverifier", "") {
		t.Fatal("expected empty challenge to fail")
	}
}

func TestVerifyPKCE_PlainMethodRejected(t *testing.T) {
	verifier := "plain-verifier"
	if VerifyPKCE("plain", verifier, verifier) {
		t.Fatal("expected \"plain\" method to be rejected regardless of match")
	}
}

func TestVerifyPKCE_UnknownMethodRejected(t *testing.T) {
	verifier := "some-verifier"
	if VerifyPKCE("S512", verifier, challengeFor(verifier)) {
		t.Fatal("expected unsupported method to be rejected")
	}
}

func TestGenerateClientID(t *testing.T) {
	id1, err := GenerateClientID()
	if err != nil {
		t.Fatalf("GenerateClientID: %v", err)
	}
	id2, err := GenerateClientID()
	if err != nil {
		t.Fatalf("GenerateClientID: %v", err)
	}
	if id1 == "" || id2 == "" {
		t.Fatal("expected non-empty client ids")
	}
	if id1 == id2 {
		t.Fatal("expected unique client ids across calls")
	}
	if len(id1) < 4 || id1[:3] != "oc_" {
		t.Fatalf("expected client id to have oc_ prefix, got %q", id1)
	}
}

func TestGenerateOpaqueSecret(t *testing.T) {
	s1, err := GenerateOpaqueSecret()
	if err != nil {
		t.Fatalf("GenerateOpaqueSecret: %v", err)
	}
	s2, err := GenerateOpaqueSecret()
	if err != nil {
		t.Fatalf("GenerateOpaqueSecret: %v", err)
	}
	if s1 == "" || s2 == "" {
		t.Fatal("expected non-empty secrets")
	}
	if s1 == s2 {
		t.Fatal("expected unique secrets across calls")
	}
}
