package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestResolveEligiblePublicProjectRequiresPublishedDurableNonstagingRow(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "public-resolver@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ownerCtx := WithUserID(ctx, owner.ID)
	project, err := st.CreateProject(ownerCtx, "Public Resolver")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	assertMissing := func(slug string) {
		t.Helper()
		if _, err := st.ResolveEligiblePublicProject(ctx, slug); !errors.Is(err, ErrNotFound) {
			t.Fatalf("ResolveEligiblePublicProject(%q) error = %v, want ErrNotFound", slug, err)
		}
	}
	assertMissing(project.Slug)
	if _, err := st.db.ExecContext(ctx, `UPDATE projects SET public_view_enabled = 1 WHERE id = ?`, project.ID); err != nil {
		t.Fatalf("publish project: %v", err)
	}
	if got, err := st.ResolveEligiblePublicProject(ctx, project.Slug); err != nil || got != project.ID {
		t.Fatalf("eligible resolve = %d, %v; want %d", got, err, project.ID)
	}

	if _, err := st.db.ExecContext(ctx, `UPDATE projects SET import_batch_id = 'pending' WHERE id = ?`, project.ID); err != nil {
		t.Fatalf("mark staging: %v", err)
	}
	assertMissing(project.Slug)
	if _, err := st.db.ExecContext(ctx, `UPDATE projects SET import_batch_id = NULL, expires_at = 1 WHERE id = ?`, project.ID); err != nil {
		t.Fatalf("mark expiring: %v", err)
	}
	assertMissing(project.Slug)
	assertMissing("missing-project")
	assertMissing("INVALID")

	reserved, err := st.CreateProject(ownerCtx, "Reserved Legacy")
	if err != nil {
		t.Fatalf("CreateProject reserved fixture: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE projects SET slug = 'fr', public_view_enabled = 1 WHERE id = ?`, reserved.ID); err != nil {
		t.Fatalf("seed reserved published row: %v", err)
	}
	assertMissing("fr")

	temporary, err := st.CreateAnonymousBoard(ownerCtx)
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE projects SET public_view_enabled = 1 WHERE id = ?`, temporary.ID); err != nil {
		t.Fatalf("seed temporary publication: %v", err)
	}
	assertMissing(temporary.Slug)
}

func TestPublicBoardProjectionsAreAllowlistedProjectScopedAndSideEffectFree(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "public-projection@example.com", "password123", "Private Owner Name")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ownerCtx := WithUserID(ctx, owner.ID)
	publicProject, err := st.CreateProject(ownerCtx, "Projection Public")
	if err != nil {
		t.Fatalf("CreateProject public: %v", err)
	}
	otherProject, err := st.CreateProject(ownerCtx, "Projection Other Secret")
	if err != nil {
		t.Fatalf("CreateProject other: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE projects SET public_view_enabled = 1 WHERE id IN (?, ?)`, publicProject.ID, otherProject.ID); err != nil {
		t.Fatalf("publish fixtures: %v", err)
	}
	now := time.Now().UTC().UnixMilli()
	sprint, err := st.CreateSprint(ownerCtx, publicProject.ID, "Public Sprint", time.Now().UTC(), time.Now().UTC().Add(7*24*time.Hour))
	if err != nil {
		t.Fatalf("CreateSprint: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `
INSERT INTO todos(project_id, local_id, title, body, column_key, rank, estimation_points, assignee_user_id, sprint_id, created_at, updated_at, priority_key, created_by_user_id, archived_at)
VALUES
  (?, 1, 'Visible One', 'Visible body', 'backlog', 10, 3, ?, ?, ?, ?, 'high', ?, NULL),
  (?, 2, 'Visible Two', 'Second body', 'backlog', 20, NULL, NULL, NULL, ?, ?, NULL, NULL, NULL),
  (?, 3, 'Archived Secret', 'archived-body-sentinel', 'backlog', 30, NULL, NULL, NULL, ?, ?, NULL, NULL, ?),
  (?, 1, 'Other Project Secret', 'cross-project-body-sentinel', 'backlog', 5, NULL, ?, NULL, ?, ?, NULL, ?, NULL),
  (?, 99, 'Cross Project Link Secret', 'cross-link-sentinel', 'backlog', 6, NULL, NULL, NULL, ?, ?, NULL, NULL, NULL)`,
		publicProject.ID, owner.ID, sprint.ID, now, now, owner.ID,
		publicProject.ID, now, now,
		publicProject.ID, now, now, now,
		otherProject.ID, owner.ID, now, now, owner.ID,
		otherProject.ID, now, now,
	); err != nil {
		t.Fatalf("insert todo fixtures: %v", err)
	}
	var visibleOneID int64
	if err := st.db.QueryRowContext(ctx, `SELECT id FROM todos WHERE project_id = ? AND local_id = 1`, publicProject.ID).Scan(&visibleOneID); err != nil {
		t.Fatalf("load visible todo id: %v", err)
	}
	res, err := st.db.ExecContext(ctx, `INSERT INTO tags(user_id, name, created_at, project_id, color) VALUES (?, 'public-tag', ?, NULL, NULL)`, owner.ID, now)
	if err != nil {
		t.Fatalf("insert tag: %v", err)
	}
	tagID, _ := res.LastInsertId()
	if _, err := st.db.ExecContext(ctx, `
INSERT INTO todo_tags(todo_id, tag_id) VALUES (?, ?);
INSERT INTO project_tags(project_id, tag_id, created_at) VALUES (?, ?, ?);
INSERT INTO user_tag_colors(user_id, tag_id, color) VALUES (?, ?, '#a1b2c3')`, visibleOneID, tagID, publicProject.ID, tagID, now, owner.ID, tagID); err != nil {
		t.Fatalf("insert tag associations: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `
INSERT INTO todo_links(project_id, from_local_id, to_local_id, link_type, created_at)
VALUES (?, 1, 2, 'relates_to', ?), (?, 1, 3, 'blocks', ?), (?, 1, 99, 'blocks', ?)`,
		publicProject.ID, now, publicProject.ID, now, publicProject.ID, now); err != nil {
		t.Fatalf("insert link fixtures: %v", err)
	}

	var beforeActivity int64
	var beforeAudits int
	if err := st.db.QueryRowContext(ctx, `SELECT last_activity_at FROM projects WHERE id = ?`, publicProject.ID).Scan(&beforeActivity); err != nil {
		t.Fatalf("read activity before: %v", err)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events`).Scan(&beforeAudits); err != nil {
		t.Fatalf("read audits before: %v", err)
	}

	snapshot, err := st.GetPublicBoardSnapshot(ctx, publicProject.ID, publicProject.Slug, PublicBoardQuery{Limit: 20})
	if err != nil {
		t.Fatalf("GetPublicBoardSnapshot: %v", err)
	}
	if snapshot.Project.Slug != publicProject.Slug || snapshot.Project.Name != publicProject.Name || snapshot.Project.SprintsEnabled != publicProject.SprintsEnabled {
		t.Fatalf("public project projection = %+v", snapshot.Project)
	}
	items := snapshot.Columns[DefaultColumnBacklog]
	if len(items) != 2 || items[0].LocalID != 1 || items[0].Title != "Visible One" || items[1].LocalID != 2 {
		t.Fatalf("public backlog = %+v", items)
	}
	if items[0].EstimationPoints == nil || *items[0].EstimationPoints != 3 || items[0].PriorityKey == nil || *items[0].PriorityKey != "high" || items[0].SprintNumber == nil || *items[0].SprintNumber != sprint.Number {
		t.Fatalf("public approved fields = %+v", items[0])
	}
	if len(items[0].Tags) != 1 || items[0].Tags[0].Name != "public-tag" || items[0].Tags[0].Color != publicTagDefaultColor {
		t.Fatalf("public tags = %+v; personal color must not escape", items[0].Tags)
	}
	if len(snapshot.Tags) != 1 || snapshot.Tags[0].Name != "public-tag" || snapshot.Tags[0].ActiveCount != 1 || snapshot.Tags[0].Color != publicTagDefaultColor {
		t.Fatalf("public tag summary = %+v", snapshot.Tags)
	}

	detail, err := st.GetPublicTodoDetail(ctx, publicProject.ID, publicProject.Slug, 1)
	if err != nil || detail.Title != "Visible One" || len(detail.Tags) != 1 {
		t.Fatalf("GetPublicTodoDetail = %+v, %v", detail, err)
	}
	if _, err := st.GetPublicTodoDetail(ctx, publicProject.ID, publicProject.Slug, 3); !errors.Is(err, ErrNotFound) {
		t.Fatalf("archived detail error = %v, want ErrNotFound", err)
	}
	if _, err := st.GetPublicTodoDetail(ctx, publicProject.ID, publicProject.Slug, 99); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-project detail error = %v, want ErrNotFound", err)
	}

	links, err := st.ListPublicTodoLinks(ctx, publicProject.ID, publicProject.Slug, 1)
	if err != nil {
		t.Fatalf("ListPublicTodoLinks: %v", err)
	}
	if len(links) != 1 || links[0].Direction != "outbound" || links[0].LocalID != 2 || links[0].Title != "Visible Two" {
		t.Fatalf("public links = %+v", links)
	}
	sprints, err := st.ListPublicSprints(ctx, publicProject.ID, publicProject.Slug)
	if err != nil || len(sprints) != 1 || sprints[0].Number != sprint.Number || sprints[0].Name != "Public Sprint" {
		t.Fatalf("public sprints = %+v, %v", sprints, err)
	}

	var afterActivity int64
	var afterAudits int
	if err := st.db.QueryRowContext(ctx, `SELECT last_activity_at FROM projects WHERE id = ?`, publicProject.ID).Scan(&afterActivity); err != nil {
		t.Fatalf("read activity after: %v", err)
	}
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_events`).Scan(&afterAudits); err != nil {
		t.Fatalf("read audits after: %v", err)
	}
	if beforeActivity != afterActivity || beforeAudits != afterAudits {
		t.Fatalf("public reads mutated state: activity %d->%d audits %d->%d", beforeActivity, afterActivity, beforeAudits, afterAudits)
	}

	const renamedSlug = "projection-public-renamed"
	if _, err := st.db.ExecContext(ctx, `UPDATE projects SET slug = ? WHERE id = ?`, renamedSlug, publicProject.ID); err != nil {
		t.Fatalf("change project to another valid slug: %v", err)
	}
	if _, err := st.GetPublicBoardSnapshot(ctx, publicProject.ID, publicProject.Slug, PublicBoardQuery{Limit: 20}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("snapshot through stale slug error = %v, want ErrNotFound", err)
	}
	if _, err := st.GetPublicBoardSnapshot(ctx, publicProject.ID, renamedSlug, PublicBoardQuery{Limit: 20}); err != nil {
		t.Fatalf("snapshot through current slug: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE projects SET slug = 'dashboard' WHERE id = ?`, publicProject.ID); err != nil {
		t.Fatalf("change project to reserved slug: %v", err)
	}
	if _, err := st.GetPublicBoardSnapshot(ctx, publicProject.ID, "dashboard", PublicBoardQuery{Limit: 20}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("snapshot after reserved rename error = %v, want ErrNotFound", err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE projects SET slug = ?, public_view_enabled = 0 WHERE id = ?`, renamedSlug, publicProject.ID); err != nil {
		t.Fatalf("unpublish: %v", err)
	}
	if _, err := st.GetPublicBoardSnapshot(ctx, publicProject.ID, renamedSlug, PublicBoardQuery{Limit: 20}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("snapshot after unpublish error = %v, want ErrNotFound", err)
	}
}

func TestPublicLanePaginationAndFiltersAreStableBoundedAndProjectScoped(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "public-pagination@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ownerCtx := WithUserID(ctx, owner.ID)
	project, err := st.CreateProject(ownerCtx, "Public Pagination")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	other, err := st.CreateProject(ownerCtx, "Public Pagination Other")
	if err != nil {
		t.Fatalf("CreateProject other: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE projects SET public_view_enabled = 1 WHERE id IN (?, ?)`, project.ID, other.ID); err != nil {
		t.Fatalf("publish fixtures: %v", err)
	}
	sprint, err := st.CreateSprint(ownerCtx, project.ID, "Filter Sprint", time.Now().UTC(), time.Now().UTC().Add(7*24*time.Hour))
	if err != nil {
		t.Fatalf("CreateSprint: %v", err)
	}
	now := time.Now().UTC().UnixMilli()
	if _, err := st.db.ExecContext(ctx, `
INSERT INTO todos(project_id, local_id, title, body, column_key, rank, sprint_id, created_at, updated_at, priority_key, archived_at)
VALUES
 (?, 1, 'Alpha', 'needle one', 'backlog', 10, ?, ?, ?, 'high', NULL),
 (?, 2, 'Beta', 'needle two', 'backlog', 10, ?, ?, ?, 'low', NULL),
 (?, 3, 'Gamma', 'other', 'backlog', 20, NULL, ?, ?, 'high', NULL),
 (?, 4, 'Archived', 'needle archived', 'backlog', 30, ?, ?, ?, 'high', ?),
 (?, 5, 'Deleted', 'needle deleted', 'backlog', 40, NULL, ?, ?, 'high', NULL),
 (?, 1, 'Other Secret', 'needle other project', 'backlog', 1, NULL, ?, ?, 'high', NULL)`,
		project.ID, sprint.ID, now, now,
		project.ID, sprint.ID, now, now,
		project.ID, now, now,
		project.ID, sprint.ID, now, now, now,
		project.ID, now, now,
		other.ID, now, now,
	); err != nil {
		t.Fatalf("insert todos: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `DELETE FROM todos WHERE project_id = ? AND local_id = 5`, project.ID); err != nil {
		t.Fatalf("delete todo fixture: %v", err)
	}
	insertTag := func(name string) int64 {
		t.Helper()
		res, err := st.db.ExecContext(ctx, `INSERT INTO tags(user_id, name, created_at, project_id, color) VALUES (?, ?, ?, NULL, NULL)`, owner.ID, name, now)
		if err != nil {
			t.Fatalf("insert tag %q: %v", name, err)
		}
		id, _ := res.LastInsertId()
		return id
	}
	bugID := insertTag("bug")
	featureID := insertTag("feature")
	var todo1ID, todo2ID, todo3ID int64
	for localID, target := range map[int64]*int64{1: &todo1ID, 2: &todo2ID, 3: &todo3ID} {
		if err := st.db.QueryRowContext(ctx, `SELECT id FROM todos WHERE project_id = ? AND local_id = ?`, project.ID, localID).Scan(target); err != nil {
			t.Fatalf("load todo %d id: %v", localID, err)
		}
	}
	if _, err := st.db.ExecContext(ctx, `
INSERT INTO todo_tags(todo_id, tag_id) VALUES
 (?, ?), (?, ?), (?, ?), (?, ?)`,
		todo1ID, bugID, todo1ID, featureID, todo2ID, bugID, todo3ID, featureID); err != nil {
		t.Fatalf("insert todo tags: %v", err)
	}

	first, err := st.GetPublicBoardLane(ctx, project.ID, project.Slug, "backlog", PublicBoardQuery{Limit: 1})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(first.Items) != 1 || first.Items[0].LocalID != 1 || !first.HasMore || first.TotalCount != 3 || first.NextOrder == nil {
		t.Fatalf("first page = %+v", first)
	}
	second, err := st.GetPublicBoardLane(ctx, project.ID, project.Slug, "backlog", PublicBoardQuery{Limit: 1, After: first.NextOrder})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if len(second.Items) != 1 || second.Items[0].LocalID != 2 || !second.HasMore || second.NextOrder == nil {
		t.Fatalf("second page = %+v", second)
	}
	third, err := st.GetPublicBoardLane(ctx, project.ID, project.Slug, "backlog", PublicBoardQuery{Limit: 1, After: second.NextOrder})
	if err != nil {
		t.Fatalf("third page: %v", err)
	}
	if len(third.Items) != 1 || third.Items[0].LocalID != 3 || third.HasMore || third.NextOrder != nil {
		t.Fatalf("third page = %+v", third)
	}

	assertLocalIDs := func(name string, query PublicBoardQuery, want ...int64) {
		t.Helper()
		page, err := st.GetPublicBoardLane(ctx, project.ID, project.Slug, "backlog", query)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(page.Items) != len(want) {
			t.Fatalf("%s local IDs = %+v, want %v", name, page.Items, want)
		}
		for i := range want {
			if page.Items[i].LocalID != want[i] {
				t.Fatalf("%s item %d local ID = %d, want %d", name, i, page.Items[i].LocalID, want[i])
			}
		}
	}
	assertLocalIDs("search", PublicBoardQuery{Search: "needle", Limit: 20}, 1, 2)
	assertLocalIDs("tag AND", PublicBoardQuery{Tags: []string{"bug", "feature"}, Limit: 20}, 1)
	assertLocalIDs("sprint", PublicBoardQuery{SprintNumber: &sprint.Number, Limit: 20}, 1, 2)
	high := "high"
	assertLocalIDs("priority", PublicBoardQuery{PriorityKey: &high, Limit: 20}, 1, 3)
	assertLocalIDs("combined", PublicBoardQuery{Search: "needle", Tags: []string{"bug", "feature"}, SprintNumber: &sprint.Number, PriorityKey: &high, Limit: 20}, 1)
	assertLocalIDs("parameterized injection text", PublicBoardQuery{Search: `%' OR 1=1 --`, Limit: 20})
	emptyLane, err := st.GetPublicBoardLane(ctx, project.ID, project.Slug, "doing", PublicBoardQuery{Limit: 20})
	if err != nil || len(emptyLane.Items) != 0 || emptyLane.TotalCount != 0 || emptyLane.HasMore {
		t.Fatalf("empty lane = %+v, %v", emptyLane, err)
	}

	if _, err := st.GetPublicBoardLane(ctx, project.ID, project.Slug, "unknown_lane", PublicBoardQuery{Limit: 20}); !errors.Is(err, ErrValidation) {
		t.Fatalf("unknown lane error = %v, want ErrValidation", err)
	}
	if _, err := st.GetPublicBoardLane(ctx, project.ID, project.Slug, "backlog", PublicBoardQuery{Limit: 51}); !errors.Is(err, ErrValidation) {
		t.Fatalf("oversized limit error = %v, want ErrValidation", err)
	}
}
