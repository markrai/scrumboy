package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// isSecureRequest is true when the client used HTTPS (direct TLS, X-Forwarded-Proto, or CF-Visitor behind a proxy/tunnel).
func isSecureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if strings.TrimSpace(strings.ToLower(r.Header.Get("X-Forwarded-Proto"))) == "https" {
		return true
	}
	// CF-Visitor: {"scheme":"https"} (Cloudflare and similar proxies)
	var cfVisitor struct {
		Scheme string `json:"scheme"`
	}
	if err := json.Unmarshal([]byte(r.Header.Get("CF-Visitor")), &cfVisitor); err == nil && strings.ToLower(strings.TrimSpace(cfVisitor.Scheme)) == "https" {
		return true
	}
	return false
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, expiresAt time.Time) {
	secure := isSecureRequest(r)
	http.SetCookie(w, &http.Cookie{
		Name:     "scrumboy_session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
		Expires:  expiresAt,
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	secure := isSecureRequest(r)
	http.SetCookie(w, &http.Cookie{
		Name:     "scrumboy_session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0).UTC(),
	})
}
