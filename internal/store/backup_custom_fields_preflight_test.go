package store

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"scrumboy/internal/version"
)

func customFieldParityData(slug string, defs []CustomFieldExport, values map[string]any, archive bool) *ExportData {
	now := time.Now().UTC().Truncate(time.Millisecond)
	v := version.ExportFormatVersion
	if archive {
		v = version.ArchiveExportFormatVersion
	}
	return &ExportData{Version: v, ExportedAt: now, Mode: "full", Scope: "full", Projects: []ProjectExport{{
		Slug: slug, Name: "Identity", CreatedAt: now, UpdatedAt: now, CustomFields: defs,
		Todos: []TodoExport{
			{LocalID: 1, Title: "one", Status: "BACKLOG", Rank: 1000, Tags: []string{}, CreatedAt: now, UpdatedAt: now, CustomFields: values},
			{LocalID: 2, Title: "two", Status: "BACKLOG", Rank: 2000, Tags: []string{}, CreatedAt: now, UpdatedAt: now},
		},
	}}}
}

// TestImportCustomFieldsPreviewMatchesImport checks that preview rejects exactly the custom field
// backups that the import itself would reject, per mode and for JSON and archive imports. The
// destination project (from setupImportIdentityProject) defines ref:text and points:number.
func TestImportCustomFieldsPreviewMatchesImport(t *testing.T) {
	manyDefs := func(n int) []CustomFieldExport {
		out := make([]CustomFieldExport, n)
		for i := range out {
			out[i] = CustomFieldExport{Key: fmt.Sprintf("f%02d", i), Name: fmt.Sprintf("F%02d", i), Type: "text"}
		}
		return out
	}
	all := map[string]bool{"replace": true, "merge": true, "copy": true}
	none := map[string]bool{}
	newProjectOnly := map[string]bool{"replace": true, "copy": true}
	mergeOnly := map[string]bool{"merge": true}

	cases := []struct {
		name    string
		defs    []CustomFieldExport
		values  map[string]any
		ok      map[string]bool // import modes expected to succeed
		errHint string          // substring the rejection must carry
	}{
		{name: "valid", defs: []CustomFieldExport{{Key: "ref", Name: "Ref", Type: "text"}}, values: map[string]any{"ref": "x"}, ok: all},
		{name: "type conflicts with existing", defs: []CustomFieldExport{{Key: "ref", Name: "Ref", Type: "url"}}, values: map[string]any{"ref": "https://example.com"}, ok: newProjectOnly, errHint: `"ref" already exists with type text`},
		{name: "invalid definition type", defs: []CustomFieldExport{{Key: "due", Name: "Due", Type: "date"}}, ok: none, errHint: "invalid custom field definition"},
		{name: "invalid definition key", defs: []CustomFieldExport{{Key: "Bad Key!", Name: "Bad", Type: "text"}}, ok: none, errHint: "invalid custom field definition"},
		{name: "empty definition name", defs: []CustomFieldExport{{Key: "blank", Name: "  ", Type: "text"}}, ok: none, errHint: "invalid custom field definition"},
		{name: "duplicate keys with conflicting types", defs: []CustomFieldExport{{Key: "dup", Name: "Dup", Type: "text"}, {Key: "dup", Name: "Dup", Type: "number"}}, ok: none, errHint: `"dup"`},
		{name: "limit reached with existing fields", defs: manyDefs(24), ok: newProjectOnly, errHint: "at most 25 custom fields"},
		{name: "backup alone over limit", defs: manyDefs(26), ok: none, errHint: "at most 25 custom fields"},
		{name: "undefined value key", values: map[string]any{"nope": "x"}, ok: none, errHint: `undefined custom field "nope"`},
		{name: "value key defined only in destination", values: map[string]any{"points": float64(3)}, ok: mergeOnly, errHint: `undefined custom field "points"`},
		{name: "invalid url value", defs: []CustomFieldExport{{Key: "site", Name: "Site", Type: "url"}}, values: map[string]any{"site": "ftp://example.com"}, ok: none, errHint: "http(s) URL"},
		{name: "string for number value", defs: []CustomFieldExport{{Key: "n", Name: "N", Type: "number"}}, values: map[string]any{"n": "12"}, ok: none, errHint: "expected a number"},
		{name: "number out of range", defs: []CustomFieldExport{{Key: "n", Name: "N", Type: "number"}}, values: map[string]any{"n": float64(1 << 54)}, ok: none, errHint: "out of range"},
		{name: "empty text value", defs: []CustomFieldExport{{Key: "note", Name: "Note", Type: "text"}}, values: map[string]any{"note": "   "}, ok: none, errHint: "cannot be empty"},
	}
	for _, tc := range cases {
		for _, importMode := range []string{"replace", "merge", "copy"} {
			for _, archive := range []bool{false, true} {
				kind := "json"
				if archive {
					kind = "sbz"
				}
				t.Run(tc.name+"/"+importMode+"/"+kind, func(t *testing.T) {
					st, sqlDB, ctx, p := setupImportIdentityProject(t)
					data := customFieldParityData(p.Slug, tc.defs, tc.values, archive)

					var previewErr error
					if archive {
						_, previewErr = st.PreviewImportArchive(ctx, data, ModeFull, importMode)
					} else {
						_, previewErr = st.PreviewImport(ctx, data, ModeFull, importMode)
					}
					before := importIdentitySnapshot(t, sqlDB)
					var importErr error
					if archive {
						if err := st.PreflightImport(ctx, data, ModeFull, importMode, true); err != nil {
							importErr = err
						} else {
							_, importErr = st.ImportProjectsWithAssets(ctx, data, ModeFull, importMode, nil)
						}
					} else {
						_, importErr = st.ImportProjects(ctx, data, ModeFull, importMode)
					}

					wantOK := tc.ok[importMode]
					if wantOK {
						if previewErr != nil || importErr != nil {
							t.Fatalf("want success: preview err = %v, import err = %v", previewErr, importErr)
						}
						return
					}
					if !errors.Is(importErr, ErrValidation) {
						t.Fatalf("import err = %v, want validation error", importErr)
					}
					if !errors.Is(previewErr, ErrValidation) {
						t.Fatalf("preview err = %v, but import fails with %v", previewErr, importErr)
					}
					for _, err := range []error{previewErr, importErr} {
						if !strings.Contains(err.Error(), tc.errHint) {
							t.Fatalf("error %q does not mention %q", err, tc.errHint)
						}
					}
					if after := importIdentitySnapshot(t, sqlDB); after != before {
						t.Fatalf("rejected import changed data:\nbefore:\n%s\nafter:\n%s", before, after)
					}
				})
			}
		}
	}
}

// TestImportCustomFieldsArchivePreflightRejectsBeforeStaging pins that the archive service's
// preflight (which runs before any image file is written) catches custom field conflicts.
func TestImportCustomFieldsArchivePreflightRejectsBeforeStaging(t *testing.T) {
	st, _, ctx, p := setupImportIdentityProject(t)
	data := customFieldParityData(p.Slug, []CustomFieldExport{{Key: "points", Name: "Points", Type: "text"}}, nil, true)
	if err := st.PreflightImport(ctx, data, ModeFull, "merge", true); !errors.Is(err, ErrValidation) {
		t.Fatalf("PreflightImport err = %v, want validation error", err)
	}
	if err := st.PreflightImport(ctx, data, ModeFull, "copy", true); err != nil {
		t.Fatalf("copy PreflightImport err = %v, want nil (copy creates a new project)", err)
	}
}
