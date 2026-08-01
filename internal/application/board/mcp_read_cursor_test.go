package board

import (
	"context"
	"encoding/base64"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"scrumboy/internal/store"
)

func encodeMCPBoardCursorForTest(raw string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func TestMCPBoardReadCursorValidationOrder(t *testing.T) {
	t.Run("unknown column fails after workflow before lanes", func(t *testing.T) {
		h := newMCPBoardReadHarness()
		prepared := prepareMCPBoardRead(t, h, context.Background())

		_, err := prepared.Read(MCPBoardReadQuery{
			Limit:          20,
			CursorByColumn: map[string]string{"unknown": encodeMCPBoardCursorForTest("1:1")},
		})

		var cursorErr *MCPBoardCursorError
		if !errors.As(err, &cursorErr) ||
			cursorErr.Kind != MCPBoardCursorUnknownColumn ||
			cursorErr.ColumnKey != "unknown" {
			t.Fatalf("Read error = %#v, want unknown-column cursor error", err)
		}
		if got, want := h.recorder.operations(), []string{"access", "workflow"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("operations = %v, want %v", got, want)
		}
	})

	malformedTokens := map[string]string{
		"invalid base64url": "%%%",
		"malformed raw":     encodeMCPBoardCursorForTest("malformed"),
		"zero sentinel":     encodeMCPBoardCursorForTest("0:0"),
	}
	for name, token := range malformedTokens {
		t.Run(name, func(t *testing.T) {
			h := newMCPBoardReadHarness()
			prepared := prepareMCPBoardRead(t, h, context.Background())

			_, err := prepared.Read(MCPBoardReadQuery{
				Limit:          20,
				CursorByColumn: map[string]string{"triage": token},
			})

			var cursorErr *MCPBoardCursorError
			if !errors.As(err, &cursorErr) ||
				cursorErr.Kind != MCPBoardCursorMalformed ||
				cursorErr.ColumnKey != "triage" {
				t.Fatalf("Read error = %#v, want malformed triage cursor", err)
			}
			if got, want := h.recorder.operations(), []string{"access", "workflow"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("operations = %v, want %v", got, want)
			}
		})
	}

	t.Run("later malformed cursor preserves earlier lane calls", func(t *testing.T) {
		h := newMCPBoardReadHarness()
		prepared := prepareMCPBoardRead(t, h, context.Background())

		_, err := prepared.Read(MCPBoardReadQuery{
			Limit:          20,
			CursorByColumn: map[string]string{"shipped": "%%%"},
		})

		var cursorErr *MCPBoardCursorError
		if !errors.As(err, &cursorErr) ||
			cursorErr.Kind != MCPBoardCursorMalformed ||
			cursorErr.ColumnKey != "shipped" {
			t.Fatalf("Read error = %#v, want malformed shipped cursor", err)
		}
		wantOperations := []string{"access", "workflow", "list:triage", "count:triage"}
		if got := h.recorder.operations(); !reflect.DeepEqual(got, wantOperations) {
			t.Fatalf("operations = %v, want %v", got, wantOperations)
		}
	})
}

func TestMCPBoardReadCursorSentinelsAndContinuation(t *testing.T) {
	tests := []struct {
		name       string
		sortOrder  store.SortOrder
		createdAt  time.Time
		wantAfterA int64
		wantAfterB int64
		wantRaw    string
	}{
		{
			name:       "manual rank",
			sortOrder:  store.SortOrderDefault,
			createdAt:  time.UnixMilli(1),
			wantAfterA: math.MinInt64,
			wantAfterB: 0,
			wantRaw:    "900:41",
		},
		{
			name:       "oldest",
			sortOrder:  store.SortOrderOldest,
			createdAt:  time.UnixMilli(1700000000123),
			wantAfterA: math.MinInt64,
			wantAfterB: 0,
			wantRaw:    "1700000000123:41",
		},
		{
			name:       "newest",
			sortOrder:  store.SortOrderNewest,
			createdAt:  time.UnixMilli(1700000000123),
			wantAfterA: math.MaxInt64,
			wantAfterB: math.MaxInt64,
			wantRaw:    "1700000000123:41",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newMCPBoardReadHarness()
			h.workflow.result = h.workflow.result[:1]
			h.lanes.pages["triage"] = mcpBoardReadLanePage{
				todos: []store.Todo{{
					ID:        41,
					ProjectID: 17,
					ColumnKey: "triage",
					Rank:      900,
					CreatedAt: tt.createdAt,
				}},
				storeToken: "ignored-store-token",
				hasMore:    true,
			}
			prepared := prepareMCPBoardRead(t, h, context.Background())

			result, err := prepared.Read(MCPBoardReadQuery{
				Limit:     20,
				SortOrder: tt.sortOrder,
			})
			if err != nil {
				t.Fatalf("Read: %v", err)
			}

			listCall := h.recorder.calls[2]
			if listCall.operation != "list:triage" ||
				listCall.afterA != tt.wantAfterA ||
				listCall.afterB != tt.wantAfterB ||
				listCall.sortOrder != tt.sortOrder {
				t.Fatalf("list call = %#v", listCall)
			}
			wantToken := encodeMCPBoardCursorForTest(tt.wantRaw)
			if result.Columns[0].NextCursor == nil || *result.Columns[0].NextCursor != wantToken {
				t.Fatalf("next cursor = %#v, want %q", result.Columns[0].NextCursor, wantToken)
			}
		})
	}
}

func TestMCPBoardReadIndependentInputCursors(t *testing.T) {
	h := newMCPBoardReadHarness()
	prepared := prepareMCPBoardRead(t, h, context.Background())

	_, err := prepared.Read(MCPBoardReadQuery{
		Limit: 20,
		CursorByColumn: map[string]string{
			"triage":  encodeMCPBoardCursorForTest("100:11"),
			"shipped": encodeMCPBoardCursorForTest("300:33"),
		},
	})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}

	triage := h.recorder.calls[2]
	shipped := h.recorder.calls[4]
	if triage.operation != "list:triage" || triage.afterA != 100 || triage.afterB != 11 {
		t.Fatalf("triage list call = %#v", triage)
	}
	if shipped.operation != "list:shipped" || shipped.afterA != 300 || shipped.afterB != 33 {
		t.Fatalf("shipped list call = %#v", shipped)
	}
}

func TestMCPBoardReadEmptyCursorUsesFirstPageBoundary(t *testing.T) {
	for _, token := range []string{"", "   "} {
		t.Run("token="+token, func(t *testing.T) {
			h := newMCPBoardReadHarness()
			h.workflow.result = h.workflow.result[:1]
			prepared := prepareMCPBoardRead(t, h, context.Background())

			_, err := prepared.Read(MCPBoardReadQuery{
				Limit:          20,
				CursorByColumn: map[string]string{"triage": token},
			})
			if err != nil {
				t.Fatalf("Read: %v", err)
			}

			listCall := h.recorder.calls[2]
			if listCall.afterA != math.MinInt64 || listCall.afterB != 0 {
				t.Fatalf("list boundary = (%d,%d), want (%d,0)", listCall.afterA, listCall.afterB, int64(math.MinInt64))
			}
		})
	}
}

func TestMCPBoardReadNextCursorRequiresMoreAndItems(t *testing.T) {
	tests := []struct {
		name    string
		page    mcpBoardReadLanePage
		wantNil bool
	}{
		{
			name: "has more false with item",
			page: mcpBoardReadLanePage{
				todos: []store.Todo{{ID: 1, Rank: 10}},
			},
			wantNil: true,
		},
		{
			name:    "has more true without items",
			page:    mcpBoardReadLanePage{todos: []store.Todo{}, hasMore: true},
			wantNil: true,
		},
		{
			name: "has more true with item",
			page: mcpBoardReadLanePage{
				todos:   []store.Todo{{ID: 1, Rank: 10}},
				hasMore: true,
			},
			wantNil: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newMCPBoardReadHarness()
			h.workflow.result = h.workflow.result[:1]
			h.lanes.pages["triage"] = tt.page
			prepared := prepareMCPBoardRead(t, h, context.Background())

			result, err := prepared.Read(MCPBoardReadQuery{Limit: 20})
			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if gotNil := result.Columns[0].NextCursor == nil; gotNil != tt.wantNil {
				t.Fatalf("next cursor nil = %v, want %v", gotNil, tt.wantNil)
			}
		})
	}
}
