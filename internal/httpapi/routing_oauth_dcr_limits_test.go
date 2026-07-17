package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func oauthDCRMetadataResponse(t *testing.T, baseURL, clientName, redirectURI string) (int, map[string]any) {
	t.Helper()
	var out map[string]any
	resp, body := doJSON(t, http.DefaultClient, http.MethodPost, baseURL+"/oauth/register", map[string]any{
		"client_name":   clientName,
		"redirect_uris": []string{redirectURI},
	}, &out)
	if len(body) == 0 {
		t.Fatal("DCR metadata response was empty")
	}
	return resp.StatusCode, out
}

func validOAuthRedirectURIWithLength(t *testing.T, length int) string {
	t.Helper()
	const prefix = "https://client.example.com/"
	if length < len(prefix) {
		t.Fatalf("requested redirect URI length %d is shorter than prefix %d", length, len(prefix))
	}
	return prefix + strings.Repeat("a", length-len(prefix))
}

func TestOAuthDCRMetadataLengthLimits(t *testing.T) {
	srv := newTestOAuthServer(t, Options{})
	ts := httptest.NewServer(srv)
	defer ts.Close()

	t.Run("client name exactly 128 runes accepted after trim", func(t *testing.T) {
		name := strings.Repeat("界", maxOAuthClientNameRunes)
		status, out := oauthDCRMetadataResponse(t, ts.URL, " \t"+name+" \n", "https://client.example.com/callback")
		if status != http.StatusCreated {
			t.Fatalf("128-rune client_name status=%d body=%+v", status, out)
		}
		if out["client_name"] != name {
			t.Fatalf("client_name was not trimmed/preserved at boundary: got %q", out["client_name"])
		}
	})

	t.Run("client name 129 runes rejected", func(t *testing.T) {
		status, out := oauthDCRMetadataResponse(t, ts.URL, strings.Repeat("界", maxOAuthClientNameRunes+1), "https://client.example.com/callback")
		if status != http.StatusBadRequest || out["error"] != "invalid_client_metadata" {
			t.Fatalf("129-rune client_name status=%d body=%+v", status, out)
		}
	})

	t.Run("redirect URI exactly 2048 bytes accepted after trim", func(t *testing.T) {
		redirectURI := validOAuthRedirectURIWithLength(t, maxOAuthRedirectURIBytes)
		status, out := oauthDCRMetadataResponse(t, ts.URL, "Boundary Client", " \t"+redirectURI+" \n")
		if status != http.StatusCreated {
			t.Fatalf("2048-byte redirect URI status=%d body=%+v", status, out)
		}
		redirects, ok := out["redirect_uris"].([]any)
		if !ok || len(redirects) != 1 || redirects[0] != redirectURI {
			t.Fatalf("redirect URI was not trimmed/preserved at boundary: %+v", out)
		}
	})

	t.Run("redirect URI 2049 bytes rejected", func(t *testing.T) {
		status, out := oauthDCRMetadataResponse(t, ts.URL, "Boundary Client", validOAuthRedirectURIWithLength(t, maxOAuthRedirectURIBytes+1))
		if status != http.StatusBadRequest || out["error"] != "invalid_redirect_uri" {
			t.Fatalf("2049-byte redirect URI status=%d body=%+v", status, out)
		}
	})

	t.Run("empty trimmed client name remains accepted", func(t *testing.T) {
		status, out := oauthDCRMetadataResponse(t, ts.URL, " \t\n", "https://client.example.com/callback")
		if status != http.StatusCreated || out["client_name"] != "" {
			t.Fatalf("empty trimmed client_name status=%d body=%+v", status, out)
		}
	})
}
