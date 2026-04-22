package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"scrumboy/internal/version"
)

// getExportScopeCounts returns row counts for the current export scope (same as ExportAllProjects).
func getExportScopeCounts(ctx context.Context, s *Store, mode Mode) (projects, todos, links, tags, members int, err error) {
	whereClause, args, err := s.getExportableProjectsSelector(ctx, mode)
	if err != nil {
		return 0, 0, 0, 0, 0, err
	}
	subq := fmt.Sprintf("SELECT id FROM projects WHERE %s", whereClause)

	if err := s.db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM projects WHERE %s", whereClause), args...).Scan(&projects); err != nil {
		return 0, 0, 0, 0, 0, err
	}
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM todos WHERE project_id IN (%s)", subq), args...).Scan(&todos); err != nil {
		return 0, 0, 0, 0, 0, err
	}
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM todo_links WHERE project_id IN (%s)", subq), args...).Scan(&links); err != nil {
		return 0, 0, 0, 0, 0, err
	}
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT COUNT(DISTINCT g.name) FROM todos t
		JOIN todo_tags tt ON tt.todo_id = t.id
		JOIN tags g ON g.id = tt.tag_id
		WHERE t.project_id IN (%s)`, subq), args...).Scan(&tags); err != nil {
		return 0, 0, 0, 0, 0, err
	}
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM project_members WHERE project_id IN (%s)", subq), args...).Scan(&members); err != nil {
		return 0, 0, 0, 0, 0, err
	}
	return projects, todos, links, tags, members, nil
}

func int64Ptr(v int64) *int64 { return &v }

// linkTypeCanonical normalizes empty link type to "relates_to" to match import behavior.
func linkTypeCanonical(linkType string) string {
	if linkType == "" {
		return "relates_to"
	}
	return linkType
}

// projectStructuralHash returns a deterministic SHA256 hex hash of (sorted todos with estimationPoints and tags, sorted links with canonical linkType, dominant_color).
// LinkType is normalized to "relates_to" when empty to match import canonical behavior.
func projectStructuralHash(p ProjectExport) string {
	todos := make([]TodoExport, len(p.Todos))
	copy(todos, p.Todos)
	sort.Slice(todos, func(i, j int) bool { return todos[i].LocalID < todos[j].LocalID })

	var blob string
	for _, t := range todos {
		assignee := "nil"
		if t.AssigneeUserId != nil {
			assignee = fmt.Sprintf("%d", *t.AssigneeUserId)
		}
		estPoints := "nil"
		if t.EstimationPoints != nil {
			estPoints = fmt.Sprintf("%d", *t.EstimationPoints)
		}
		doneAt := "nil"
		if t.DoneAt != nil {
			doneAt = fmt.Sprintf("%d", *t.DoneAt)
		}
		tagNames := make([]string, len(t.Tags))
		copy(tagNames, t.Tags)
		sort.Strings(tagNames)
		blob += fmt.Sprintf("%d|%s|%s|%d|%s|%s|%s|%s\n", t.LocalID, t.Title, t.Status, t.Rank, estPoints, assignee, strings.Join(tagNames, ","), doneAt)
	}
	links := make([]LinkExport, len(p.Links))
	copy(links, p.Links)
	sort.Slice(links, func(i, j int) bool {
		if links[i].FromLocalID != links[j].FromLocalID {
			return links[i].FromLocalID < links[j].FromLocalID
		}
		if links[i].ToLocalID != links[j].ToLocalID {
			return links[i].ToLocalID < links[j].ToLocalID
		}
		return linkTypeCanonical(links[i].LinkType) < linkTypeCanonical(links[j].LinkType)
	})
	for _, l := range links {
		blob += fmt.Sprintf("%d|%d|%s\n", l.FromLocalID, l.ToLocalID, linkTypeCanonical(l.LinkType))
	}
	dc := p.DominantColor
	if dc == "" {
		dc = "#888888"
	}
	blob += dc

	sum := sha256.Sum256([]byte(blob))
	return hex.EncodeToString(sum[:])
}

func TestBackupExportReplace_FullIntegrity(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "integrity@example.com", "password", "User")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxUser := WithUserID(ctx, user.ID)

	// Create project with todos, tags, and a link
	project, err := st.CreateProject(ctxUser, "Integrity Project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	t1, err := st.CreateTodo(ctxUser, project.ID, CreateTodoInput{
		Title:     "Todo One",
		Body:      "body",
		Tags:      []string{"bug"},
		ColumnKey: DefaultColumnBacklog,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo 1: %v", err)
	}
	t2, err := st.CreateTodo(ctxUser, project.ID, CreateTodoInput{
		Title:     "Todo Two",
		Body:      "",
		Tags:      []string{"feature"},
		ColumnKey: DefaultColumnNotStarted,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo 2: %v", err)
	}
	err = st.AddLink(ctxUser, project.ID, t1.LocalID, t2.LocalID, "relates_to", ModeFull)
	if err != nil {
		t.Fatalf("AddLink: %v", err)
	}

	// Before: export-scope counts
	projBefore, todosBefore, linksBefore, tagsBefore, membersBefore, err := getExportScopeCounts(ctxUser, st, ModeFull)
	if err != nil {
		t.Fatalf("getExportScopeCounts before: %v", err)
	}

	// Export
	data, err := st.ExportAllProjects(ctxUser, ModeFull)
	if err != nil {
		t.Fatalf("ExportAllProjects: %v", err)
	}

	// Replace import (store does not enforce confirmation; API does)
	_, err = st.ImportProjects(ctxUser, data, ModeFull, "replace")
	if err != nil {
		t.Fatalf("ImportProjects replace: %v", err)
	}

	// After: same counts
	projAfter, todosAfter, linksAfter, tagsAfter, membersAfter, err := getExportScopeCounts(ctxUser, st, ModeFull)
	if err != nil {
		t.Fatalf("getExportScopeCounts after: %v", err)
	}

	if projAfter != projBefore {
		t.Errorf("projects count: before=%d after=%d", projBefore, projAfter)
	}
	if todosAfter != todosBefore {
		t.Errorf("todos count: before=%d after=%d", todosBefore, todosAfter)
	}
	if linksAfter != linksBefore {
		t.Errorf("todo_links count: before=%d after=%d", linksBefore, linksAfter)
	}
	if tagsAfter != tagsBefore {
		t.Errorf("tags (distinct names) count: before=%d after=%d", tagsBefore, tagsAfter)
	}
	if membersAfter != membersBefore {
		t.Errorf("project_members count: before=%d after=%d", membersBefore, membersAfter)
	}

	// Structural hashes: from first export
	hashesBefore := make(map[string]string)
	for i := range data.Projects {
		p := &data.Projects[i]
		hashesBefore[p.Slug] = projectStructuralHash(*p)
	}

	// Re-export after import
	dataAfter, err := st.ExportAllProjects(ctxUser, ModeFull)
	if err != nil {
		t.Fatalf("ExportAllProjects after: %v", err)
	}

	if len(dataAfter.Projects) != len(data.Projects) {
		t.Errorf("project count: before=%d after=%d", len(data.Projects), len(dataAfter.Projects))
	}

	setBefore := make(map[string]struct{})
	for i := range data.Projects {
		setBefore[data.Projects[i].Slug] = struct{}{}
	}
	setAfter := make(map[string]struct{})
	for i := range dataAfter.Projects {
		setAfter[dataAfter.Projects[i].Slug] = struct{}{}
	}
	if len(setAfter) != len(setBefore) {
		t.Errorf("slug set size: before=%d after=%d (no missing or extra projects)", len(setBefore), len(setAfter))
	}
	for slug := range setBefore {
		if _, ok := setAfter[slug]; !ok {
			t.Errorf("slug %q missing after re-export", slug)
		}
	}
	for slug := range setAfter {
		if _, ok := setBefore[slug]; !ok {
			t.Errorf("extra slug %q after re-export", slug)
		}
	}

	hashesAfter := make(map[string]string)
	for i := range dataAfter.Projects {
		p := &dataAfter.Projects[i]
		hashesAfter[p.Slug] = projectStructuralHash(*p)
	}
	for slug, hBefore := range hashesBefore {
		hAfter := hashesAfter[slug]
		if hAfter != hBefore {
			t.Errorf("project %q structural hash mismatch: before=%s after=%s", slug, hBefore, hAfter)
		}
	}
}

// TestImportMergeUpdate_MigratedSchema verifies merge import works correctly with column_key schema.
func TestImportMergeUpdate_MigratedSchema(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "merge-schema@example.com", "password", "User")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxUser := WithUserID(ctx, user.ID)

	project, err := st.CreateProject(ctxUser, "Merge Schema Project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	todo, err := st.CreateTodo(ctxUser, project.ID, CreateTodoInput{
		Title:     "Original",
		ColumnKey: DefaultColumnDoing,
	}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	now := time.Now().UTC()
	data := &ExportData{
		Version:    version.ExportFormatVersion,
		ExportedAt: now,
		Mode:       "full",
		Scope:      "full",
		Projects: []ProjectExport{
			{
				Slug:      project.Slug,
				Name:      project.Name,
				CreatedAt: now,
				UpdatedAt: now,
				Todos: []TodoExport{
					{
						LocalID:   todo.LocalID,
						Title:     "Merged",
						Body:      "updated",
						Status:    "DONE",
						Rank:      todo.Rank,
						CreatedAt: now,
						UpdatedAt: now,
					},
				},
			},
		},
	}

	_, err = st.ImportProjects(ctxUser, data, ModeFull, "merge")
	if err != nil {
		t.Fatalf("ImportProjects merge: %v", err)
	}

	got, err := st.GetTodoByLocalID(ctxUser, project.ID, todo.LocalID, ModeFull)
	if err != nil {
		t.Fatalf("GetTodoByLocalID: %v", err)
	}
	if got.ColumnKey != DefaultColumnDone {
		t.Errorf("expected column_key %q after merge, got %q", DefaultColumnDone, got.ColumnKey)
	}
	if got.Title != "Merged" {
		t.Errorf("expected title Merged, got %q", got.Title)
	}
}

// TestImportCreateCopy_MigratedSchema verifies copy import works correctly with column_key schema.
func TestImportCreateCopy_MigratedSchema(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "copy-schema@example.com", "password", "User")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxUser := WithUserID(ctx, user.ID)

	now := time.Now().UTC()
	data := &ExportData{
		Version:    version.ExportFormatVersion,
		ExportedAt: now,
		Mode:       "full",
		Scope:      "full",
		Projects: []ProjectExport{
			{
				Slug:      "copy-schema-source",
				Name:      "Copy Schema Source",
				CreatedAt: now,
				UpdatedAt: now,
				Todos: []TodoExport{
					{LocalID: 1, Title: "In Doing", Status: "IN_PROGRESS", Rank: 1000, CreatedAt: now, UpdatedAt: now},
					{LocalID: 2, Title: "In Testing", Status: "TESTING", Rank: 2000, CreatedAt: now, UpdatedAt: now},
				},
			},
		},
	}

	_, err = st.ImportProjects(ctxUser, data, ModeFull, "copy")
	if err != nil {
		t.Fatalf("ImportProjects copy: %v", err)
	}

	var count int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM todos WHERE title IN ('In Doing', 'In Testing')`).Scan(&count); err != nil {
		t.Fatalf("count todos: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 todos, got %d", count)
	}

	var col1, col2 string
	if err := st.db.QueryRowContext(ctx, `SELECT column_key FROM todos WHERE title = 'In Doing'`).Scan(&col1); err != nil {
		t.Fatalf("get column_key In Doing: %v", err)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT column_key FROM todos WHERE title = 'In Testing'`).Scan(&col2); err != nil {
		t.Fatalf("get column_key In Testing: %v", err)
	}
	if col1 != DefaultColumnDoing {
		t.Errorf("expected column_key %q for In Doing, got %q", DefaultColumnDoing, col1)
	}
	if col2 != DefaultColumnTesting {
		t.Errorf("expected column_key %q for In Testing, got %q", DefaultColumnTesting, col2)
	}
}

// Phase 2: Preflight validation tests

func TestImportPreflight_MalformedRejectedBeforeWrites(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	user, _ := st.BootstrapUser(ctx, "preflight@example.com", "password", "User")
	ctxUser := WithUserID(ctx, user.ID)

	tests := []struct {
		name string
		data *ExportData
		want string
	}{
		{
			name: "missing project name",
			data: &ExportData{Version: version.ExportFormatVersion, ExportedAt: time.Now().UTC(), Mode: "full", Scope: "full",
				Projects: []ProjectExport{{Slug: "x", Name: "", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), Todos: []TodoExport{}}}},
			want: "missing name",
		},
		{
			name: "duplicate slug",
			data: &ExportData{Version: version.ExportFormatVersion, ExportedAt: time.Now().UTC(), Mode: "full", Scope: "full",
				Projects: []ProjectExport{
					{Slug: "dup", Name: "A", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), Todos: []TodoExport{}},
					{Slug: "dup", Name: "B", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), Todos: []TodoExport{}},
				}},
			want: "duplicate slug",
		},
		{
			name: "duplicate slug case insensitive",
			data: &ExportData{Version: version.ExportFormatVersion, ExportedAt: time.Now().UTC(), Mode: "full", Scope: "full",
				Projects: []ProjectExport{
					{Slug: "billing", Name: "A", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), Todos: []TodoExport{}},
					{Slug: "Billing", Name: "B", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), Todos: []TodoExport{}},
				}},
			want: "duplicate slug",
		},
		{
			name: "todo missing localId",
			data: &ExportData{Version: version.ExportFormatVersion, ExportedAt: time.Now().UTC(), Mode: "full", Scope: "full",
				Projects: []ProjectExport{{Slug: "x", Name: "X", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
					Todos: []TodoExport{{LocalID: 0, Title: "t", Status: "BACKLOG", Rank: 1000, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}}}}},
			want: "missing localId",
		},
		{
			name: "invalid link reference",
			data: &ExportData{Version: version.ExportFormatVersion, ExportedAt: time.Now().UTC(), Mode: "full", Scope: "full",
				Projects: []ProjectExport{{Slug: "x", Name: "X", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
					Todos: []TodoExport{{LocalID: 1, Title: "t", Status: "BACKLOG", Rank: 1000, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}},
					Links: []LinkExport{{FromLocalID: 1, ToLocalID: 99, LinkType: "relates_to"}}}}},
			want: "does not match any todo",
		},
		{
			name: "self-link",
			data: &ExportData{Version: version.ExportFormatVersion, ExportedAt: time.Now().UTC(), Mode: "full", Scope: "full",
				Projects: []ProjectExport{{Slug: "x", Name: "X", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
					Todos: []TodoExport{{LocalID: 1, Title: "t", Status: "BACKLOG", Rank: 1000, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}},
					Links: []LinkExport{{FromLocalID: 1, ToLocalID: 1, LinkType: "relates_to"}}}}},
			want: "self-link",
		},
		{
			name: "merge missing slug",
			data: &ExportData{Version: version.ExportFormatVersion, ExportedAt: time.Now().UTC(), Mode: "full", Scope: "full",
				Projects: []ProjectExport{{Slug: "", Name: "NoSlug", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), Todos: []TodoExport{}}}},
			want: "missing slug",
		},
		{
			name: "custom workflow todo with unknown column",
			data: &ExportData{Version: version.ExportFormatVersion, ExportedAt: time.Now().UTC(), Mode: "full", Scope: "full",
				Projects: []ProjectExport{{
					Slug: "wf", Name: "WF", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
					WorkflowColumns: []WorkflowColumnExport{
						{Key: "todo", Name: "To Do", Color: "#94a3b8", Position: 0, IsDone: false},
						{Key: "done", Name: "Done", Color: "#ef4444", Position: 1, IsDone: true},
					},
					Todos: []TodoExport{
						{LocalID: 1, Title: "T", Status: "NONEXISTENT", Rank: 1000, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()},
					},
				}}},
			want: "unknown workflow column",
		},
		{
			name: "workflow columns fewer than 2",
			data: &ExportData{Version: version.ExportFormatVersion, ExportedAt: time.Now().UTC(), Mode: "full", Scope: "full",
				Projects: []ProjectExport{{
					Slug: "wf", Name: "WF", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
					WorkflowColumns: []WorkflowColumnExport{{Key: "only", Name: "Only", Color: "#94a3b8", Position: 0, IsDone: false}},
					Todos: []TodoExport{},
				}}},
			want: "at least 2 columns",
		},
		{
			name: "workflow duplicate keys",
			data: &ExportData{Version: version.ExportFormatVersion, ExportedAt: time.Now().UTC(), Mode: "full", Scope: "full",
				Projects: []ProjectExport{{
					Slug: "wf", Name: "WF", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
					WorkflowColumns: []WorkflowColumnExport{
						{Key: "todo", Name: "To Do", Color: "#94a3b8", Position: 0, IsDone: false},
						{Key: "todo", Name: "Duplicate", Color: "#ef4444", Position: 1, IsDone: true},
					},
					Todos: []TodoExport{},
				}}},
			want: "duplicate",
		},
		{
			name: "workflow no done column",
			data: &ExportData{Version: version.ExportFormatVersion, ExportedAt: time.Now().UTC(), Mode: "full", Scope: "full",
				Projects: []ProjectExport{{
					Slug: "wf", Name: "WF", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
					WorkflowColumns: []WorkflowColumnExport{
						{Key: "a", Name: "A", Color: "#94a3b8", Position: 0, IsDone: false},
						{Key: "b", Name: "B", Color: "#ef4444", Position: 1, IsDone: false},
					},
					Todos: []TodoExport{},
				}}},
			want: "exactly one done column",
		},
		{
			name: "workflow invalid color",
			data: &ExportData{Version: version.ExportFormatVersion, ExportedAt: time.Now().UTC(), Mode: "full", Scope: "full",
				Projects: []ProjectExport{{
					Slug: "wf", Name: "WF", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
					WorkflowColumns: []WorkflowColumnExport{
						{Key: "todo", Name: "To Do", Color: "not-a-hex", Position: 0, IsDone: false},
						{Key: "done", Name: "Done", Color: "#ef4444", Position: 1, IsDone: true},
					},
					Todos: []TodoExport{},
				}}},
			want: "invalid color",
		},
		{
			name: "sprint invalid state",
			data: &ExportData{Version: version.ExportFormatVersion, ExportedAt: time.Now().UTC(), Mode: "full", Scope: "full",
				Projects: []ProjectExport{{
					Slug: "sp", Name: "SP", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
					Sprints: []SprintExport{
						{Number: 1, Name: "S1", PlannedStartAt: 0, PlannedEndAt: 1, State: "INVALID"},
					},
					Todos: []TodoExport{},
				}}},
			want: "invalid state",
		},
		{
			name: "sprint duplicate number",
			data: &ExportData{Version: version.ExportFormatVersion, ExportedAt: time.Now().UTC(), Mode: "full", Scope: "full",
				Projects: []ProjectExport{{
					Slug: "sp", Name: "SP", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
					Sprints: []SprintExport{
						{Number: 1, Name: "S1", PlannedStartAt: 0, PlannedEndAt: 1, State: "PLANNED"},
						{Number: 1, Name: "S2", PlannedStartAt: 2, PlannedEndAt: 3, State: "PLANNED"},
					},
					Todos: []TodoExport{},
				}}},
			want: "duplicate sprint number",
		},
		{
			name: "sprint plannedEndAt before plannedStartAt",
			data: &ExportData{Version: version.ExportFormatVersion, ExportedAt: time.Now().UTC(), Mode: "full", Scope: "full",
				Projects: []ProjectExport{{
					Slug: "sp", Name: "SP", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
					Sprints: []SprintExport{
						{Number: 1, Name: "S1", PlannedStartAt: 1000, PlannedEndAt: 500, State: "PLANNED"},
					},
					Todos: []TodoExport{},
				}}},
			want: "plannedEndAt before plannedStartAt",
		},
		{
			name: "todo SprintNumber not in sprints",
			data: &ExportData{Version: version.ExportFormatVersion, ExportedAt: time.Now().UTC(), Mode: "full", Scope: "full",
				Projects: []ProjectExport{{
					Slug: "sp", Name: "SP", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
					Sprints: []SprintExport{
						{Number: 1, Name: "S1", PlannedStartAt: 0, PlannedEndAt: 1, State: "PLANNED"},
					},
					Todos: []TodoExport{
						{LocalID: 1, Title: "T", Status: "BACKLOG", Rank: 1000, SprintNumber: int64Ptr(99), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()},
					},
				}}},
			want: "SprintNumber 99 does not match",
		},
		{
			name: "todo SprintNumber but no sprints",
			data: &ExportData{Version: version.ExportFormatVersion, ExportedAt: time.Now().UTC(), Mode: "full", Scope: "full",
				Projects: []ProjectExport{{
					Slug: "sp", Name: "SP", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
					Todos: []TodoExport{
						{LocalID: 1, Title: "T", Status: "BACKLOG", Rank: 1000, SprintNumber: int64Ptr(1), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()},
					},
				}}},
			want: "has no sprints in backup",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mode := "copy"
			if tt.name == "merge missing slug" {
				mode = "merge"
			}
			_, err := st.ImportProjects(ctxUser, tt.data, ModeFull, mode)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.want)
			}
		})
	}
}

func TestImportPreflight_UnauthorizedRejectedBeforeWrites(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	owner, _ := st.BootstrapUser(ctx, "owner-pf@example.com", "password", "Owner")
	viewer, _ := st.CreateUser(ctx, "viewer-pf@example.com", "password", "Viewer")
	ctxOwner := WithUserID(ctx, owner.ID)
	ctxViewer := WithUserID(ctx, viewer.ID)

	project, _ := st.CreateProject(ctxOwner, "Auth Project")
	_ = st.AddProjectMember(ctxOwner, owner.ID, project.ID, viewer.ID, RoleViewer)

	now := time.Now().UTC()
	data := &ExportData{
		Version: version.ExportFormatVersion, ExportedAt: now, Mode: "full", Scope: "full",
		Projects: []ProjectExport{{
			Slug: project.Slug, Name: project.Name, CreatedAt: now, UpdatedAt: now,
			Todos: []TodoExport{{LocalID: 1, Title: "x", Status: "BACKLOG", Rank: 1000, CreatedAt: now, UpdatedAt: now}},
		}},
	}

	_, err := st.ImportProjects(ctxViewer, data, ModeFull, "merge")
	if err == nil {
		t.Fatal("viewer should not merge")
	}
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got %v", err)
	}
}

func TestPreviewImport_SurfacesUnknownStatusWarnings(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	user, _ := st.BootstrapUser(ctx, "preview@example.com", "password", "User")
	ctxUser := WithUserID(ctx, user.ID)

	now := time.Now().UTC()
	data := &ExportData{
		Version: version.ExportFormatVersion, ExportedAt: now, Mode: "full", Scope: "full",
		Projects: []ProjectExport{{
			Slug: "p", Name: "P", CreatedAt: now, UpdatedAt: now,
			Todos: []TodoExport{
				{LocalID: 1, Title: "a", Status: "CUSTOM_XYZ", Rank: 1000, CreatedAt: now, UpdatedAt: now},
				{LocalID: 2, Title: "b", Status: "CUSTOM_XYZ", Rank: 2000, CreatedAt: now, UpdatedAt: now},
			},
		}},
	}

	result, err := st.PreviewImport(ctxUser, data, ModeFull, "copy")
	if err != nil {
		t.Fatalf("PreviewImport: %v", err)
	}
	var found bool
	for _, w := range result.Warnings {
		if strings.Contains(w, "CUSTOM_XYZ") && strings.Contains(w, "defaulted to backlog") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected warning about unknown workflow column CUSTOM_XYZ, got warnings: %v", result.Warnings)
	}
}

// TestPreviewImport_ValidCustomWorkflowColumn_NoWarning verifies that PreviewImport does not warn when
// todo status matches a valid custom workflow column defined in the backup.
func TestPreviewImport_ValidCustomWorkflowColumn_NoWarning(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	user, _ := st.BootstrapUser(ctx, "preview-valid@example.com", "password", "User")
	ctxUser := WithUserID(ctx, user.ID)

	now := time.Now().UTC()
	data := &ExportData{
		Version: version.ExportFormatVersion, ExportedAt: now, Mode: "full", Scope: "full",
		Projects: []ProjectExport{{
			Slug: "p", Name: "P", CreatedAt: now, UpdatedAt: now,
			WorkflowColumns: []WorkflowColumnExport{
				{Key: "todo", Name: "To Do", Color: "#94a3b8", Position: 0, IsDone: false},
				{Key: "review", Name: "Review", Color: "#f59e0b", Position: 1, IsDone: false},
				{Key: "done", Name: "Done", Color: "#ef4444", Position: 2, IsDone: true},
			},
			Todos: []TodoExport{
				{LocalID: 1, Title: "a", Status: "review", Rank: 1000, CreatedAt: now, UpdatedAt: now},
				{LocalID: 2, Title: "b", Status: "todo", Rank: 2000, CreatedAt: now, UpdatedAt: now},
			},
		}},
	}

	result, err := st.PreviewImport(ctxUser, data, ModeFull, "copy")
	if err != nil {
		t.Fatalf("PreviewImport: %v", err)
	}
	for _, w := range result.Warnings {
		if strings.Contains(w, "Unknown workflow column") {
			t.Errorf("should not warn for valid custom workflow columns, got: %q", w)
		}
	}
}

// TestPreviewImport_InvalidColumn_Warning verifies that PreviewImport warns when todo status
// does not match any column in the backup workflow.
func TestPreviewImport_InvalidColumn_Warning(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	user, _ := st.BootstrapUser(ctx, "preview-invalid@example.com", "password", "User")
	ctxUser := WithUserID(ctx, user.ID)

	now := time.Now().UTC()
	data := &ExportData{
		Version: version.ExportFormatVersion, ExportedAt: now, Mode: "full", Scope: "full",
		Projects: []ProjectExport{{
			Slug: "p", Name: "P", CreatedAt: now, UpdatedAt: now,
			WorkflowColumns: []WorkflowColumnExport{
				{Key: "todo", Name: "To Do", Color: "#94a3b8", Position: 0, IsDone: false},
				{Key: "done", Name: "Done", Color: "#ef4444", Position: 1, IsDone: true},
			},
			Todos: []TodoExport{
				{LocalID: 1, Title: "a", Status: "nonexistent", Rank: 1000, CreatedAt: now, UpdatedAt: now},
			},
		}},
	}

	result, err := st.PreviewImport(ctxUser, data, ModeFull, "copy")
	if err != nil {
		t.Fatalf("PreviewImport: %v", err)
	}
	var found bool
	for _, w := range result.Warnings {
		if strings.Contains(w, "nonexistent") && strings.Contains(w, "defaulted to backlog") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected warning about unknown workflow column 'nonexistent', got warnings: %v", result.Warnings)
	}
}

func TestImportPreflight_InvalidReferencesRejectedBeforeWrites(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	user, _ := st.BootstrapUser(ctx, "ref@example.com", "password", "User")
	ctxUser := WithUserID(ctx, user.ID)

	now := time.Now().UTC()
	data := &ExportData{
		Version: version.ExportFormatVersion, ExportedAt: now, Mode: "full", Scope: "full",
		Projects: []ProjectExport{{
			Slug: "ref", Name: "Ref", CreatedAt: now, UpdatedAt: now,
			Todos: []TodoExport{{LocalID: 1, Title: "a", Status: "BACKLOG", Rank: 1000, CreatedAt: now, UpdatedAt: now}},
			Links: []LinkExport{{FromLocalID: 1, ToLocalID: 2, LinkType: "relates_to"}},
		}},
	}

	_, err := st.ImportProjects(ctxUser, data, ModeFull, "copy")
	if err == nil {
		t.Fatal("expected error for link to non-existent todo")
	}
	if !strings.Contains(err.Error(), "toLocalId 2") && !strings.Contains(err.Error(), "does not match") {
		t.Errorf("expected invalid link reference error, got %v", err)
	}
}

// TestImportPreflight_InvalidWorkflowSprintRejected verifies that invalid workflow and sprint
// payloads are rejected by preflight before any database writes occur.
func TestImportPreflight_InvalidWorkflowSprintRejected(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "preflight-wfs@example.com", "password", "User")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxUser := WithUserID(ctx, user.ID)

	// Establish baseline: one project, one todo
	project, err := st.CreateProject(ctxUser, "Baseline")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	_, err = st.CreateTodo(ctxUser, project.ID, CreateTodoInput{Title: "T", ColumnKey: DefaultColumnBacklog}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	countProjects := func() int {
		var n int
		_ = st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects`).Scan(&n)
		return n
	}
	countTodos := func() int {
		var n int
		_ = st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM todos`).Scan(&n)
		return n
	}
	countWorkflowCols := func() int {
		var n int
		_ = st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM project_workflow_columns`).Scan(&n)
		return n
	}
	countSprints := func() int {
		var n int
		_ = st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sprints`).Scan(&n)
		return n
	}

	beforeProj, beforeTodos := countProjects(), countTodos()
	beforeWf, beforeSprints := countWorkflowCols(), countSprints()

	tests := []struct {
		name string
		data *ExportData
		want string
	}{
		{
			name: "workflow fewer than 2 columns",
			data: &ExportData{Version: version.ExportFormatVersion, ExportedAt: time.Now().UTC(), Mode: "full", Scope: "full",
				Projects: []ProjectExport{{
					Slug: "wf", Name: "WF", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
					WorkflowColumns: []WorkflowColumnExport{{Key: "only", Name: "Only", Color: "#94a3b8", Position: 0, IsDone: false}},
					Todos: []TodoExport{},
				}}},
			want: "at least 2 columns",
		},
		{
			name: "workflow duplicate keys",
			data: &ExportData{Version: version.ExportFormatVersion, ExportedAt: time.Now().UTC(), Mode: "full", Scope: "full",
				Projects: []ProjectExport{{
					Slug: "wf", Name: "WF", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
					WorkflowColumns: []WorkflowColumnExport{
						{Key: "todo", Name: "To Do", Color: "#94a3b8", Position: 0, IsDone: false},
						{Key: "todo", Name: "Dup", Color: "#ef4444", Position: 1, IsDone: true},
					},
					Todos: []TodoExport{},
				}}},
			want: "duplicate",
		},
		{
			name: "workflow no done column",
			data: &ExportData{Version: version.ExportFormatVersion, ExportedAt: time.Now().UTC(), Mode: "full", Scope: "full",
				Projects: []ProjectExport{{
					Slug: "wf", Name: "WF", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
					WorkflowColumns: []WorkflowColumnExport{
						{Key: "a", Name: "A", Color: "#94a3b8", Position: 0, IsDone: false},
						{Key: "b", Name: "B", Color: "#ef4444", Position: 1, IsDone: false},
					},
					Todos: []TodoExport{},
				}}},
			want: "exactly one done column",
		},
		{
			name: "workflow invalid color",
			data: &ExportData{Version: version.ExportFormatVersion, ExportedAt: time.Now().UTC(), Mode: "full", Scope: "full",
				Projects: []ProjectExport{{
					Slug: "wf", Name: "WF", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
					WorkflowColumns: []WorkflowColumnExport{
						{Key: "todo", Name: "To Do", Color: "not-hex", Position: 0, IsDone: false},
						{Key: "done", Name: "Done", Color: "#ef4444", Position: 1, IsDone: true},
					},
					Todos: []TodoExport{},
				}}},
			want: "invalid color",
		},
		{
			name: "sprint duplicate number",
			data: &ExportData{Version: version.ExportFormatVersion, ExportedAt: time.Now().UTC(), Mode: "full", Scope: "full",
				Projects: []ProjectExport{{
					Slug: "sp", Name: "SP", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
					Sprints: []SprintExport{
						{Number: 1, Name: "S1", PlannedStartAt: 0, PlannedEndAt: 1, State: "PLANNED"},
						{Number: 1, Name: "S2", PlannedStartAt: 2, PlannedEndAt: 3, State: "PLANNED"},
					},
					Todos: []TodoExport{},
				}}},
			want: "duplicate sprint number",
		},
		{
			name: "sprint plannedEndAt before plannedStartAt",
			data: &ExportData{Version: version.ExportFormatVersion, ExportedAt: time.Now().UTC(), Mode: "full", Scope: "full",
				Projects: []ProjectExport{{
					Slug: "sp", Name: "SP", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
					Sprints: []SprintExport{
						{Number: 1, Name: "S1", PlannedStartAt: 1000, PlannedEndAt: 500, State: "PLANNED"},
					},
					Todos: []TodoExport{},
				}}},
			want: "plannedEndAt before plannedStartAt",
		},
		{
			name: "sprint invalid state",
			data: &ExportData{Version: version.ExportFormatVersion, ExportedAt: time.Now().UTC(), Mode: "full", Scope: "full",
				Projects: []ProjectExport{{
					Slug: "sp", Name: "SP", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
					Sprints: []SprintExport{
						{Number: 1, Name: "S1", PlannedStartAt: 0, PlannedEndAt: 1, State: "INVALID"},
					},
					Todos: []TodoExport{},
				}}},
			want: "invalid state",
		},
		{
			name: "todo SprintNumber not in sprints",
			data: &ExportData{Version: version.ExportFormatVersion, ExportedAt: time.Now().UTC(), Mode: "full", Scope: "full",
				Projects: []ProjectExport{{
					Slug: "sp", Name: "SP", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
					Sprints: []SprintExport{
						{Number: 1, Name: "S1", PlannedStartAt: 0, PlannedEndAt: 1, State: "PLANNED"},
					},
					Todos: []TodoExport{
						{LocalID: 1, Title: "T", Status: "BACKLOG", Rank: 1000, SprintNumber: int64Ptr(99), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()},
					},
				}}},
			want: "SprintNumber 99",
		},
		{
			name: "todo SprintNumber but no sprints",
			data: &ExportData{Version: version.ExportFormatVersion, ExportedAt: time.Now().UTC(), Mode: "full", Scope: "full",
				Projects: []ProjectExport{{
					Slug: "sp", Name: "SP", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
					Todos: []TodoExport{
						{LocalID: 1, Title: "T", Status: "BACKLOG", Rank: 1000, SprintNumber: int64Ptr(1), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()},
					},
				}}},
			want: "has no sprints",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := st.ImportProjects(ctxUser, tt.data, ModeFull, "copy")
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.want)
			}
			// Preflight runs before any writes: row counts must be unchanged
			if got := countProjects(); got != beforeProj {
				t.Errorf("projects count changed: before=%d after=%d (preflight should block writes)", beforeProj, got)
			}
			if got := countTodos(); got != beforeTodos {
				t.Errorf("todos count changed: before=%d after=%d (preflight should block writes)", beforeTodos, got)
			}
			if got := countWorkflowCols(); got != beforeWf {
				t.Errorf("workflow columns count changed: before=%d after=%d (preflight should block writes)", beforeWf, got)
			}
			if got := countSprints(); got != beforeSprints {
				t.Errorf("sprints count changed: before=%d after=%d (preflight should block writes)", beforeSprints, got)
			}
		})
	}
}

// TestExportImportRoundTrip_DefaultSprintWeeks verifies default_sprint_weeks survives export→replace→import.
func TestExportImportRoundTrip_DefaultSprintWeeks(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "sprintweeks@example.com", "password", "User")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxUser := WithUserID(ctx, user.ID)

	project, err := st.CreateProject(ctxUser, "Sprint Weeks Project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Set default_sprint_weeks to 1 via direct update (no dedicated API)
	if _, err := st.db.ExecContext(ctx, `UPDATE projects SET default_sprint_weeks = 1 WHERE id = ?`, project.ID); err != nil {
		t.Fatalf("set default_sprint_weeks: %v", err)
	}

	data, err := st.ExportAllProjects(ctxUser, ModeFull)
	if err != nil {
		t.Fatalf("ExportAllProjects: %v", err)
	}
	if len(data.Projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(data.Projects))
	}
	if data.Projects[0].DefaultSprintWeeks != 1 {
		t.Errorf("export: expected DefaultSprintWeeks 1, got %d", data.Projects[0].DefaultSprintWeeks)
	}

	_, err = st.ImportProjects(ctxUser, data, ModeFull, "replace")
	if err != nil {
		t.Fatalf("ImportProjects replace: %v", err)
	}

	var weeks int
	if err := st.db.QueryRowContext(ctx, `SELECT default_sprint_weeks FROM projects WHERE slug = ?`, project.Slug).Scan(&weeks); err != nil {
		t.Fatalf("get default_sprint_weeks: %v", err)
	}
	if weeks != 1 {
		t.Errorf("after import: expected default_sprint_weeks 1, got %d", weeks)
	}
}

// TestExportImportRoundTrip_WorkflowColumns verifies custom workflow columns survive export→replace→import.
func TestExportImportRoundTrip_WorkflowColumns(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "workflow@example.com", "password", "User")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxUser := WithUserID(ctx, user.ID)

	project, err := st.CreateProject(ctxUser, "Workflow Project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Replace default workflow with custom columns
	if _, err := st.db.ExecContext(ctx, `DELETE FROM project_workflow_columns WHERE project_id = ?`, project.ID); err != nil {
		t.Fatalf("delete default workflow: %v", err)
	}
	customCols := []WorkflowColumn{
		{Key: "todo", Name: "To Do", Color: "#94a3b8", Position: 0, IsDone: false, System: false},
		{Key: "doing", Name: "Doing", Color: "#22c55e", Position: 1, IsDone: false, System: false},
		{Key: "done", Name: "Done", Color: "#ef4444", Position: 2, IsDone: true, System: false},
	}
	if err := st.InsertWorkflowColumns(ctx, project.ID, customCols); err != nil {
		t.Fatalf("InsertWorkflowColumns: %v", err)
	}

	_, err = st.CreateTodo(ctxUser, project.ID, CreateTodoInput{Title: "T", ColumnKey: "doing"}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	data, err := st.ExportAllProjects(ctxUser, ModeFull)
	if err != nil {
		t.Fatalf("ExportAllProjects: %v", err)
	}
	if len(data.Projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(data.Projects))
	}
	if len(data.Projects[0].WorkflowColumns) != 3 {
		t.Fatalf("export: expected 3 workflow columns, got %d", len(data.Projects[0].WorkflowColumns))
	}

	_, err = st.ImportProjects(ctxUser, data, ModeFull, "replace")
	if err != nil {
		t.Fatalf("ImportProjects replace: %v", err)
	}

	// Replace creates new project; fetch by slug
	projAfter, err := st.GetProjectBySlug(ctx, project.Slug)
	if err != nil {
		t.Fatalf("GetProjectBySlug: %v", err)
	}
	workflow, err := st.GetProjectWorkflow(ctx, projAfter.ID)
	if err != nil {
		t.Fatalf("GetProjectWorkflow: %v", err)
	}
	if len(workflow) != 3 {
		t.Fatalf("after import: expected 3 workflow columns, got %d", len(workflow))
	}
	for i, c := range workflow {
		want := customCols[i]
		if c.Key != want.Key || c.Name != want.Name || c.Color != want.Color || c.IsDone != want.IsDone {
			t.Errorf("column %d: got key=%q name=%q color=%q isDone=%v, want key=%q name=%q color=%q isDone=%v",
				i, c.Key, c.Name, c.Color, c.IsDone, want.Key, want.Name, want.Color, want.IsDone)
		}
	}
}

// TestExportImportRoundTrip_Sprints verifies sprints and todo sprint assignments survive export→replace→import.
func TestExportImportRoundTrip_Sprints(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "sprints@example.com", "password", "User")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxUser := WithUserID(ctx, user.ID)

	project, err := st.CreateProject(ctxUser, "Sprints Project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	start := time.Now().UTC()
	end := start.Add(14 * 24 * time.Hour)
	sprint, err := st.CreateSprint(ctx, project.ID, "Sprint 1", start, end)
	if err != nil {
		t.Fatalf("CreateSprint: %v", err)
	}

	todo, err := st.CreateTodo(ctxUser, project.ID, CreateTodoInput{Title: "In Sprint", ColumnKey: DefaultColumnBacklog}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
	_, err = st.UpdateTodoByLocalID(ctxUser, project.ID, todo.LocalID, UpdateTodoInput{Title: todo.Title, SprintID: &sprint.ID}, ModeFull)
	if err != nil {
		t.Fatalf("UpdateTodo assign sprint: %v", err)
	}

	data, err := st.ExportAllProjects(ctxUser, ModeFull)
	if err != nil {
		t.Fatalf("ExportAllProjects: %v", err)
	}
	if len(data.Projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(data.Projects))
	}
	if len(data.Projects[0].Sprints) != 1 {
		t.Fatalf("export: expected 1 sprint, got %d", len(data.Projects[0].Sprints))
	}
	var todoWithSprint *TodoExport
	for i := range data.Projects[0].Todos {
		if data.Projects[0].Todos[i].SprintNumber != nil {
			todoWithSprint = &data.Projects[0].Todos[i]
			break
		}
	}
	if todoWithSprint == nil || *todoWithSprint.SprintNumber != sprint.Number {
		t.Fatalf("export: expected todo with SprintNumber %d, got %v", sprint.Number, todoWithSprint)
	}

	_, err = st.ImportProjects(ctxUser, data, ModeFull, "replace")
	if err != nil {
		t.Fatalf("ImportProjects replace: %v", err)
	}

	// Replace creates new project; fetch by slug
	projAfter, err := st.GetProjectBySlug(ctx, project.Slug)
	if err != nil {
		t.Fatalf("GetProjectBySlug: %v", err)
	}
	sprints, err := st.ListSprints(ctx, projAfter.ID)
	if err != nil {
		t.Fatalf("ListSprints: %v", err)
	}
	if len(sprints) != 1 {
		t.Fatalf("after import: expected 1 sprint, got %d", len(sprints))
	}

	gotTodo, err := st.GetTodoByLocalID(ctxUser, projAfter.ID, todo.LocalID, ModeFull)
	if err != nil {
		t.Fatalf("GetTodoByLocalID: %v", err)
	}
	if gotTodo.SprintID == nil || *gotTodo.SprintID != sprints[0].ID {
		t.Errorf("after import: expected todo in sprint %d, got SprintID=%v", sprints[0].ID, gotTodo.SprintID)
	}
}

// TestImportMerge_ExistingCustomWorkflow verifies that merge into a project with custom workflow
// replaces the workflow with the backup's when the backup has custom workflow columns.
func TestImportMerge_ExistingCustomWorkflow(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "merge-wf@example.com", "password", "User")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxUser := WithUserID(ctx, user.ID)

	project, err := st.CreateProject(ctxUser, "Merge Workflow Project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Replace default workflow with custom (todo, doing, done)
	if _, err := st.db.ExecContext(ctx, `DELETE FROM project_workflow_columns WHERE project_id = ?`, project.ID); err != nil {
		t.Fatalf("delete default workflow: %v", err)
	}
	initialCols := []WorkflowColumn{
		{Key: "todo", Name: "To Do", Color: "#94a3b8", Position: 0, IsDone: false, System: false},
		{Key: "doing", Name: "Doing", Color: "#22c55e", Position: 1, IsDone: false, System: false},
		{Key: "done", Name: "Done", Color: "#ef4444", Position: 2, IsDone: true, System: false},
	}
	if err := st.InsertWorkflowColumns(ctx, project.ID, initialCols); err != nil {
		t.Fatalf("InsertWorkflowColumns: %v", err)
	}

	todo, err := st.CreateTodo(ctxUser, project.ID, CreateTodoInput{Title: "Original", ColumnKey: "doing"}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	// Backup has different custom workflow (todo, review, done) and same todo in "review"
	now := time.Now().UTC()
	data := &ExportData{
		Version:    version.ExportFormatVersion,
		ExportedAt: now,
		Mode:       "full",
		Scope:      "full",
		Projects: []ProjectExport{{
			Slug:      project.Slug,
			Name:      project.Name,
			CreatedAt: now,
			UpdatedAt: now,
			WorkflowColumns: []WorkflowColumnExport{
				{Key: "todo", Name: "To Do", Color: "#94a3b8", Position: 0, IsDone: false},
				{Key: "review", Name: "Review", Color: "#f59e0b", Position: 1, IsDone: false},
				{Key: "done", Name: "Done", Color: "#ef4444", Position: 2, IsDone: true},
			},
			Todos: []TodoExport{
				{LocalID: todo.LocalID, Title: "Merged", Status: "review", Rank: todo.Rank, CreatedAt: now, UpdatedAt: now},
			},
		}},
	}

	_, err = st.ImportProjects(ctxUser, data, ModeFull, "merge")
	if err != nil {
		t.Fatalf("ImportProjects merge: %v", err)
	}

	// Project workflow should be replaced with backup's (todo, review, done)
	workflow, err := st.GetProjectWorkflow(ctx, project.ID)
	if err != nil {
		t.Fatalf("GetProjectWorkflow: %v", err)
	}
	if len(workflow) != 3 {
		t.Fatalf("expected 3 workflow columns, got %d", len(workflow))
	}
	keys := make([]string, len(workflow))
	for i, c := range workflow {
		keys[i] = c.Key
	}
	wantKeys := []string{"todo", "review", "done"}
	for i, k := range keys {
		if i >= len(wantKeys) || k != wantKeys[i] {
			t.Errorf("workflow keys: got %v, want %v", keys, wantKeys)
			break
		}
	}

	// Todo should be in "review" from backup
	got, err := st.GetTodoByLocalID(ctxUser, project.ID, todo.LocalID, ModeFull)
	if err != nil {
		t.Fatalf("GetTodoByLocalID: %v", err)
	}
	if got.ColumnKey != "review" {
		t.Errorf("expected column_key review after merge, got %q", got.ColumnKey)
	}
	if got.Title != "Merged" {
		t.Errorf("expected title Merged, got %q", got.Title)
	}
}

// TestImportMerge_WorkflowReplacementRejectedWhenStrandedTodos verifies that merge is rejected when
// replacing workflow would strand existing todos (todos not in backup that reference removed columns).
func TestImportMerge_WorkflowReplacementRejectedWhenStrandedTodos(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "stranded@example.com", "password", "User")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxUser := WithUserID(ctx, user.ID)

	project, err := st.CreateProject(ctxUser, "Stranded Project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Custom workflow: todo, doing, done
	if _, err := st.db.ExecContext(ctx, `DELETE FROM project_workflow_columns WHERE project_id = ?`, project.ID); err != nil {
		t.Fatalf("delete default workflow: %v", err)
	}
	initialCols := []WorkflowColumn{
		{Key: "todo", Name: "To Do", Color: "#94a3b8", Position: 0, IsDone: false, System: false},
		{Key: "doing", Name: "Doing", Color: "#22c55e", Position: 1, IsDone: false, System: false},
		{Key: "done", Name: "Done", Color: "#ef4444", Position: 2, IsDone: true, System: false},
	}
	if err := st.InsertWorkflowColumns(ctx, project.ID, initialCols); err != nil {
		t.Fatalf("InsertWorkflowColumns: %v", err)
	}

	// Todo in "doing" - NOT included in backup
	_, err = st.CreateTodo(ctxUser, project.ID, CreateTodoInput{Title: "Stranded Todo", ColumnKey: "doing"}, ModeFull)
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	// Backup has different workflow (todo, review, done) and no todos
	now := time.Now().UTC()
	data := &ExportData{
		Version:    version.ExportFormatVersion,
		ExportedAt: now,
		Mode:       "full",
		Scope:      "full",
		Projects: []ProjectExport{{
			Slug:      project.Slug,
			Name:      project.Name,
			CreatedAt: now,
			UpdatedAt: now,
			WorkflowColumns: []WorkflowColumnExport{
				{Key: "todo", Name: "To Do", Color: "#94a3b8", Position: 0, IsDone: false},
				{Key: "review", Name: "Review", Color: "#f59e0b", Position: 1, IsDone: false},
				{Key: "done", Name: "Done", Color: "#ef4444", Position: 2, IsDone: true},
			},
			Todos: []TodoExport{},
		}},
	}

	_, err = st.ImportProjects(ctxUser, data, ModeFull, "merge")
	if err == nil {
		t.Fatal("expected validation error for stranded todos")
	}
	if !errors.Is(err, ErrValidation) {
		t.Errorf("expected ErrValidation, got %v", err)
	}
	if !strings.Contains(err.Error(), "cannot replace workflow") {
		t.Errorf("error should mention cannot replace workflow, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "doing") {
		t.Errorf("error should mention removed column 'doing', got %q", err.Error())
	}
}

// TestImportMerge_WorkflowReplacementAllowedWhenAllTodosInBackup verifies that merge is allowed when
// all existing todos are in the backup (they will be updated with valid column_key).
func TestImportMerge_WorkflowReplacementAllowedWhenAllTodosInBackup(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "allowed@example.com", "password", "User")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxUser := WithUserID(ctx, user.ID)

	project, err := st.CreateProject(ctxUser, "Allowed Project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Custom workflow: todo, doing, done
	if _, err := st.db.ExecContext(ctx, `DELETE FROM project_workflow_columns WHERE project_id = ?`, project.ID); err != nil {
		t.Fatalf("delete default workflow: %v", err)
	}
	initialCols := []WorkflowColumn{
		{Key: "todo", Name: "To Do", Color: "#94a3b8", Position: 0, IsDone: false, System: false},
		{Key: "doing", Name: "Doing", Color: "#22c55e", Position: 1, IsDone: false, System: false},
		{Key: "done", Name: "Done", Color: "#ef4444", Position: 2, IsDone: true, System: false},
	}
	if err := st.InsertWorkflowColumns(ctx, project.ID, initialCols); err != nil {
		t.Fatalf("InsertWorkflowColumns: %v", err)
	}

	t1, _ := st.CreateTodo(ctxUser, project.ID, CreateTodoInput{Title: "T1", ColumnKey: "doing"}, ModeFull)
	t2, _ := st.CreateTodo(ctxUser, project.ID, CreateTodoInput{Title: "T2", ColumnKey: "todo"}, ModeFull)

	// Backup has different workflow (todo, review, done) and includes BOTH todos with valid status
	now := time.Now().UTC()
	data := &ExportData{
		Version:    version.ExportFormatVersion,
		ExportedAt: now,
		Mode:       "full",
		Scope:      "full",
		Projects: []ProjectExport{{
			Slug:      project.Slug,
			Name:      project.Name,
			CreatedAt: now,
			UpdatedAt: now,
			WorkflowColumns: []WorkflowColumnExport{
				{Key: "todo", Name: "To Do", Color: "#94a3b8", Position: 0, IsDone: false},
				{Key: "review", Name: "Review", Color: "#f59e0b", Position: 1, IsDone: false},
				{Key: "done", Name: "Done", Color: "#ef4444", Position: 2, IsDone: true},
			},
			Todos: []TodoExport{
				{LocalID: t1.LocalID, Title: "T1", Status: "review", Rank: t1.Rank, CreatedAt: now, UpdatedAt: now},
				{LocalID: t2.LocalID, Title: "T2", Status: "todo", Rank: t2.Rank, CreatedAt: now, UpdatedAt: now},
			},
		}},
	}

	_, err = st.ImportProjects(ctxUser, data, ModeFull, "merge")
	if err != nil {
		t.Fatalf("ImportProjects merge: %v", err)
	}

	workflow, _ := st.GetProjectWorkflow(ctx, project.ID)
	if len(workflow) != 3 {
		t.Fatalf("expected 3 workflow columns, got %d", len(workflow))
	}
	got1, _ := st.GetTodoByLocalID(ctxUser, project.ID, t1.LocalID, ModeFull)
	if got1.ColumnKey != "review" {
		t.Errorf("t1: expected column_key review, got %q", got1.ColumnKey)
	}
	got2, _ := st.GetTodoByLocalID(ctxUser, project.ID, t2.LocalID, ModeFull)
	if got2.ColumnKey != "todo" {
		t.Errorf("t2: expected column_key todo, got %q", got2.ColumnKey)
	}
}

// TestImportMerge_SprintNumberConflictUpdatesExisting verifies that when merging into a project
// that already has a sprint with the same number, the existing sprint is updated from backup (not ignored).
func TestImportMerge_SprintNumberConflictUpdatesExisting(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "merge-sprint@example.com", "password", "User")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxUser := WithUserID(ctx, user.ID)

	project, err := st.CreateProject(ctxUser, "Merge Sprint Project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Create existing sprint 1 with name "Old Sprint"
	start := time.Now().UTC()
	end := start.Add(14 * 24 * time.Hour)
	existingSprint, err := st.CreateSprint(ctx, project.ID, "Old Sprint", start, end)
	if err != nil {
		t.Fatalf("CreateSprint: %v", err)
	}
	if existingSprint.Number != 1 {
		t.Fatalf("expected sprint number 1, got %d", existingSprint.Number)
	}

	// Export-like payload: sprint 1 with different name (backup data)
	now := time.Now().UTC()
	backupStart := int64(1000000)
	backupEnd := int64(2000000)
	data := &ExportData{
		Version:    version.ExportFormatVersion,
		ExportedAt: now,
		Mode:       "full",
		Scope:      "full",
		Projects: []ProjectExport{{
			Slug:      project.Slug,
			Name:      project.Name,
			CreatedAt: now,
			UpdatedAt: now,
			Sprints: []SprintExport{
				{Number: 1, Name: "Backup Sprint", PlannedStartAt: backupStart, PlannedEndAt: backupEnd, State: "PLANNED"},
			},
			Todos: []TodoExport{
				{LocalID: 1, Title: "In Sprint", Status: "BACKLOG", Rank: 1000, SprintNumber: int64Ptr(1), CreatedAt: now, UpdatedAt: now},
			},
		}},
	}

	_, err = st.ImportProjects(ctxUser, data, ModeFull, "merge")
	if err != nil {
		t.Fatalf("ImportProjects merge: %v", err)
	}

	// Existing sprint should be updated from backup (same id, updated fields)
	got, err := st.GetSprintByProjectNumber(ctx, project.ID, 1)
	if err != nil {
		t.Fatalf("GetSprintByProjectNumber: %v", err)
	}
	if got.ID != existingSprint.ID {
		t.Errorf("sprint id changed: was %d, got %d (should update in place)", existingSprint.ID, got.ID)
	}
	if got.Name != "Backup Sprint" {
		t.Errorf("sprint name not updated from backup: got %q, want %q", got.Name, "Backup Sprint")
	}
	if got.PlannedStartAt.UnixMilli() != backupStart {
		t.Errorf("sprint planned_start_at not updated: got %d, want %d", got.PlannedStartAt.UnixMilli(), backupStart)
	}
}

// TestImportMerge_SprintNumberConflict_AddsNewAndLeavesExisting defines merge sprint behavior:
// - Backup sprint with same number as existing: update existing in place.
// - Backup has new sprint numbers: add them.
// - Project has sprints not in backup: leave them unchanged (no deletion).
func TestImportMerge_SprintNumberConflict_AddsNewAndLeavesExisting(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "merge-sprint2@example.com", "password", "User")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxUser := WithUserID(ctx, user.ID)

	project, err := st.CreateProject(ctxUser, "Merge Sprint Project")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Project has sprint 1 and 2
	start := time.Now().UTC()
	end := start.Add(14 * 24 * time.Hour)
	s1, err := st.CreateSprint(ctx, project.ID, "Sprint 1 Old", start, end)
	if err != nil {
		t.Fatalf("CreateSprint 1: %v", err)
	}
	s2, err := st.CreateSprint(ctx, project.ID, "Sprint 2 Old", start.Add(14*24*time.Hour), end.Add(14*24*time.Hour))
	if err != nil {
		t.Fatalf("CreateSprint 2: %v", err)
	}

	// Backup has sprint 1 (updated) and 3 (new). No sprint 2 in backup.
	now := time.Now().UTC()
	backupStart1 := int64(1000000)
	backupEnd1 := int64(2000000)
	backupStart3 := int64(3000000)
	backupEnd3 := int64(4000000)
	data := &ExportData{
		Version:    version.ExportFormatVersion,
		ExportedAt: now,
		Mode:       "full",
		Scope:      "full",
		Projects: []ProjectExport{{
			Slug:      project.Slug,
			Name:      project.Name,
			CreatedAt: now,
			UpdatedAt: now,
			Sprints: []SprintExport{
				{Number: 1, Name: "Sprint 1 From Backup", PlannedStartAt: backupStart1, PlannedEndAt: backupEnd1, State: "PLANNED"},
				{Number: 3, Name: "Sprint 3 New", PlannedStartAt: backupStart3, PlannedEndAt: backupEnd3, State: "PLANNED"},
			},
			Todos: []TodoExport{},
		}},
	}

	_, err = st.ImportProjects(ctxUser, data, ModeFull, "merge")
	if err != nil {
		t.Fatalf("ImportProjects merge: %v", err)
	}

	// Sprint 1: updated from backup (same id)
	got1, err := st.GetSprintByProjectNumber(ctx, project.ID, 1)
	if err != nil {
		t.Fatalf("GetSprintByProjectNumber 1: %v", err)
	}
	if got1.ID != s1.ID {
		t.Errorf("sprint 1 id changed: was %d, got %d", s1.ID, got1.ID)
	}
	if got1.Name != "Sprint 1 From Backup" {
		t.Errorf("sprint 1 name: got %q, want Sprint 1 From Backup", got1.Name)
	}

	// Sprint 2: unchanged (not in backup, should remain)
	got2, err := st.GetSprintByProjectNumber(ctx, project.ID, 2)
	if err != nil {
		t.Fatalf("GetSprintByProjectNumber 2: %v", err)
	}
	if got2.ID != s2.ID {
		t.Errorf("sprint 2 id changed: was %d, got %d", s2.ID, got2.ID)
	}
	if got2.Name != "Sprint 2 Old" {
		t.Errorf("sprint 2 should be unchanged: got name %q", got2.Name)
	}

	// Sprint 3: added from backup
	got3, err := st.GetSprintByProjectNumber(ctx, project.ID, 3)
	if err != nil {
		t.Fatalf("GetSprintByProjectNumber 3: %v", err)
	}
	if got3.Name != "Sprint 3 New" {
		t.Errorf("sprint 3 name: got %q, want Sprint 3 New", got3.Name)
	}
}

// seedWallFixture creates two notes and a connecting edge on the given project
// and returns the edge for cross-check in round-trip assertions.
func seedWallFixture(t *testing.T, st *Store, ctx context.Context, projectID int64) (WallNote, WallNote, WallEdge) {
	t.Helper()
	n1, _, err := st.CreateNote(ctx, projectID, CreateNoteInput{X: 10, Y: 20, Width: 180, Height: 140, Color: "#B0E0E6", Text: "first"})
	if err != nil {
		t.Fatalf("CreateNote 1: %v", err)
	}
	n2, _, err := st.CreateNote(ctx, projectID, CreateNoteInput{X: 300, Y: 400, Width: 200, Height: 160, Color: "#FFBF00", Text: "second"})
	if err != nil {
		t.Fatalf("CreateNote 2: %v", err)
	}
	edge, _, err := st.CreateEdge(ctx, projectID, n1.ID, n2.ID)
	if err != nil {
		t.Fatalf("CreateEdge: %v", err)
	}
	return n1, n2, edge
}

func assertWallRoundTrip(t *testing.T, st *Store, ctx context.Context, projectID int64, n1, n2 WallNote) {
	t.Helper()
	wall, err := st.GetWall(ctx, projectID)
	if err != nil {
		t.Fatalf("GetWall after import: %v", err)
	}
	if len(wall.Notes) != 2 {
		t.Fatalf("expected 2 notes, got %d", len(wall.Notes))
	}
	if len(wall.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(wall.Edges))
	}
	byID := map[string]WallNote{}
	for _, n := range wall.Notes {
		byID[n.ID] = n
	}
	got1, ok := byID[n1.ID]
	if !ok {
		t.Fatalf("note %q missing after import", n1.ID)
	}
	if got1.Text != n1.Text || got1.Color != n1.Color || got1.X != n1.X || got1.Y != n1.Y {
		t.Errorf("note 1 round-trip mismatch: got %+v, want text=%q color=%q xy=(%v,%v)", got1, n1.Text, n1.Color, n1.X, n1.Y)
	}
	got2, ok := byID[n2.ID]
	if !ok {
		t.Fatalf("note %q missing after import", n2.ID)
	}
	if got2.Text != n2.Text || got2.Color != n2.Color {
		t.Errorf("note 2 round-trip mismatch: got %+v, want text=%q color=%q", got2, n2.Text, n2.Color)
	}
	edge := wall.Edges[0]
	if !((edge.From == n1.ID && edge.To == n2.ID) || (edge.From == n2.ID && edge.To == n1.ID)) {
		t.Errorf("edge endpoints mismatch: got %q->%q, want between %q and %q", edge.From, edge.To, n1.ID, n2.ID)
	}
}

// TestExportImportRoundTrip_Wall_Replace verifies Scrumbaby wall notes and
// edges survive export -> replace import. Replace deletes the original project
// row (and the cascaded wall) and recreates everything from backup, so this is
// the strictest round-trip: anything not in the export is lost.
func TestExportImportRoundTrip_Wall_Replace(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "wall-replace@example.com", "password", "User")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxUser := WithUserID(ctx, user.ID)

	project, err := st.CreateProject(ctxUser, "Wall Project Replace")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	n1, n2, _ := seedWallFixture(t, st, ctx, project.ID)

	data, err := st.ExportAllProjects(ctxUser, ModeFull)
	if err != nil {
		t.Fatalf("ExportAllProjects: %v", err)
	}
	if len(data.Projects) != 1 || data.Projects[0].Wall == nil {
		t.Fatalf("export: expected wall payload, got %+v", data.Projects[0].Wall)
	}
	if len(data.Projects[0].Wall.Notes) != 2 || len(data.Projects[0].Wall.Edges) != 1 {
		t.Fatalf("export: wall payload incomplete: notes=%d edges=%d", len(data.Projects[0].Wall.Notes), len(data.Projects[0].Wall.Edges))
	}

	if _, err := st.ImportProjects(ctxUser, data, ModeFull, "replace"); err != nil {
		t.Fatalf("ImportProjects replace: %v", err)
	}

	projAfter, err := st.GetProjectBySlug(ctx, project.Slug)
	if err != nil {
		t.Fatalf("GetProjectBySlug: %v", err)
	}
	assertWallRoundTrip(t, st, ctx, projAfter.ID, n1, n2)
}

// TestExportImportRoundTrip_Wall_Merge_ReplacesWithBackup verifies that when a
// backup carries a wall payload, merge mode overwrites the target's existing
// wall with the backup (same policy as todo_links in merge mode).
func TestExportImportRoundTrip_Wall_Merge_ReplacesWithBackup(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "wall-merge@example.com", "password", "User")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxUser := WithUserID(ctx, user.ID)

	project, err := st.CreateProject(ctxUser, "Wall Project Merge")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	n1, n2, _ := seedWallFixture(t, st, ctx, project.ID)

	data, err := st.ExportAllProjects(ctxUser, ModeFull)
	if err != nil {
		t.Fatalf("ExportAllProjects: %v", err)
	}

	// After export, mutate the local wall: add a transient note that should be
	// wiped by the merge since it is not part of the backup.
	if _, _, err := st.CreateNote(ctx, project.ID, CreateNoteInput{X: 0, Y: 0, Color: "#DC143C", Text: "ephemeral"}); err != nil {
		t.Fatalf("CreateNote transient: %v", err)
	}

	if _, err := st.ImportProjects(ctxUser, data, ModeFull, "merge"); err != nil {
		t.Fatalf("ImportProjects merge: %v", err)
	}

	assertWallRoundTrip(t, st, ctx, project.ID, n1, n2)
}

// TestExportImportRoundTrip_Wall_Merge_PreservesWhenBackupHasNoWall verifies
// the upgrade path: a pre-3.14 backup (no wall field) must not wipe an
// existing wall on the target project during merge.
func TestExportImportRoundTrip_Wall_Merge_PreservesWhenBackupHasNoWall(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "wall-merge-pre@example.com", "password", "User")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxUser := WithUserID(ctx, user.ID)

	project, err := st.CreateProject(ctxUser, "Wall Project Merge Pre")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	n1, n2, _ := seedWallFixture(t, st, ctx, project.ID)

	data, err := st.ExportAllProjects(ctxUser, ModeFull)
	if err != nil {
		t.Fatalf("ExportAllProjects: %v", err)
	}
	// Strip the wall field to simulate a backup produced before Scrumbaby.
	for i := range data.Projects {
		data.Projects[i].Wall = nil
	}

	if _, err := st.ImportProjects(ctxUser, data, ModeFull, "merge"); err != nil {
		t.Fatalf("ImportProjects merge: %v", err)
	}

	// Existing wall must be untouched.
	assertWallRoundTrip(t, st, ctx, project.ID, n1, n2)
}

// TestExportImportRoundTrip_Wall_Copy verifies that copy mode reproduces the
// wall onto the newly created project under a rewritten slug.
func TestExportImportRoundTrip_Wall_Copy(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "wall-copy@example.com", "password", "User")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxUser := WithUserID(ctx, user.ID)

	project, err := st.CreateProject(ctxUser, "Wall Project Copy")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	n1, n2, _ := seedWallFixture(t, st, ctx, project.ID)

	data, err := st.ExportAllProjects(ctxUser, ModeFull)
	if err != nil {
		t.Fatalf("ExportAllProjects: %v", err)
	}

	if _, err := st.ImportProjects(ctxUser, data, ModeFull, "copy"); err != nil {
		t.Fatalf("ImportProjects copy: %v", err)
	}

	// Copy mode rewrites slugs to "<base>-imported"; locate the new project
	// by scanning all the user's projects for one that is not the original.
	projects, err := st.ListProjects(ctxUser)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	var copyID int64
	for _, p := range projects {
		if p.Project.ID != project.ID && strings.HasPrefix(p.Project.Slug, "wall-project-copy") {
			copyID = p.Project.ID
			break
		}
	}
	if copyID == 0 {
		t.Fatalf("copied project not found; projects=%+v", projects)
	}

	// Original wall must still be intact.
	assertWallRoundTrip(t, st, ctx, project.ID, n1, n2)
	// Copy must carry the same note/edge content; note IDs are preserved
	// verbatim (opaque strings, safe across projects).
	assertWallRoundTrip(t, st, ctx, copyID, n1, n2)
}

// TestExportImport_Wall_EmptyWallOmitsField verifies that projects with no
// wall activity do not emit a "wall" field in the export, keeping the JSON
// shape stable for users who never touch Scrumbaby.
func TestExportImport_Wall_EmptyWallOmitsField(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "wall-empty@example.com", "password", "User")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxUser := WithUserID(ctx, user.ID)

	if _, err := st.CreateProject(ctxUser, "No Wall Project"); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	data, err := st.ExportAllProjects(ctxUser, ModeFull)
	if err != nil {
		t.Fatalf("ExportAllProjects: %v", err)
	}
	if len(data.Projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(data.Projects))
	}
	if data.Projects[0].Wall != nil {
		t.Errorf("expected nil Wall for project with no notes/edges, got %+v", data.Projects[0].Wall)
	}
}
