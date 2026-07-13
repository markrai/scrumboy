package oauth

import (
	"encoding/json"
	"net/http"
)

// Error codes from RFC 6749 §5.2 (token endpoint) and §4.1.2.1 (authorize endpoint).
const (
	ErrInvalidRequest       = "invalid_request"
	ErrInvalidClient        = "invalid_client"
	ErrInvalidGrant         = "invalid_grant"
	ErrUnauthorizedClient   = "unauthorized_client"
	ErrUnsupportedGrantType = "unsupported_grant_type"
	ErrInvalidScope         = "invalid_scope"
	ErrAccessDenied         = "access_denied"
	ErrUnsupportedResponse  = "unsupported_response_type"
	ErrServerError          = "server_error"
)

// Error codes from RFC 7591 §3.2.2 (dynamic client registration).
const (
	ErrInvalidRedirectURI    = "invalid_redirect_uri"
	ErrInvalidClientMetadata = "invalid_client_metadata"
)

// ErrorResponse is the flat wire shape required by RFC 6749 §5.2 and RFC 7591
// §3.2.2 — deliberately not Scrumboy's usual {"error":{"code":...}} envelope,
// since OAuth clients parse these exact top-level "error"/"error_description" keys.
type ErrorResponse struct {
	Code        string `json:"error"`
	Description string `json:"error_description,omitempty"`
}

// WriteJSON writes an OAuth-shaped error response with the given HTTP status.
func WriteJSON(w http.ResponseWriter, status int, code, description string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(ErrorResponse{Code: code, Description: description})
}

// StatusForCode returns the RFC 6749 §5.2-mandated HTTP status for a given
// token-endpoint error code (400 for everything except invalid_client, which
// is 401).
func StatusForCode(code string) int {
	if code == ErrInvalidClient {
		return http.StatusUnauthorized
	}
	return http.StatusBadRequest
}
