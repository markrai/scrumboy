package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"scrumboy/internal/db"
	"scrumboy/internal/migrate"
	"scrumboy/internal/store"
)

type countingSlugStore struct {
	storeAPI
	slugLookups atomic.Int64
}

func (s *countingSlugStore) GetProjectContextBySlug(
	ctx context.Context,
	slug string,
	mode store.Mode,
) (store.ProjectContext, error) {
	s.slugLookups.Add(1)
	return s.storeAPI.GetProjectContextBySlug(ctx, slug, mode)
}

func (s *countingSlugStore) resetSlugLookups() {
	s.slugLookups.Store(0)
}

func (s *countingSlugStore) slugLookupCount() int64 {
	return s.slugLookups.Load()
}

func newCountingSlugTestHTTPServer(
	t *testing.T,
	mode string,
) (*httptest.Server, *store.Store, *countingSlugStore, func()) {
	t.Helper()

	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "app.db"), db.Options{
		BusyTimeout: 5000,
		JournalMode: "WAL",
		Synchronous: "FULL",
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := migrate.Apply(context.Background(), sqlDB); err != nil {
		_ = sqlDB.Close()
		t.Fatalf("migrate: %v", err)
	}

	st := store.New(sqlDB, nil)
	counting := &countingSlugStore{storeAPI: st}
	srv := NewServer(counting, Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   mode,
	})
	ts := httptest.NewServer(srv)
	return ts, st, counting, func() {
		ts.Close()
		_ = sqlDB.Close()
	}
}

func TestBoardSlugReadAccess_RESTResolvesSlugOnce(t *testing.T) {
	ts, st, counting, cleanup := newCountingSlugTestHTTPServer(t, "full")
	defer cleanup()

	ownerClient := newCookieClient(t)
	ownerJSON := bootstrapUserClient(
		t,
		ownerClient,
		ts.URL,
		"Owner",
		"board-slug-lookup-count@example.com",
		"password123",
	)
	ownerID := int64(ownerJSON["id"].(float64))
	ctxOwner := store.WithUserID(context.Background(), ownerID)
	project, err := st.CreateProject(ctxOwner, "Slug Lookup Count")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	seedSlugBoardTodo(t, st, ctxOwner, project.ID, "slug lookup count todo", store.ModeFull)

	for _, route := range slugBoardReadRoutes {
		route := route
		t.Run("valid "+route.name+" read", func(t *testing.T) {
			counting.resetSlugLookups()
			resp, body := doJSON(
				t,
				ownerClient,
				http.MethodGet,
				slugBoardReadURL(ts.URL, project.Slug, route, ""),
				nil,
				nil,
			)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET %s read: status=%d body=%s", route.name, resp.StatusCode, string(body))
			}
			if got := counting.slugLookupCount(); got != 1 {
				t.Fatalf("slug lookups = %d, want 1", got)
			}
		})
	}

	t.Run("invalid slug performs no lookup", func(t *testing.T) {
		counting.resetSlugLookups()
		resp, body := doJSON(
			t,
			ownerClient,
			http.MethodGet,
			ts.URL+"/api/board/-invalid",
			nil,
			nil,
		)
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("GET invalid slug: status=%d body=%s", resp.StatusCode, string(body))
		}
		if got := counting.slugLookupCount(); got != 0 {
			t.Fatalf("slug lookups = %d, want 0", got)
		}
	})

	t.Run("denied valid slug performs one lookup", func(t *testing.T) {
		counting.resetSlugLookups()
		signedOut := newCookieClient(t)
		resp, body := doJSON(
			t,
			signedOut,
			http.MethodGet,
			ts.URL+"/api/board/"+project.Slug,
			nil,
			nil,
		)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("GET denied slug: status=%d body=%s", resp.StatusCode, string(body))
		}
		if got := counting.slugLookupCount(); got != 1 {
			t.Fatalf("slug lookups = %d, want 1", got)
		}
	})
}
