package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"scrumboy/internal/assets/blobstore"
	"scrumboy/internal/db"
	"scrumboy/internal/migrate"
	"scrumboy/internal/store"
)

type assetHTTPFixture struct {
	ts      *httptest.Server
	srv     *Server
	st      *store.Store
	sqlDB   *sql.DB
	blobs   *blobstore.Store
	dataDir string
	project store.Project
	tokens  map[string]string // role name -> session token
	users   map[string]store.User
}

type assetFixtureOptions struct {
	disableFlag bool
	budgets     store.AssetBudgets
	configure   func(*Options)
}

func newAssetHTTPFixture(t *testing.T, fo assetFixtureOptions) *assetHTTPFixture {
	t.Helper()
	dataDir := t.TempDir()
	sqlDB, err := db.Open(filepath.Join(dataDir, "app.db"), db.Options{BusyTimeout: 5000, JournalMode: "WAL", Synchronous: "FULL"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	ctx := context.Background()
	if err := migrate.Apply(ctx, sqlDB); err != nil {
		t.Fatal(err)
	}
	st := store.New(sqlDB, &store.StoreOptions{AssetBudgets: fo.budgets})
	blobs, err := blobstore.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	f := &assetHTTPFixture{st: st, sqlDB: sqlDB, blobs: blobs, dataDir: dataDir, tokens: map[string]string{}, users: map[string]store.User{}}

	owner, err := st.BootstrapUser(ctx, "owner@example.com", "password", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	ownerCtx := store.WithUserID(ctx, owner.ID)
	f.project, err = st.CreateProject(ownerCtx, "Images")
	if err != nil {
		t.Fatal(err)
	}
	f.users["maintainer"] = owner
	for role, pr := range map[string]store.ProjectRole{"contributor": store.RoleContributor, "viewer": store.RoleViewer, "outsider": ""} {
		u, err := st.CreateUser(ctx, role+"@example.com", "password", role)
		if err != nil {
			t.Fatal(err)
		}
		if pr != "" {
			if err := st.AddProjectMember(ownerCtx, owner.ID, f.project.ID, u.ID, pr); err != nil {
				t.Fatal(err)
			}
		}
		f.users[role] = u
	}
	for role, u := range f.users {
		token, _, err := st.CreateSession(ctx, u.ID, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		f.tokens[role] = token
	}

	opts := Options{
		Logger:                log.New(io.Discard, "", 0),
		MaxRequestBody:        1 << 20,
		ScrumboyMode:          "full",
		DataDir:               dataDir,
		MarkdownNotesEnabled:  true,
		PublicProjectsEnabled: true,
		InlineImagesEnabled:   !fo.disableFlag,
		AssetBlobs:            blobs,
		AssetPending:          blobstore.NewPending(),
	}
	if fo.configure != nil {
		fo.configure(&opts)
	}
	f.srv = NewServer(st, opts)
	f.ts = httptest.NewServer(f.srv)
	t.Cleanup(f.ts.Close)
	return f
}

func (f *assetHTTPFixture) ownerCtx() context.Context {
	return store.WithUserID(context.Background(), f.users["maintainer"].ID)
}

func (f *assetHTTPFixture) createTodo(t *testing.T, title, body string, assignee *int64) store.Todo {
	t.Helper()
	todo, err := f.st.CreateTodo(f.ownerCtx(), f.project.ID, store.CreateTodoInput{Title: title, Body: body, ColumnKey: store.DefaultColumnBacklog, AssigneeUserID: assignee}, store.ModeFull)
	if err != nil {
		t.Fatal(err)
	}
	return todo
}

func (f *assetHTTPFixture) publish(t *testing.T) {
	t.Helper()
	if _, err := f.sqlDB.Exec(`UPDATE projects SET public_view_enabled = 1 WHERE id = ?`, f.project.ID); err != nil {
		t.Fatal(err)
	}
}

type uploadResult struct {
	status int
	body   map[string]any
	raw    []byte
}

func (r uploadResult) assetID() string {
	id, _ := r.body["assetId"].(string)
	return id
}

func (r uploadResult) reason() string {
	errObj, _ := r.body["error"].(map[string]any)
	details, _ := errObj["details"].(map[string]any)
	reason, _ := details["reason"].(string)
	return reason
}

func (r uploadResult) code() string {
	errObj, _ := r.body["error"].(map[string]any)
	code, _ := errObj["code"].(string)
	return code
}

func multipartBody(t *testing.T, parts func(*multipart.Writer)) (*bytes.Buffer, string) {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	parts(mw)
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	return &b, mw.FormDataContentType()
}

func fileParts(name string, data []byte) func(*multipart.Writer) {
	return func(mw *multipart.Writer) {
		w, _ := mw.CreateFormFile("file", name)
		_, _ = w.Write(data)
	}
}

func (f *assetHTTPFixture) doUpload(t *testing.T, role, query string, body io.Reader, contentType string) uploadResult {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, f.ts.URL+"/api/board/"+f.project.Slug+"/assets"+query, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Scrumboy", "1")
	req.Header.Set("Content-Type", contentType)
	if token := f.tokens[role]; token != "" {
		req.AddCookie(&http.Cookie{Name: "scrumboy_session", Value: token})
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	res := uploadResult{status: resp.StatusCode, raw: raw}
	_ = json.Unmarshal(raw, &res.body)
	return res
}

func (f *assetHTTPFixture) upload(t *testing.T, role, query, name string, data []byte) uploadResult {
	t.Helper()
	body, ct := multipartBody(t, fileParts(name, data))
	return f.doUpload(t, role, query, body, ct)
}

func (f *assetHTTPFixture) get(t *testing.T, role, assetID string, header map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, f.ts.URL+"/api/assets/"+assetID, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	if token := f.tokens[role]; token != "" {
		req.AddCookie(&http.Cookie{Name: "scrumboy_session", Value: token})
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func (f *assetHTTPFixture) assetRows(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.sqlDB.QueryRow(`SELECT COUNT(*) FROM project_assets`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (f *assetHTTPFixture) assetFiles(t *testing.T) int {
	t.Helper()
	n := 0
	_ = f.blobs.Walk(context.Background(), func(string, string, time.Time) error { n++; return nil })
	return n
}

// testPNG returns a small valid PNG; seed varies the pixels (and so the SHA-256).
func testPNG(t testing.TB, seed int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for i := range img.Pix {
		img.Pix[i] = uint8(i*7 + seed*31)
	}
	img.Set(0, 0, color.RGBA{uint8(seed), uint8(seed >> 8), uint8(seed >> 16), 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// paddedPNG returns a valid PNG of exactly size bytes, padded with a private ancillary chunk
// (kept by the validator).
func paddedPNG(t testing.TB, size int, seed int) []byte {
	t.Helper()
	base := testPNG(t, seed)
	pad := size - len(base) - 12
	if pad < 0 {
		t.Fatalf("size %d too small", size)
	}
	chunk := make([]byte, 8, 12+pad)
	binary.BigEndian.PutUint32(chunk, uint32(pad))
	copy(chunk[4:], "prVt")
	chunk = append(chunk, make([]byte, pad)...)
	chunk = binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(chunk[4:]))
	iend := len(base) - 12
	out := append(append(append([]byte(nil), base[:iend]...), chunk...), base[iend:]...)
	if len(out) != size {
		t.Fatalf("padded size %d != %d", len(out), size)
	}
	return out
}
