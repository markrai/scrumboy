package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"

	"scrumboy/internal/config"
)

// envFeatureOptions applies the notes feature flags exactly as cmd/scrumboy does: parsed by
// config.FromEnv from env (a missing key is unset) and passed through to Options.
func envFeatureOptions(t *testing.T, mode string, env map[string]string) func(*Options) {
	t.Helper()
	t.Setenv("DATA_DIR", t.TempDir())
	t.Setenv("SQLITE_PATH", "")
	t.Setenv("SCRUMBOY_MODE", mode)
	for _, key := range []string{"SCRUMBOY_MARKDOWN_NOTES_ENABLED", "SCRUMBOY_MERMAID_NOTES_ENABLED", "SCRUMBOY_INLINE_IMAGES_ENABLED"} {
		v, ok := env[key]
		t.Setenv(key, v)
		if !ok {
			if err := os.Unsetenv(key); err != nil {
				t.Fatal(err)
			}
		}
	}
	cfg := config.FromEnv()
	return func(o *Options) {
		o.ScrumboyMode = cfg.ScrumboyMode
		o.MarkdownNotesEnabled = cfg.MarkdownNotesEnabled
		o.MermaidNotesEnabled = cfg.MermaidNotesEnabled
		o.InlineImagesEnabled = cfg.InlineImagesEnabled
	}
}

func (f *assetHTTPFixture) doJSON(t *testing.T, role, method, path string, body any) (*http.Response, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, f.ts.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Scrumboy", "1")
	req.Header.Set("Content-Type", "application/json")
	if token := f.tokens[role]; token != "" {
		req.AddCookie(&http.Cookie{Name: "scrumboy_session", Value: token})
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp, raw
}

type noteFeatures struct {
	Markdown bool `json:"markdownNotesEnabled"`
	Mermaid  bool `json:"mermaidNotesEnabled"`
	Images   bool `json:"inlineImagesEnabled"`
}

// /api/auth/status reports the effective capabilities for every configuration, not the raw
// environment values.
func TestAuthStatusReportsEffectiveNoteFeatures(t *testing.T) {
	cases := []struct {
		name string
		mode string
		env  map[string]string
		want noteFeatures
	}{
		{"full defaults", "full", nil, noteFeatures{true, true, true}},
		{"full empty values", "full", map[string]string{"SCRUMBOY_MARKDOWN_NOTES_ENABLED": "", "SCRUMBOY_MERMAID_NOTES_ENABLED": "", "SCRUMBOY_INLINE_IMAGES_ENABLED": ""}, noteFeatures{true, true, true}},
		{"all 0", "full", map[string]string{"SCRUMBOY_MARKDOWN_NOTES_ENABLED": "0", "SCRUMBOY_MERMAID_NOTES_ENABLED": "0", "SCRUMBOY_INLINE_IMAGES_ENABLED": "0"}, noteFeatures{}},
		{"markdown 0, others 1", "full", map[string]string{"SCRUMBOY_MARKDOWN_NOTES_ENABLED": "0", "SCRUMBOY_MERMAID_NOTES_ENABLED": "1", "SCRUMBOY_INLINE_IMAGES_ENABLED": "1"}, noteFeatures{}},
		{"mermaid 0", "full", map[string]string{"SCRUMBOY_MERMAID_NOTES_ENABLED": "0"}, noteFeatures{true, false, true}},
		{"images 0", "full", map[string]string{"SCRUMBOY_INLINE_IMAGES_ENABLED": "0"}, noteFeatures{true, true, false}},
		{"mixed case", "full", map[string]string{"SCRUMBOY_MARKDOWN_NOTES_ENABLED": "YES", "SCRUMBOY_MERMAID_NOTES_ENABLED": "Off", "SCRUMBOY_INLINE_IMAGES_ENABLED": "True"}, noteFeatures{true, false, true}},
		{"anonymous defaults", "anonymous", nil, noteFeatures{true, true, false}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newAssetHTTPFixture(t, assetFixtureOptions{configure: envFeatureOptions(t, c.mode, c.env)})
			resp, raw := f.doJSON(t, "", http.MethodGet, "/api/auth/status", nil)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status %d: %s", resp.StatusCode, raw)
			}
			var got noteFeatures
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			if got != c.want || f.srv.inlineImagesEnabled != c.want.Images {
				t.Fatalf("reported %+v (server images %v), want %+v", got, f.srv.inlineImagesEnabled, c.want)
			}
		})
	}
}

// With images disabled explicitly, or through Markdown, the image routes stay
// indistinguishable from unknown routes, as before the default changed.
func TestDisabledImageRoutesStayHidden(t *testing.T) {
	on := newAssetHTTPFixture(t, assetFixtureOptions{configure: envFeatureOptions(t, "full", nil)})
	id := on.upload(t, "maintainer", "", "x.png", testPNG(t, 1)).assetID()
	if id == "" {
		t.Fatal("default configuration must accept uploads")
	}
	for name, env := range map[string]map[string]string{
		"images 0":   {"SCRUMBOY_INLINE_IMAGES_ENABLED": "0"},
		"markdown 0": {"SCRUMBOY_MARKDOWN_NOTES_ENABLED": "0"},
	} {
		t.Run(name, func(t *testing.T) {
			off := newAssetHTTPFixture(t, assetFixtureOptions{configure: envFeatureOptions(t, "full", env)})
			up := off.upload(t, "maintainer", "", "x.png", testPNG(t, 1))
			baseline := off.doUpload(t, "maintainer", "", bytes.NewReader(nil), "application/json")
			if up.status != http.StatusNotFound || string(up.raw) != string(baseline.raw) || off.assetRows(t) != 0 {
				t.Fatalf("upload: %d %s vs %s", up.status, up.raw, baseline.raw)
			}
			get := off.get(t, "maintainer", id, nil)
			body, _ := io.ReadAll(get.Body)
			unknown := off.get(t, "maintainer", "../nothing", nil)
			unknownBody, _ := io.ReadAll(unknown.Body)
			if get.StatusCode != http.StatusNotFound || string(body) != string(unknownBody) {
				t.Fatalf("serve: %d %s", get.StatusCode, body)
			}
			if off.srv.assetService != nil {
				t.Fatal("asset service constructed while disabled")
			}
		})
	}
}

// Under the new defaults, visitors and nonmembers still cannot upload to a published board, and
// public reads still serve only images an active story embeds.
func TestPublicBoardImageRestrictionsUnderDefaults(t *testing.T) {
	f := newAssetHTTPFixture(t, assetFixtureOptions{configure: envFeatureOptions(t, "full", nil)})
	active := f.upload(t, "maintainer", "", "a.png", testPNG(t, 1)).assetID()
	private := f.upload(t, "maintainer", "", "b.png", testPNG(t, 2)).assetID()
	f.createTodo(t, "Active", "![a](/api/assets/"+active+")", nil)
	f.publish(t)
	for _, role := range []string{"", "outsider", "viewer"} {
		rows := f.assetRows(t)
		if res := f.upload(t, role, "", "c.png", testPNG(t, 3)); res.status == http.StatusCreated || f.assetRows(t) != rows {
			t.Fatalf("%q uploaded to a published board: %d", role, res.status)
		}
	}
	if r := f.get(t, "", active, nil); r.StatusCode != http.StatusOK {
		t.Fatalf("public read of embedded image: %d", r.StatusCode)
	}
	if r := f.get(t, "", private, nil); r.StatusCode != http.StatusNotFound {
		t.Fatalf("public read of unembedded image: %d", r.StatusCode)
	}
}

// Notes written before the default changed open, save and export byte-for-byte unchanged.
func TestLegacyNotesUnchangedUnderDefaults(t *testing.T) {
	f := newAssetHTTPFixture(t, assetFixtureOptions{configure: envFeatureOptions(t, "full", nil)})
	bodies := []string{
		"plain text\r\nwith CRLF and trailing spaces  \n",
		"<script>alert(1)</script> <b>raw html</b> &amp; &#x41;",
		"![remote](https://example.com/a.png) and [link](javascript:alert(1))",
		"```mermaid\ngraph TD; A-->B\n```\n",
		"![dangling](/api/assets/33333333-3333-4333-8333-333333333333)",
	}
	for i, body := range bodies {
		todo := f.createTodo(t, "Legacy", body, nil)
		path := "/api/board/" + f.project.Slug + "/todos/" + itoa(todo.LocalID)
		var got todoJSON
		resp, raw := f.doJSON(t, "maintainer", http.MethodGet, path, nil)
		if resp.StatusCode != http.StatusOK || json.Unmarshal(raw, &got) != nil || got.Body != body {
			t.Fatalf("body %d open: %d %q", i, resp.StatusCode, got.Body)
		}
		resp, raw = f.doJSON(t, "maintainer", http.MethodPatch, path, map[string]any{"title": "Legacy edited", "body": body, "tags": []string{}, "assigneeUserId": nil})
		if resp.StatusCode != http.StatusOK || json.Unmarshal(raw, &got) != nil || got.Body != body {
			t.Fatalf("body %d save: %d %s", i, resp.StatusCode, raw)
		}
	}
	resp, raw := f.doJSON(t, "maintainer", http.MethodGet, "/api/backup/export", nil)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") == "application/zip" {
		t.Fatalf("export: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	var data struct {
		Projects []struct {
			Todos []struct {
				Body string `json:"body"`
			} `json:"todos"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	var exported []string
	for _, p := range data.Projects {
		for _, td := range p.Todos {
			exported = append(exported, td.Body)
		}
	}
	if len(exported) != len(bodies) {
		t.Fatalf("exported %d bodies, want %d", len(exported), len(bodies))
	}
	for i := range bodies {
		found := false
		for _, e := range exported {
			found = found || e == bodies[i]
		}
		if !found {
			t.Fatalf("body %d changed in export", i)
		}
	}
}
