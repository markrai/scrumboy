package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type call struct {
	Tool  string         `json:"tool"`
	Input map[string]any `json:"input"`
	Auth  string
}

// fakeServer answers POST /mcp from a tool->response map and records calls.
func fakeServer(t *testing.T, responses map[string]string) (*httptest.Server, *[]call) {
	t.Helper()
	var calls []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/mcp" {
			http.NotFound(w, r)
			return
		}
		var c call
		if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
			t.Errorf("bad request body: %v", err)
		}
		c.Auth = r.Header.Get("Authorization")
		calls = append(calls, c)
		resp, ok := responses[c.Tool]
		if !ok {
			w.WriteHeader(404)
			io.WriteString(w, `{"ok":false,"error":{"code":"NOT_FOUND","message":"tool not found","details":{}}}`)
			return
		}
		io.WriteString(w, resp)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

type harness struct {
	app      *app
	out, err *bytes.Buffer
	cfgPath  string
}

func newHarness(t *testing.T, srvURL string, env map[string]string) *harness {
	t.Helper()
	h := &harness{out: &bytes.Buffer{}, err: &bytes.Buffer{}, cfgPath: filepath.Join(t.TempDir(), "cfg.json")}
	e := map[string]string{envServer: srvURL, envToken: "sb_test", envConfig: h.cfgPath}
	for k, v := range env {
		e[k] = v
	}
	h.app = &app{out: h.out, err: h.err, in: strings.NewReader(""), getenv: func(k string) string { return e[k] }}
	return h
}

func (h *harness) run(args ...string) int { return run(args, h.app) }

const todoJSON = `{"projectSlug":"demo","localId":7,"title":"Fix it","body":"","columnKey":"doing","tags":["a"],"priorityKey":null}`

func TestBearerAndEnvelope(t *testing.T) {
	srv, calls := fakeServer(t, map[string]string{
		"projects_list": `{"ok":true,"data":{"items":[{"projectSlug":"demo","name":"Demo","role":"maintainer"}]},"meta":{}}`,
	})
	h := newHarness(t, srv.URL, nil)
	if code := h.run("project", "list"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	if (*calls)[0].Auth != "Bearer sb_test" {
		t.Errorf("auth header = %q", (*calls)[0].Auth)
	}
	if (*calls)[0].Input == nil {
		t.Errorf("input must be sent as {} not null")
	}
	if !strings.Contains(h.out.String(), "demo") || !strings.Contains(h.out.String(), "maintainer") {
		t.Errorf("table missing row: %q", h.out)
	}
}

func TestAPIErrorExitCode(t *testing.T) {
	srv, _ := fakeServer(t, map[string]string{})
	h := newHarness(t, srv.URL, nil)
	if code := h.run("project", "list"); code != 1 {
		t.Errorf("exit = %d, want 1", code)
	}
	if !strings.Contains(h.err.String(), "NOT_FOUND") {
		t.Errorf("stderr = %q", h.err)
	}
}

func TestAPIErrorIncludesSafeDetailsMax(t *testing.T) {
	err := &APIError{
		Code:    "VALIDATION_ERROR",
		Message: "validation: custom field number out of range",
		Details: map[string]any{
			"reason": "custom_field_number_out_of_range",
			"max":    float64(1 << 53),
		},
		Status: 400,
	}
	msg := err.Error()
	if !strings.Contains(msg, `"max":9.007199254740992e+15`) && !strings.Contains(msg, `"max":9007199254740992`) {
		t.Fatalf("Error() missing safe max detail: %q", msg)
	}
	if !strings.Contains(msg, "custom_field_number_out_of_range") {
		t.Fatalf("Error() missing reason: %q", msg)
	}
}

func TestNonJSONResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "<html>login</html>")
	}))
	defer srv.Close()
	h := newHarness(t, srv.URL, nil)
	if code := h.run("project", "list"); code != 1 || !strings.Contains(h.err.String(), "unexpected response") {
		t.Errorf("code=%d stderr=%q", code, h.err)
	}
}

func TestNotLoggedIn(t *testing.T) {
	h := newHarness(t, "", map[string]string{envToken: ""})
	if code := h.run("project", "list"); code != 1 || !strings.Contains(h.err.String(), "auth login") {
		t.Errorf("code=%d stderr=%q", code, h.err)
	}
}

func TestTodoUpdateSendsOnlyPassedFields(t *testing.T) {
	srv, calls := fakeServer(t, map[string]string{
		"todos_update": `{"ok":true,"data":{"todo":` + todoJSON + `},"meta":{}}`,
	})
	h := newHarness(t, srv.URL, nil)
	code := h.run("todo", "update", "demo/7", "--priority", "none", "--points", "5", "--field", "mr_url=https://x/1", "--field", "n:=3", "--field", "gone=", "--due", "2026-03-04")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	patch := (*calls)[0].Input["patch"].(map[string]any)
	if v, ok := patch["priorityKey"]; !ok || v != nil {
		t.Errorf("priorityKey should be explicit null: %#v", patch)
	}
	if patch["estimationPoints"] != float64(5) || patch["dueAt"] != "2026-03-04T00:00:00Z" {
		t.Errorf("patch = %#v", patch)
	}
	cf := patch["customFields"].(map[string]any)
	if cf["mr_url"] != "https://x/1" || cf["n"] != float64(3) || cf["gone"] != nil {
		t.Errorf("customFields = %#v", cf)
	}
	for _, absent := range []string{"title", "body", "tags", "assigneeUserId", "createdByUserId", "sprintId"} {
		if _, ok := patch[absent]; ok {
			t.Errorf("%s must not be sent when its flag is absent", absent)
		}
	}
	if (*calls)[0].Input["localId"] != float64(7) || (*calls)[0].Input["projectSlug"] != "demo" {
		t.Errorf("input = %#v", (*calls)[0].Input)
	}
}

func TestTodoUpdateCreatorNumericAndMe(t *testing.T) {
	t.Run("numeric combines with ordinary fields", func(t *testing.T) {
		srv, calls := fakeServer(t, map[string]string{
			"todos_update": `{"ok":true,"data":{"todo":` + todoJSON + `},"meta":{}}`,
		})
		h := newHarness(t, srv.URL, nil)
		if code := h.run("todo", "update", "demo/7", "--creator", "42", "--title", "Corrected"); code != 0 {
			t.Fatalf("exit %d: %s", code, h.err)
		}
		if len(*calls) != 1 {
			t.Fatalf("calls=%d want=1", len(*calls))
		}
		patch := (*calls)[0].Input["patch"].(map[string]any)
		if patch["createdByUserId"] != float64(42) || patch["title"] != "Corrected" {
			t.Fatalf("patch=%#v", patch)
		}
	})

	t.Run("me resolves through authenticated identity", func(t *testing.T) {
		srv, calls := fakeServer(t, map[string]string{
			"me_get":       `{"ok":true,"data":{"userId":23},"meta":{}}`,
			"todos_update": `{"ok":true,"data":{"todo":` + todoJSON + `},"meta":{}}`,
		})
		h := newHarness(t, srv.URL, nil)
		if code := h.run("todo", "update", "demo/7", "--creator", "me"); code != 0 {
			t.Fatalf("exit %d: %s", code, h.err)
		}
		if len(*calls) != 2 || (*calls)[0].Tool != "me_get" || (*calls)[1].Tool != "todos_update" {
			t.Fatalf("calls=%#v", *calls)
		}
		patch := (*calls)[1].Input["patch"].(map[string]any)
		if patch["createdByUserId"] != float64(23) {
			t.Fatalf("patch=%#v", patch)
		}
	})
}

func TestTodoUpdateCreatorRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{"", "none", "null", "0", "-1", "1.5", "abc", "9223372036854775808"} {
		t.Run(value, func(t *testing.T) {
			srv, calls := fakeServer(t, nil)
			h := newHarness(t, srv.URL, nil)
			if code := h.run("todo", "update", "demo/7", "--creator", value); code != 2 {
				t.Fatalf("exit=%d want=2 stderr=%q", code, h.err.String())
			}
			if !strings.Contains(h.err.String(), "--creator") || strings.Contains(h.err.String(), "--assignee") {
				t.Fatalf("creator error used wrong flag name: %q", h.err.String())
			}
			if len(*calls) != 0 {
				t.Fatalf("invalid creator reached server: %#v", *calls)
			}
		})
	}
}

func TestTodoUpdateCreatorRejectsInvalidResolvedIdentity(t *testing.T) {
	srv, calls := fakeServer(t, map[string]string{
		"me_get": `{"ok":true,"data":{"userId":0},"meta":{}}`,
	})
	h := newHarness(t, srv.URL, nil)
	if code := h.run("todo", "update", "demo/7", "--creator", "me"); code != 2 {
		t.Fatalf("exit=%d want=2 stderr=%q", code, h.err.String())
	}
	if !strings.Contains(h.err.String(), "--creator") || strings.Contains(h.err.String(), "--assignee") {
		t.Fatalf("resolved identity error=%q", h.err.String())
	}
	if len(*calls) != 1 || (*calls)[0].Tool != "me_get" {
		t.Fatalf("calls=%#v", *calls)
	}
}

func TestTodoCreateDoesNotAcceptCreator(t *testing.T) {
	srv, calls := fakeServer(t, nil)
	h := newHarness(t, srv.URL, map[string]string{envProject: "demo"})
	if code := h.run("todo", "create", "No creator flag", "--creator", "42"); code != 2 {
		t.Fatalf("exit=%d want=2 stderr=%q", code, h.err.String())
	}
	if len(*calls) != 0 {
		t.Fatalf("unsupported create flag reached server: %#v", *calls)
	}
}

func TestTodoUpdateAssigneeResolutionRemainsUnchanged(t *testing.T) {
	t.Run("none clears", func(t *testing.T) {
		srv, calls := fakeServer(t, map[string]string{
			"todos_update": `{"ok":true,"data":{"todo":` + todoJSON + `},"meta":{}}`,
		})
		h := newHarness(t, srv.URL, nil)
		if code := h.run("todo", "update", "demo/7", "--assignee", "none"); code != 0 {
			t.Fatalf("exit %d: %s", code, h.err)
		}
		patch := (*calls)[0].Input["patch"].(map[string]any)
		if value, present := patch["assigneeUserId"]; !present || value != nil {
			t.Fatalf("assignee clear patch=%#v", patch)
		}
	})

	t.Run("me resolves", func(t *testing.T) {
		srv, calls := fakeServer(t, map[string]string{
			"me_get":       `{"ok":true,"data":{"userId":31},"meta":{}}`,
			"todos_update": `{"ok":true,"data":{"todo":` + todoJSON + `},"meta":{}}`,
		})
		h := newHarness(t, srv.URL, nil)
		if code := h.run("todo", "update", "demo/7", "--assignee", "me"); code != 0 {
			t.Fatalf("exit %d: %s", code, h.err)
		}
		if len(*calls) != 2 || (*calls)[0].Tool != "me_get" || (*calls)[1].Input["patch"].(map[string]any)["assigneeUserId"] != float64(31) {
			t.Fatalf("calls=%#v", *calls)
		}
	})
}

func TestTodoUpdateNeedsAField(t *testing.T) {
	srv, calls := fakeServer(t, nil)
	h := newHarness(t, srv.URL, nil)
	if code := h.run("todo", "update", "demo/7"); code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if len(*calls) != 0 {
		t.Errorf("no request should be made")
	}
}

func TestTodoCreateFlagsAfterPositional(t *testing.T) {
	srv, calls := fakeServer(t, map[string]string{
		"todos_create": `{"ok":true,"data":{"todo":` + todoJSON + `},"meta":{}}`,
	})
	h := newHarness(t, srv.URL, map[string]string{envProject: "demo"})
	if code := h.run("todo", "create", "Fix it", "--tag", "a", "--tag", "b", "--column", "doing"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	in := (*calls)[0].Input
	if in["title"] != "Fix it" || in["projectSlug"] != "demo" || in["columnKey"] != "doing" {
		t.Errorf("input = %#v", in)
	}
	if tags := in["tags"].([]any); len(tags) != 2 {
		t.Errorf("tags = %#v", tags)
	}
}

func TestTodoListFlattensAndPaginates(t *testing.T) {
	page := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var c call
		json.NewDecoder(r.Body).Decode(&c)
		if c.Input["columnKey"] == "todo" && c.Input["cursorByColumn"] != nil {
			page++
			io.WriteString(w, `{"ok":true,"data":{"columns":[{"key":"todo","name":"Todo","items":[{"projectSlug":"demo","localId":2,"title":"two","columnKey":"todo"}]}]},"meta":{"nextCursorByColumn":{"todo":null}}}`)
			return
		}
		io.WriteString(w, `{"ok":true,"data":{"columns":[`+
			`{"key":"todo","name":"Todo","items":[{"projectSlug":"demo","localId":1,"title":"one","columnKey":"todo"}]},`+
			`{"key":"done","name":"Done","isDone":true,"items":[]}]},`+
			`"meta":{"nextCursorByColumn":{"todo":"CUR","done":null},"totalCountByColumn":{"todo":2,"done":0}}}`)
	}))
	defer srv.Close()
	h := newHarness(t, srv.URL, map[string]string{envProject: "demo"})
	if code := h.run("todo", "list", "--all", "--json"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	var items []todo
	if err := json.Unmarshal(h.out.Bytes(), &items); err != nil {
		t.Fatalf("output is not a JSON array: %v\n%s", err, h.out)
	}
	if len(items) != 2 || page != 1 {
		t.Errorf("items=%d pages=%d, want 2 items from 1 extra page", len(items), page)
	}
}

func TestDeleteRefusesWithoutYes(t *testing.T) {
	srv, calls := fakeServer(t, map[string]string{"todos_delete": `{"ok":true,"data":{},"meta":{}}`})
	h := newHarness(t, srv.URL, nil)
	if code := h.run("todo", "delete", "demo/7"); code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
	if len(*calls) != 0 {
		t.Errorf("delete must not be sent without --yes")
	}
	if code := h.run("todo", "delete", "demo/7", "--yes"); code != 0 {
		t.Errorf("exit = %d with --yes: %s", code, h.err)
	}
}

func TestToolPassthrough(t *testing.T) {
	srv, calls := fakeServer(t, map[string]string{"metrics_getBurndown": `{"ok":true,"data":{"points":[]},"meta":{"x":1}}`})
	h := newHarness(t, srv.URL, nil)
	if code := h.run("tool", "metrics_getBurndown", "--input", `{"projectSlug":"demo"}`, "--envelope"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	if (*calls)[0].Input["projectSlug"] != "demo" || !strings.Contains(h.out.String(), `"meta"`) {
		t.Errorf("calls=%#v out=%s", *calls, h.out)
	}
}

func TestLoginSavesPrivateConfig(t *testing.T) {
	srv, _ := fakeServer(t, map[string]string{
		"me_get": `{"ok":true,"data":{"userId":1,"email":"a@b.c","name":"Ann"},"meta":{}}`,
	})
	h := newHarness(t, "", map[string]string{envToken: ""})
	h.app.in = strings.NewReader("sb_secret\n")
	if code := h.run("auth", "login", "--server", srv.URL, "--with-token"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	st, err := os.Stat(h.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 { // Windows has no POSIX modes
		t.Errorf("config mode = %v, want 0600", st.Mode().Perm())
	}
	cfg, _ := loadConfig(h.cfgPath)
	if cfg.Token != "sb_secret" || cfg.Server != srv.URL {
		t.Errorf("cfg = %+v", cfg)
	}
	// Saved config alone is enough for later commands.
	h2 := newHarness(t, "", map[string]string{envToken: "", envConfig: h.cfgPath})
	if code := h2.run("auth", "status"); code != 0 || !strings.Contains(h2.out.String(), "Ann") {
		t.Errorf("status code=%d out=%q err=%q", code, h2.out, h2.err)
	}
}

func TestLoginRejectsBadTokenWithoutSaving(t *testing.T) {
	srv, _ := fakeServer(t, map[string]string{})
	h := newHarness(t, "", map[string]string{envToken: ""})
	h.app.in = strings.NewReader("sb_bad\n")
	if code := h.run("auth", "login", "--server", srv.URL, "--with-token"); code == 0 {
		t.Fatal("login should fail")
	}
	if _, err := os.Stat(h.cfgPath); err == nil {
		t.Error("config must not be written when verification fails")
	}
}

func TestSettingsPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	if err := saveConfig(path, fileConfig{Server: "http://file", Token: "file", Project: "file"}); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{envServer: "http://env", envToken: "env"}
	s, _, err := resolveSettings("http://flag", "", "", path, func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if s.Server != "http://flag" || s.Token != "env" || s.Project != "file" {
		t.Errorf("settings = %+v", s)
	}
}

func TestParseRef(t *testing.T) {
	cases := []struct {
		in, def, slug string
		id            int64
		bad           bool
	}{
		{"demo/12", "", "demo", 12, false},
		{"demo#12", "", "demo", 12, false},
		{"12", "demo", "demo", 12, false},
		{"other/3", "demo", "other", 3, false},
		{"12", "", "", 0, true},
		{"demo/x", "", "", 0, true},
		{"demo/0", "", "", 0, true},
	}
	for _, c := range cases {
		slug, id, err := parseRef(c.in, c.def)
		if (err != nil) != c.bad || slug != c.slug || id != c.id {
			t.Errorf("parseRef(%q,%q) = %q,%d,%v", c.in, c.def, slug, id, err)
		}
	}
}

func TestNormalizeServer(t *testing.T) {
	for in, want := range map[string]string{
		"https://x.test/":         "https://x.test",
		"x.test":                  "https://x.test",
		"http://localhost:8080//": "http://localhost:8080",
	} {
		if got, err := normalizeServer(in); err != nil || got != want {
			t.Errorf("normalizeServer(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := normalizeServer("ftp://x"); err == nil {
		t.Error("ftp scheme must be rejected")
	}
	if insecureWarning("http://example.com") == "" || insecureWarning("http://localhost:8080") != "" {
		t.Error("insecureWarning should flag only non-local http")
	}
}

func TestSprintListUsesStateAndMillisDates(t *testing.T) {
	srv, _ := fakeServer(t, map[string]string{
		"sprints_list": `{"ok":true,"data":{"items":[{"sprintId":4,"number":1,"name":"S1","state":"ACTIVE","plannedStartAt":1767225600000,"plannedEndAt":1768435200000}]},"meta":{}}`,
	})
	h := newHarness(t, srv.URL, map[string]string{envProject: "demo"})
	if code := h.run("sprint", "list"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	for _, want := range []string{"ACTIVE", "2026-01-01", "2026-01-15"} {
		if !strings.Contains(h.out.String(), want) {
			t.Errorf("output missing %q: %s", want, h.out)
		}
	}
}

func TestDoubleDashMakesRestPositional(t *testing.T) {
	srv, calls := fakeServer(t, map[string]string{
		"todos_create": `{"ok":true,"data":{"todo":` + todoJSON + `},"meta":{}}`,
	})
	h := newHarness(t, srv.URL, map[string]string{envProject: "demo"})
	if code := h.run("todo", "create", "--tag", "x", "--", "-fix --json"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	if (*calls)[0].Input["title"] != "-fix --json" {
		t.Errorf("title = %v", (*calls)[0].Input["title"])
	}
	if strings.HasPrefix(strings.TrimSpace(h.out.String()), "{") {
		t.Errorf("--json after -- must not be parsed as a flag")
	}
}

func TestEmptyValueDoesNotClear(t *testing.T) {
	srv, calls := fakeServer(t, nil)
	h := newHarness(t, srv.URL, nil)
	for _, flag := range []string{"--assignee", "--priority", "--points", "--due", "--sprint"} {
		if code := h.run("todo", "update", "demo/7", flag, ""); code != 2 {
			t.Errorf("%s \"\": exit %d, want 2", flag, code)
		}
	}
	if len(*calls) != 0 {
		t.Error("an empty flag value must never reach the server")
	}
}

func TestMoveAndExtraPositionals(t *testing.T) {
	srv, calls := fakeServer(t, map[string]string{
		"todos_move": `{"ok":true,"data":{"todo":` + todoJSON + `},"meta":{}}`,
	})
	h := newHarness(t, srv.URL, map[string]string{envProject: "demo"})
	if code := h.run("todo", "move", "7", "done", "--after", "3"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	in := (*calls)[0].Input
	if in["toColumnKey"] != "done" || in["afterLocalId"] != float64(3) || in["localId"] != float64(7) {
		t.Errorf("input = %#v", in)
	}
	if code := h.run("todo", "list", "stray"); code != 2 {
		t.Errorf("stray positional: exit %d, want 2", code)
	}
}

func TestListJSONIsLossless(t *testing.T) {
	srv, _ := fakeServer(t, map[string]string{
		"board_get": `{"ok":true,"data":{"columns":[{"key":"todo","name":"Todo","items":[{"projectSlug":"demo","localId":1,"title":"a","columnKey":"todo","createdByUserId":9}]}]},"meta":{}}`,
	})
	h := newHarness(t, srv.URL, map[string]string{envProject: "demo"})
	if code := h.run("todo", "list", "--json"); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	if !strings.Contains(h.out.String(), `"createdByUserId": 9`) {
		t.Errorf("server fields must pass through: %s", h.out)
	}
}

func TestServerNormalizedOutsideLogin(t *testing.T) {
	srv, _ := fakeServer(t, map[string]string{"projects_list": `{"ok":true,"data":{"items":[]},"meta":{}}`})
	h := newHarness(t, strings.TrimPrefix(srv.URL, "http://")+"/", nil) // no scheme, trailing slash
	h.app.getenv = func(k string) string {
		return map[string]string{envServer: strings.TrimPrefix(srv.URL, "http://") + "/", envToken: "sb_t", envConfig: h.cfgPath}[k]
	}
	// Scheme-less hosts default to https, which the plain-http test server
	// cannot serve; the point is that the URL is accepted and normalized.
	if code := h.run("project", "list"); code == 2 {
		t.Errorf("scheme-less server URL should not be a usage error: %s", h.err)
	}
}
