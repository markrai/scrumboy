package mcp

import (
	"math"
	"net/http"
	"testing"
	"time"

	"scrumboy/internal/store"
)

func TestBoardGetContract_LimitDefaultsAndBounds(t *testing.T) {
	tests := []struct {
		name      string
		limit     any
		wantLimit int
		wantError bool
	}{
		{name: "absent", wantLimit: 20},
		{name: "zero", limit: 0, wantLimit: 20},
		{name: "one", limit: 1, wantLimit: 1},
		{name: "maximum", limit: 100, wantLimit: 100},
		{name: "negative", limit: -1, wantError: true},
		{name: "above maximum", limit: 101, wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newBoardGetContractHarness(t)
			input := map[string]any{"projectSlug": h.Project.Slug}
			if tt.limit != nil {
				input["limit"] = tt.limit
			}

			_, _, err := h.call(input)

			if tt.wantError {
				requireBoardGetError(t, err, http.StatusBadRequest, CodeValidationError, "invalid limit", map[string]any{"field": "limit"})
				requireOperationNames(t, h.Recording, "countUsers")
				return
			}
			if err != nil {
				t.Fatalf("board_get: %v", err)
			}
			for _, call := range h.Recording.callsFor("list") {
				if call.Limit != tt.wantLimit {
					t.Fatalf("%s limit = %d, want %d", call.ColumnKey, call.Limit, tt.wantLimit)
				}
			}
		})
	}
}

func TestBoardGetContract_FirstPageCursorSentinels(t *testing.T) {
	tests := []struct {
		name   string
		sort   string
		wantA  int64
		wantB  int64
		wantSO store.SortOrder
	}{
		{name: "manual default", wantA: math.MinInt64, wantB: 0, wantSO: store.SortOrderDefault},
		{name: "newest", sort: "newest", wantA: math.MaxInt64, wantB: math.MaxInt64, wantSO: store.SortOrderNewest},
		{name: "oldest", sort: "oldest", wantA: math.MinInt64, wantB: 0, wantSO: store.SortOrderOldest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newBoardGetContractHarness(t)

			_, _, err := h.call(map[string]any{"projectSlug": h.Project.Slug, "sort": tt.sort})
			if err != nil {
				t.Fatalf("board_get: %v", err)
			}

			for _, call := range h.Recording.callsFor("list") {
				if call.AfterA != tt.wantA || call.AfterB != tt.wantB || call.Sort != tt.wantSO {
					t.Fatalf("%s cursor/sort = (%d,%d,%q), want (%d,%d,%q)",
						call.ColumnKey, call.AfterA, call.AfterB, call.Sort,
						tt.wantA, tt.wantB, tt.wantSO,
					)
				}
			}
		})
	}
}

func TestBoardGetContract_PerColumnCursorIndependence(t *testing.T) {
	h := newBoardGetContractHarness(t)

	_, _, err := h.call(map[string]any{
		"projectSlug": h.Project.Slug,
		"cursorByColumn": map[string]any{
			"triage":  encodeBoardCursor("100:11"),
			"shipped": encodeBoardCursor("300:33"),
		},
	})
	if err != nil {
		t.Fatalf("board_get: %v", err)
	}

	got := make(map[string][2]int64)
	for _, call := range h.Recording.callsFor("list") {
		got[call.ColumnKey] = [2]int64{call.AfterA, call.AfterB}
	}
	want := map[string][2]int64{
		"triage":   {100, 11},
		"building": {math.MinInt64, 0},
		"shipped":  {300, 33},
	}
	for key, bounds := range want {
		if got[key] != bounds {
			t.Fatalf("%s cursor = %v, want %v (all=%v)", key, got[key], bounds, got)
		}
	}
}

func TestBoardGetContract_SuppliedCursorOverridesSortSentinel(t *testing.T) {
	for _, sortOrder := range []string{"", "newest", "oldest"} {
		t.Run("sort="+sortOrder, func(t *testing.T) {
			h := newBoardGetContractHarness(t)

			_, _, err := h.call(map[string]any{
				"projectSlug": h.Project.Slug,
				"sort":        sortOrder,
				"cursorByColumn": map[string]any{
					"triage": encodeBoardCursor("1234:56"),
				},
			})
			if err != nil {
				t.Fatalf("board_get: %v", err)
			}

			call := h.Recording.callsFor("list")[0]
			if call.ColumnKey != "triage" || call.AfterA != 1234 || call.AfterB != 56 {
				t.Fatalf("supplied cursor call = %#v", call)
			}
		})
	}
}

func TestBoardGetContract_EmptyCursorTokensUseFirstPageBoundary(t *testing.T) {
	for _, token := range []string{"", "   "} {
		h := newBoardGetContractHarness(t)

		_, _, err := h.call(map[string]any{
			"projectSlug": h.Project.Slug,
			"cursorByColumn": map[string]any{
				"triage": token,
			},
		})
		if err != nil {
			t.Fatalf("token %q: %v", token, err)
		}
		call := h.Recording.callsFor("list")[0]
		if call.AfterA != math.MinInt64 || call.AfterB != 0 {
			t.Fatalf("token %q cursor = (%d,%d), want first page", token, call.AfterA, call.AfterB)
		}
	}
}

func TestBoardGetContract_MalformedCursorVariants(t *testing.T) {
	tests := []struct {
		name  string
		token string
	}{
		{name: "invalid base64url", token: "not-base64!"},
		{name: "decoded malformed store cursor", token: encodeBoardCursor("malformed")},
		{name: "decoded zero sentinel", token: encodeBoardCursor("0:0")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newBoardGetContractHarness(t)

			_, _, err := h.call(map[string]any{
				"projectSlug": h.Project.Slug,
				"cursorByColumn": map[string]any{
					"triage": tt.token,
				},
			})

			requireBoardGetError(t, err, http.StatusBadRequest, CodeValidationError, "invalid board cursor", map[string]any{
				"field":     "cursorByColumn",
				"columnKey": "triage",
			})
			requireOperationNames(t, h.Recording, "countUsers", "access", "workflow")
		})
	}
}

func TestBoardGetContract_OpaqueOutputCursorConstruction(t *testing.T) {
	createdAt := time.UnixMilli(1_700_000_000_123).UTC()
	tests := []struct {
		name       string
		sort       string
		wantCursor string
	}{
		{name: "manual rank", wantCursor: encodeBoardCursor("900:41")},
		{name: "newest creation time", sort: "newest", wantCursor: encodeBoardCursor("1700000000123:41")},
		{name: "oldest creation time", sort: "oldest", wantCursor: encodeBoardCursor("1700000000123:41")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newBoardGetContractHarness(t)
			h.Recording.ListResults["triage"] = boardGetListResult{
				Todos: []store.Todo{
					boardGetTodo(40, h.Project.ID, 800, "triage", "first", createdAt.Add(-time.Second)),
					boardGetTodo(41, h.Project.ID, 900, "triage", "last", createdAt),
				},
				Cursor:  "store-cursor-is-not-exposed",
				HasMore: true,
			}
			h.Recording.CountResults["triage"] = 7

			_, meta, err := h.call(map[string]any{"projectSlug": h.Project.Slug, "sort": tt.sort})
			if err != nil {
				t.Fatalf("board_get: %v", err)
			}

			next := meta["nextCursorByColumn"].(map[string]any)
			hasMore := meta["hasMoreByColumn"].(map[string]bool)
			total := meta["totalCountByColumn"].(map[string]int)
			if next["triage"] != tt.wantCursor {
				t.Fatalf("next cursor = %#v, want %q", next["triage"], tt.wantCursor)
			}
			if hasMore["triage"] != true || total["triage"] != 7 {
				t.Fatalf("triage metadata = next=%#v hasMore=%v total=%d", next["triage"], hasMore["triage"], total["triage"])
			}
			for _, key := range []string{"triage", "building", "shipped"} {
				if _, ok := next[key]; !ok {
					t.Fatalf("nextCursorByColumn missing %q: %#v", key, next)
				}
				if _, ok := hasMore[key]; !ok {
					t.Fatalf("hasMoreByColumn missing %q: %#v", key, hasMore)
				}
				if _, ok := total[key]; !ok {
					t.Fatalf("totalCountByColumn missing %q: %#v", key, total)
				}
			}
		})
	}
}

func TestBoardGetContract_NoNextCursorWithoutReturnedContinuationRow(t *testing.T) {
	tests := []struct {
		name     string
		hasMore  bool
		withTodo bool
	}{
		{name: "hasMore false with todo", withTodo: true},
		{name: "hasMore true without todo", hasMore: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newBoardGetContractHarness(t)
			result := boardGetListResult{HasMore: tt.hasMore}
			if tt.withTodo {
				result.Todos = []store.Todo{
					boardGetTodo(1, h.Project.ID, 10, "triage", "only", time.Unix(1, 0).UTC()),
				}
			}
			h.Recording.ListResults["triage"] = result

			_, meta, err := h.call(map[string]any{"projectSlug": h.Project.Slug})
			if err != nil {
				t.Fatalf("board_get: %v", err)
			}
			next := meta["nextCursorByColumn"].(map[string]any)
			if next["triage"] != nil {
				t.Fatalf("next cursor = %#v, want nil", next["triage"])
			}
		})
	}
}

func TestBoardGetContract_SuccessfulStorePortCardinality(t *testing.T) {
	h := newBoardGetContractHarness(t)

	_, _, err := h.call(map[string]any{"projectSlug": h.Project.Slug})
	if err != nil {
		t.Fatalf("board_get: %v", err)
	}

	if len(h.Recording.callsFor("access")) != 1 ||
		len(h.Recording.callsFor("sprint")) != 0 ||
		len(h.Recording.callsFor("workflow")) != 1 ||
		len(h.Recording.callsFor("list")) != 3 ||
		len(h.Recording.callsFor("count")) != 3 ||
		len(h.Recording.callsFor("activity")) != 0 {
		t.Fatalf("unexpected cardinality: operations=%v", h.Recording.operationNames())
	}
}
