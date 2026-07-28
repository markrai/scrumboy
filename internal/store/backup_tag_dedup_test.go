package store

import (
	"context"
	"errors"
	"testing"
)

// TestBackupExport_OneEntryPerCanonicalName verifies that backup export (which
// consumes the grouped tag listing) emits a single TagExport per canonical name,
// even when multiple members own same-named backing rows.
func TestBackupExport_OneEntryPerCanonicalName(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	u1, _ := st.BootstrapUser(ctx, "u1@example.com", "password", "U1")
	u2, _ := st.CreateUser(ctx, "u2@example.com", "password", "U2")
	ctx1 := WithUserID(ctx, u1.ID)
	p, _ := st.CreateProject(ctx1, "P")
	plAddMember(t, st, p.ID, u2.ID, RoleMaintainer)
	plNewTodo(t, st, ctx1, p.ID, "a", "bug")
	plNewTodo(t, st, WithUserID(ctx, u2.ID), p.ID, "b", "bug")

	data, err := st.ExportAllProjects(ctx1, ModeFull)
	if err != nil {
		t.Fatalf("ExportAllProjects: %v", err)
	}
	var found *ProjectExport
	for i := range data.Projects {
		if data.Projects[i].Slug == p.Slug {
			found = &data.Projects[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("exported project not found")
	}
	bugCount := 0
	for _, te := range found.Tags {
		if te.Name == "bug" {
			bugCount++
		}
	}
	if bugCount != 1 {
		t.Fatalf("expected exactly one 'bug' TagExport, got %d (%#v)", bugCount, found.Tags)
	}
}

// TestBulkUpsertTags_DedupesLegacyDuplicateNames verifies that a legacy backup
// containing multiple same-named tag entries with conflicting colors imports
// deterministically: one tag row, first valid color wins.
func TestBulkUpsertTags_DedupesLegacyDuplicateNames(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	u, _ := st.BootstrapUser(ctx, "u@example.com", "password", "U")
	ctxU := WithUserID(ctx, u.ID)
	p, _ := st.CreateProject(ctxU, "P")

	c1, c2 := "#111111", "#222222"
	tags := []TagExport{
		{Name: "bug", Color: &c1},
		{Name: "bug", Color: &c2}, // conflicting duplicate
		{Name: "Bug", Color: nil}, // canonicalizes to "bug"
	}

	tx, err := st.db.BeginTx(ctxU, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	tagMap, err := bulkUpsertTags(ctxU, tx, p.ID, tags, ModeFull)
	if err != nil {
		tx.Rollback()
		t.Fatalf("bulkUpsertTags: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if len(tagMap) != 1 {
		t.Fatalf("expected 1 deduped tag mapping, got %d: %#v", len(tagMap), tagMap)
	}
	tagID, ok := tagMap["bug"]
	if !ok {
		t.Fatalf("expected 'bug' in tag map: %#v", tagMap)
	}

	var color *string
	if err := st.db.QueryRowContext(ctx, `SELECT color FROM user_tag_colors WHERE user_id=? AND tag_id=?`, u.ID, tagID).Scan(&color); err != nil {
		t.Fatalf("read imported color: %v", err)
	}
	if color == nil || *color != c1 {
		t.Errorf("expected first-wins color %s, got %#v", c1, color)
	}

	var rowCount int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tags WHERE name='bug' AND user_id=?`, u.ID).Scan(&rowCount); err != nil {
		t.Fatalf("count bug rows: %v", err)
	}
	if rowCount != 1 {
		t.Errorf("expected exactly one 'bug' tag row, got %d", rowCount)
	}
}

// TestBulkUpsertTags_RejectsInvalidTagName pins that deduplication did not weaken
// import validation: an unimportable tag name still fails the whole import instead of
// being silently dropped from the restored project.
func TestBulkUpsertTags_RejectsInvalidTagName(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	u, _ := st.BootstrapUser(ctx, "u@example.com", "password", "U")
	ctxU := WithUserID(ctx, u.ID)
	p, _ := st.CreateProject(ctxU, "P")

	tx, err := st.db.BeginTx(ctxU, nil)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer tx.Rollback()

	_, err = bulkUpsertTags(ctxU, tx, p.ID, []TagExport{{Name: "bug"}, {Name: "not a valid tag!"}}, ModeFull)
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation for an invalid backup tag name, got %v", err)
	}
}

// TestBackupExport_RejectsUncanonicalizableTagName pins that export refuses to emit
// a backup containing a grouped tag label that CanonicalizeTag rejects. Importing
// such a name would restore todos the normal create/update path cannot edit.
func TestBackupExport_RejectsUncanonicalizableTagName(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()

	u, _ := st.BootstrapUser(ctx, "u@example.com", "password", "U")
	ctxU := WithUserID(ctx, u.ID)
	p, _ := st.CreateProject(ctxU, "P")

	const legacyName = "wont-canonicalize!"
	if CanonicalizeTag(legacyName) != "" {
		t.Fatalf("test fixture must be uncanonicalizable, got %q", CanonicalizeTag(legacyName))
	}
	plNewTodo(t, st, ctxU, p.ID, "legacy-card")
	legacyID := plInsertPersonalTagRow(t, st, u.ID, legacyName)
	plAttachTagToTodo(t, st, p.ID, legacyID)

	_, err := st.ExportAllProjects(ctxU, ModeFull)
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("expected ErrValidation exporting uncanonicalizable tag %q, got %v", legacyName, err)
	}
}
