package mcp

import (
	"reflect"
	"testing"
)

func TestJSONRPCBoardGetResultComposition(t *testing.T) {
	data := map[string]any{
		"project": map[string]any{"projectSlug": "phase-nine"},
		"columns": []any{},
	}
	meta := map[string]any{
		"nextCursorByColumn": map[string]any{"backlog": "opaque"},
		"hasMoreByColumn":    map[string]bool{"backlog": true},
		"totalCountByColumn": map[string]int{"backlog": 3},
		"unrelated":          "must not leak",
	}

	got := jsonRPCToolStructuredContent("board_get", data, meta)

	want := map[string]any{
		"project":            map[string]any{"projectSlug": "phase-nine"},
		"columns":            []any{},
		"nextCursorByColumn": map[string]any{"backlog": "opaque"},
		"hasMoreByColumn":    map[string]bool{"backlog": true},
		"totalCountByColumn": map[string]int{"backlog": 3},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("structured content = %#v, want %#v", got, want)
	}
	if _, ok := data["nextCursorByColumn"]; ok {
		t.Fatalf("source data was mutated: %#v", data)
	}
	gotMap, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("board result type = %T, want map[string]any", got)
	}
	gotMap["cloneProbe"] = true
	if _, exists := data["cloneProbe"]; exists {
		t.Fatalf("board result was not cloned: source=%#v", data)
	}
	if len(meta) != 4 || meta["unrelated"] != "must not leak" {
		t.Fatalf("source metadata was mutated: %#v", meta)
	}
}

func TestJSONRPCBoardGetResultCompositionSupportsDottedAlias(t *testing.T) {
	data := map[string]any{"project": "kept", "columns": "kept"}
	meta := map[string]any{
		"nextCursorByColumn": "next",
		"hasMoreByColumn":    "more",
		"totalCountByColumn": "total",
	}

	got := jsonRPCToolStructuredContent("board.get", data, meta)

	want := map[string]any{
		"project":            "kept",
		"columns":            "kept",
		"nextCursorByColumn": "next",
		"hasMoreByColumn":    "more",
		"totalCountByColumn": "total",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("alias structured content = %#v, want %#v", got, want)
	}
}

func TestJSONRPCToolResultCompositionAddsApprovedNonBoardMetadata(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		data     any
		meta     map[string]any
		want     map[string]any
	}{
		{
			name:     "system capabilities typed data",
			toolName: "system_getCapabilities",
			data: capabilitiesData{
				ServerMode: "anonymous",
				Identity: identityCapabilities{
					Project: "projectSlug",
				},
			},
			meta: map[string]any{
				"adapterVersion": 1,
				"unrelated":      "must not leak",
			},
			want: map[string]any{
				"serverMode": "anonymous",
				"auth": map[string]any{
					"mode":                     "",
					"authenticated":            false,
					"authenticatedToolsUsable": false,
				},
				"bootstrapAvailable": false,
				"identity": map[string]any{
					"project": "projectSlug",
					"todo":    nil,
				},
				"pagination": map[string]any{
					"defaultInput":  nil,
					"defaultOutput": nil,
				},
				"implementedTools": nil,
				"adapterVersion":   1,
			},
		},
		{
			name:     "system capabilities dotted alias",
			toolName: "system.getCapabilities",
			data:     map[string]any{"serverMode": "anonymous"},
			meta:     map[string]any{"adapterVersion": 1},
			want:     map[string]any{"serverMode": "anonymous", "adapterVersion": 1},
		},
		{
			name:     "sprints",
			toolName: "sprints_list",
			data:     map[string]any{"items": []any{"sprint"}},
			meta: map[string]any{
				"unscheduledCount": 4,
				"unrelated":        "must not leak",
			},
			want: map[string]any{"items": []any{"sprint"}, "unscheduledCount": 4},
		},
		{
			name:     "sprints dotted alias",
			toolName: "sprints.list",
			data:     map[string]any{"items": []any{}},
			meta:     map[string]any{"unscheduledCount": 0},
			want:     map[string]any{"items": []any{}, "unscheduledCount": 0},
		},
		{
			name:     "dashboard todos",
			toolName: "dashboard_listTodos",
			data:     map[string]any{"items": []any{"todo"}},
			meta: map[string]any{
				"nextCursor": "opaque",
				"hasMore":    true,
				"unrelated":  "must not leak",
			},
			want: map[string]any{
				"items":      []any{"todo"},
				"nextCursor": "opaque",
				"hasMore":    true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := jsonRPCToolStructuredContent(tt.toolName, tt.data, tt.meta)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("structured content = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestJSONRPCToolResultCompositionLeavesUnapprovedToolsUnchanged(t *testing.T) {
	data := map[string]any{"items": []any{"kept"}}
	meta := map[string]any{"nextCursor": "must remain private", "hasMore": true}

	got := jsonRPCToolStructuredContent("projects_list", data, meta)

	if !reflect.DeepEqual(got, data) {
		t.Fatalf("unrelated tool data changed: got=%#v want=%#v", got, data)
	}
	got.(map[string]any)["identityProbe"] = true
	if data["identityProbe"] != true {
		t.Fatal("unrelated tool data should be returned without cloning or composition")
	}
}

func TestJSONRPCToolResultCompositionPreservesDataOnCollision(t *testing.T) {
	data := map[string]any{
		"items":      []any{"kept"},
		"nextCursor": "owned-by-data",
	}
	meta := map[string]any{
		"nextCursor": "must-not-overwrite",
		"hasMore":    true,
	}

	got := jsonRPCToolStructuredContent("dashboard_listTodos", data, meta)

	want := map[string]any{
		"items":      []any{"kept"},
		"nextCursor": "owned-by-data",
		"hasMore":    true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("collision result = %#v, want %#v", got, want)
	}
	if data["nextCursor"] != "owned-by-data" {
		t.Fatalf("source data was mutated: %#v", data)
	}
	if meta["nextCursor"] != "must-not-overwrite" {
		t.Fatalf("source metadata was mutated: %#v", meta)
	}
}

func TestJSONRPCBoardGetResultCompositionHandlesMissingOrUnexpectedData(t *testing.T) {
	t.Run("nil metadata clones data without additions", func(t *testing.T) {
		data := map[string]any{"project": "kept", "columns": "kept"}

		got := jsonRPCToolStructuredContent("board_get", data, nil)

		if !reflect.DeepEqual(got, data) {
			t.Fatalf("nil metadata result = %#v, want %#v", got, data)
		}
		got.(map[string]any)["cloneProbe"] = true
		if _, exists := data["cloneProbe"]; exists {
			t.Fatal("board data should still be cloned")
		}
	})

	t.Run("unexpected board data type is preserved", func(t *testing.T) {
		data := []any{"unexpected"}

		got := jsonRPCToolStructuredContent("board_get", data, map[string]any{
			"nextCursorByColumn": "not merged",
		})

		if !reflect.DeepEqual(got, data) {
			t.Fatalf("unexpected data result = %#v, want %#v", got, data)
		}
	})
}
