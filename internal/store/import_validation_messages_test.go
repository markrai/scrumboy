package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"scrumboy/internal/version"
)

func TestImportValidation_ReasonedErrorsIdentifyRecords(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := WithUserID(context.Background(), mustBootstrap(t, st, "import-msg@example.com").ID)
	now := time.Now().UTC()

	hostileProject := `<script>alert(1)</script>`
	hostileTodo := `<script>alert(2)</script>`

	cases := []struct {
		name   string
		mode   string
		data   *ExportData
		reason string
		check  func(t *testing.T, err error)
	}{
		{
			name: "missing project name",
			mode: "copy",
			data: &ExportData{Version: version.ExportFormatVersion, ExportedAt: now, Mode: "full", Scope: "full",
				Projects: []ProjectExport{{Slug: "x", Name: "", CreatedAt: now, UpdatedAt: now}}},
			reason: ReasonImportProjectMissingName,
		},
		{
			name: "duplicate slug",
			mode: "copy",
			data: &ExportData{Version: version.ExportFormatVersion, ExportedAt: now, Mode: "full", Scope: "full",
				Projects: []ProjectExport{
					{Slug: "dup", Name: "A", CreatedAt: now, UpdatedAt: now},
					{Slug: "dup", Name: "B", CreatedAt: now, UpdatedAt: now},
				}},
			reason: ReasonImportDuplicateSlug,
			check: func(t *testing.T, err error) {
				if got := ErrorDetails(err)["slug"]; got != "dup" {
					t.Fatalf("slug detail=%v", got)
				}
			},
		},
		{
			name: "merge missing slug",
			mode: "merge",
			data: &ExportData{Version: version.ExportFormatVersion, ExportedAt: now, Mode: "full", Scope: "full",
				Projects: []ProjectExport{{Slug: "", Name: hostileProject, CreatedAt: now, UpdatedAt: now}}},
			reason: ReasonImportProjectMissingSlug,
			check: func(t *testing.T, err error) {
				if !strings.Contains(err.Error(), hostileProject) {
					t.Fatalf("message should include project name as text: %v", err)
				}
				if got := ErrorDetails(err)["project"]; got != hostileProject {
					t.Fatalf("project detail=%v", got)
				}
			},
		},
		{
			name: "todo missing localId",
			mode: "copy",
			data: &ExportData{Version: version.ExportFormatVersion, ExportedAt: now, Mode: "full", Scope: "full",
				Projects: []ProjectExport{{Slug: "x", Name: "X", CreatedAt: now, UpdatedAt: now,
					Todos: []TodoExport{{LocalID: 0, Title: "t", Status: "BACKLOG", Rank: 1000, CreatedAt: now, UpdatedAt: now}}}}},
			reason: ReasonImportTodoMissingLocalID,
		},
		{
			name: "todo missing title",
			mode: "copy",
			data: &ExportData{Version: version.ExportFormatVersion, ExportedAt: now, Mode: "full", Scope: "full",
				Projects: []ProjectExport{{Slug: "x", Name: "X", CreatedAt: now, UpdatedAt: now,
					Todos: []TodoExport{{LocalID: 1, Title: "", Status: "BACKLOG", Rank: 1000, CreatedAt: now, UpdatedAt: now}}}}},
			reason: ReasonImportTodoMissingTitle,
		},
		{
			name: "unknown workflow column with hostile names",
			mode: "copy",
			data: &ExportData{Version: version.ExportFormatVersion, ExportedAt: now, Mode: "full", Scope: "full",
				Projects: []ProjectExport{{
					Slug: "wf", Name: hostileProject, CreatedAt: now, UpdatedAt: now,
					WorkflowColumns: []WorkflowColumnExport{
						{Key: "todo", Name: "To Do", Color: "#94a3b8", Position: 0, IsDone: false},
						{Key: "done", Name: "Done", Color: "#ef4444", Position: 1, IsDone: true},
					},
					Todos: []TodoExport{
						{LocalID: 1, Title: hostileTodo, Status: "NONEXISTENT", Rank: 1000, CreatedAt: now, UpdatedAt: now},
					},
				}}},
			reason: ReasonImportTodoUnknownColumn,
			check: func(t *testing.T, err error) {
				details := ErrorDetails(err)
				if details["project"] != hostileProject || details["todo"] != hostileTodo {
					t.Fatalf("details=%v", details)
				}
				if !strings.Contains(err.Error(), hostileProject) || !strings.Contains(err.Error(), hostileTodo) {
					t.Fatalf("message should carry names as plain text: %v", err)
				}
				if strings.Contains(err.Error(), "<script>") && strings.Contains(err.Error(), "javascript:") {
					t.Fatalf("unexpected executable payload shape: %v", err)
				}
			},
		},
		{
			name: "invalid link",
			mode: "copy",
			data: &ExportData{Version: version.ExportFormatVersion, ExportedAt: now, Mode: "full", Scope: "full",
				Projects: []ProjectExport{{Slug: "x", Name: "X", CreatedAt: now, UpdatedAt: now,
					Todos: []TodoExport{{LocalID: 1, Title: "t", Status: "BACKLOG", Rank: 1000, CreatedAt: now, UpdatedAt: now}},
					Links: []LinkExport{{FromLocalID: 1, ToLocalID: 99, LinkType: "relates_to"}}}}},
			reason: ReasonImportInvalidLink,
		},
		{
			name: "workflow min columns",
			mode: "copy",
			data: &ExportData{Version: version.ExportFormatVersion, ExportedAt: now, Mode: "full", Scope: "full",
				Projects: []ProjectExport{{
					Slug: "wf", Name: "WF", CreatedAt: now, UpdatedAt: now,
					WorkflowColumns: []WorkflowColumnExport{{Key: "only", Name: "Only", Color: "#94a3b8", Position: 0, IsDone: false}},
				}}},
			reason: ReasonImportWorkflowMinColumns,
		},
		{
			name: "workflow done column required",
			mode: "copy",
			data: &ExportData{Version: version.ExportFormatVersion, ExportedAt: now, Mode: "full", Scope: "full",
				Projects: []ProjectExport{{
					Slug: "wf", Name: "WF", CreatedAt: now, UpdatedAt: now,
					WorkflowColumns: []WorkflowColumnExport{
						{Key: "a", Name: "A", Color: "#94a3b8", Position: 0, IsDone: false},
						{Key: "b", Name: "B", Color: "#ef4444", Position: 1, IsDone: false},
					},
				}}},
			reason: ReasonImportWorkflowDoneColumnRequired,
		},
		{
			name: "invalid estimation",
			mode: "copy",
			data: &ExportData{Version: version.ExportFormatVersion, ExportedAt: now, Mode: "full", Scope: "full",
				Projects: []ProjectExport{{Slug: "x", Name: "X", CreatedAt: now, UpdatedAt: now,
					Todos: []TodoExport{{LocalID: 1, Title: "t", Status: "BACKLOG", Rank: 1000, EstimationPoints: int64Ptr(7), CreatedAt: now, UpdatedAt: now}}}}},
			reason: ReasonImportTodoInvalidEstimation,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := st.ImportProjects(ctx, tc.data, ModeFull, tc.mode)
			if !errors.Is(err, ErrValidation) {
				t.Fatalf("err=%v", err)
			}
			if ErrorReason(err) != tc.reason {
				t.Fatalf("reason=%q want %s (%v)", ErrorReason(err), tc.reason, err)
			}
			if tc.check != nil {
				tc.check(t, err)
			}
		})
	}
}

func TestImportValidation_WorkLimitReason(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	st.SetImportWorkLimits(1, 1)
	err := st.rejectImportWorkLimit(&ExportData{Projects: []ProjectExport{
		{Name: "a"}, {Name: "b"},
	}})
	if !errors.Is(err, ErrValidation) || ErrorReason(err) != ReasonImportWorkLimitExceeded {
		t.Fatalf("err=%v reason=%q", err, ErrorReason(err))
	}
	details := ErrorDetails(err)
	if details["rule"] != "max_projects" || details["max"] != 1 {
		t.Fatalf("details=%v", details)
	}
}

func TestImportValidation_InternalErrorRedacted(t *testing.T) {
	cause := errors.New(`sql: near "/var/lib/scrumboy/data.db": syntax error SELECT * FROM secrets`)
	err := redactImportInternal(cause)
	if ErrorReason(err) != ReasonImportInternalRedacted {
		t.Fatalf("reason=%q", ErrorReason(err))
	}
	msg := err.Error()
	for _, leak := range []string{"SELECT", "/var/lib", "secrets", "syntax error", "sql:"} {
		if strings.Contains(msg, leak) {
			t.Fatalf("redacted message still contains %q: %s", leak, msg)
		}
	}
	if msg != "Import failed due to an internal error." {
		t.Fatalf("message=%q", msg)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("cause not retained via Unwrap: %v", err)
	}
}

func TestValidateBackupData_MalformedVersionReasoned(t *testing.T) {
	err := validateBackupData(&ExportData{Version: "0.0.1"}, false)
	// file-level version errors use ReasonImportFileInvalid (no project context)
	if !errors.Is(err, ErrValidation) || ErrorReason(err) != ReasonImportFileInvalid {
		t.Fatalf("err=%v reason=%q", err, ErrorReason(err))
	}
	if ErrorDetails(err)["rule"] != "unsupported_export_version" {
		t.Fatalf("details=%v", ErrorDetails(err))
	}
}

func TestValidateBackupData_NilDataReasoned(t *testing.T) {
	err := validateBackupData(nil, false)
	if !errors.Is(err, ErrValidation) || ErrorReason(err) != ReasonImportFileInvalid {
		t.Fatalf("err=%v reason=%q", err, ErrorReason(err))
	}
	if ErrorDetails(err)["rule"] != "missing_export_data" {
		t.Fatalf("details=%v", ErrorDetails(err))
	}
}

func mustBootstrap(t *testing.T, st *Store, email string) User {
	t.Helper()
	u, err := st.BootstrapUser(context.Background(), email, "password123", "User")
	if err != nil {
		t.Fatalf("BootstrapUser: %v", err)
	}
	return u
}
