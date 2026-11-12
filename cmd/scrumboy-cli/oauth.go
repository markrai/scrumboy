package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// OAuth 2.1 login for Scrumboy (docs/oauth.md): discovery (RFC 8414 / 9728),
// dynamic client registration (RFC 7591), authorization code + PKCE S256 with a
// loopback redirect (RFC 8252), refresh-token rotation, and revocation. The
// resulting opaque access token is only accepted by /mcp/rpc.

const oauthClientName = "scrumboy-cli"

// oauthConfig is the persisted OAuth session.
type oauthConfig struct {
	// Server is the server this session belongs to; it is never used with another.
	Server         string    `json:"server"`
	ClientID       string    `json:"client_id"`
	TokenEndpoint  string    `json:"token_endpoint"`
	RevokeEndpoint string    `json:"revoke_endpoint,omitempty"`
	Resource       string    `json:"resource"`
	AccessToken    string    `json:"access_token"`
	RefreshToken   string    `json:"refresh_token,omitempty"`
	ExpiresAt      time.Time `json:"expires_at,omitempty"`
}

type oauthMeta struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	RegistrationEndpoint  string `json:"registration_endpoint"`
	RevocationEndpoint    string `json:"revocation_endpoint"`
}

type tokenResp struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresIn        int    `json:"expires_in"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// oauthHTTP never follows redirects: Go would re-send a POST body (a refresh
// token, an authorization code) to the redirect target on 307/308.
func oauthHTTP() *http.Client {
	return &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// origin returns scheme://host[:port] of a URL, or "" if it is not absolute.
func origin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return strings.ToLower(u.Scheme + "://" + u.Host)
}

func isLoopbackHost(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// requireSameOrigin guards the places credentials are sent: every endpoint the
// server advertises must live on the origin the user pointed the CLI at, or a
// poisoned discovery document could steer tokens to another host.
func requireSameOrigin(server string, endpoints map[string]string) error {
	want := origin(server)
	for name, ep := range endpoints {
		if ep == "" {
			continue
		}
		if origin(ep) != want {
			return fmt.Errorf("the server advertised a %s on a different origin (%s, expected %s); "+
				"refusing to send credentials there (use the server's public URL with --server)", name, origin(ep), want)
		}
	}
	return nil
}

// safeText makes server-supplied text safe to print on a terminal.
func safeText(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 0x20 && r < 0x7f {
			b.WriteRune(r)
		}
		if b.Len() >= 120 {
			break
		}
	}
	return b.String()
}

func getJSON(ctx context.Context, hc *http.Client, u string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: HTTP %d", u, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(v)
}

// discover finds the OAuth endpoints and the protected-resource identifier
// (which the server requires verbatim as the `resource` parameter).
func discover(ctx context.Context, hc *http.Client, server string) (oauthMeta, string, error) {
	var meta oauthMeta
	if err := getJSON(ctx, hc, server+"/.well-known/oauth-authorization-server", &meta); err != nil {
		return meta, "", fmt.Errorf("OAuth discovery failed (is this a full-mode Scrumboy server?): %w", err)
	}
	if meta.AuthorizationEndpoint == "" || meta.TokenEndpoint == "" || meta.RegistrationEndpoint == "" {
		return meta, "", errors.New("server does not advertise the OAuth endpoints needed for login")
	}
	var pr struct {
		Resource string `json:"resource"`
	}
	resource := server + "/mcp/rpc"
	if err := getJSON(ctx, hc, server+"/.well-known/oauth-protected-resource/mcp/rpc", &pr); err == nil && pr.Resource != "" {
		resource = pr.Resource
	}
	if err := requireSameOrigin(server, map[string]string{
		"authorization endpoint": meta.AuthorizationEndpoint,
		"token endpoint":         meta.TokenEndpoint,
		"registration endpoint":  meta.RegistrationEndpoint,
		"revocation endpoint":    meta.RevocationEndpoint,
		"protected resource":     resource,
	}); err != nil {
		return meta, "", err
	}
	return meta, resource, nil
}

func register(ctx context.Context, hc *http.Client, endpoint, redirectURI string) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"client_name":                oauthClientName,
		"redirect_uris":              []string{redirectURI},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		ClientID         string `json:"client_id"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	_ = json.Unmarshal(raw, &out)
	if out.ClientID == "" {
		if out.Error != "" {
			return "", fmt.Errorf("client registration failed: %s: %s", out.Error, out.ErrorDescription)
		}
		return "", fmt.Errorf("client registration failed (HTTP %d): %s", resp.StatusCode, snippet(raw))
	}
	return out.ClientID, nil
}

func postToken(ctx context.Context, hc *http.Client, endpoint string, form url.Values) (*tokenResp, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var tr tokenResp
	if err := json.Unmarshal(raw, &tr); err != nil {
		return nil, fmt.Errorf("token endpoint returned HTTP %d: %s", resp.StatusCode, snippet(raw))
	}
	if tr.Error != "" {
		return nil, fmt.Errorf("%s: %s", tr.Error, tr.ErrorDescription)
	}
	if tr.AccessToken == "" {
		return nil, fmt.Errorf("token endpoint returned no access token (HTTP %d)", resp.StatusCode)
	}
	return &tr, nil
}

func randomURLSafe(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

type callbackResult struct {
	code, state, err string
}

// parseRedirect extracts code/state/error from a redirect URL (or bare query)
// that a user pasted back.
func parseRedirect(s string) (callbackResult, error) {
	s = strings.TrimSpace(s)
	u, err := url.Parse(s)
	if err != nil || (u.RawQuery == "" && !strings.Contains(s, "code=")) {
		return callbackResult{}, errors.New("not a redirect URL containing ?code=...")
	}
	q := u.Query()
	if u.RawQuery == "" { // bare "code=...&state=..."
		q, _ = url.ParseQuery(s)
	}
	return callbackResult{code: q.Get("code"), state: q.Get("state"), err: q.Get("error")}, nil
}

// oauthLogin runs the browser (or paste) authorization-code flow and returns
// the new session.
func oauthLogin(ctx context.Context, c *cmdCtx, server string, noBrowser bool) (*oauthConfig, error) {
	if strings.HasPrefix(server, "http://") && !isLoopbackHost(server) {
		return nil, errors.New("refusing OAuth login over plain http to a non-local server; use https")
	}
	hc := oauthHTTP()
	meta, resource, err := discover(ctx, hc, server)
	if err != nil {
		return nil, err
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("start loopback listener: %w", err)
	}
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", ln.Addr().(*net.TCPAddr).Port)

	clientID, err := register(ctx, hc, meta.RegistrationEndpoint, redirectURI)
	if err != nil {
		ln.Close()
		return nil, err
	}
	verifier, err := randomURLSafe(32)
	if err != nil {
		ln.Close()
		return nil, err
	}
	state, err := randomURLSafe(16)
	if err != nil {
		ln.Close()
		return nil, err
	}

	results := make(chan callbackResult, 2)
	srv := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/callback" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		res := callbackResult{code: q.Get("code"), state: q.Get("state"), err: q.Get("error")}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		// A request without our state is not the authorization response (a
		// stray local request must not be able to abort or answer the login).
		if res.state != state {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, "<h1>Unexpected request</h1>")
			return
		}
		if res.err != "" || res.code == "" {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, "<h1>Login failed</h1><p>%s</p>", html.EscapeString(safeText(res.err)))
		} else {
			fmt.Fprint(w, "<h1>Logged in</h1><p>You can close this window and return to the terminal.</p>")
		}
		select {
		case results <- res:
		default:
		}
	})}
	go srv.Serve(ln)
	defer srv.Close()

	authURL := meta.AuthorizationEndpoint + "?" + url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"code_challenge":        {pkceChallenge(verifier)},
		"code_challenge_method": {"S256"},
		"state":                 {state},
		"resource":              {resource},
	}.Encode()

	fmt.Fprintf(c.err, "Open this URL in a browser to sign in to Scrumboy:\n\n  %s\n\n", authURL)
	if !noBrowser {
		if err := c.openURL(authURL); err != nil {
			fmt.Fprintf(c.err, "(could not open a browser automatically: %v)\n", err)
		}
	}
	if noBrowser && c.isTTY {
		fmt.Fprintln(c.err, "On another machine? After approving, paste the full URL the browser was redirected to")
		fmt.Fprintln(c.err, "(it will fail to load; that is fine) and press Enter.")
		go func() {
			rd := bufio.NewReader(c.in)
			for {
				line, err := rd.ReadString('\n')
				if res, perr := parseRedirect(line); perr == nil && res.state == state {
					select {
					case results <- res:
					default:
					}
					return
				} else if strings.TrimSpace(line) != "" {
					fmt.Fprintln(c.err, "That is not the redirect URL for this login; paste the full URL (it contains code= and state=).")
				}
				if err != nil {
					return
				}
			}
		}()
	}
	fmt.Fprintln(c.err, "Waiting for authorization (Ctrl-C to cancel)...")

	var cb callbackResult
	select {
	case cb = <-results:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(5 * time.Minute):
		return nil, errors.New("timed out waiting for authorization")
	}
	switch {
	case cb.err != "":
		return nil, fmt.Errorf("authorization denied: %s", safeText(cb.err))
	case cb.state != state:
		return nil, errors.New("authorization response had a mismatched state; aborting")
	case cb.code == "":
		return nil, errors.New("authorization response had no code")
	}

	tr, err := postToken(ctx, hc, meta.TokenEndpoint, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {cb.code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"code_verifier": {verifier},
		"resource":      {resource},
	})
	if err != nil {
		return nil, fmt.Errorf("token exchange failed: %w", err)
	}
	return &oauthConfig{
		Server:         server,
		ClientID:       clientID,
		TokenEndpoint:  meta.TokenEndpoint,
		RevokeEndpoint: meta.RevocationEndpoint,
		Resource:       resource,
		AccessToken:    tr.AccessToken,
		RefreshToken:   tr.RefreshToken,
		ExpiresAt:      expiry(tr.ExpiresIn),
	}, nil
}

func expiry(expiresIn int) time.Time {
	if expiresIn <= 0 {
		return time.Time{}
	}
	return time.Now().Add(time.Duration(expiresIn) * time.Second)
}

// refreshOAuth exchanges the refresh token (which the server rotates on every
// use) for a new session.
func refreshOAuth(ctx context.Context, o oauthConfig) (*oauthConfig, error) {
	if o.RefreshToken == "" {
		return nil, errors.New("session expired and there is no refresh token; run 'auth login --oauth' again")
	}
	tr, err := postToken(ctx, oauthHTTP(), o.TokenEndpoint, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {o.RefreshToken},
		"client_id":     {o.ClientID},
		"resource":      {o.Resource},
	})
	if err != nil {
		return nil, fmt.Errorf("session expired and refreshing it failed (%w); run 'auth login --oauth' again", err)
	}
	o.AccessToken = tr.AccessToken
	if tr.RefreshToken != "" {
		o.RefreshToken = tr.RefreshToken
	}
	o.ExpiresAt = expiry(tr.ExpiresIn)
	return &o, nil
}

// revokeOAuth best-effort revokes both tokens (RFC 7009).
func revokeOAuth(ctx context.Context, server string, o oauthConfig) {
	endpoint := o.RevokeEndpoint
	if endpoint == "" {
		endpoint = server + "/oauth/revoke"
	}
	for _, t := range []struct{ tok, hint string }{{o.RefreshToken, "refresh_token"}, {o.AccessToken, "access_token"}} {
		if t.tok == "" {
			continue
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint,
			strings.NewReader(url.Values{"token": {t.tok}, "token_type_hint": {t.hint}, "client_id": {o.ClientID}}.Encode()))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if resp, err := oauthHTTP().Do(req); err == nil {
			resp.Body.Close()
		}
	}
}

// openBrowser opens u in the user's default browser.
func openBrowser(u string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	return cmd.Start()
}
