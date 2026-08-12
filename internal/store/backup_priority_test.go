package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"scrumboy/internal/version"
)

func TestPriorityExportPresenceWireContract(t *testing.T) {
	project := ProjectExport{
		Slug:                 "presence",
		Name:                 "Presence",
		PriorityTiersPresent: true,
		PriorityTiers:        []PriorityTierExport{},
		Todos: []TodoExport{{
			LocalID:            1,
			Title:              "Unprioritized",
			PriorityKeyPresent: true,
		}},
	}
	raw, err := json.Marshal(project)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Contains(raw, []byte(`"priorityTiers":[]`)) || !bytes.Contains(raw, []byte(`"priorityKey":null`)) {
		t.Fatalf("new wire form=%s", raw)
	}

	var roundTrip ProjectExport
	if err := json.Unmarshal(raw, &roundTrip); err != nil {
		t.Fatalf("unmarshal round trip: %v", err)
	}
	if !roundTrip.PriorityTiersPresent || len(roundTrip.PriorityTiers) != 0 || len(roundTrip.Todos) != 1 || !roundTrip.Todos[0].PriorityKeyPresent || roundTrip.Todos[0].PriorityKey != nil {
		t.Fatalf("round trip lost presence: %+v", roundTrip)
	}

	legacy := []byte(`{"slug":"legacy","name":"Legacy","todos":[{"localId":1,"title":"Old"}]}`)
	var old ProjectExport
	if err := json.Unmarshal(legacy, &old); err != nil {
		t.Fatalf("unmarshal legacy: %v", err)
	}
	if old.PriorityTiersPresent || old.Todos[0].PriorityKeyPresent {
		t.Fatalf("legacy absence was not preserved: %+v", old)
	}
	remarshaled, err := json.Marshal(old)
	if err != nil {
		t.Fatalf("remarshal legacy: %v", err)
	}
	if bytes.Contains(remarshaled, []byte("priorityTiers")) || bytes.Contains(remarshaled, []byte("priorityKey")) {
		t.Fatalf("legacy absence was emitted: %s", remarshaled)
	}

	if err := json.Unmarshal([]byte(`{"slug":"bad","name":"Bad","priorityTiers":null}`), &ProjectExport{}); err == nil {
		t.Fatal("priorityTiers:null should be rejected")
	}
}

func TestExportEmitsExplicitPriorityPresenceAndRejectsMissingDefinitions(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	user, err := st.BootstrapUser(ctx, "priority-export@example.com", "password123", "Owner")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	ctx = WithUserID(ctx, user.ID)
	project, err := st.CreateProject(ctx, "Priority export")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{Title: "No priority"}, ModeFull); err != nil {
		t.Fatalf("create todo: %v", err)
	}
	exported, err := st.ExportAllProjects(ctx, ModeFull)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	raw, err := json.Marshal(exported)
	if err != nil {
		t.Fatalf("marshal export: %v", err)
	}
	if !bytes.Contains(raw, []byte(`"priorityTiers":[]`)) || !bytes.Contains(raw, []byte(`"priorityKey":null`)) {
		t.Fatalf("export lacks explicit priority presence: %s", raw)
	}
	if exported.Version != "1.1" {
		t.Fatalf("export version=%q", exported.Version)
	}

	if _, err := st.db.ExecContext(ctx, `DELETE FROM project_priorities WHERE project_id = ?`, project.ID); err != nil {
		t.Fatalf("corrupt priorities: %v", err)
	}
	if _, err := st.ExportAllProjects(ctx, ModeFull); err == nil {
		t.Fatal("export should reject a project with no priority definitions")
	}
}

func TestPriorityImportDefinitionValidationParity(t *testing.T) {
	valid := make([]PriorityTierExport, 0, maxPriorityTiers)
	for i := 0; i < maxPriorityTiers; i++ {
		valid = append(valid, PriorityTierExport{Key: "tier_" + string(rune('a'+i)), Name: "Tier", Color: "#123456", Position: maxPriorityTiers - i})
	}
	got, err := validatePriorityTierExports("limits", valid)
	if err != nil || len(got) != maxPriorityTiers {
		t.Fatalf("12 tiers got=%+v err=%v", got, err)
	}
	for i, tier := range got {
		if tier.Position != i {
			t.Fatalf("position[%d]=%d", i, tier.Position)
		}
	}
	if _, err := validatePriorityTierExports("limits", append(valid, PriorityTierExport{Key: "tier_z", Name: "Tier", Color: "#123456"})); !errors.Is(err, ErrValidation) {
		t.Fatalf("13 tiers error=%v", err)
	}
	cases := []PriorityTierExport{
		{Key: "bad key", Name: "Valid", Color: "#123456"},
		{Key: "ok", Name: "   ", Color: "#123456"},
		{Key: "ok", Name: strings.Repeat("x", 201), Color: "#123456"},
		{Key: "ok", Name: "Valid", Color: "blue"},
	}
	for _, tier := range cases {
		if _, err := validatePriorityTierExports("invalid", []PriorityTierExport{tier}); !errors.Is(err, ErrValidation) {
			t.Fatalf("tier=%+v error=%v", tier, err)
		}
	}
	if _, err := validatePriorityTierExports("duplicate", []PriorityTierExport{{Key: "High", Name: "High"}, {Key: "high", Name: "Again"}}); !errors.Is(err, ErrValidation) {
		t.Fatalf("case-insensitive duplicate error=%v", err)
	}
	one, err := validatePriorityTierExports("one", []PriorityTierExport{{Key: "only", Name: strings.Repeat("x", 200)}})
	if err != nil || len(one) != 1 || one[0].Color != defaultPriorityColor {
		t.Fatalf("one custom tier=%+v err=%v", one, err)
	}
	defaults, err := validatePriorityTierExports("defaults", []PriorityTierExport{})
	if err != nil || len(defaults) != 4 {
		t.Fatalf("explicit defaults=%+v err=%v", defaults, err)
	}
}

func TestImportMergePriorityReplacementIntegrity(t *testing.T) {
	for _, tc := range []struct {
		name          string
		includeTodo   bool
		priorityField bool
		wantSuccess   bool
	}{
		{name: "explicit null clears before tier removal", includeTodo: true, priorityField: true, wantSuccess: true},
		{name: "absent field preserves and rejects tier removal", includeTodo: true, priorityField: false},
		{name: "unrepresented todo preserves and rejects tier removal", includeTodo: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, cleanup := newTestStore(t)
			defer cleanup()
			ctx := context.Background()
			user, err := st.BootstrapUser(ctx, "priority-import@example.com", "password123", "Owner")
			if err != nil {
				t.Fatalf("bootstrap: %v", err)
			}
			ctx = WithUserID(ctx, user.ID)
			project, err := st.CreateProject(ctx, "Priority import")
			if err != nil {
				t.Fatalf("create project: %v", err)
			}
			if _, err := st.db.ExecContext(ctx, `DELETE FROM project_priorities WHERE project_id = ?`, project.ID); err != nil {
				t.Fatalf("delete defaults: %v", err)
			}
			if _, err := st.db.ExecContext(ctx, `INSERT INTO project_priorities(project_id,key,name,color,position) VALUES (?, 'old', 'Old', '#111111', 0), (?, 'keep', 'Keep', '#222222', 1)`, project.ID, project.ID); err != nil {
				t.Fatalf("insert custom tiers: %v", err)
			}
			old := "old"
			todo, err := st.CreateTodo(ctx, project.ID, CreateTodoInput{Title: "Existing", PriorityKey: &old}, ModeFull)
			if err != nil {
				t.Fatalf("create todo: %v", err)
			}

			now := time.Now().UTC()
			projectExport := ProjectExport{
				Slug: project.Slug, Name: project.Name, CreatedAt: now, UpdatedAt: now,
				PriorityTiersPresent: true,
				PriorityTiers:        []PriorityTierExport{{Key: "keep", Name: "Keep", Color: "#222222", Position: 0}},
			}
			if tc.includeTodo {
				projectExport.Todos = []TodoExport{{
					LocalID: todo.LocalID, Title: "Imported", Status: todo.ColumnKey, Rank: todo.Rank,
					CreatedAt: now, UpdatedAt: now, PriorityKeyPresent: tc.priorityField,
				}}
			}
			data := &ExportData{
				Version: version.ExportFormatVersion, ExportedAt: now, Mode: "full", Scope: "full",
				Projects: []ProjectExport{projectExport},
			}
			_, err = st.ImportProjects(ctx, data, ModeFull, "merge")
			if tc.wantSuccess {
				if err != nil {
					t.Fatalf("merge: %v", err)
				}
				got, err := st.GetTodoByLocalID(ctx, project.ID, todo.LocalID, ModeFull)
				if err != nil {
					t.Fatalf("reload todo: %v", err)
				}
				if got.PriorityKey != nil {
					t.Fatalf("priority=%v want nil", got.PriorityKey)
				}
				tiers, err := st.GetProjectPriorities(ctx, project.ID)
				if err != nil || len(tiers) != 1 || tiers[0].Key != "keep" {
					t.Fatalf("tiers=%+v err=%v", tiers, err)
				}
				var dangling int
				if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM todos t LEFT JOIN project_priorities pp ON pp.project_id=t.project_id AND pp.key=t.priority_key WHERE t.project_id=? AND t.priority_key IS NOT NULL AND pp.id IS NULL`, project.ID).Scan(&dangling); err != nil || dangling != 0 {
					t.Fatalf("dangling=%d err=%v", dangling, err)
				}
				return
			}

			if !errors.Is(err, ErrValidation) {
				t.Fatalf("merge error=%v want validation", err)
			}
			tiers, getErr := st.GetProjectPriorities(ctx, project.ID)
			if getErr != nil || len(tiers) != 2 || tiers[0].Key != "old" || tiers[1].Key != "keep" {
				t.Fatalf("rollback tiers=%+v err=%v", tiers, getErr)
			}
			got, getErr := st.GetTodoByLocalID(ctx, project.ID, todo.LocalID, ModeFull)
			if getErr != nil || got.PriorityKey == nil || *got.PriorityKey != "old" || got.Title != "Existing" {
				t.Fatalf("rollback todo=%+v err=%v", got, getErr)
			}
		})
	}
}
