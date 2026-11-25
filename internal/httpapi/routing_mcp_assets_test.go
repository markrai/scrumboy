package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"scrumboy/internal/assets/blobstore"
	"scrumboy/internal/mcp"
	"scrumboy/internal/store"
)

type mcpAssetServer struct {
	*assetHTTPFixture
	url string
}

type mcpAssetResponse struct {
	status int
	header http.Header
	body   []byte
}

func newMCPAssetServer(t *testing.T) *mcpAssetServer {
	t.Helper()
	f := newAssetHTTPFixture(t, assetFixtureOptions{})
	srv := NewServer(f.st, Options{
		Logger:                log.New(io.Discard, "", 0),
		MaxRequestBody:        1 << 20,
		ScrumboyMode:          "full",
		DataDir:               f.dataDir,
		MarkdownNotesEnabled:  true,
		PublicProjectsEnabled: true,
		InlineImagesEnabled:   true,
		AssetBlobs:            f.blobs,
		AssetPending:          blobstore.NewPending(),
		MCPHandler:            mcp.New(f.st, mcp.Options{Mode: "full"}),
	})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return &mcpAssetServer{assetHTTPFixture: f, url: ts.URL}
}

func (m *mcpAssetServer) do(t *testing.T, method, path, bearer, cookie string, body []byte, contentType string, header map[string]string) mcpAssetResponse {
	t.Helper()
	req, err := http.NewRequest(method, m.url+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: "scrumboy_session", Value: cookie})
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return mcpAssetResponse{status: resp.StatusCode, header: resp.Header, body: b}
}

func (m *mcpAssetServer) download(t *testing.T, assetID, bearer, cookie string) mcpAssetResponse {
	t.Helper()
	return m.do(t, http.MethodGet, "/mcp/assets/"+assetID, bearer, cookie, nil, "", nil)
}

func (m *mcpAssetServer) uploadTo(t *testing.T, slug, bearer, cookie string, data []byte) mcpAssetResponse {
	t.Helper()
	body, ct := multipartBody(t, fileParts("shot.png", data))
	return m.do(t, http.MethodPost, "/mcp/assets/"+slug, bearer, cookie, body.Bytes(), ct, nil)
}

func (r mcpAssetResponse) assetID(t *testing.T) string {
	t.Helper()
	var up struct {
		AssetID string `json:"assetId"`
	}
	if r.status != http.StatusCreated {
		t.Fatalf("upload: %d %s", r.status, r.body)
	}
	if err := json.Unmarshal(r.body, &up); err != nil || up.AssetID == "" {
		t.Fatalf("upload response %s (%v)", r.body, err)
	}
	return up.AssetID
}

func (m *mcpAssetServer) apiToken(t *testing.T, role string) string {
	t.Helper()
	_, tok, _, err := m.st.CreateUserAPIToken(context.Background(), m.users[role].ID, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func (m *mcpAssetServer) mcpResource() string {
	return m.url + "/mcp/rpc"
}

func (m *mcpAssetServer) oauthClient(t *testing.T, clientID string) {
	t.Helper()
	if _, err := m.st.CreateOAuthClient(context.Background(), clientID, "Asset Test Client", "http://127.0.0.1/callback"); err != nil {
		t.Fatal(err)
	}
}

func (m *mcpAssetServer) oauthToken(t *testing.T, clientID, role, resource string) string {
	t.Helper()
	pair, err := m.st.IssueOAuthTokenPair(context.Background(), clientID, m.users[role].ID, resource)
	if err != nil {
		t.Fatal(err)
	}
	return pair.AccessToken
}

func (m *mcpAssetServer) oauthLogin(t *testing.T, role string) string {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	base, _ := url.Parse(m.url)
	jar.SetCookies(base, []*http.Cookie{{Name: "scrumboy_session", Value: m.tokens[role]}})
	browser := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	redirectURI := "http://localhost:9999/callback"
	clientID := registerOAuthClient(t, m.url, redirectURI)
	verifier, challenge := pkcePair(t)
	loc := approveConsent(t, browser, m.url, clientID, redirectURI, challenge, "s1")
	status, tok := exchangeToken(t, m.url, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {loc.Query().Get("code")},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"code_verifier": {verifier},
	})
	access, _ := tok["access_token"].(string)
	if status != http.StatusOK || access == "" {
		t.Fatalf("token exchange: %d %+v", status, tok)
	}
	return access
}

func TestMCPAssetRoutesUseBearerOnly(t *testing.T) {
	m := newMCPAssetServer(t)
	png := testPNG(t, 7)
	slug := m.project.Slug

	owner, viewer, outsider := m.apiToken(t, "maintainer"), m.apiToken(t, "viewer"), m.apiToken(t, "outsider")

	assetID := m.uploadTo(t, slug, owner, "", png).assetID(t)

	if res := m.download(t, assetID, owner, ""); res.status != http.StatusOK || !bytes.Equal(res.body, png) {
		t.Fatalf("download: %d, bytes equal=%v", res.status, bytes.Equal(res.body, png))
	}
	if res := m.download(t, assetID, viewer, ""); res.status != http.StatusOK {
		t.Fatalf("viewer download: %d", res.status)
	}
	if res := m.download(t, assetID, outsider, ""); res.status != http.StatusNotFound {
		t.Fatalf("outsider download: %d", res.status)
	}
	if res := m.uploadTo(t, slug, viewer, "", png); res.status != http.StatusForbidden {
		t.Fatalf("viewer upload: %d", res.status)
	}
	if res := m.uploadTo(t, slug, outsider, "", png); res.status != http.StatusNotFound {
		t.Fatalf("outsider upload: %d", res.status)
	}
	if res := m.uploadTo(t, "no-such-project", owner, "", png); res.status != http.StatusNotFound {
		t.Fatalf("unknown project: %d", res.status)
	}
	// No credential, an invalid one, or only a session cookie (no ambient browser auth) is a 401.
	if res := m.download(t, assetID, "", ""); res.status != http.StatusUnauthorized {
		t.Fatalf("anonymous download: %d", res.status)
	}
	if res := m.download(t, assetID, "sb_bogus", ""); res.status != http.StatusUnauthorized {
		t.Fatalf("bad token download: %d", res.status)
	}
	if res := m.uploadTo(t, slug, "", m.tokens["maintainer"], png); res.status != http.StatusUnauthorized {
		t.Fatalf("cookie-only upload: %d", res.status)
	}
	// A session cookie must never override or upgrade the bearer identity: a viewer's token with a
	// maintainer's cookie is still a viewer.
	if res := m.uploadTo(t, slug, viewer, m.tokens["maintainer"], png); res.status != http.StatusForbidden {
		t.Fatalf("viewer token + maintainer cookie upload: %d", res.status)
	}
	if res := m.download(t, assetID, outsider, m.tokens["maintainer"]); res.status != http.StatusNotFound {
		t.Fatalf("outsider token + maintainer cookie download: %d", res.status)
	}
	if res := m.uploadTo(t, "Bad!Slug", owner, "", png); res.status != http.StatusNotFound {
		t.Fatalf("malformed slug: %d", res.status)
	}
	if res := m.do(t, http.MethodDelete, "/mcp/assets/"+assetID, owner, "", nil, "", nil); res.status != http.StatusMethodNotAllowed {
		t.Fatalf("delete: %d", res.status)
	}
}

func TestMCPAssetDownloadIsNoStore(t *testing.T) {
	m := newMCPAssetServer(t)
	owner, outsider := m.apiToken(t, "maintainer"), m.apiToken(t, "outsider")
	png := testPNG(t, 3)
	assetID := m.uploadTo(t, m.project.Slug, owner, "", png).assetID(t)

	get := m.download(t, assetID, owner, "")
	if get.status != http.StatusOK || !bytes.Equal(get.body, png) {
		t.Fatalf("GET: %d", get.status)
	}
	head := m.do(t, http.MethodHead, "/mcp/assets/"+assetID, owner, "", nil, "", nil)
	if head.status != http.StatusOK || len(head.body) != 0 {
		t.Fatalf("HEAD: %d, %d bytes", head.status, len(head.body))
	}
	etag := get.header.Get("ETag")
	notModified := m.do(t, http.MethodGet, "/mcp/assets/"+assetID, owner, "", nil, "", map[string]string{"If-None-Match": etag})
	if notModified.status != http.StatusNotModified {
		t.Fatalf("If-None-Match: %d", notModified.status)
	}
	for name, res := range map[string]mcpAssetResponse{"GET": get, "HEAD": head, "304": notModified} {
		if got := res.header.Get("Cache-Control"); got != "no-store" {
			t.Errorf("%s Cache-Control = %q, want no-store", name, got)
		}
	}
	for k, want := range map[string]string{
		"Content-Type":            "image/png",
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": "default-src 'none'; sandbox",
	} {
		if got := get.header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if etag == "" {
		t.Error("missing ETag")
	}

	if r := m.get(t, "viewer", assetID, nil); r.StatusCode != http.StatusOK || r.Header.Get("Cache-Control") != "private, no-cache" {
		t.Fatalf("/api/assets member: %d Cache-Control=%q", r.StatusCode, r.Header.Get("Cache-Control"))
	}

	m.createTodo(t, "Shown", "![a](/api/assets/"+assetID+")", nil)
	m.publish(t)
	pub := m.download(t, assetID, outsider, "")
	if pub.status != http.StatusOK || pub.header.Get("Cache-Control") != "no-store" || pub.header.Get("X-Robots-Tag") == "" {
		t.Fatalf("/mcp/assets public: %d Cache-Control=%q X-Robots-Tag=%q", pub.status, pub.header.Get("Cache-Control"), pub.header.Get("X-Robots-Tag"))
	}
	if r := m.get(t, "", assetID, nil); r.StatusCode != http.StatusOK || r.Header.Get("Cache-Control") != "private, no-cache" || r.Header.Get("X-Robots-Tag") == "" {
		t.Fatalf("/api/assets public: %d Cache-Control=%q X-Robots-Tag=%q", r.StatusCode, r.Header.Get("Cache-Control"), r.Header.Get("X-Robots-Tag"))
	}
}

func TestMCPAssetRoutesOAuthAccessTokens(t *testing.T) {
	m := newMCPAssetServer(t)
	ctx := context.Background()
	slug := m.project.Slug
	png := testPNG(t, 11)

	const clientID = "mcp-assets-test"
	m.oauthClient(t, clientID)
	canonical := m.mcpResource()

	owner := m.oauthLogin(t, "maintainer")
	contributor := m.oauthToken(t, clientID, "contributor", canonical)
	viewer := m.oauthToken(t, clientID, "viewer", canonical)
	outsider := m.oauthToken(t, clientID, "outsider", canonical)

	assetID := m.uploadTo(t, slug, owner, "", png).assetID(t)
	if res := m.download(t, assetID, owner, ""); res.status != http.StatusOK || !bytes.Equal(res.body, png) {
		t.Fatalf("owner OAuth download: %d", res.status)
	}
	if res := m.download(t, assetID, viewer, ""); res.status != http.StatusOK || !bytes.Equal(res.body, png) {
		t.Fatalf("viewer OAuth download: %d", res.status)
	}

	rows, files := m.assetRows(t), m.assetFiles(t)
	for name, c := range map[string]struct {
		token string
		want  int
	}{
		"viewer":      {viewer, http.StatusForbidden},
		"contributor": {contributor, http.StatusForbidden},
		"outsider":    {outsider, http.StatusNotFound},
	} {
		if res := m.uploadTo(t, slug, c.token, "", png); res.status != c.want {
			t.Fatalf("%s OAuth upload: %d, want %d", name, res.status, c.want)
		}
	}
	if res := m.download(t, assetID, outsider, ""); res.status != http.StatusNotFound {
		t.Fatalf("outsider OAuth download: %d", res.status)
	}
	if m.assetRows(t) != rows || m.assetFiles(t) != files {
		t.Fatal("denied OAuth upload left a row or file")
	}

	outsiderProject, err := m.st.CreateProject(store.WithUserID(ctx, m.users["outsider"].ID), "Elsewhere")
	if err != nil {
		t.Fatal(err)
	}
	elsewhere := m.uploadTo(t, outsiderProject.Slug, outsider, "", testPNG(t, 12)).assetID(t)
	if res := m.download(t, elsewhere, owner, ""); res.status != http.StatusNotFound {
		t.Fatalf("download from unrelated project: %d", res.status)
	}
	if res := m.uploadTo(t, outsiderProject.Slug, owner, "", png); res.status != http.StatusNotFound {
		t.Fatalf("upload to unrelated project: %d", res.status)
	}

	if res := m.download(t, assetID, "", m.tokens["maintainer"]); res.status != http.StatusUnauthorized {
		t.Fatalf("cookie-only download: %d", res.status)
	}
	if res := m.uploadTo(t, slug, "", m.tokens["maintainer"], png); res.status != http.StatusUnauthorized {
		t.Fatalf("cookie-only upload: %d", res.status)
	}
	if res := m.uploadTo(t, slug, viewer, m.tokens["maintainer"], png); res.status != http.StatusForbidden {
		t.Fatalf("viewer OAuth + maintainer cookie upload: %d", res.status)
	}
	if res := m.download(t, assetID, outsider, m.tokens["maintainer"]); res.status != http.StatusNotFound {
		t.Fatalf("outsider OAuth + maintainer cookie download: %d", res.status)
	}

	for _, resource := range []string{
		"https://other.example/mcp/rpc",
		m.url + "/mcp",
		m.url + "/mcp/assets",
		m.url + "/mcp/rpc/",
	} {
		wrong := m.oauthToken(t, clientID, "maintainer", resource)
		if res := m.download(t, assetID, wrong, ""); res.status != http.StatusUnauthorized || res.header.Get("WWW-Authenticate") != "Bearer" {
			t.Fatalf("resource %q download: %d WWW-Authenticate=%q", resource, res.status, res.header.Get("WWW-Authenticate"))
		}
		if res := m.uploadTo(t, slug, wrong, "", png); res.status != http.StatusUnauthorized {
			t.Fatalf("resource %q upload: %d", resource, res.status)
		}
	}

	const expiredClient = "mcp-assets-expired"
	m.oauthClient(t, expiredClient)
	expired := m.oauthToken(t, expiredClient, "maintainer", canonical)
	if res := m.download(t, assetID, expired, ""); res.status != http.StatusOK {
		t.Fatalf("pre-expiry download: %d", res.status)
	}
	if _, err := m.sqlDB.Exec(`UPDATE oauth_access_tokens SET expires_at = ? WHERE client_id = ?`, time.Now().Add(-time.Minute).UnixMilli(), expiredClient); err != nil {
		t.Fatal(err)
	}
	if res := m.download(t, assetID, expired, ""); res.status != http.StatusUnauthorized {
		t.Fatalf("expired download: %d", res.status)
	}
	if res := m.uploadTo(t, slug, expired, "", png); res.status != http.StatusUnauthorized {
		t.Fatalf("expired upload: %d", res.status)
	}

	revoke, err := http.PostForm(m.url+"/oauth/revoke", url.Values{"token": {owner}})
	if err != nil {
		t.Fatal(err)
	}
	revoke.Body.Close()
	if revoke.StatusCode != http.StatusOK {
		t.Fatalf("revoke: %d", revoke.StatusCode)
	}
	if res := m.download(t, assetID, owner, ""); res.status != http.StatusUnauthorized {
		t.Fatalf("revoked download: %d", res.status)
	}
	if res := m.uploadTo(t, slug, owner, "", png); res.status != http.StatusUnauthorized {
		t.Fatalf("revoked upload: %d", res.status)
	}

	if res := m.download(t, assetID, m.apiToken(t, "viewer"), ""); res.status != http.StatusOK {
		t.Fatalf("API token download: %d", res.status)
	}
}
