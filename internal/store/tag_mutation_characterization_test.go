package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func tagMutationStoreColor(t *testing.T, st *Store, userID, tagID int64) *string {
	t.Helper()
	var color sql.NullString
	if err := st.db.QueryRow(`SELECT color FROM user_tag_colors WHERE user_id = ? AND tag_id = ?`, userID, tagID).Scan(&color); err == sql.ErrNoRows {
		return nil
	} else if err != nil {
		t.Fatalf("read color preference: %v", err)
	}
	if !color.Valid {
		return nil
	}
	value := color.String
	return &value
}

func tagMutationStoreBoardColor(t *testing.T, st *Store, tagID int64) *string {
	t.Helper()
	var color sql.NullString
	if err := st.db.QueryRow(`SELECT color FROM tags WHERE id = ?`, tagID).Scan(&color); err != nil {
		t.Fatalf("read board color: %v", err)
	}
	if !color.Valid {
		return nil
	}
	value := color.String
	return &value
}

func assertTagMutationStoreColor(t *testing.T, got *string, want *string) {
	t.Helper()
	if got == nil || want == nil {
		if got != nil || want != nil {
			t.Fatalf("color=%v want=%v", got, want)
		}
		return
	}
	if *got != *want {
		t.Fatalf("color=%q want=%q", *got, *want)
	}
}

// TestTagMutationStoreColorClearContracts freezes the deliberately different
// clear/idempotency semantics of the raw, mine, grouped-name, and board-row methods.
func TestTagMutationStoreColorClearContracts(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	owner, err := st.BootstrapUser(ctx, "tag-color-owner@example.com", "password", "Owner")
	if err != nil {
		t.Fatal(err)
	}
	ctxOwner := WithUserID(ctx, owner.ID)
	project, err := st.CreateProject(ctxOwner, "Tag Color Contracts")
	if err != nil {
		t.Fatal(err)
	}
	plNewTodo(t, st, ctxOwner, project.ID, "personal", "personal")
	personalID := plTagRowID(t, st, "personal", owner.ID)
	boardID := plInsertBoardScopedTag(t, st, project.ID, "board", nil)

	t.Run("raw personal clear reports missing preference", func(t *testing.T) {
		if err := st.UpdateTagColor(ctxOwner, &owner.ID, personalID, nil); !errors.Is(err, ErrNotFound) {
			t.Fatalf("nil clear error=%v want ErrNotFound", err)
		}
		empty := ""
		if err := st.UpdateTagColor(ctxOwner, &owner.ID, personalID, &empty); !errors.Is(err, ErrNotFound) {
			t.Fatalf("empty clear error=%v want ErrNotFound", err)
		}
		whitespace := "   "
		if err := st.UpdateTagColor(ctxOwner, &owner.ID, personalID, &whitespace); !errors.Is(err, ErrValidation) {
			t.Fatalf("whitespace clear error=%v want ErrValidation", err)
		}
		trimmed := "  #a1b2c3  "
		if err := st.UpdateTagColor(ctxOwner, &owner.ID, personalID, &trimmed); err != nil {
			t.Fatalf("set trimmed color: %v", err)
		}
		if trimmed != "#a1b2c3" {
			t.Fatalf("raw method did not normalize input pointer: %q", trimmed)
		}
		assertTagMutationStoreColor(t, tagMutationStoreColor(t, st, owner.ID, personalID), &trimmed)
		if err := st.UpdateTagColor(ctxOwner, &owner.ID, personalID, nil); err != nil {
			t.Fatalf("clear existing preference: %v", err)
		}
		if err := st.UpdateTagColor(ctxOwner, &owner.ID, personalID, nil); !errors.Is(err, ErrNotFound) {
			t.Fatalf("second raw clear error=%v want ErrNotFound", err)
		}
	})

	t.Run("mine clear normalizes raw not-found but not whitespace validation", func(t *testing.T) {
		if err := st.UpdateMyTagColor(ctxOwner, owner.ID, personalID, nil); err != nil {
			t.Fatalf("nil clear: %v", err)
		}
		empty := ""
		if err := st.UpdateMyTagColor(ctxOwner, owner.ID, personalID, &empty); err != nil {
			t.Fatalf("empty clear: %v", err)
		}
		whitespace := "   "
		if err := st.UpdateMyTagColor(ctxOwner, owner.ID, personalID, &whitespace); !errors.Is(err, ErrValidation) {
			t.Fatalf("whitespace clear error=%v want ErrValidation", err)
		}
	})

	t.Run("grouped name treats all empty forms as idempotent clear", func(t *testing.T) {
		for _, color := range []*string{nil, func() *string { value := ""; return &value }(), func() *string { value := "   "; return &value }()} {
			if err := st.SetViewerTagColorByName(ctxOwner, project.ID, owner.ID, "personal", color); err != nil {
				t.Fatalf("grouped clear %v: %v", color, err)
			}
			assertTagMutationStoreColor(t, tagMutationStoreColor(t, st, owner.ID, personalID), nil)
		}
		color := "  #b1c2d3  "
		if err := st.SetViewerTagColorByName(ctxOwner, project.ID, owner.ID, "personal", &color); err != nil {
			t.Fatalf("grouped set: %v", err)
		}
		want := "#b1c2d3"
		assertTagMutationStoreColor(t, tagMutationStoreColor(t, st, owner.ID, personalID), &want)
		// Unlike UpdateTagColor, this method normalizes a copy and leaves its input alone.
		if color != "  #b1c2d3  " {
			t.Fatalf("grouped method mutated input pointer: %q", color)
		}
	})

	t.Run("board row clear is always idempotent except whitespace validation", func(t *testing.T) {
		if err := st.UpdateTagColor(ctxOwner, &owner.ID, boardID, nil); err != nil {
			t.Fatalf("nil clear: %v", err)
		}
		empty := ""
		if err := st.UpdateTagColor(ctxOwner, &owner.ID, boardID, &empty); err != nil {
			t.Fatalf("empty clear: %v", err)
		}
		whitespace := "   "
		if err := st.UpdateTagColor(ctxOwner, &owner.ID, boardID, &whitespace); !errors.Is(err, ErrValidation) {
			t.Fatalf("whitespace clear error=%v want ErrValidation", err)
		}
		assertTagMutationStoreColor(t, tagMutationStoreBoardColor(t, st, boardID), nil)
	})
}

// TestTagMutationStoreDurableIDCompatibilityAndAuthority freezes the HTTP-only
// durable-ID compatibility behavior for personal rows and the Maintainer gate for
// shared board rows.
func TestTagMutationStoreDurableIDCompatibilityAndAuthority(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	owner, _ := st.BootstrapUser(ctx, "tag-durable-owner@example.com", "password", "Owner")
	viewer, _ := st.CreateUser(ctx, "tag-durable-viewer@example.com", "password", "Viewer")
	contributor, _ := st.CreateUser(ctx, "tag-durable-contributor@example.com", "password", "Contributor")
	maintainer, _ := st.CreateUser(ctx, "tag-durable-maintainer@example.com", "password", "Maintainer")
	ctxOwner := WithUserID(ctx, owner.ID)
	project, _ := st.CreateProject(ctxOwner, "Tag Durable A")
	other, _ := st.CreateProject(ctxOwner, "Tag Durable B")
	plAddMember(t, st, project.ID, viewer.ID, RoleViewer)
	plAddMember(t, st, project.ID, contributor.ID, RoleContributor)
	plAddMember(t, st, project.ID, maintainer.ID, RoleMaintainer)
	plNewTodo(t, st, ctxOwner, project.ID, "personal", "personal")
	personalID := plTagRowID(t, st, "personal", owner.ID)
	boardID := plInsertBoardScopedTag(t, st, project.ID, "board", nil)
	foreignID := plInsertBoardScopedTag(t, st, other.ID, "foreign", nil)

	t.Run("viewer may write preference for another member personal row by compatibility id", func(t *testing.T) {
		color := "#123456"
		if err := st.UpdateTagColorForDurableProjectByID(WithUserID(ctx, viewer.ID), project.ID, viewer.ID, personalID, &color); err != nil {
			t.Fatalf("viewer personal compatibility update: %v", err)
		}
		assertTagMutationStoreColor(t, tagMutationStoreColor(t, st, viewer.ID, personalID), &color)
		if ownerColor := tagMutationStoreColor(t, st, owner.ID, personalID); ownerColor != nil {
			t.Fatalf("viewer compatibility write changed owner preference: %v", ownerColor)
		}
		if err := st.UpdateTagColorForDurableProjectByID(WithUserID(ctx, viewer.ID), project.ID, viewer.ID, personalID, nil); err != nil {
			t.Fatalf("clear existing viewer preference: %v", err)
		}
		if err := st.UpdateTagColorForDurableProjectByID(WithUserID(ctx, viewer.ID), project.ID, viewer.ID, personalID, nil); !errors.Is(err, ErrNotFound) {
			t.Fatalf("second compatibility clear error=%v want ErrNotFound", err)
		}
	})

	for _, tc := range []struct {
		name string
		user User
		want error
	}{
		{"viewer", viewer, ErrUnauthorized},
		{"contributor", contributor, ErrUnauthorized},
		{"maintainer", maintainer, nil},
		{"owner", owner, nil},
	} {
		t.Run(tc.name+" board row authority", func(t *testing.T) {
			color := "#abcdef"
			err := st.UpdateTagColorForDurableProjectByID(WithUserID(ctx, tc.user.ID), project.ID, tc.user.ID, boardID, &color)
			if tc.want == nil && err != nil {
				t.Fatalf("update: %v", err)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("error=%v want %v", err, tc.want)
			}
		})
	}

	t.Run("wrong project is hidden before shared-row role matters", func(t *testing.T) {
		color := "#abcdef"
		err := st.UpdateTagColorForDurableProjectByID(WithUserID(ctx, viewer.ID), project.ID, viewer.ID, foreignID, &color)
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("error=%v want ErrNotFound", err)
		}
	})
}

// TestTagMutationStoreTemporaryAndExpiryContracts records the current split: anonymous
// deletion revalidates project kind and expiry, while direct temporary color mutation
// validates tag/project relationship but currently does not reject an expired board.
func TestTagMutationStoreTemporaryAndExpiryContracts(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	owner, _ := st.BootstrapUser(ctx, "tag-temp-owner@example.com", "password", "Owner")
	ctxOwner := WithUserID(ctx, owner.ID)
	creatorTemporary, err := st.CreateAnonymousBoard(ctxOwner)
	if err != nil {
		t.Fatal(err)
	}
	otherTemporary, err := st.CreateAnonymousBoard(ctxOwner)
	if err != nil {
		t.Fatal(err)
	}
	plNewTodo(t, st, ctxOwner, creatorTemporary.ID, "personal", "personal-temp")
	personalID := plTagRowID(t, st, "personal-temp", owner.ID)
	boardID := plInsertBoardScopedTag(t, st, creatorTemporary.ID, "board-temp", nil)
	foreignID := plInsertBoardScopedTag(t, st, otherTemporary.ID, "foreign-temp", nil)

	t.Run("creator and anonymous-link display dispatch differ for personal row", func(t *testing.T) {
		ownerColor := "#111111"
		if err := st.UpdateTagColorForTemporaryBoard(ctxOwner, creatorTemporary.ID, &owner.ID, personalID, &ownerColor); err != nil {
			t.Fatalf("owner temp color: %v", err)
		}
		assertTagMutationStoreColor(t, tagMutationStoreColor(t, st, owner.ID, personalID), &ownerColor)
		anonymousColor := "#222222"
		if err := st.UpdateTagColorForTemporaryBoard(ctx, creatorTemporary.ID, nil, personalID, &anonymousColor); err != nil {
			t.Fatalf("anonymous link color: %v", err)
		}
		assertTagMutationStoreColor(t, tagMutationStoreBoardColor(t, st, personalID), &anonymousColor)
	})

	t.Run("foreign board row hidden", func(t *testing.T) {
		color := "#333333"
		if err := st.UpdateTagColorForTemporaryBoard(ctxOwner, creatorTemporary.ID, &owner.ID, foreignID, &color); !errors.Is(err, ErrNotFound) {
			t.Fatalf("foreign update error=%v want ErrNotFound", err)
		}
	})

	t.Run("creator temporary board is not anonymous deletion scope", func(t *testing.T) {
		if err := st.DeleteTag(ctx, 0, boardID, true); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("anonymous-claim delete error=%v want ErrUnauthorized", err)
		}
		if err := st.DeleteTag(ctxOwner, owner.ID, boardID, false); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("creator authenticated delete error=%v want ErrUnauthorized", err)
		}
		var remaining int
		if err := st.db.QueryRow(`SELECT COUNT(*) FROM tags WHERE id = ?`, boardID).Scan(&remaining); err != nil || remaining != 1 {
			t.Fatalf("creator temporary board row remaining=%d err=%v want 1", remaining, err)
		}
	})

	t.Run("expired anonymous delete rejects but direct temp color currently succeeds", func(t *testing.T) {
		anonymous, err := st.CreateAnonymousBoard(ctx)
		if err != nil {
			t.Fatal(err)
		}
		anonymousTagID := plInsertBoardScopedTag(t, st, anonymous.ID, "expired", nil)
		if _, err := st.db.Exec(`UPDATE projects SET expires_at = ? WHERE id = ?`, time.Now().UTC().Add(-time.Hour).UnixMilli(), anonymous.ID); err != nil {
			t.Fatalf("expire anonymous board: %v", err)
		}
		color := "#444444"
		if err := st.UpdateTagColorForTemporaryBoard(ctx, anonymous.ID, nil, anonymousTagID, &color); err != nil {
			t.Fatalf("current direct temp color behavior changed: %v", err)
		}
		assertTagMutationStoreColor(t, tagMutationStoreBoardColor(t, st, anonymousTagID), &color)
		if err := st.DeleteTag(ctx, 0, anonymousTagID, true); !errors.Is(err, ErrNotFound) {
			t.Fatalf("expired anonymous delete error=%v want ErrNotFound", err)
		}
		var remaining int
		if err := st.db.QueryRow(`SELECT COUNT(*) FROM tags WHERE id = ?`, anonymousTagID).Scan(&remaining); err != nil || remaining != 1 {
			t.Fatalf("expired tag remaining=%d err=%v", remaining, err)
		}
	})
}

// TestTagMutationStoreDeleteAffectedProjectsAndAtomicity freezes zero/multi-project
// results, own-row-only grouped deletion, cascade cleanup, and transaction rollback.
func TestTagMutationStoreDeleteAffectedProjectsAndAtomicity(t *testing.T) {
	t.Run("unused personal row returns zero affected projects", func(t *testing.T) {
		st, cleanup := newTestStore(t)
		defer cleanup()
		ctx := context.Background()
		owner, _ := st.BootstrapUser(ctx, "tag-unused@example.com", "password", "Owner")
		result, err := st.db.Exec(`INSERT INTO tags(user_id, name, created_at, project_id, color) VALUES (?, 'unused', ?, NULL, NULL)`, owner.ID, time.Now().UTC().UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
		tagID, _ := result.LastInsertId()
		affected, err := st.DeleteMyTagByID(WithUserID(ctx, owner.ID), owner.ID, tagID)
		if err != nil || len(affected) != 0 {
			t.Fatalf("affected=%v err=%v want empty success", affected, err)
		}
	})

	t.Run("grouped personal delete removes only caller rows and returns all projects", func(t *testing.T) {
		st, cleanup := newTestStore(t)
		defer cleanup()
		ctx := context.Background()
		owner, _ := st.BootstrapUser(ctx, "tag-group-owner@example.com", "password", "Owner")
		member, _ := st.CreateUser(ctx, "tag-group-member@example.com", "password", "Member")
		ctxOwner := WithUserID(ctx, owner.ID)
		ctxMember := WithUserID(ctx, member.ID)
		projectA, _ := st.CreateProject(ctxOwner, "Tag Group A")
		projectB, _ := st.CreateProject(ctxOwner, "Tag Group B")
		plAddMember(t, st, projectA.ID, member.ID, RoleMaintainer)
		plNewTodo(t, st, ctxOwner, projectA.ID, "owner a", "shared")
		plNewTodo(t, st, ctxOwner, projectB.ID, "owner b", "shared")
		plNewTodo(t, st, ctxMember, projectA.ID, "member a", "shared")
		ownerTagID := plTagRowID(t, st, "shared", owner.ID)
		memberTagID := plTagRowID(t, st, "shared", member.ID)

		affected, err := st.DeleteMyTagByName(ctxOwner, projectA.ID, owner.ID, "shared")
		if err != nil {
			t.Fatal(err)
		}
		if want := []int64{projectA.ID, projectB.ID}; !reflect.DeepEqual(affected, want) {
			t.Fatalf("affected=%v want=%v", affected, want)
		}
		var ownerRows, memberRows int
		_ = st.db.QueryRow(`SELECT COUNT(*) FROM tags WHERE id = ?`, ownerTagID).Scan(&ownerRows)
		_ = st.db.QueryRow(`SELECT COUNT(*) FROM tags WHERE id = ?`, memberTagID).Scan(&memberRows)
		if ownerRows != 0 || memberRows != 1 {
			t.Fatalf("owner rows=%d member rows=%d want 0/1", ownerRows, memberRows)
		}
	})

	t.Run("delete transaction rolls todo links back when tag delete fails", func(t *testing.T) {
		st, cleanup := newTestStore(t)
		defer cleanup()
		ctx := context.Background()
		owner, _ := st.BootstrapUser(ctx, "tag-rollback@example.com", "password", "Owner")
		ctxOwner := WithUserID(ctx, owner.ID)
		project, _ := st.CreateProject(ctxOwner, "Tag Rollback")
		plNewTodo(t, st, ctxOwner, project.ID, "rollback", "rollback")
		tagID := plTagRowID(t, st, "rollback", owner.ID)
		color := "#123456"
		if err := st.UpdateTagColor(ctxOwner, &owner.ID, tagID, &color); err != nil {
			t.Fatal(err)
		}
		trigger := fmt.Sprintf(`
CREATE TRIGGER reject_characterized_tag_delete
BEFORE DELETE ON tags
WHEN OLD.id = %d
BEGIN
  SELECT RAISE(ABORT, 'forced tag delete failure');
END`, tagID)
		if _, err := st.db.Exec(trigger); err != nil {
			t.Fatalf("create test trigger: %v", err)
		}
		if _, err := st.DeleteMyTagByID(ctxOwner, owner.ID, tagID); err == nil {
			t.Fatal("expected forced delete failure")
		}
		for table, query := range map[string]string{
			"tags":            `SELECT COUNT(*) FROM tags WHERE id = ?`,
			"todo_tags":       `SELECT COUNT(*) FROM todo_tags WHERE tag_id = ?`,
			"project_tags":    `SELECT COUNT(*) FROM project_tags WHERE tag_id = ?`,
			"user_tag_colors": `SELECT COUNT(*) FROM user_tag_colors WHERE tag_id = ?`,
		} {
			var count int
			if err := st.db.QueryRow(query, tagID).Scan(&count); err != nil || count != 1 {
				t.Fatalf("%s count=%d err=%v want 1 after rollback", table, count, err)
			}
		}
		if _, err := st.db.Exec(`DROP TRIGGER reject_characterized_tag_delete`); err != nil {
			t.Fatalf("drop test trigger: %v", err)
		}
		if _, err := st.DeleteMyTagByID(ctxOwner, owner.ID, tagID); err != nil {
			t.Fatalf("delete after removing test trigger: %v", err)
		}
		for table, query := range map[string]string{
			"tags":            `SELECT COUNT(*) FROM tags WHERE id = ?`,
			"todo_tags":       `SELECT COUNT(*) FROM todo_tags WHERE tag_id = ?`,
			"project_tags":    `SELECT COUNT(*) FROM project_tags WHERE tag_id = ?`,
			"user_tag_colors": `SELECT COUNT(*) FROM user_tag_colors WHERE tag_id = ?`,
		} {
			var count int
			if err := st.db.QueryRow(query, tagID).Scan(&count); err != nil || count != 0 {
				t.Fatalf("%s count=%d err=%v want 0 after successful delete/cascade", table, count, err)
			}
		}
	})
}
