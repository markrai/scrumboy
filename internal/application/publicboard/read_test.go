package publicboard

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"scrumboy/internal/store"
)

type eligibilityFake struct {
	calls int
	slug  string
	id    int64
	err   error
}

type projectionFake struct {
	laneCalls    int
	projectID    int64
	expectedSlug string
	columnKey    string
	query        store.PublicBoardQuery
	lane         store.PublicLaneProjection
	err          error
}

func (f *projectionFake) GetPublicBoardSnapshot(context.Context, int64, string, store.PublicBoardQuery) (store.PublicBoardSnapshotProjection, error) {
	return store.PublicBoardSnapshotProjection{}, f.err
}

func (f *projectionFake) GetPublicBoardLane(_ context.Context, projectID int64, expectedSlug, columnKey string, query store.PublicBoardQuery) (store.PublicLaneProjection, error) {
	f.laneCalls++
	f.projectID = projectID
	f.expectedSlug = expectedSlug
	f.columnKey = columnKey
	f.query = query
	return f.lane, f.err
}

func (f *projectionFake) GetPublicTodoDetail(context.Context, int64, string, int64) (store.PublicTodoProjection, error) {
	return store.PublicTodoProjection{}, f.err
}

func (f *projectionFake) ListPublicTodoLinks(context.Context, int64, string, int64) ([]store.PublicTodoLinkProjection, error) {
	return nil, f.err
}

func (f *projectionFake) ListPublicSprints(context.Context, int64, string) ([]store.PublicSprintProjection, error) {
	return nil, f.err
}

func (f *eligibilityFake) ResolveEligiblePublicProject(_ context.Context, slug string) (int64, error) {
	f.calls++
	f.slug = slug
	return f.id, f.err
}

func TestReadServiceEligibilityGateFailsClosedBeforeStore(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mode    store.Mode
		enabled bool
		slug    string
	}{
		{name: "full flag off", mode: store.ModeFull, slug: "ignite"},
		{name: "anonymous flag off", mode: store.ModeAnonymous, slug: "ignite"},
		{name: "anonymous flag on", mode: store.ModeAnonymous, enabled: true, slug: "ignite"},
		{name: "invalid slug", mode: store.ModeFull, enabled: true, slug: "INVALID"},
		{name: "reserved app slug", mode: store.ModeFull, enabled: true, slug: "dashboard"},
		{name: "reserved locale slug", mode: store.ModeFull, enabled: true, slug: "fr"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &eligibilityFake{id: 7}
			service := NewReadService(ReadServiceOptions{Eligibility: fake, Mode: tc.mode, PublicProjectsEnabled: tc.enabled})
			if _, err := service.Resolve(context.Background(), tc.slug); !errors.Is(err, ErrPublicNotFound) {
				t.Fatalf("Resolve error = %v, want ErrPublicNotFound", err)
			}
			if fake.calls != 0 {
				t.Fatalf("store calls = %d, want 0", fake.calls)
			}
		})
	}
}

func TestReadServiceEligibilityResultAndErrors(t *testing.T) {
	t.Run("eligible", func(t *testing.T) {
		fake := &eligibilityFake{id: 42}
		service := NewReadService(ReadServiceOptions{Eligibility: fake, Mode: store.ModeFull, PublicProjectsEnabled: true})
		prepared, err := service.Resolve(context.Background(), "ignite")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if fake.calls != 1 || fake.slug != "ignite" || prepared.projectID != 42 || prepared.Slug() != "ignite" {
			t.Fatalf("result/call = %+v calls:%d slug:%q", prepared, fake.calls, fake.slug)
		}
	})

	t.Run("missing is generic", func(t *testing.T) {
		fake := &eligibilityFake{err: store.ErrNotFound}
		service := NewReadService(ReadServiceOptions{Eligibility: fake, Mode: store.ModeFull, PublicProjectsEnabled: true})
		if _, err := service.Resolve(context.Background(), "missing"); !errors.Is(err, ErrPublicNotFound) {
			t.Fatalf("Resolve error = %v, want ErrPublicNotFound", err)
		}
	})

	t.Run("internal failure remains internal", func(t *testing.T) {
		internalErr := errors.New("database unavailable")
		fake := &eligibilityFake{err: internalErr}
		service := NewReadService(ReadServiceOptions{Eligibility: fake, Mode: store.ModeFull, PublicProjectsEnabled: true})
		_, err := service.Resolve(context.Background(), "ignite")
		if !errors.Is(err, internalErr) || errors.Is(err, ErrPublicNotFound) {
			t.Fatalf("Resolve error = %v, want wrapped internal error", err)
		}
	})
}

func TestPublicLaneCursorIsVersionedProjectLocalAndBoundToLaneAndFilters(t *testing.T) {
	eligibility := &eligibilityFake{id: 42}
	projection := &projectionFake{lane: store.PublicLaneProjection{
		Items:   []store.PublicTodoProjection{{LocalID: 7, Title: "Visible"}},
		HasMore: true, TotalCount: 2,
		NextOrder: &store.PublicTodoOrder{Rank: 100, LocalID: 7},
	}}
	service := NewReadService(ReadServiceOptions{
		Eligibility: eligibility, Projections: projection,
		Mode: store.ModeFull, PublicProjectsEnabled: true,
	})
	prepared, err := service.Resolve(context.Background(), "ignite")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	first, err := prepared.ReadLane(context.Background(), "backlog", QueryInput{
		Search: "visible", Tags: []string{"feature", "bug", "feature"}, PriorityKey: "high", Limit: 1,
	})
	if err != nil {
		t.Fatalf("ReadLane first: %v", err)
	}
	if first.NextCursor == nil || strings.Contains(*first.NextCursor, "42") {
		t.Fatalf("next cursor = %v; must be opaque and contain no project ID", first.NextCursor)
	}
	if projection.projectID != 42 || projection.expectedSlug != "ignite" {
		t.Fatalf("projection scope = id:%d slug:%q, want id:42 slug:ignite", projection.projectID, projection.expectedSlug)
	}

	projection.lane = store.PublicLaneProjection{Items: []store.PublicTodoProjection{}, TotalCount: 2}
	second, err := prepared.ReadLane(context.Background(), "backlog", QueryInput{
		Search: "visible", Tags: []string{"bug", "feature"}, PriorityKey: "high", Limit: 1, AfterCursor: *first.NextCursor,
	})
	if err != nil {
		t.Fatalf("ReadLane second: %v", err)
	}
	if second.NextCursor != nil || projection.query.After == nil || projection.query.After.Rank != 100 || projection.query.After.LocalID != 7 {
		t.Fatalf("decoded continuation = %+v result=%+v", projection.query.After, second)
	}

	calls := projection.laneCalls
	decodedCursor, err := base64.RawURLEncoding.DecodeString(*first.NextCursor)
	if err != nil {
		t.Fatalf("decode generated cursor: %v", err)
	}
	trailingCursor := base64.RawURLEncoding.EncodeToString(append(decodedCursor, []byte("garbage")...))
	for _, tc := range []struct {
		name   string
		column string
		input  QueryInput
	}{
		{name: "different lane", column: "doing", input: QueryInput{Search: "visible", Tags: []string{"bug", "feature"}, PriorityKey: "high", Limit: 1, AfterCursor: *first.NextCursor}},
		{name: "different filter", column: "backlog", input: QueryInput{Search: "different", Tags: []string{"bug", "feature"}, PriorityKey: "high", Limit: 1, AfterCursor: *first.NextCursor}},
		{name: "malformed", column: "backlog", input: QueryInput{AfterCursor: "%%%"}},
		{name: "trailing data", column: "backlog", input: QueryInput{AfterCursor: trailingCursor}},
		{name: "oversized", column: "backlog", input: QueryInput{AfterCursor: strings.Repeat("a", MaxCursorLength+1)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := prepared.ReadLane(context.Background(), tc.column, tc.input); !errors.Is(err, ErrInvalidPublicQuery) {
				t.Fatalf("ReadLane error = %v, want ErrInvalidPublicQuery", err)
			}
		})
	}
	eligibility.id = 84
	otherPrepared, err := service.Resolve(context.Background(), "other-public")
	if err != nil {
		t.Fatalf("Resolve other project: %v", err)
	}
	if _, err := otherPrepared.ReadLane(context.Background(), "backlog", QueryInput{
		Search: "visible", Tags: []string{"bug", "feature"}, PriorityKey: "high", Limit: 1, AfterCursor: *first.NextCursor,
	}); !errors.Is(err, ErrInvalidPublicQuery) {
		t.Fatalf("cross-project cursor error = %v, want ErrInvalidPublicQuery", err)
	}
	if projection.laneCalls != calls {
		t.Fatalf("invalid cursors reached store: calls %d -> %d", calls, projection.laneCalls)
	}
}

func TestPublicQueryValidationBounds(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input QueryInput
	}{
		{name: "search", input: QueryInput{Search: strings.Repeat("é", MaxSearchRunes+1)}},
		{name: "tags", input: QueryInput{Tags: []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"}}},
		{name: "invalid tag", input: QueryInput{Tags: []string{"x') OR 1=1 --"}}},
		{name: "sprint", input: QueryInput{SprintNumber: int64Pointer(0)}},
		{name: "priority", input: QueryInput{PriorityKey: "high' OR 1=1 --"}},
		{name: "negative limit", input: QueryInput{Limit: -1}},
		{name: "large limit", input: QueryInput{Limit: MaxPageSize + 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := normalizeQuery(tc.input); !errors.Is(err, ErrInvalidPublicQuery) {
				t.Fatalf("normalizeQuery error = %v, want ErrInvalidPublicQuery", err)
			}
		})
	}
	query, err := normalizeQuery(QueryInput{})
	if err != nil || query.storeQuery.Limit != DefaultPageSize {
		t.Fatalf("default query = %+v, %v", query, err)
	}
}

func int64Pointer(value int64) *int64 { return &value }
