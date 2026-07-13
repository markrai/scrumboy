// Package oauth implements the pure, DB-independent parts of a minimal OAuth
// 2.1 authorization server for Scrumboy's MCP endpoint: PKCE verification,
// opaque secret generation, and the OAuth error wire format. DB-backed
// persistence (clients, codes, tokens) lives in internal/store/oauth.go;
// HTTP glue lives in internal/httpapi/routing_oauth.go.
package oauth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

// GenerateClientID returns a new public client identifier for dynamic client
// registration (RFC 7591). Prefixed distinctly from api_tokens' "sb_" secrets
// since a client_id is a public identifier, not a bearer secret.
func GenerateClientID() (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("rand client id: %w", err)
	}
	return "oc_" + base64.RawURLEncoding.EncodeToString(b), nil
}

// GenerateOpaqueSecret returns a new random opaque secret (authorization
// code, access token, or refresh token plaintext). Unlike api_tokens' "sb_"
// prefix, these carry no fixed prefix: they are never human-copied, always
// exchanged programmatically, and a distinct shape lets GetUserByAPIToken's
// existing "sb_" prefix check short-circuit instantly instead of doing a
// wasted hash+DB lookup on every MCP request bearing one of these tokens.
func GenerateOpaqueSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("rand secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
