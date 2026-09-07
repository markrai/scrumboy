package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Regression: invited members of an authenticated temporary board (expires_at set, creator_user_id set)
// must see the project in GET /api/projects (ListProjects). Previously only the creator matched the listing query.
func TestListProjects_IncludesAuthenticatedTemporaryBoardForInvitedMember(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	creator, err := st.BootstrapUser(ctx, "creator@example.com", "password", "Creator")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	invitee, err := st.CreateUser(ctx, "invitee@example.com", "password", "Invitee")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	ctxCreator := WithUserID(ctx, creator.ID)
	tempBoard, err := st.CreateAnonymousBoard(ctxCreator)
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	if tempBoard.ExpiresAt == nil {
		t.Fatal("expected temporary board with expires_at")
	}
	// Creator needs a project_members row so AddProjectMember authorization (GetProjectRole) succeeds.
	if err := st.EnsureMaintainerMembership(ctx, tempBoard.ID, creator.ID); err != nil {
		t.Fatalf("EnsureMaintainerMembership creator: %v", err)
	}
	if err := st.AddProjectMember(ctx, creator.ID, tempBoard.ID, invitee.ID, RoleMaintainer); err != nil {
		t.Fatalf("AddProjectMember: %v", err)
	}

	ctxInvitee := WithUserID(ctx, invitee.ID)
	list, err := st.ListProjects(ctxInvitee)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	var found bool
	for _, e := range list {
		if e.Project.ID == tempBoard.ID {
			found = true
			if e.Role != RoleMaintainer {
				t.Fatalf("expected role maintainer, got %q", e.Role)
			}
			break
		}
	}
	if !found {
		t.Fatal("invited member should see authenticated temporary board in ListProjects")
	}
}

// Orphan project_members on a true anonymous temp board (creator_user_id NULL) must not list for invitees.
func TestListProjects_ExcludesAnonymousTemporaryBoardEvenWithOrphanMembership(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	_, err := st.BootstrapUser(ctx, "owner@example.com", "password", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	invitee, err := st.CreateUser(ctx, "invitee@example.com", "password", "Invitee")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	anonBoard, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("CreateAnonymousBoard: %v", err)
	}
	if anonBoard.CreatorUserID != nil {
		t.Fatal("expected anonymous board without creator_user_id")
	}
	nowMs := anonBoard.CreatedAt.UnixMilli()
	if _, err := st.db.ExecContext(ctx, `
INSERT INTO project_members (project_id, user_id, role, created_at) VALUES (?, ?, ?, ?)`,
		anonBoard.ID, invitee.ID, RoleMaintainer, nowMs); err != nil {
		t.Fatalf("seed orphan membership: %v", err)
	}

	ctxInvitee := WithUserID(ctx, invitee.ID)
	list, err := st.ListProjects(ctxInvitee)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	for _, e := range list {
		if e.Project.ID == anonBoard.ID {
			t.Fatal("anonymous temp board must not appear in ListProjects via membership alone")
		}
	}
}

func TestListProjectSummaries_AuthorizationParity(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "summary-owner@example.com", "password", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	viewer, err := st.CreateUser(ctx, "summary-viewer@example.com", "password", "Viewer")
	if err != nil {
		t.Fatalf("CreateUser viewer: %v", err)
	}
	contributor, err := st.CreateUser(ctx, "summary-contributor@example.com", "password", "Contributor")
	if err != nil {
		t.Fatalf("CreateUser contributor: %v", err)
	}
	outsider, err := st.CreateUser(ctx, "summary-outsider@example.com", "password", "Outsider")
	if err != nil {
		t.Fatalf("CreateUser outsider: %v", err)
	}

	ctxOwner := WithUserID(ctx, owner.ID)
	durableViewer, err := st.CreateProject(ctxOwner, "Summary Viewer Project")
	if err != nil {
		t.Fatalf("CreateProject viewer: %v", err)
	}
	if err := st.AddProjectMember(ctx, owner.ID, durableViewer.ID, viewer.ID, RoleViewer); err != nil {
		t.Fatalf("AddProjectMember viewer: %v", err)
	}
	durableContributor, err := st.CreateProject(ctxOwner, "Summary Contributor Project")
	if err != nil {
		t.Fatalf("CreateProject contributor: %v", err)
	}
	if err := st.AddProjectMember(ctx, owner.ID, durableContributor.ID, contributor.ID, RoleContributor); err != nil {
		t.Fatalf("AddProjectMember contributor: %v", err)
	}

	temporary, err := st.CreateAnonymousBoard(ctxOwner)
	if err != nil {
		t.Fatalf("CreateAnonymousBoard authenticated: %v", err)
	}
	if err := st.EnsureMaintainerMembership(ctx, temporary.ID, owner.ID); err != nil {
		t.Fatalf("EnsureMaintainerMembership temporary creator: %v", err)
	}
	if err := st.AddProjectMember(ctx, owner.ID, temporary.ID, viewer.ID, RoleViewer); err != nil {
		t.Fatalf("AddProjectMember temporary viewer: %v", err)
	}

	anonymous, err := st.CreateAnonymousBoard(ctx)
	if err != nil {
		t.Fatalf("CreateAnonymousBoard anonymous: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `
INSERT INTO project_members (project_id, user_id, role, created_at) VALUES (?, ?, ?, ?)`,
		anonymous.ID, viewer.ID, RoleMaintainer, anonymous.CreatedAt.UnixMilli()); err != nil {
		t.Fatalf("seed anonymous orphan membership: %v", err)
	}

	staged, err := st.CreateProject(ctxOwner, "Summary Staged Project")
	if err != nil {
		t.Fatalf("CreateProject staged: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE projects SET import_batch_id = ? WHERE id = ?`, "summary-batch", staged.ID); err != nil {
		t.Fatalf("mark project staged: %v", err)
	}

	assertRoles := func(userID int64, want map[int64]ProjectRole) {
		t.Helper()
		summaries, nextCursor, err := st.ListProjectSummaries(WithUserID(ctx, userID), 100, nil)
		if err != nil {
			t.Fatalf("ListProjectSummaries user %d: %v", userID, err)
		}
		if nextCursor != nil {
			t.Fatalf("ListProjectSummaries user %d nextCursor=%q, want nil", userID, *nextCursor)
		}
		got := make(map[int64]ProjectRole, len(summaries))
		for _, summary := range summaries {
			got[summary.ID] = summary.Role
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("ListProjectSummaries user %d roles=%v, want %v", userID, got, want)
		}
	}

	assertRoles(owner.ID, map[int64]ProjectRole{
		durableViewer.ID:      RoleMaintainer,
		durableContributor.ID: RoleMaintainer,
		temporary.ID:          RoleMaintainer,
	})
	assertRoles(viewer.ID, map[int64]ProjectRole{
		durableViewer.ID: RoleViewer,
		temporary.ID:     RoleViewer,
	})
	assertRoles(contributor.ID, map[int64]ProjectRole{
		durableContributor.ID: RoleContributor,
	})

	empty, nextCursor, err := st.ListProjectSummaries(WithUserID(ctx, outsider.ID), 100, nil)
	if err != nil {
		t.Fatalf("ListProjectSummaries outsider: %v", err)
	}
	if empty == nil || len(empty) != 0 || nextCursor != nil {
		t.Fatalf("outsider page=%v nextCursor=%v, want non-nil empty page and nil cursor", empty, nextCursor)
	}
}

func TestListProjectSummaries_KeysetPaginationAndImageIsolation(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "pagination-owner@example.com", "password", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxOwner := WithUserID(ctx, owner.ID)

	projects := make([]Project, 0, 5)
	for i := 1; i <= 5; i++ {
		project, err := st.CreateProject(ctxOwner, fmt.Sprintf("Summary Page %d", i))
		if err != nil {
			t.Fatalf("CreateProject %d: %v", i, err)
		}
		projects = append(projects, project)
	}

	updatedAt := []int64{3000, 3000, 2000, 1000, 1000}
	for i, project := range projects {
		if _, err := st.db.ExecContext(ctx, `UPDATE projects SET updated_at = ? WHERE id = ?`, updatedAt[i], project.ID); err != nil {
			t.Fatalf("set updated_at for project %d: %v", project.ID, err)
		}
	}
	const imageSentinel = "data:image/png;base64,MCP_PROJECT_IMAGE_SENTINEL_STORE"
	if _, err := st.db.ExecContext(ctx, `UPDATE projects SET image = ? WHERE id = ?`, imageSentinel, projects[2].ID); err != nil {
		t.Fatalf("seed image sentinel: %v", err)
	}

	wantIDs := []int64{projects[1].ID, projects[0].ID, projects[2].ID, projects[4].ID, projects[3].ID}
	wantPageSizes := []int{2, 2, 1}
	var cursor *string
	var gotIDs []int64
	for pageIndex, wantSize := range wantPageSizes {
		page, nextCursor, err := st.ListProjectSummaries(ctxOwner, 2, cursor)
		if err != nil {
			t.Fatalf("ListProjectSummaries page %d: %v", pageIndex+1, err)
		}
		if len(page) != wantSize {
			t.Fatalf("page %d len=%d, want %d", pageIndex+1, len(page), wantSize)
		}
		for _, summary := range page {
			gotIDs = append(gotIDs, summary.ID)
		}
		if pageIndex < len(wantPageSizes)-1 && nextCursor == nil {
			t.Fatalf("page %d nextCursor=nil, want continuation", pageIndex+1)
		}
		if pageIndex == len(wantPageSizes)-1 && nextCursor != nil {
			t.Fatalf("final page nextCursor=%q, want nil", *nextCursor)
		}
		if pageIndex == 0 {
			wantCursor := encodeProjectSummaryCursor(page[len(page)-1].UpdatedAt.UnixMilli(), page[len(page)-1].ID)
			if nextCursor == nil || *nextCursor != wantCursor {
				t.Fatalf("first page cursor=%v, want last returned row cursor %q", nextCursor, wantCursor)
			}
		}
		cursor = nextCursor
	}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("concatenated project IDs=%v, want %v", gotIDs, wantIDs)
	}

	exactPage, exactNextCursor, err := st.ListProjectSummaries(ctxOwner, len(projects), nil)
	if err != nil {
		t.Fatalf("ListProjectSummaries exact limit: %v", err)
	}
	if len(exactPage) != len(projects) || exactNextCursor != nil {
		t.Fatalf("exact-limit page len=%d nextCursor=%v, want len=%d and nil cursor", len(exactPage), exactNextCursor, len(projects))
	}

	blankCursor := "  "
	firstPage, _, err := st.ListProjectSummaries(ctxOwner, 2, &blankCursor)
	if err != nil {
		t.Fatalf("ListProjectSummaries blank cursor: %v", err)
	}
	if got := []int64{firstPage[0].ID, firstPage[1].ID}; !reflect.DeepEqual(got, wantIDs[:2]) {
		t.Fatalf("blank cursor first page=%v, want %v", got, wantIDs[:2])
	}

	pastEndCursor := "0:1"
	pastEnd, nextCursor, err := st.ListProjectSummaries(ctxOwner, 2, &pastEndCursor)
	if err != nil {
		t.Fatalf("ListProjectSummaries past end: %v", err)
	}
	if pastEnd == nil || len(pastEnd) != 0 || nextCursor != nil {
		t.Fatalf("past-end page=%v nextCursor=%v, want non-nil empty page and nil cursor", pastEnd, nextCursor)
	}

	encoded, err := json.Marshal(firstPage)
	if err != nil {
		t.Fatalf("marshal summaries: %v", err)
	}
	if strings.Contains(string(encoded), imageSentinel) || strings.Contains(strings.ToLower(string(encoded)), "image") {
		t.Fatalf("summary payload exposed image data: %s", encoded)
	}
	if _, ok := reflect.TypeOf(ProjectSummary{}).FieldByName("Image"); ok {
		t.Fatal("ProjectSummary must not contain an Image field")
	}

	fullProjects, err := st.ListProjects(ctxOwner)
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	var fullImage *string
	for _, entry := range fullProjects {
		if entry.Project.ID == projects[2].ID {
			fullImage = entry.Project.Image
			break
		}
	}
	if fullImage == nil || *fullImage != imageSentinel {
		t.Fatalf("full ListProjects image=%v, want preserved sentinel", fullImage)
	}
}

func TestProjectSummaryQueryProjectionExcludesImage(t *testing.T) {
	for _, withCursor := range []bool{false, true} {
		query := strings.ToLower(visibleProjectsQuery(projectSummarySelectColumns, withCursor, true))
		selectProjection := strings.SplitN(query, "from projects p", 2)[0]
		if strings.Contains(selectProjection, "image") || strings.Contains(query, "p.image") {
			t.Fatalf("project summary query selects image (withCursor=%v): %s", withCursor, query)
		}
		if !strings.Contains(query, "order by p.updated_at desc, p.id desc") {
			t.Fatalf("project summary query lost deterministic ordering: %s", query)
		}
	}
}

func TestListProjectSummaries_AuthorizationPrecedesCursorValidation(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	malformedCursor := "malformed"

	summaries, nextCursor, err := st.ListProjectSummaries(ctx, 20, &malformedCursor)
	if err != nil {
		t.Fatalf("auth-disabled ListProjectSummaries error=%v, want nil", err)
	}
	if summaries == nil || len(summaries) != 0 || nextCursor != nil {
		t.Fatalf("auth-disabled page=%v nextCursor=%v, want non-nil empty page and nil cursor", summaries, nextCursor)
	}

	owner, err := st.BootstrapUser(ctx, "cursor-precedence@example.com", "password", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}

	_, _, err = st.ListProjectSummaries(ctx, 20, &malformedCursor)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("auth-enabled unauthenticated error=%v, want ErrUnauthorized", err)
	}
	if errors.Is(err, ErrValidation) {
		t.Fatalf("auth-enabled unauthenticated error=%v, ErrValidation must not take precedence", err)
	}

	_, _, err = st.ListProjectSummaries(WithUserID(ctx, owner.ID), 20, &malformedCursor)
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("authenticated malformed cursor error=%v, want ErrValidation", err)
	}
}

func TestListProjectSummaries_RejectsMalformedCursor(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "cursor-owner@example.com", "password", "Owner")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	ctxOwner := WithUserID(ctx, owner.ID)

	for _, cursor := range []string{"1", "1:2:3", "not-a-time:1", "1:not-an-id", "1:0", "-1:1"} {
		t.Run(cursor, func(t *testing.T) {
			_, _, err := st.ListProjectSummaries(ctxOwner, 20, &cursor)
			if !errors.Is(err, ErrValidation) {
				t.Fatalf("ListProjectSummaries cursor %q error=%v, want ErrValidation", cursor, err)
			}
		})
	}
}
