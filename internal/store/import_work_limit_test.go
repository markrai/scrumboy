package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestRejectImportWorkLimitStories(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	todos := make([]TodoExport, DefaultMaxImportTodos+1)
	for i := range todos {
		todos[i] = TodoExport{Title: fmt.Sprintf("s%d", i), LocalID: int64(i + 1)}
	}
	err := st.rejectImportWorkLimit(&ExportData{Projects: []ProjectExport{{Name: "p", Todos: todos}}})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("err=%v", err)
	}
}

func TestRejectImportWorkLimitProjects(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()

	projects := make([]ProjectExport, DefaultMaxImportProjects+1)
	for i := range projects {
		projects[i] = ProjectExport{Name: fmt.Sprintf("p%d", i)}
	}
	err := st.rejectImportWorkLimit(&ExportData{Projects: projects})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("err=%v", err)
	}
}

func TestSetImportWorkLimitsCannotDisable(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	st.SetImportWorkLimits(0, 0)
	if st.maxImportProjects != DefaultMaxImportProjects || st.maxImportTodos != DefaultMaxImportTodos {
		t.Fatalf("ceilings=%d/%d", st.maxImportProjects, st.maxImportTodos)
	}
	st.SetImportWorkLimits(3, 4)
	if st.maxImportProjects != 3 || st.maxImportTodos != 4 {
		t.Fatalf("override=%d/%d", st.maxImportProjects, st.maxImportTodos)
	}
}

func TestValidateImportPreflightWorkLimitIsAnonymousOnly(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	st.SetImportWorkLimits(1, 1)
	data := &ExportData{Projects: []ProjectExport{
		{Name: "a"},
		{Name: "b"},
	}}
	if err := st.validateImportPreflight(context.Background(), data, ModeFull, "copy"); err != nil {
		t.Fatalf("full-mode oversized restore: %v", err)
	}
	err := st.validateImportPreflight(context.Background(), data, ModeAnonymous, "copy")
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("anonymous oversized import err=%v", err)
	}
}
