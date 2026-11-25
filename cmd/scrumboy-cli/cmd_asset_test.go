package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const testAssetID = "11111111-2222-4333-8444-555555555555"

// assetServer is a fake /mcp/assets backend: GET serves one image, POST records the upload.
type assetServer struct {
	*httptest.Server
	uploads []struct {
		path, auth, name, ct string
		data                 []byte
	}
}

func newAssetServer(t *testing.T, img []byte) *assetServer {
	t.Helper()
	as := &assetServer{}
	as.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/mcp/assets/"+testAssetID:
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(img)
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/mcp/assets/"):
			f, hdr, err := r.FormFile("file")
			if err != nil {
				t.Errorf("form file: %v", err)
				http.Error(w, "bad", 400)
				return
			}
			data, _ := io.ReadAll(f)
			as.uploads = append(as.uploads, struct {
				path, auth, name, ct string
				data                 []byte
			}{r.URL.RequestURI(), r.Header.Get("Authorization"), hdr.Filename, r.Header.Get("Content-Type"), data})
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"assetId": "99999999-2222-4333-8444-555555555555", "url": "/api/assets/99999999-2222-4333-8444-555555555555", "mimeType": "image/png", "sizeBytes": len(data)})
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":{"code":"NOT_FOUND","message":"not found"}}`)
		}
	}))
	t.Cleanup(as.Close)
	return as
}

func TestAssetGetPutCopy(t *testing.T) {
	img := []byte("\x89PNG fake image bytes")
	as := newAssetServer(t, img)
	h := newHarness(t, as.URL, nil)
	dir := t.TempDir()

	// get: accepts a bare id or a link, writes <id>.png, never overwrites.
	out := filepath.Join(dir, "got.png")
	if code := h.run("asset", "get", "/api/assets/"+testAssetID, "-o", out); code != 0 {
		t.Fatalf("get exit %d: %s", code, h.err)
	}
	if got, _ := os.ReadFile(out); !bytes.Equal(got, img) {
		t.Fatalf("downloaded %q", got)
	}
	if code := h.run("asset", "get", testAssetID, "-o", out); code == 0 {
		t.Fatal("get overwrote an existing file")
	}
	h.out.Reset()
	if code := h.run("asset", "get", testAssetID, "-o", "-"); code != 0 || !bytes.Equal(h.out.Bytes(), img) {
		t.Fatalf("get to stdout: exit %d, %q", code, h.out)
	}

	// put: multipart upload to the project, optional ?todo=, prints the new URL.
	h.out.Reset()
	if code := h.run("asset", "put", out, "-p", "tech", "--todo", "5"); code != 0 {
		t.Fatalf("put exit %d: %s", code, h.err)
	}
	if strings.TrimSpace(h.out.String()) != "/api/assets/99999999-2222-4333-8444-555555555555" {
		t.Fatalf("put printed %q", h.out)
	}
	up := as.uploads[0]
	if up.path != "/mcp/assets/tech?todo=5" || up.auth != "Bearer sb_test" || up.name != "got.png" || !bytes.Equal(up.data, img) || !strings.HasPrefix(up.ct, "multipart/form-data") {
		t.Fatalf("upload %+v", up)
	}

	// copy: download then upload under the source id + extension.
	h.out.Reset()
	if code := h.run("asset", "copy", "https://example.test/api/assets/"+testAssetID, "-p", "tech"); code != 0 {
		t.Fatalf("copy exit %d: %s", code, h.err)
	}
	cp := as.uploads[1]
	if cp.path != "/mcp/assets/tech" || cp.name != testAssetID+".png" || !bytes.Equal(cp.data, img) {
		t.Fatalf("copy upload %+v", cp)
	}
}

func TestAssetErrors(t *testing.T) {
	as := newAssetServer(t, nil)
	h := newHarness(t, as.URL, nil)
	if code := h.run("asset", "get", "not-an-id"); code != 2 {
		t.Fatalf("bad ref exit %d", code)
	}
	if code := h.run("asset", "get", "00000000-0000-4000-8000-000000000000", "-o", "-"); code != 1 || !strings.Contains(h.err.String(), "NOT_FOUND") {
		t.Fatalf("missing asset exit %d: %s", code, h.err)
	}
	if code := h.run("asset", "put", "x.png"); code != 2 || !strings.Contains(h.err.String(), "no project") {
		t.Fatalf("put without -p exit %d: %s", code, h.err)
	}
	if id, err := assetID("https://h/api/assets/" + strings.ToUpper(testAssetID) + "?x=" + "22222222-2222-4333-8444-555555555555"); err != nil || id != testAssetID {
		t.Fatalf("assetID = %q, %v", id, err)
	}
}

func TestAssetTodoFlag(t *testing.T) {
	img := []byte("\x89PNG fake image bytes")
	file := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(file, img, 0o600); err != nil {
		t.Fatal(err)
	}
	const notPositive = "--todo must be a positive todo id"
	cases := []struct {
		name     string
		args     []string
		wantCode int
		wantPath string
		wantErr  string
	}{
		{"put without --todo", []string{"asset", "put", file, "-p", "tech"}, 0, "/mcp/assets/tech", ""},
		{"put --todo 0", []string{"asset", "put", file, "-p", "tech", "--todo", "0"}, 2, "", notPositive},
		{"put --todo=0", []string{"asset", "put", file, "-p", "tech", "--todo=0"}, 2, "", notPositive},
		{"put --todo -1", []string{"asset", "put", file, "-p", "tech", "--todo", "-1"}, 2, "", notPositive},
		{"put --todo 5 after positional", []string{"asset", "put", file, "-p", "tech", "--todo", "5"}, 0, "/mcp/assets/tech?todo=5", ""},
		{"put --todo=5 before positional", []string{"asset", "put", "--todo=5", file, "-p", "tech"}, 0, "/mcp/assets/tech?todo=5", ""},
		{"put --todo abc", []string{"asset", "put", file, "-p", "tech", "--todo", "abc"}, 2, "", "invalid flags"},
		{"put --todo 1.5", []string{"asset", "put", file, "-p", "tech", "--todo", "1.5"}, 2, "", "invalid flags"},
		{"copy without --todo", []string{"asset", "copy", testAssetID, "-p", "tech"}, 0, "/mcp/assets/tech", ""},
		{"copy --todo 0", []string{"asset", "copy", testAssetID, "-p", "tech", "--todo", "0"}, 2, "", notPositive},
		{"copy --todo=0", []string{"asset", "copy", "--todo=0", testAssetID, "-p", "tech"}, 2, "", notPositive},
		{"copy --todo -1", []string{"asset", "copy", testAssetID, "-p", "tech", "--todo", "-1"}, 2, "", notPositive},
		{"copy --todo 5 after positional", []string{"asset", "copy", testAssetID, "-p", "tech", "--todo", "5"}, 0, "/mcp/assets/tech?todo=5", ""},
		{"copy --todo 5 between flags", []string{"asset", "copy", "-p", "tech", "--todo", "5", testAssetID}, 0, "/mcp/assets/tech?todo=5", ""},
		{"copy --todo abc", []string{"asset", "copy", testAssetID, "-p", "tech", "--todo", "abc"}, 2, "", "invalid flags"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			as := newAssetServer(t, img)
			h := newHarness(t, as.URL, nil)
			code := h.run(tc.args...)
			if code != tc.wantCode || !strings.Contains(h.err.String(), tc.wantErr) {
				t.Fatalf("exit %d, want %d; stderr %q", code, tc.wantCode, h.err)
			}
			if tc.wantCode != 0 {
				if len(as.uploads) != 0 {
					t.Fatalf("rejected --todo still uploaded: %+v", as.uploads)
				}
				return
			}
			if len(as.uploads) != 1 || as.uploads[0].path != tc.wantPath || !bytes.Equal(as.uploads[0].data, img) {
				t.Fatalf("uploads %+v, want one to %s", as.uploads, tc.wantPath)
			}
		})
	}
}

func TestRawRejectsOversizedResponses(t *testing.T) {
	big := bytes.Repeat([]byte{'x'}, maxResponseBytes+1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch strings.TrimPrefix(r.URL.Path, "/mcp/assets/") {
		case "declared":
			w.Header().Set("Content-Length", strconv.Itoa(len(big)))
			_, _ = w.Write(big)
		case "exact":
			w.(http.Flusher).Flush()
			_, _ = w.Write(big[:maxResponseBytes])
		case "forbidden":
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"error":{"code":"FORBIDDEN","message":"no access"}}`)
		default:
			w.Header().Set("Content-Type", "image/png")
			w.(http.Flusher).Flush()
			_, _ = w.Write(big)
		}
	}))
	t.Cleanup(srv.Close)
	cl := NewClient(srv.URL, "sb_test", "test")
	ctx := context.Background()

	for _, path := range []string{"/mcp/assets/declared", "/mcp/assets/streamed"} {
		if _, data, err := cl.Raw(ctx, http.MethodGet, path, "", nil); err == nil || !strings.Contains(err.Error(), "larger than 32 MiB") || data != nil {
			t.Fatalf("%s: err=%v, %d bytes", path, err, len(data))
		}
	}
	if _, data, err := cl.Raw(ctx, http.MethodGet, "/mcp/assets/exact", "", nil); err != nil || len(data) != maxResponseBytes {
		t.Fatalf("exactly the limit: err=%v, %d bytes", err, len(data))
	}
	var apiErr *APIError
	if _, _, err := cl.Raw(ctx, http.MethodGet, "/mcp/assets/forbidden", "", nil); !errors.As(err, &apiErr) || apiErr.Code != "FORBIDDEN" || apiErr.Status != http.StatusForbidden {
		t.Fatalf("structured error: %v", err)
	}

	h := newHarness(t, srv.URL, nil)
	out := filepath.Join(t.TempDir(), "big.png")
	if code := h.run("asset", "get", testAssetID, "-o", out); code != 1 || !strings.Contains(h.err.String(), "larger than 32 MiB") {
		t.Fatalf("get exit %d: %s", code, h.err)
	}
	if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oversized download left a file: %v", err)
	}
}

func TestRawRefreshesOnceOn401AndRefusesRedirects(t *testing.T) {
	var redirectHits int
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirectHits++ }))
	t.Cleanup(target.Close)
	img := []byte("\x89PNG")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/mcp/assets/moved":
			http.Redirect(w, r, target.URL+"/mcp/assets/moved", http.StatusFound)
		case r.Header.Get("Authorization") != "Bearer fresh":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":{"code":"AUTH_REQUIRED","message":"Authentication required"}}`)
		default:
			_, _ = w.Write(img)
		}
	}))
	t.Cleanup(srv.Close)

	refreshes := 0
	cl := NewClient(srv.URL, "stale", "test")
	cl.Refresh = func(context.Context) (string, time.Time, error) {
		refreshes++
		return "fresh", time.Now().Add(time.Hour), nil
	}
	if _, data, err := cl.Raw(context.Background(), http.MethodGet, "/mcp/assets/"+testAssetID, "", nil); err != nil || !bytes.Equal(data, img) || refreshes != 1 {
		t.Fatalf("refresh-on-401: err=%v data=%q refreshes=%d", err, data, refreshes)
	}
	if _, _, err := cl.Raw(context.Background(), http.MethodGet, "/mcp/assets/moved", "", nil); err == nil || !strings.Contains(err.Error(), "redirect") || redirectHits != 0 {
		t.Fatalf("redirect: err=%v, target hits=%d", err, redirectHits)
	}
}
