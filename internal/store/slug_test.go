package store

import (
	"context"
	"testing"
)

func TestGenerateSlugFromName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		want    string
		wantErr bool
	}{
		{name: "VO2 Max Coach", want: "vo2-max-coach"},
		{name: "My   Project!!", want: "my-project"},
		{name: "  A  ", want: "a"},
		{name: "___", wantErr: true},
		{name: "   ", wantErr: true},
		// Non-ascii letters are removed; should still produce a valid slug if possible.
		{name: "Café", want: "caf"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := generateSlugFromName(tc.name)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got slug=%q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
			if !isValidSlug(got) {
				t.Fatalf("generated slug is not valid: %q", got)
			}
		})
	}
}

func TestCreateProject_SlugCollisionAddsSuffix(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p1, err := st.CreateProject(ctx, "VO2 Max Coach")
	if err != nil {
		t.Fatalf("CreateProject #1: %v", err)
	}
	p2, err := st.CreateProject(ctx, "VO2 Max Coach")
	if err != nil {
		t.Fatalf("CreateProject #2: %v", err)
	}

	if p1.Slug != "vo2-max-coach" {
		t.Fatalf("p1 slug got %q want %q", p1.Slug, "vo2-max-coach")
	}
	if p2.Slug != "vo2-max-coach-2" {
		t.Fatalf("p2 slug got %q want %q", p2.Slug, "vo2-max-coach-2")
	}
}

func TestRewriteDurableProjectSlugs_RewritesFromLegacyRandom(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	ctx := context.Background()
	p1, err := st.CreateProject(ctx, "VO2 Max Coach")
	if err != nil {
		t.Fatalf("CreateProject #1: %v", err)
	}
	p2, err := st.CreateProject(ctx, "VO2 Max Coach")
	if err != nil {
		t.Fatalf("CreateProject #2: %v", err)
	}

	// Simulate legacy randomized slugs.
	if _, err := st.db.ExecContext(ctx, `UPDATE projects SET slug = ? WHERE id = ?`, "deadbeef", p1.ID); err != nil {
		t.Fatalf("set legacy slug p1: %v", err)
	}
	if _, err := st.db.ExecContext(ctx, `UPDATE projects SET slug = ? WHERE id = ?`, "cafebabe", p2.ID); err != nil {
		t.Fatalf("set legacy slug p2: %v", err)
	}

	n, err := st.RewriteDurableProjectSlugs(ctx)
	if err != nil {
		t.Fatalf("RewriteDurableProjectSlugs: %v", err)
	}
	if n < 2 {
		t.Fatalf("expected to rewrite at least 2 slugs, got %d", n)
	}

	p1r, err := st.GetProject(ctx, p1.ID)
	if err != nil {
		t.Fatalf("GetProject p1: %v", err)
	}
	p2r, err := st.GetProject(ctx, p2.ID)
	if err != nil {
		t.Fatalf("GetProject p2: %v", err)
	}
	if p1r.Slug != "vo2-max-coach" {
		t.Fatalf("p1 slug got %q want %q", p1r.Slug, "vo2-max-coach")
	}
	if p2r.Slug != "vo2-max-coach-2" {
		t.Fatalf("p2 slug got %q want %q", p2r.Slug, "vo2-max-coach-2")
	}
}
