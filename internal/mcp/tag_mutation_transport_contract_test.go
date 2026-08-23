package mcp_test

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"scrumboy/internal/db"
	"scrumboy/internal/httpapi"
	"scrumboy/internal/mcp"
	"scrumboy/internal/migrate"
	"scrumboy/internal/store"
)

type tagMutationMCPStore struct {
	*store.Store

	mu sync.Mutex

	active  bool
	trace   []string
	mutated bool

	postReadErr error
	omitPostTag bool
	targetTagID int64
	targetName  string

	projectReadCalls int
	mineReadCalls    int
	updateCalls      int
	deleteCalls      int
}

func (s *tagMutationMCPStore) activate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = true
	s.trace = nil
	s.mutated = false
	s.projectReadCalls = 0
	s.mineReadCalls = 0
	s.updateCalls = 0
	s.deleteCalls = 0
}

func (s *tagMutationMCPStore) record(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return false
	}
	s.trace = append(s.trace, name)
	return true
}

func (s *tagMutationMCPStore) traceSnapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.trace...)
}

func (s *tagMutationMCPStore) GetProjectContextBySlug(ctx context.Context, slug string, mode store.Mode) (store.ProjectContext, error) {
	if !s.record("access") {
		return s.Store.GetProjectContextBySlug(ctx, slug, mode)
	}
	return s.Store.GetProjectContextBySlug(ctx, slug, mode)
}

func (s *tagMutationMCPStore) ListUserTags(ctx context.Context, userID int64) ([]store.TagWithColor, error) {
	if !s.record("mine-read") {
		return s.Store.ListUserTags(ctx, userID)
	}
	s.mu.Lock()
	s.mineReadCalls++
	s.mu.Unlock()
	return s.Store.ListUserTags(ctx, userID)
}

func (s *tagMutationMCPStore) ListTagCounts(ctx context.Context, pc *store.ProjectContext) ([]store.TagCount, error) {
	s.mu.Lock()
	if !s.active {
		s.mu.Unlock()
		return s.Store.ListTagCounts(ctx, pc)
	}
	s.projectReadCalls++
	isPost := s.mutated
	stage := "project-read-pre"
	if isPost {
		stage = "project-read-post"
	}
	s.trace = append(s.trace, stage)
	postErr := s.postReadErr
	omit := s.omitPostTag
	targetID := s.targetTagID
	targetName := s.targetName
	s.mu.Unlock()
	if isPost && postErr != nil {
		return nil, postErr
	}
	tags, err := s.Store.ListTagCounts(ctx, pc)
	if err != nil || !isPost || !omit {
		return tags, err
	}
	filtered := tags[:0]
	for _, tag := range tags {
		if (targetID != 0 && tag.TagID == targetID) || (targetName != "" && tag.Name == targetName) {
			continue
		}
		filtered = append(filtered, tag)
	}
	return filtered, nil
}

func (s *tagMutationMCPStore) UpdateTagColor(ctx context.Context, viewerUserID *int64, tagID int64, color *string) error {
	if !s.record("mine-color") {
		return s.Store.UpdateTagColor(ctx, viewerUserID, tagID, color)
	}
	s.mu.Lock()
	s.updateCalls++
	s.mu.Unlock()
	return s.Store.UpdateTagColor(ctx, viewerUserID, tagID, color)
}

func (s *tagMutationMCPStore) UpdateTagColorForDurableProjectByID(ctx context.Context, projectID, viewerUserID, tagID int64, color *string) error {
	if !s.record("durable-id-color") {
		return s.Store.UpdateTagColorForDurableProjectByID(ctx, projectID, viewerUserID, tagID, color)
	}
	s.mu.Lock()
	s.updateCalls++
	s.mu.Unlock()
	err := s.Store.UpdateTagColorForDurableProjectByID(ctx, projectID, viewerUserID, tagID, color)
	if err == nil {
		s.mu.Lock()
		s.mutated = true
		s.mu.Unlock()
	}
	return err
}

func (s *tagMutationMCPStore) UpdateTagColorForTemporaryBoard(ctx context.Context, projectID int64, viewerUserID *int64, tagID int64, color *string) error {
	if !s.record("temporary-id-color") {
		return s.Store.UpdateTagColorForTemporaryBoard(ctx, projectID, viewerUserID, tagID, color)
	}
	s.mu.Lock()
	s.updateCalls++
	s.mu.Unlock()
	err := s.Store.UpdateTagColorForTemporaryBoard(ctx, projectID, viewerUserID, tagID, color)
	if err == nil {
		s.mu.Lock()
		s.mutated = true
		s.mu.Unlock()
	}
	return err
}

func (s *tagMutationMCPStore) SetViewerTagColorByName(ctx context.Context, projectID, viewerUserID int64, name string, color *string) error {
	if !s.record("name-color") {
		return s.Store.SetViewerTagColorByName(ctx, projectID, viewerUserID, name, color)
	}
	s.mu.Lock()
	s.updateCalls++
	s.mu.Unlock()
	err := s.Store.SetViewerTagColorByName(ctx, projectID, viewerUserID, name, color)
	if err == nil {
		s.mu.Lock()
		s.mutated = true
		s.mu.Unlock()
	}
	return err
}

func (s *tagMutationMCPStore) GetProjectScopedTagByID(ctx context.Context, projectID, tagID int64) (store.TagWithColor, error) {
	if !s.record("project-target") {
		return s.Store.GetProjectScopedTagByID(ctx, projectID, tagID)
	}
	return s.Store.GetProjectScopedTagByID(ctx, projectID, tagID)
}

func (s *tagMutationMCPStore) DeleteTag(ctx context.Context, userID, tagID int64, isAnonymousBoard bool) error {
	if !s.record("delete") {
		return s.Store.DeleteTag(ctx, userID, tagID, isAnonymousBoard)
	}
	s.mu.Lock()
	s.deleteCalls++
	s.mu.Unlock()
	err := s.Store.DeleteTag(ctx, userID, tagID, isAnonymousBoard)
	if err == nil {
		s.mu.Lock()
		s.mutated = true
		s.mu.Unlock()
	}
	return err
}

type tagMutationMCPFixture struct {
	ts      *httptest.Server
	db      *sql.DB
	st      *store.Store
	wrapped *tagMutationMCPStore
	client  *http.Client
	ownerID int64
	ctx     context.Context
	project store.Project
}

func newTagMutationMCPFixture(t *testing.T, name string) *tagMutationMCPFixture {
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
	wrapped := &tagMutationMCPStore{Store: st}
	srv := httpapi.NewServer(wrapped, httpapi.Options{
		MaxRequestBody: 1 << 20,
		ScrumboyMode:   "full",
		MCPHandler:     mcp.New(wrapped, mcp.Options{Mode: "full"}),
	})
	ts := httptest.NewServer(srv)
	t.Cleanup(func() {
		ts.Close()
		_ = sqlDB.Close()
	})
	client := newCookieClient(t, ts)
	bootstrapUser(t, client, ts.URL)
	ownerID := firstUserID(t, sqlDB)
	ctx := store.WithUserID(context.Background(), ownerID)
	project, err := st.CreateProject(ctx, name)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	return &tagMutationMCPFixture{
		ts: ts, db: sqlDB, st: st, wrapped: wrapped, client: client,
		ownerID: ownerID, ctx: ctx, project: project,
	}
}

func createTagMutationMCPPersonalTag(t *testing.T, fx *tagMutationMCPFixture, name string) int64 {
	t.Helper()
	_, err := fx.st.CreateTodo(fx.ctx, fx.project.ID, store.CreateTodoInput{
		Title: "todo " + name, Tags: []string{name}, ColumnKey: "backlog",
	}, store.ModeFull)
	if err != nil {
		t.Fatalf("create personal tag fixture %q: %v", name, err)
	}
	var tagID int64
	if err := fx.db.QueryRow(`SELECT id FROM tags WHERE user_id = ? AND name = ?`, fx.ownerID, name).Scan(&tagID); err != nil {
		t.Fatalf("read personal tag %q: %v", name, err)
	}
	return tagID
}

func subscribeTagMutationMCPEvents(t *testing.T, fx *tagMutationMCPFixture, project store.Project) *todoUpdateMCPEventStream {
	t.Helper()
	return subscribeTodoUpdateMCPEvents(t, fx.client, fx.ts.URL+"/api/board/"+project.Slug+"/events")
}

func assertTagMutationMCPSilence(t *testing.T, stream *todoUpdateMCPEventStream) {
	t.Helper()
	if events := collectTodoUpdateMCPEvents(t, stream); len(events) != 0 {
		t.Fatalf("MCP tag mutation emitted realtime events: %+v", events)
	}
}

func assertTagMutationMCPTrace(t *testing.T, wrapped *tagMutationMCPStore, want ...string) {
	t.Helper()
	if got := wrapped.traceSnapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("store trace=%v want=%v", got, want)
	}
}

func assertTagMutationMCPTag(t *testing.T, data map[string]any, wantID int64, wantName string, wantColor any) {
	t.Helper()
	if got, want := sortedMapKeys(data), []string{"tag"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("data keys=%v want=%v data=%+v", got, want, data)
	}
	tag, ok := data["tag"].(map[string]any)
	if !ok {
		t.Fatalf("tag projection type=%T data=%+v", data["tag"], data)
	}
	if int64(tag["tagId"].(float64)) != wantID || tag["name"] != wantName || tag["color"] != wantColor {
		t.Fatalf("tag=%+v want id=%d name=%q color=%+v", tag, wantID, wantName, wantColor)
	}
}

// TestMCPTagsMutationTransportsAliasesAndRealtime proves that all four canonical
// mutation names and their permanent dotted aliases execute over both MCP transports.
// It also freezes the intentional absence of board refresh/SSE publication.
func TestMCPTagsMutationTransportsAliasesAndRealtime(t *testing.T) {
	tools := []struct {
		canonical string
		alias     string
		kind      string
	}{
		{"tags_updateMineColor", "tags.updateMineColor", "update-mine"},
		{"tags_updateProjectColor", "tags.updateProjectColor", "update-project"},
		{"tags_deleteMine", "tags.deleteMine", "delete-mine"},
		{"tags_deleteProject", "tags.deleteProject", "delete-project"},
	}
	for _, toolCase := range tools {
		for _, toolName := range []string{toolCase.canonical, toolCase.alias} {
			for _, transport := range []string{"legacy", "jsonrpc"} {
				t.Run(transport+"/"+toolName, func(t *testing.T) {
					fx := newTagMutationMCPFixture(t, strings.ReplaceAll(toolName, ".", "-")+"-"+transport)
					var tagID int64
					var args map[string]any
					switch toolCase.kind {
					case "update-mine":
						tagID = createTagMutationMCPPersonalTag(t, fx, "mine-update")
						args = map[string]any{"tagId": tagID, "color": "#123456"}
					case "delete-mine":
						tagID = createTagMutationMCPPersonalTag(t, fx, "mine-delete")
						args = map[string]any{"tagId": tagID}
					case "update-project":
						tagID = insertProjectScopedTag(t, fx.db, fx.project.ID, "project-update", nil)
						args = map[string]any{"projectSlug": fx.project.Slug, "tagId": tagID, "color": "#654321"}
					case "delete-project":
						tagID = insertProjectScopedTag(t, fx.db, fx.project.ID, "project-delete", nil)
						args = map[string]any{"projectSlug": fx.project.Slug, "tagId": tagID}
					}

					stream := subscribeTagMutationMCPEvents(t, fx, fx.project)
					defer stream.close()
					resp, out := callTodoUpdateMCP(t, fx.client, fx.ts.URL, transport, toolName, args)
					if resp.StatusCode != http.StatusOK {
						t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
					}
					data := todoLinkMCPData(t, transport, out)
					switch toolCase.kind {
					case "update-mine":
						assertTagMutationMCPTag(t, data, tagID, "mine-update", "#123456")
					case "update-project":
						assertTagMutationMCPTag(t, data, tagID, "project-update", "#654321")
					case "delete-mine":
						deleted := data["deleted"].(map[string]any)
						if got, want := sortedMapKeys(deleted), []string{"tagId"}; !reflect.DeepEqual(got, want) || int64(deleted["tagId"].(float64)) != tagID {
							t.Fatalf("deleted=%+v", deleted)
						}
					case "delete-project":
						deleted := data["deleted"].(map[string]any)
						if got, want := sortedMapKeys(deleted), []string{"projectSlug", "tagId"}; !reflect.DeepEqual(got, want) || deleted["projectSlug"] != fx.project.Slug || int64(deleted["tagId"].(float64)) != tagID {
							t.Fatalf("deleted=%+v", deleted)
						}
					}
					assertTagMutationMCPSilence(t, stream)
				})
			}
		}
	}
}

// TestMCPTagsMutationReadAndDispatchSequencing characterizes the application-boundary
// store call sequence: mine update is pre-read plus synthetic projection, project ID
// update is pre-read/mutate/post-read, name update is mutate/post-read, and deletes do
// not perform a post-delete read.
func TestMCPTagsMutationReadAndDispatchSequencing(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		t.Run(transport+"/mine update synthetic result", func(t *testing.T) {
			fx := newTagMutationMCPFixture(t, "mine-sequence-"+transport)
			tagID := createTagMutationMCPPersonalTag(t, fx, "mine-sequence")
			fx.wrapped.activate()
			resp, out := callTodoUpdateMCP(t, fx.client, fx.ts.URL, transport, "tags_updateMineColor", map[string]any{"tagId": tagID, "color": "  #abcdef  "})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
			}
			assertTagMutationMCPTag(t, todoLinkMCPData(t, transport, out), tagID, "mine-sequence", "#abcdef")
			assertTagMutationMCPTrace(t, fx.wrapped, "mine-read", "mine-color")
			if fx.wrapped.mineReadCalls != 1 || fx.wrapped.updateCalls != 1 || fx.wrapped.projectReadCalls != 0 {
				t.Fatalf("calls mine=%d update=%d project=%d", fx.wrapped.mineReadCalls, fx.wrapped.updateCalls, fx.wrapped.projectReadCalls)
			}
		})

		t.Run(transport+"/durable id update pre and post read", func(t *testing.T) {
			fx := newTagMutationMCPFixture(t, "id-sequence-"+transport)
			tagID := insertProjectScopedTag(t, fx.db, fx.project.ID, "id-sequence", nil)
			fx.wrapped.activate()
			resp, out := callTodoUpdateMCP(t, fx.client, fx.ts.URL, transport, "tags_updateProjectColor", map[string]any{"projectSlug": fx.project.Slug, "tagId": tagID, "color": "#abcdef"})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
			}
			assertTagMutationMCPTrace(t, fx.wrapped, "access", "project-read-pre", "durable-id-color", "project-read-post")
		})

		t.Run(transport+"/temporary id update skips durable role gate", func(t *testing.T) {
			fx := newTagMutationMCPFixture(t, "temporary-id-sequence-"+transport)
			temporary, err := fx.st.CreateAnonymousBoard(fx.ctx)
			if err != nil {
				t.Fatalf("create creator temporary board: %v", err)
			}
			if _, err := fx.st.CreateTodo(fx.ctx, temporary.ID, store.CreateTodoInput{
				Title: "temporary", Tags: []string{"temporary-id"}, ColumnKey: "backlog",
			}, store.ModeFull); err != nil {
				t.Fatalf("create temporary tag fixture: %v", err)
			}
			var tagID int64
			if err := fx.db.QueryRow(`SELECT id FROM tags WHERE user_id = ? AND name = 'temporary-id'`, fx.ownerID).Scan(&tagID); err != nil {
				t.Fatalf("read temporary tag: %v", err)
			}
			fx.wrapped.activate()
			resp, out := callTodoUpdateMCP(t, fx.client, fx.ts.URL, transport, "tags_updateProjectColor", map[string]any{"projectSlug": temporary.Slug, "tagId": tagID, "color": "#abcdef"})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
			}
			assertTagMutationMCPTrace(t, fx.wrapped, "access", "project-read-pre", "temporary-id-color", "project-read-post")
		})

		t.Run(transport+"/name update post read only", func(t *testing.T) {
			fx := newTagMutationMCPFixture(t, "name-sequence-"+transport)
			createTagMutationMCPPersonalTag(t, fx, "name-sequence")
			fx.wrapped.activate()
			resp, out := callTodoUpdateMCP(t, fx.client, fx.ts.URL, transport, "tags_updateProjectColor", map[string]any{"projectSlug": fx.project.Slug, "tagName": "name-sequence", "color": "#abcdef"})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
			}
			assertTagMutationMCPTrace(t, fx.wrapped, "access", "name-color", "project-read-post")
		})

		t.Run(transport+"/mine delete has no post read", func(t *testing.T) {
			fx := newTagMutationMCPFixture(t, "mine-delete-sequence-"+transport)
			tagID := createTagMutationMCPPersonalTag(t, fx, "mine-delete-sequence")
			fx.wrapped.activate()
			resp, out := callTodoUpdateMCP(t, fx.client, fx.ts.URL, transport, "tags_deleteMine", map[string]any{"tagId": tagID})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
			}
			_ = todoLinkMCPData(t, transport, out)
			assertTagMutationMCPTrace(t, fx.wrapped, "mine-read", "delete")
		})

		t.Run(transport+"/project delete has no post read", func(t *testing.T) {
			fx := newTagMutationMCPFixture(t, "project-delete-sequence-"+transport)
			tagID := insertProjectScopedTag(t, fx.db, fx.project.ID, "project-delete-sequence", nil)
			fx.wrapped.activate()
			resp, out := callTodoUpdateMCP(t, fx.client, fx.ts.URL, transport, "tags_deleteProject", map[string]any{"projectSlug": fx.project.Slug, "tagId": tagID})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
			}
			_ = todoLinkMCPData(t, transport, out)
			assertTagMutationMCPTrace(t, fx.wrapped, "access", "project-target", "delete")
		})
	}
}

// TestMCPTagsMutationCommittedUpdatePostReadFailure freezes the non-atomic public
// behavior: a successful color write followed by a failed projection returns INTERNAL,
// while the write remains committed and is neither retried nor rolled back.
func TestMCPTagsMutationCommittedUpdatePostReadFailure(t *testing.T) {
	for _, tc := range []struct {
		name      string
		transport string
		byName    bool
		readErr   bool
	}{
		{"legacy id post-read error", "legacy", false, true},
		{"jsonrpc name post-read error", "jsonrpc", true, true},
		{"jsonrpc id post-read target missing", "jsonrpc", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newTagMutationMCPFixture(t, strings.ReplaceAll(tc.name, " ", "-"))
			args := map[string]any{"projectSlug": fx.project.Slug, "color": "#a1b2c3"}
			var tagID int64
			if tc.byName {
				tagID = createTagMutationMCPPersonalTag(t, fx, "post-read-name")
				args["tagName"] = "post-read-name"
				fx.wrapped.targetName = "post-read-name"
			} else {
				tagID = insertProjectScopedTag(t, fx.db, fx.project.ID, "post-read-id", nil)
				args["tagId"] = tagID
				fx.wrapped.targetTagID = tagID
			}
			if tc.readErr {
				fx.wrapped.postReadErr = errors.New("forced post-read failure")
			} else {
				fx.wrapped.omitPostTag = true
			}
			stream := subscribeTagMutationMCPEvents(t, fx, fx.project)
			defer stream.close()
			fx.wrapped.activate()
			resp, out := callTodoUpdateMCP(t, fx.client, fx.ts.URL, tc.transport, "tags_updateProjectColor", args)
			publicError := assertTodoLinkMCPError(t, tc.transport, resp, out, http.StatusInternalServerError, "INTERNAL", "internal error")
			if tc.transport == "jsonrpc" {
				assertEmptyTodoLinkMCPDetails(t, publicError)
			}
			if fx.wrapped.updateCalls != 1 {
				t.Fatalf("update calls=%d want=1", fx.wrapped.updateCalls)
			}
			if tc.byName {
				assertTagMutationMCPTrace(t, fx.wrapped, "access", "name-color", "project-read-post")
				var color string
				if err := fx.db.QueryRow(`SELECT color FROM user_tag_colors WHERE user_id = ? AND tag_id = ?`, fx.ownerID, tagID).Scan(&color); err != nil || color != "#a1b2c3" {
					t.Fatalf("committed name color=%q err=%v", color, err)
				}
			} else {
				assertTagMutationMCPTrace(t, fx.wrapped, "access", "project-read-pre", "durable-id-color", "project-read-post")
				var color string
				if err := fx.db.QueryRow(`SELECT color FROM tags WHERE id = ?`, tagID).Scan(&color); err != nil || color != "#a1b2c3" {
					t.Fatalf("committed id color=%q err=%v", color, err)
				}
			}
			assertTagMutationMCPSilence(t, stream)
		})
	}
}

// TestMCPTagsMutationValidationAndAuthorityOrdering pins the adapter-level decision
// order that is otherwise easy to change while moving orchestration inward.
func TestMCPTagsMutationValidationAndAuthorityOrdering(t *testing.T) {
	t.Run("exactly-one validation before missing project", func(t *testing.T) {
		fx := newTagMutationMCPFixture(t, "exactly-one-order")
		fx.wrapped.activate()
		resp, out := callTodoUpdateMCP(t, fx.client, fx.ts.URL, "jsonrpc", "tags_updateProjectColor", map[string]any{
			"projectSlug": "missing-project", "tagId": 1, "tagName": "also-supplied", "color": "#123456",
		})
		assertTodoLinkMCPError(t, "jsonrpc", resp, out, http.StatusBadRequest, "VALIDATION_ERROR", "provide exactly one of tagId or tagName")
		assertTagMutationMCPTrace(t, fx.wrapped)
	})

	t.Run("empty color validation before inaccessible project", func(t *testing.T) {
		fx := newTagMutationMCPFixture(t, "empty-color-order")
		fx.wrapped.activate()
		resp, out := callTodoUpdateMCP(t, fx.client, fx.ts.URL, "legacy", "tags_updateProjectColor", map[string]any{
			"projectSlug": "missing-project", "tagId": 1, "color": "   ",
		})
		assertTodoLinkMCPError(t, "legacy", resp, out, http.StatusBadRequest, "VALIDATION_ERROR", "color cannot be empty; use null to clear")
		assertTagMutationMCPTrace(t, fx.wrapped)
	})

	t.Run("wrong-project color precedes insufficient role but delete role precedes target", func(t *testing.T) {
		fx := newTagMutationMCPFixture(t, "wrong-project-role-order")
		other, err := fx.st.CreateProject(fx.ctx, "Wrong Project Other")
		if err != nil {
			t.Fatalf("create other project: %v", err)
		}
		foreignTagID := insertProjectScopedTag(t, fx.db, other.ID, "foreign", nil)
		viewer, err := fx.st.CreateUser(context.Background(), "tag-order-viewer@example.com", "password123", "Viewer")
		if err != nil {
			t.Fatalf("create viewer: %v", err)
		}
		if err := fx.st.AddProjectMember(context.Background(), fx.ownerID, fx.project.ID, viewer.ID, store.RoleViewer); err != nil {
			t.Fatalf("add viewer: %v", err)
		}
		viewerClient := newSessionClientForUser(t, fx.ts, fx.st, viewer.ID)

		fx.wrapped.activate()
		resp, out := callTodoUpdateMCP(t, viewerClient, fx.ts.URL, "legacy", "tags_updateProjectColor", map[string]any{
			"projectSlug": fx.project.Slug, "tagId": foreignTagID, "color": "#123456",
		})
		assertTodoLinkMCPError(t, "legacy", resp, out, http.StatusNotFound, "NOT_FOUND", "not found")
		assertTagMutationMCPTrace(t, fx.wrapped, "access", "project-read-pre")

		fx.wrapped.activate()
		resp, out = callTodoUpdateMCP(t, viewerClient, fx.ts.URL, "jsonrpc", "tags_deleteProject", map[string]any{
			"projectSlug": fx.project.Slug, "tagId": foreignTagID,
		})
		assertTodoLinkMCPError(t, "jsonrpc", resp, out, http.StatusForbidden, "FORBIDDEN", "maintainer or higher required")
		assertTagMutationMCPTrace(t, fx.wrapped, "access")
	})

	t.Run("temporary name color reaches durable-only store method", func(t *testing.T) {
		fx := newTagMutationMCPFixture(t, "temporary-name-color")
		temporary, err := fx.st.CreateAnonymousBoard(fx.ctx)
		if err != nil {
			t.Fatalf("create creator temporary board: %v", err)
		}
		fx.wrapped.activate()
		resp, out := callTodoUpdateMCP(t, fx.client, fx.ts.URL, "jsonrpc", "tags_updateProjectColor", map[string]any{
			"projectSlug": temporary.Slug, "tagName": "missing", "color": "#123456",
		})
		assertTodoLinkMCPError(t, "jsonrpc", resp, out, http.StatusBadRequest, "VALIDATION_ERROR", "validation: name-based tag operations require a durable project")
		assertTagMutationMCPTrace(t, fx.wrapped, "access", "name-color")
	})

	t.Run("creator temporary project delete fails role gate before tag read", func(t *testing.T) {
		fx := newTagMutationMCPFixture(t, "temporary-delete")
		temporary, err := fx.st.CreateAnonymousBoard(fx.ctx)
		if err != nil {
			t.Fatalf("create creator temporary board: %v", err)
		}
		tagID := insertProjectScopedTag(t, fx.db, temporary.ID, "temporary-delete", nil)
		fx.wrapped.activate()
		resp, out := callTodoUpdateMCP(t, fx.client, fx.ts.URL, "legacy", "tags_deleteProject", map[string]any{
			"projectSlug": temporary.Slug, "tagId": tagID,
		})
		assertTodoLinkMCPError(t, "legacy", resp, out, http.StatusForbidden, "FORBIDDEN", "maintainer or higher required")
		assertTagMutationMCPTrace(t, fx.wrapped, "access")
	})

	t.Run("expired project hides tag before color pre-read and delete role", func(t *testing.T) {
		fx := newTagMutationMCPFixture(t, "expired-project-order")
		anonymous, err := fx.st.CreateAnonymousBoard(context.Background())
		if err != nil {
			t.Fatalf("create anonymous board: %v", err)
		}
		tagID := insertProjectScopedTag(t, fx.db, anonymous.ID, "expired", nil)
		if _, err := fx.db.Exec(`UPDATE projects SET expires_at = ? WHERE id = ?`, time.Now().UTC().Add(-time.Hour).UnixMilli(), anonymous.ID); err != nil {
			t.Fatalf("expire board: %v", err)
		}

		fx.wrapped.activate()
		resp, out := callTodoUpdateMCP(t, fx.client, fx.ts.URL, "jsonrpc", "tags_updateProjectColor", map[string]any{
			"projectSlug": anonymous.Slug, "tagId": tagID, "color": "#123456",
		})
		assertTodoLinkMCPError(t, "jsonrpc", resp, out, http.StatusNotFound, "NOT_FOUND", "not found")
		assertTagMutationMCPTrace(t, fx.wrapped, "access")

		fx.wrapped.activate()
		resp, out = callTodoUpdateMCP(t, fx.client, fx.ts.URL, "legacy", "tags_deleteProject", map[string]any{
			"projectSlug": anonymous.Slug, "tagId": tagID + 999999,
		})
		assertTodoLinkMCPError(t, "legacy", resp, out, http.StatusNotFound, "NOT_FOUND", "not found")
		assertTagMutationMCPTrace(t, fx.wrapped, "access")
	})
}

func TestMCPTagsMutationDeleteMineCrossProjectHasNoAffectedPublication(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		t.Run(transport, func(t *testing.T) {
			fx := newTagMutationMCPFixture(t, "mine-delete-cross-project-"+transport)
			tagID := createTagMutationMCPPersonalTag(t, fx, "cross-project-delete")
			other, err := fx.st.CreateProject(fx.ctx, "Mine Delete Other "+transport)
			if err != nil {
				t.Fatalf("create other project: %v", err)
			}
			if _, err := fx.st.CreateTodo(fx.ctx, other.ID, store.CreateTodoInput{
				Title: "other", Tags: []string{"cross-project-delete"}, ColumnKey: "backlog",
			}, store.ModeFull); err != nil {
				t.Fatalf("attach tag to other project: %v", err)
			}
			streamA := subscribeTagMutationMCPEvents(t, fx, fx.project)
			defer streamA.close()
			streamB := subscribeTagMutationMCPEvents(t, fx, other)
			defer streamB.close()
			fx.wrapped.activate()
			resp, out := callTodoUpdateMCP(t, fx.client, fx.ts.URL, transport, "tags_deleteMine", map[string]any{"tagId": tagID})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
			}
			_ = todoLinkMCPData(t, transport, out)
			assertTagMutationMCPTrace(t, fx.wrapped, "mine-read", "delete")
			var remaining int
			if err := fx.db.QueryRow(`SELECT COUNT(*) FROM tags WHERE id = ?`, tagID).Scan(&remaining); err != nil || remaining != 0 {
				t.Fatalf("remaining tag rows=%d err=%v", remaining, err)
			}
			assertTagMutationMCPSilence(t, streamA)
			assertTagMutationMCPSilence(t, streamB)
		})
	}
}

func TestMCPTagsMutationClearMissingPreferenceAndInputPolicy(t *testing.T) {
	for _, transport := range []string{"legacy", "jsonrpc"} {
		t.Run(transport+"/mine null clear missing preference succeeds", func(t *testing.T) {
			fx := newTagMutationMCPFixture(t, "mine-clear-"+transport)
			tagID := createTagMutationMCPPersonalTag(t, fx, "mine-clear")
			fx.wrapped.activate()
			resp, out := callTodoUpdateMCP(t, fx.client, fx.ts.URL, transport, "tags_updateMineColor", map[string]any{"tagId": tagID, "color": nil})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status=%d response=%+v", resp.StatusCode, out)
			}
			assertTagMutationMCPTag(t, todoLinkMCPData(t, transport, out), tagID, "mine-clear", nil)
			assertTagMutationMCPTrace(t, fx.wrapped, "mine-read", "mine-color")
		})

		for _, color := range []string{"", "   "} {
			t.Run(transport+"/reject empty color "+strconvForTagMutationTest(color), func(t *testing.T) {
				fx := newTagMutationMCPFixture(t, "reject-empty-"+transport+strconvForTagMutationTest(color))
				tagID := createTagMutationMCPPersonalTag(t, fx, "reject-empty")
				fx.wrapped.activate()
				resp, out := callTodoUpdateMCP(t, fx.client, fx.ts.URL, transport, "tags_updateMineColor", map[string]any{"tagId": tagID, "color": color})
				assertTodoLinkMCPError(t, transport, resp, out, http.StatusBadRequest, "VALIDATION_ERROR", "color cannot be empty; use null to clear")
				assertTagMutationMCPTrace(t, fx.wrapped)
			})
		}
	}
}

func strconvForTagMutationTest(value string) string {
	if value == "" {
		return "empty"
	}
	return "whitespace"
}
