package publicboard

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	"scrumboy/internal/store"
)

var (
	ErrPublicNotFound     = errors.New("public board not found")
	ErrInvalidPublicQuery = errors.New("invalid public board query")
)

const (
	DefaultPageSize = 20
	MaxPageSize     = 50
	MaxSearchRunes  = 200
	MaxTagFilters   = 10
	MaxCursorLength = 512
)

// EligibilityStore is the only persistence capability required to establish
// public access. It deliberately returns an internal key rather than Project.
type EligibilityStore interface {
	ResolveEligiblePublicProject(ctx context.Context, slug string) (int64, error)
}

type ProjectionStore interface {
	GetPublicBoardSnapshot(ctx context.Context, projectID int64, expectedSlug string, query store.PublicBoardQuery) (store.PublicBoardSnapshotProjection, error)
	GetPublicBoardLane(ctx context.Context, projectID int64, expectedSlug, columnKey string, query store.PublicBoardQuery) (store.PublicLaneProjection, error)
	GetPublicTodoDetail(ctx context.Context, projectID int64, expectedSlug string, localID int64) (store.PublicTodoProjection, error)
	ListPublicTodoLinks(ctx context.Context, projectID int64, expectedSlug string, localID int64) ([]store.PublicTodoLinkProjection, error)
	ListPublicSprints(ctx context.Context, projectID int64, expectedSlug string) ([]store.PublicSprintProjection, error)
}

type ReadServiceOptions struct {
	Eligibility           EligibilityStore
	Projections           ProjectionStore
	Mode                  store.Mode
	PublicProjectsEnabled bool
}

type ReadService struct {
	eligibility           EligibilityStore
	projections           ProjectionStore
	mode                  store.Mode
	publicProjectsEnabled bool
}

func NewReadService(opts ReadServiceOptions) *ReadService {
	return &ReadService{
		eligibility:           opts.Eligibility,
		projections:           opts.Projections,
		mode:                  opts.Mode,
		publicProjectsEnabled: opts.PublicProjectsEnabled,
	}
}

// PreparedRead binds one eligible public project without creating a user,
// membership, role, or ProjectContext. Later reads recheck the persisted
// eligibility predicate in their dedicated projection query.
type PreparedRead struct {
	service   *ReadService
	projectID int64
	slug      string
}

func (p PreparedRead) Slug() string { return p.slug }

func (s *ReadService) Resolve(ctx context.Context, rawSlug string) (PreparedRead, error) {
	if s.mode != store.ModeFull || !s.publicProjectsEnabled {
		return PreparedRead{}, ErrPublicNotFound
	}
	slug := strings.TrimSpace(rawSlug)
	if !store.IsValidProjectSlug(slug) || store.IsReservedProjectSlug(slug) {
		return PreparedRead{}, ErrPublicNotFound
	}
	projectID, err := s.eligibility.ResolveEligiblePublicProject(ctx, slug)
	if errors.Is(err, store.ErrNotFound) {
		return PreparedRead{}, ErrPublicNotFound
	}
	if err != nil {
		return PreparedRead{}, fmt.Errorf("resolve public board: %w", err)
	}
	return PreparedRead{service: s, projectID: projectID, slug: slug}, nil
}

type QueryInput struct {
	Search       string
	Tags         []string
	SprintNumber *int64
	PriorityKey  string
	Limit        int
	AfterCursor  string
}

type LaneMeta struct {
	HasMore    bool
	NextCursor *string
	TotalCount int
}

type SnapshotResult struct {
	Project     store.PublicProjectProjection
	Workflow    []store.PublicWorkflowProjection
	Priorities  []store.PublicPriorityProjection
	Tags        []store.PublicTagProjection
	Columns     map[string][]store.PublicTodoProjection
	ColumnsMeta map[string]LaneMeta
}

type LaneResult struct {
	Items      []store.PublicTodoProjection
	HasMore    bool
	NextCursor *string
	TotalCount int
}

type normalizedQuery struct {
	storeQuery  store.PublicBoardQuery
	fingerprint string
}

type cursorPayload struct {
	Version     int    `json:"v"`
	ColumnKey   string `json:"c"`
	Rank        int64  `json:"r"`
	LocalID     int64  `json:"l"`
	Fingerprint string `json:"f"`
}

func invalidQuery(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidPublicQuery, message)
}

func normalizeQuery(input QueryInput) (normalizedQuery, error) {
	search := strings.TrimSpace(input.Search)
	if !utf8.ValidString(search) || utf8.RuneCountInString(search) > MaxSearchRunes {
		return normalizedQuery{}, invalidQuery("search is too long")
	}
	if len(input.Tags) > MaxTagFilters {
		return normalizedQuery{}, invalidQuery("too many tag filters")
	}
	seenTags := make(map[string]struct{}, len(input.Tags))
	tags := make([]string, 0, len(input.Tags))
	for _, raw := range input.Tags {
		tag := store.CanonicalizeTag(raw)
		if tag == "" {
			return normalizedQuery{}, invalidQuery("invalid tag filter")
		}
		if _, exists := seenTags[tag]; exists {
			continue
		}
		seenTags[tag] = struct{}{}
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	if input.SprintNumber != nil && *input.SprintNumber < 1 {
		return normalizedQuery{}, invalidQuery("invalid sprint number")
	}
	priority := strings.ToLower(strings.TrimSpace(input.PriorityKey))
	if priority != "" && !store.IsValidWorkflowColumnKey(priority) {
		return normalizedQuery{}, invalidQuery("invalid priority key")
	}
	limit := input.Limit
	if limit == 0 {
		limit = DefaultPageSize
	}
	if limit < 1 || limit > MaxPageSize {
		return normalizedQuery{}, invalidQuery("invalid page limit")
	}
	query := store.PublicBoardQuery{
		Search: search, Tags: tags, SprintNumber: input.SprintNumber, Limit: limit,
	}
	if priority != "" {
		query.PriorityKey = &priority
	}
	fingerprintInput := struct {
		Search       string   `json:"q"`
		Tags         []string `json:"t"`
		SprintNumber *int64   `json:"s,omitempty"`
		PriorityKey  string   `json:"p,omitempty"`
	}{Search: search, Tags: tags, SprintNumber: input.SprintNumber, PriorityKey: priority}
	encoded, err := json.Marshal(fingerprintInput)
	if err != nil {
		return normalizedQuery{}, fmt.Errorf("fingerprint public query: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return normalizedQuery{
		storeQuery:  query,
		fingerprint: base64.RawURLEncoding.EncodeToString(sum[:12]),
	}, nil
}

func bindQueryFingerprintToProject(query normalizedQuery, slug string) normalizedQuery {
	sum := sha256.Sum256([]byte(slug + "\x00" + query.fingerprint))
	query.fingerprint = base64.RawURLEncoding.EncodeToString(sum[:12])
	return query
}

func encodeCursor(columnKey, fingerprint string, order *store.PublicTodoOrder) (*string, error) {
	if order == nil {
		return nil, nil
	}
	payload, err := json.Marshal(cursorPayload{
		Version: 1, ColumnKey: columnKey, Rank: order.Rank,
		LocalID: order.LocalID, Fingerprint: fingerprint,
	})
	if err != nil {
		return nil, fmt.Errorf("encode public cursor: %w", err)
	}
	cursor := base64.RawURLEncoding.EncodeToString(payload)
	return &cursor, nil
}

func decodeCursor(raw, columnKey, fingerprint string) (*store.PublicTodoOrder, error) {
	if raw == "" {
		return nil, nil
	}
	if len(raw) > MaxCursorLength {
		return nil, invalidQuery("cursor is too long")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(decoded) == 0 || len(decoded) > MaxCursorLength {
		return nil, invalidQuery("invalid cursor")
	}
	var payload cursorPayload
	decoder := json.NewDecoder(strings.NewReader(string(decoded)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return nil, invalidQuery("invalid cursor")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, invalidQuery("invalid cursor")
	}
	if payload.Version != 1 || payload.ColumnKey != columnKey || payload.Fingerprint != fingerprint || payload.Rank < 0 || payload.LocalID < 1 {
		return nil, invalidQuery("cursor does not match this lane and filter")
	}
	return &store.PublicTodoOrder{Rank: payload.Rank, LocalID: payload.LocalID}, nil
}

func mapReadError(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return ErrPublicNotFound
	case errors.Is(err, store.ErrValidation):
		return invalidQuery("invalid project-local filter")
	default:
		return err
	}
}

func (p PreparedRead) ReadSnapshot(ctx context.Context, input QueryInput) (SnapshotResult, error) {
	if input.AfterCursor != "" {
		return SnapshotResult{}, invalidQuery("snapshot cursor is not supported")
	}
	query, err := normalizeQuery(input)
	if err != nil {
		return SnapshotResult{}, err
	}
	query = bindQueryFingerprintToProject(query, p.slug)
	projection, err := p.service.projections.GetPublicBoardSnapshot(ctx, p.projectID, p.slug, query.storeQuery)
	if err != nil {
		return SnapshotResult{}, mapReadError(err)
	}
	meta := make(map[string]LaneMeta, len(projection.ColumnsMeta))
	for columnKey, lane := range projection.ColumnsMeta {
		nextCursor, err := encodeCursor(columnKey, query.fingerprint, lane.NextOrder)
		if err != nil {
			return SnapshotResult{}, err
		}
		meta[columnKey] = LaneMeta{HasMore: lane.HasMore, NextCursor: nextCursor, TotalCount: lane.TotalCount}
	}
	return SnapshotResult{
		Project: projection.Project, Workflow: projection.Workflow, Priorities: projection.Priorities,
		Tags: projection.Tags, Columns: projection.Columns, ColumnsMeta: meta,
	}, nil
}

func (p PreparedRead) ReadLane(ctx context.Context, columnKey string, input QueryInput) (LaneResult, error) {
	columnKey = strings.TrimSpace(strings.ToLower(columnKey))
	if !store.IsValidWorkflowColumnKey(columnKey) {
		return LaneResult{}, invalidQuery("invalid lane key")
	}
	query, err := normalizeQuery(input)
	if err != nil {
		return LaneResult{}, err
	}
	query = bindQueryFingerprintToProject(query, p.slug)
	query.storeQuery.After, err = decodeCursor(input.AfterCursor, columnKey, query.fingerprint)
	if err != nil {
		return LaneResult{}, err
	}
	projection, err := p.service.projections.GetPublicBoardLane(ctx, p.projectID, p.slug, columnKey, query.storeQuery)
	if err != nil {
		return LaneResult{}, mapReadError(err)
	}
	nextCursor, err := encodeCursor(columnKey, query.fingerprint, projection.NextOrder)
	if err != nil {
		return LaneResult{}, err
	}
	return LaneResult{Items: projection.Items, HasMore: projection.HasMore, NextCursor: nextCursor, TotalCount: projection.TotalCount}, nil
}

func (p PreparedRead) ReadTodo(ctx context.Context, localID int64) (store.PublicTodoProjection, error) {
	if localID < 1 {
		return store.PublicTodoProjection{}, ErrPublicNotFound
	}
	result, err := p.service.projections.GetPublicTodoDetail(ctx, p.projectID, p.slug, localID)
	return result, mapReadError(err)
}

func (p PreparedRead) ReadLinks(ctx context.Context, localID int64) ([]store.PublicTodoLinkProjection, error) {
	if localID < 1 {
		return nil, ErrPublicNotFound
	}
	result, err := p.service.projections.ListPublicTodoLinks(ctx, p.projectID, p.slug, localID)
	return result, mapReadError(err)
}

func (p PreparedRead) ReadSprints(ctx context.Context) ([]store.PublicSprintProjection, error) {
	result, err := p.service.projections.ListPublicSprints(ctx, p.projectID, p.slug)
	return result, mapReadError(err)
}
