package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"scrumboy/internal/assets/blobstore"
	"scrumboy/internal/mcp"
)

func TestMCPAssetRoutesUseBearerOnly(t *testing.T) {
	f := newAssetHTTPFixture(t, assetFixtureOptions{})
	srv := NewServer(f.st, Options{
		Logger:               log.New(io.Discard, "", 0),
		MaxRequestBody:       1 << 20,
		ScrumboyMode:         "full",
		DataDir:              f.dataDir,
		MarkdownNotesEnabled: true,
		InlineImagesEnabled:  true,
		AssetBlobs:           f.blobs,
		AssetPending:         blobstore.NewPending(),
		MCPHandler:           mcp.New(f.st, mcp.Options{Mode: "full"}),
	})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	apiToken := func(role string) string {
		_, tok, _, err := f.st.CreateUserAPIToken(context.Background(), f.users[role].ID, nil, false)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	do := func(method, path, bearer, cookie string, body []byte, contentType string) (int, []byte) {
		t.Helper()
		req, err := http.NewRequest(method, ts.URL+path, bytes.NewReader(body))
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
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}
	multipartBody := func(data []byte) ([]byte, string) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		fw, err := mw.CreateFormFile("file", "shot.png")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fw.Write(data)
		_ = mw.Close()
		return buf.Bytes(), mw.FormDataContentType()
	}

	png := testPNG(t, 7)
	body, ct := multipartBody(png)
	upPath := "/mcp/assets/" + f.project.Slug

	owner, viewer, outsider := apiToken("maintainer"), apiToken("viewer"), apiToken("outsider")

	status, raw := do(http.MethodPost, upPath, owner, "", body, ct)
	if status != http.StatusCreated {
		t.Fatalf("maintainer upload: %d %s", status, raw)
	}
	var up struct {
		AssetID string `json:"assetId"`
	}
	if err := json.Unmarshal(raw, &up); err != nil || up.AssetID == "" {
		t.Fatalf("upload response %s (%v)", raw, err)
	}

	if status, got := do(http.MethodGet, "/mcp/assets/"+up.AssetID, owner, "", nil, ""); status != http.StatusOK || !bytes.Equal(got, png) {
		t.Fatalf("download: %d, bytes equal=%v", status, bytes.Equal(got, png))
	}
	if status, _ := do(http.MethodGet, "/mcp/assets/"+up.AssetID, viewer, "", nil, ""); status != http.StatusOK {
		t.Fatalf("viewer download: %d", status)
	}
	if status, _ := do(http.MethodGet, "/mcp/assets/"+up.AssetID, outsider, "", nil, ""); status != http.StatusNotFound {
		t.Fatalf("outsider download: %d", status)
	}
	if status, _ := do(http.MethodPost, upPath, viewer, "", body, ct); status != http.StatusForbidden {
		t.Fatalf("viewer upload: %d", status)
	}
	if status, _ := do(http.MethodPost, upPath, outsider, "", body, ct); status != http.StatusNotFound {
		t.Fatalf("outsider upload: %d", status)
	}
	if status, _ := do(http.MethodPost, "/mcp/assets/no-such-project", owner, "", body, ct); status != http.StatusNotFound {
		t.Fatalf("unknown project: %d", status)
	}
	// No credential, an invalid one, or only a session cookie (no ambient browser auth) is a 401.
	if status, _ := do(http.MethodGet, "/mcp/assets/"+up.AssetID, "", "", nil, ""); status != http.StatusUnauthorized {
		t.Fatalf("anonymous download: %d", status)
	}
	if status, _ := do(http.MethodGet, "/mcp/assets/"+up.AssetID, "sb_bogus", "", nil, ""); status != http.StatusUnauthorized {
		t.Fatalf("bad token download: %d", status)
	}
	if status, _ := do(http.MethodPost, upPath, "", f.tokens["maintainer"], body, ct); status != http.StatusUnauthorized {
		t.Fatalf("cookie-only upload: %d", status)
	}
	// A session cookie must never override or upgrade the bearer identity: a viewer's token with a
	// maintainer's cookie is still a viewer.
	if status, _ := do(http.MethodPost, upPath, viewer, f.tokens["maintainer"], body, ct); status != http.StatusForbidden {
		t.Fatalf("viewer token + maintainer cookie upload: %d", status)
	}
	if status, _ := do(http.MethodGet, "/mcp/assets/"+up.AssetID, outsider, f.tokens["maintainer"], nil, ""); status != http.StatusNotFound {
		t.Fatalf("outsider token + maintainer cookie download: %d", status)
	}
	if status, _ := do(http.MethodPost, "/mcp/assets/Bad!Slug", owner, "", body, ct); status != http.StatusNotFound {
		t.Fatalf("malformed slug: %d", status)
	}
	if status, _ := do(http.MethodDelete, "/mcp/assets/"+up.AssetID, owner, "", nil, ""); status != http.StatusMethodNotAllowed {
		t.Fatalf("delete: %d", status)
	}
}
