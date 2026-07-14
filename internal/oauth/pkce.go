package oauth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
)

// VerifyPKCE checks a presented code_verifier against the code_challenge recorded
// at /oauth/authorize time. Only the S256 method is supported: OAuth 2.1 mandates
// S256-only PKCE, and MCP clients (Claude Code included) always use it. The
// "plain" method (RFC 7636 §4.2) is deliberately rejected.
func VerifyPKCE(method, verifier, challenge string) bool {
	if method != "S256" || verifier == "" || challenge == "" {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	computed := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(computed), []byte(challenge)) == 1
}
