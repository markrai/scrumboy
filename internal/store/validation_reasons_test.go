package store

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

func TestTodoLimitsCountCharactersNotBytes(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	p, err := st.CreateProject(ctx, "limits")
	if err != nil {
		t.Fatal(err)
	}

	// 200 CJK characters are 600 bytes: code-point limits accept them, so must the server.
	title := strings.Repeat("漢", 200)
	body := strings.Repeat("é", 20000)
	if _, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: title, Body: body, ColumnKey: DefaultColumnBacklog}, ModeFull); err != nil {
		t.Fatalf("200-char title and 20000-char body must be accepted: %v", err)
	}
	if _, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: title + "x", ColumnKey: DefaultColumnBacklog}, ModeFull); !errors.Is(err, ErrValidation) {
		t.Fatalf("201-char title: want ErrValidation, got %v", err)
	}
	if _, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: "ok", Body: body + "x", ColumnKey: DefaultColumnBacklog}, ModeFull); !errors.Is(err, ErrValidation) {
		t.Fatalf("20001-char body: want ErrValidation, got %v", err)
	}
	// Supplementary-plane emoji: 200 code points, 400 UTF-16 units / 800 UTF-8 bytes.
	emojiTitle := strings.Repeat("😀", 200)
	if _, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: emojiTitle, ColumnKey: DefaultColumnBacklog}, ModeFull); err != nil {
		t.Fatalf("200 emoji title must be accepted: %v", err)
	}
	if _, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: emojiTitle + "😀", ColumnKey: DefaultColumnBacklog}, ModeFull); !errors.Is(err, ErrValidation) {
		t.Fatalf("201 emoji title: want ErrValidation, got %v", err)
	}
	// Combining marks count as separate code points (e + combining acute).
	combining := strings.Repeat("e\u0301", 100) // 200 code points
	if _, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: combining, ColumnKey: DefaultColumnBacklog}, ModeFull); err != nil {
		t.Fatalf("200 combining-mark title must be accepted: %v", err)
	}
	if _, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: combining + "x", ColumnKey: DefaultColumnBacklog}, ModeFull); !errors.Is(err, ErrValidation) {
		t.Fatalf("201 combining-mark title: want ErrValidation, got %v", err)
	}
	mixed := strings.Repeat("名😀e\u0301", 50) // 200 code points
	if _, err := st.CreateTodo(ctx, p.ID, CreateTodoInput{Title: mixed, ColumnKey: DefaultColumnBacklog}, ModeFull); err != nil {
		t.Fatalf("mixed CJK/emoji/combining title must be accepted: %v", err)
	}
	if _, err := st.CreateProject(ctx, strings.Repeat("名", 200)); ErrorReason(err) != ReasonProjectNameNoSlug {
		t.Fatalf("name with no slug-able characters: want reason %s, got %q (%v)", ReasonProjectNameNoSlug, ErrorReason(err), err)
	}
}

func TestValidationReasonsAreExposed(t *testing.T) {
	if err := validateDueAt(timePtr(time.Date(20260, 1, 1, 0, 0, 0, 0, time.UTC))); ErrorReason(err) != ReasonInvalidDueAt {
		t.Fatalf("due date: reason %q err %v", ErrorReason(err), err)
	}

	cases := []struct {
		name   string
		field  CustomField
		value  any
		reason string
	}{
		{"empty text", CustomField{Key: "k", Type: CustomFieldText}, "  ", ReasonCustomFieldEmpty},
		{"long text", CustomField{Key: "k", Type: CustomFieldText}, strings.Repeat("a", maxCustomFieldValueLen+1), ReasonCustomFieldTooLong},
		{"bad url", CustomField{Key: "k", Type: CustomFieldURL}, "ftp://x", ReasonCustomFieldNotURL},
		{"not number", CustomField{Key: "k", Type: CustomFieldNumber}, "x", ReasonCustomFieldNotNumber},
		{"huge number", CustomField{Key: "k", Type: CustomFieldNumber}, float64(1 << 60), ReasonCustomFieldNumberRange},
	}
	for _, c := range cases {
		_, err := normalizeCustomFieldValue(c.field, c.value)
		if !errors.Is(err, ErrValidation) || ErrorReason(err) != c.reason {
			t.Errorf("%s: want %s, got %q (%v)", c.name, c.reason, ErrorReason(err), err)
		}
	}

	field := CustomField{Key: "points", Type: CustomFieldNumber}
	maxExact := float64(maxCustomFieldNumber - 1)
	for _, n := range []float64{maxExact, -maxExact} {
		if _, err := normalizeCustomFieldValue(field, n); err != nil {
			t.Fatalf("±(2^53-1) must be accepted: %v", err)
		}
	}
	for _, n := range []float64{float64(maxCustomFieldNumber), -float64(maxCustomFieldNumber)} {
		_, err := normalizeCustomFieldValue(field, n)
		if !errors.Is(err, ErrValidation) || ErrorReason(err) != ReasonCustomFieldNumberRange {
			t.Fatalf("±2^53: want %s, got %q (%v)", ReasonCustomFieldNumberRange, ErrorReason(err), err)
		}
		details := ErrorDetails(err)
		if details == nil || details["max"] != maxCustomFieldNumber {
			t.Fatalf("±2^53 details.max = %v, want %d", details, maxCustomFieldNumber)
		}
	}
	for _, n := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		_, err := normalizeCustomFieldValue(field, n)
		if !errors.Is(err, ErrValidation) || ErrorReason(err) != ReasonCustomFieldNotNumber {
			t.Fatalf("NaN/Inf: want %s, got %q (%v)", ReasonCustomFieldNotNumber, ErrorReason(err), err)
		}
	}
}

func TestConflictReasonsAreExposed(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	p, err := st.CreateProject(ctx, "conflicts")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateCustomField(ctx, p.ID, "sprint_goal", "Goal", CustomFieldText); err != nil {
		t.Fatal(err)
	}
	_, err = st.CreateCustomField(ctx, p.ID, "sprint_goal", "Goal again", CustomFieldText)
	if !errors.Is(err, ErrConflict) || ErrorReason(err) != ReasonCustomFieldKeyExists {
		t.Fatalf("duplicate custom field key: reason %q err %v", ErrorReason(err), err)
	}
}

func TestWallNoteLimitCountsCharacters(t *testing.T) {
	st, cleanup := newTestStore(t)
	defer cleanup()
	ctx := context.Background()
	p, err := st.CreateProject(ctx, "wall-limits")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.CreateNote(ctx, p.ID, CreateNoteInput{Color: "#FFFFFF", Text: strings.Repeat("漢", maxWallTextChars)}); err != nil {
		t.Fatalf("4000-char note must be accepted: %v", err)
	}
	if _, _, err := st.CreateNote(ctx, p.ID, CreateNoteInput{Color: "#FFFFFF", Text: strings.Repeat("漢", maxWallTextChars+1)}); !errors.Is(err, ErrValidation) {
		t.Fatalf("4001-char note: want ErrValidation, got %v", err)
	}
}

func timePtr(t time.Time) *time.Time { return &t }
