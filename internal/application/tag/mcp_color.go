package tag

import (
	"context"
	"errors"

	"scrumboy/internal/store"
)

var (
	// ErrMaintainerRequired reports that a durable MCP project-ID color target
	// does not satisfy the characterized role precondition.
	ErrMaintainerRequired = errors.New("tag color maintainer required")
	// ErrColorProjectionMissing reports that a successful project color
	// mutation is absent from the required post-write tag projection.
	ErrColorProjectionMissing = errors.New("updated tag color missing from projection")
)

// MCPProjectAccessStore resolves the project-slug access boundary used by MCP
// project color operations.
type MCPProjectAccessStore interface {
	GetProjectContextBySlug(
		ctx context.Context,
		slug string,
		mode store.Mode,
	) (store.ProjectContext, error)
}

// MCPColorServiceDependencies contains only the access, read, and color
// persistence capabilities required by MCP tag color operations.
type MCPColorServiceDependencies struct {
	Access           MCPProjectAccessStore
	MineRead         MineTagReadStore
	ProjectRead      ProjectTagReadStore
	MineColor        LegacyRowColorStore
	DurableIDColor   DurableProjectIDColorStore
	TemporaryIDColor TemporaryBoardIDColorStore
	DurableNameColor DurableProjectNameColorStore
}

// MCPColorService prepares MCP-specific color operations while preserving the
// mine and project read boundaries characterized at the adapter seam.
type MCPColorService struct {
	access           MCPProjectAccessStore
	mineRead         MineTagReadStore
	projectRead      ProjectTagReadStore
	mineColor        LegacyRowColorStore
	durableIDColor   DurableProjectIDColorStore
	temporaryIDColor TemporaryBoardIDColorStore
	durableNameColor DurableProjectNameColorStore
}

// NewMCPColorService constructs the additive MCP color application service.
func NewMCPColorService(deps MCPColorServiceDependencies) *MCPColorService {
	return &MCPColorService{
		access:           deps.Access,
		mineRead:         deps.MineRead,
		projectRead:      deps.ProjectRead,
		mineColor:        deps.MineColor,
		durableIDColor:   deps.DurableIDColor,
		temporaryIDColor: deps.TemporaryIDColor,
		durableNameColor: deps.DurableNameColor,
	}
}

// MCPMineIDColorTarget contains already-validated transport-neutral input for
// one mine tag color operation.
type MCPMineIDColorTarget struct {
	TagID int64
	Color ColorIntent
}

// PreparedMCPMineIDColor binds the exact context, actor, selected pre-read tag,
// and color intent for one mine color mutation.
type PreparedMCPMineIDColor struct {
	ctx     context.Context
	service *MCPColorService
	actorID int64
	tag     store.TagWithColor
	color   ColorIntent
}

// PrepareMineID extracts the actor, reads the mine library exactly once, and
// selects the requested tag by exact row ID.
func (s *MCPColorService) PrepareMineID(
	ctx context.Context,
	target MCPMineIDColorTarget,
) (*PreparedMCPMineIDColor, error) {
	actorID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return nil, ErrActorRequired
	}

	tags, err := s.mineRead.ListUserTags(ctx, actorID)
	if err != nil {
		return nil, err
	}
	tag, found := findMineTagByID(tags, target.TagID)
	if !found {
		return nil, store.ErrNotFound
	}

	return &PreparedMCPMineIDColor{
		ctx:     ctx,
		service: s,
		actorID: actorID,
		tag:     cloneTagWithColor(tag),
		color:   target.Color,
	}, nil
}

// Update performs the legacy row-oriented mine mutation once and returns the
// characterized synthetic result based on the bound pre-read item.
func (p *PreparedMCPMineIDColor) Update() (store.TagWithColor, error) {
	color := p.color.StoreValue()
	actorID := p.actorID
	err := p.service.mineColor.UpdateTagColor(p.ctx, &actorID, p.tag.TagID, color)
	if err != nil && !(p.color.IsClear() && errors.Is(err, store.ErrNotFound)) {
		return store.TagWithColor{}, err
	}

	result := cloneTagWithColor(p.tag)
	result.Color = cloneString(color)
	return result, nil
}

// MCPProjectNameColorTarget contains already-validated transport-neutral input
// for one project tag color operation addressed by name.
type MCPProjectNameColorTarget struct {
	ProjectSlug string
	Mode        store.Mode
	Name        string
	Color       ColorIntent
}

// PreparedMCPProjectNameColor binds one slug-resolved project context, actor,
// exact name, and color intent.
type PreparedMCPProjectNameColor struct {
	ctx            context.Context
	service        *MCPColorService
	projectContext store.ProjectContext
	actorID        int64
	name           string
	color          ColorIntent
}

// PrepareProjectName resolves project access exactly once and then extracts
// the actor. It deliberately adds no role gate or tag pre-read.
func (s *MCPColorService) PrepareProjectName(
	ctx context.Context,
	target MCPProjectNameColorTarget,
) (*PreparedMCPProjectNameColor, error) {
	projectContext, err := s.access.GetProjectContextBySlug(ctx, target.ProjectSlug, target.Mode)
	if err != nil {
		return nil, err
	}
	actorID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return nil, ErrActorRequired
	}

	return &PreparedMCPProjectNameColor{
		ctx:            ctx,
		service:        s,
		projectContext: cloneProjectContext(projectContext),
		actorID:        actorID,
		name:           target.Name,
		color:          target.Color,
	}, nil
}

// Update mutates the grouped name once, then performs the required project tag
// post-read and selects the result through the store grouping key.
func (p *PreparedMCPProjectNameColor) Update() (store.TagCount, error) {
	if err := p.service.durableNameColor.SetViewerTagColorByName(
		p.ctx,
		p.projectContext.Project.ID,
		p.actorID,
		p.name,
		p.color.StoreValue(),
	); err != nil {
		return store.TagCount{}, err
	}

	tags, err := p.service.projectRead.ListTagCounts(p.ctx, &p.projectContext)
	if err != nil {
		return store.TagCount{}, err
	}
	groupKey := store.TagGroupKey(p.name)
	for _, tag := range tags {
		if tag.Name == groupKey {
			return tag, nil
		}
	}
	return store.TagCount{}, ErrColorProjectionMissing
}

// MCPProjectIDColorTarget contains already-validated transport-neutral input
// for one project tag color operation addressed by row ID.
type MCPProjectIDColorTarget struct {
	ProjectSlug string
	Mode        store.Mode
	TagID       int64
	Color       ColorIntent
}

// PreparedMCPProjectIDColor binds one slug-resolved project context, actor,
// precondition-checked tag ID, and color intent.
type PreparedMCPProjectIDColor struct {
	ctx            context.Context
	service        *MCPColorService
	projectContext store.ProjectContext
	actorID        int64
	tagID          int64
	color          ColorIntent
}

// PrepareProjectID preserves the characterized access, actor, pre-read,
// existence, and durable-only role ordering.
func (s *MCPColorService) PrepareProjectID(
	ctx context.Context,
	target MCPProjectIDColorTarget,
) (*PreparedMCPProjectIDColor, error) {
	projectContext, err := s.access.GetProjectContextBySlug(ctx, target.ProjectSlug, target.Mode)
	if err != nil {
		return nil, err
	}
	projectContext = cloneProjectContext(projectContext)

	actorID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return nil, ErrActorRequired
	}

	tags, err := s.projectRead.ListTagCounts(ctx, &projectContext)
	if err != nil {
		return nil, err
	}
	if _, found := findProjectTagByID(tags, target.TagID); !found {
		return nil, store.ErrNotFound
	}
	if projectContext.Project.ExpiresAt == nil &&
		!projectContext.Role.HasMinimumRole(store.RoleMaintainer) {
		return nil, ErrMaintainerRequired
	}

	return &PreparedMCPProjectIDColor{
		ctx:            ctx,
		service:        s,
		projectContext: projectContext,
		actorID:        actorID,
		tagID:          target.TagID,
		color:          target.Color,
	}, nil
}

// Update dispatches exactly one durable or temporary ID mutation, applies only
// the characterized harmless-clear rule, and then returns the same ID from one
// project tag post-read.
func (p *PreparedMCPProjectIDColor) Update() (store.TagCount, error) {
	color := p.color.StoreValue()
	var err error
	if p.projectContext.Project.ExpiresAt != nil {
		actorID := p.actorID
		err = p.service.temporaryIDColor.UpdateTagColorForTemporaryBoard(
			p.ctx,
			p.projectContext.Project.ID,
			&actorID,
			p.tagID,
			color,
		)
	} else {
		err = p.service.durableIDColor.UpdateTagColorForDurableProjectByID(
			p.ctx,
			p.projectContext.Project.ID,
			p.actorID,
			p.tagID,
			color,
		)
	}
	if err != nil && !(p.color.IsClear() && errors.Is(err, store.ErrNotFound)) {
		return store.TagCount{}, err
	}

	tags, err := p.service.projectRead.ListTagCounts(p.ctx, &p.projectContext)
	if err != nil {
		return store.TagCount{}, err
	}
	if tag, found := findProjectTagByID(tags, p.tagID); found {
		return tag, nil
	}
	return store.TagCount{}, ErrColorProjectionMissing
}

func findMineTagByID(tags []store.TagWithColor, tagID int64) (store.TagWithColor, bool) {
	for _, tag := range tags {
		if tag.TagID == tagID {
			return tag, true
		}
	}
	return store.TagWithColor{}, false
}

func findProjectTagByID(tags []store.TagCount, tagID int64) (store.TagCount, bool) {
	for _, tag := range tags {
		if tag.TagID == tagID {
			return tag, true
		}
	}
	return store.TagCount{}, false
}

func cloneTagWithColor(tag store.TagWithColor) store.TagWithColor {
	tag.Color = cloneString(tag.Color)
	return tag
}

func cloneProjectContext(projectContext store.ProjectContext) store.ProjectContext {
	projectContext.Project.Image = cloneString(projectContext.Project.Image)
	projectContext.Project.OwnerUserID = cloneInt64(projectContext.Project.OwnerUserID)
	projectContext.Project.CreatorUserID = cloneInt64(projectContext.Project.CreatorUserID)
	if projectContext.Project.ExpiresAt != nil {
		expiresAt := *projectContext.Project.ExpiresAt
		projectContext.Project.ExpiresAt = &expiresAt
	}
	return projectContext
}
