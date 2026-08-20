package projectsettings

import (
	"context"
	"errors"

	"scrumboy/internal/application/refresh"
	"scrumboy/internal/store"
)

const refreshReasonProjectSettingsUpdated = "project_settings_updated"

var (
	ErrActorRequired   = errors.New("project settings mutation actor required")
	ErrDurableRequired = errors.New("agenda settings require a durable project")
)

type PatchCommand struct {
	DefaultSprintWeeks *int
	SprintsEnabled     *bool
	AgendaEnabled      *bool
	AgendaTimezone     *string
	AgendaTitle        *string
	AgendaColor        *string
}

type PatchResult struct {
	DefaultSprintWeeks int
	SprintsEnabled     bool
	AgendaEnabled      bool
	AgendaTimezone     string
	AgendaTitle        string
	AgendaColor        string
}

type MutationStore interface {
	CheckCanManageProject(ctx context.Context, projectID, userID int64) error
	GetProject(ctx context.Context, projectID int64) (store.Project, error)
	UpdateProjectBoardSettings(ctx context.Context, projectID, userID int64, patch store.ProjectBoardSettingsPatch) (store.ProjectBoardSettings, error)
}

type BoardRefreshPublisher interface {
	PublishBoardRefresh(ctx context.Context, projectID int64, reason string, entity refresh.Entity)
}

type BoardRefreshPublisherFunc func(ctx context.Context, projectID int64, reason string, entity refresh.Entity)

func (f BoardRefreshPublisherFunc) PublishBoardRefresh(ctx context.Context, projectID int64, reason string, entity refresh.Entity) {
	if f != nil {
		f(ctx, projectID, reason, entity)
	}
}

type nopBoardRefreshPublisher struct{}

func (nopBoardRefreshPublisher) PublishBoardRefresh(context.Context, int64, string, refresh.Entity) {}

type RESTServiceDependencies struct {
	Mutations MutationStore
	Refresh   BoardRefreshPublisher
}

type RESTService struct {
	mutations MutationStore
	refresh   BoardRefreshPublisher
}

func NewRESTService(deps RESTServiceDependencies) *RESTService {
	refresh := deps.Refresh
	if refresh == nil {
		refresh = nopBoardRefreshPublisher{}
	}
	return &RESTService{mutations: deps.Mutations, refresh: refresh}
}

type ResolvedRESTTarget struct {
	ProjectID int64
}

type PreparedREST struct {
	ctx       context.Context
	service   *RESTService
	projectID int64
	userID    int64
}

func (s *RESTService) Prepare(ctx context.Context, target ResolvedRESTTarget) (*PreparedREST, error) {
	userID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return nil, ErrActorRequired
	}
	if err := s.mutations.CheckCanManageProject(ctx, target.ProjectID, userID); err != nil {
		return nil, err
	}
	return &PreparedREST{ctx: ctx, service: s, projectID: target.ProjectID, userID: userID}, nil
}

func (c PatchCommand) hasAgenda() bool {
	return c.AgendaEnabled != nil || c.AgendaTimezone != nil || c.AgendaTitle != nil || c.AgendaColor != nil
}

func (p *PreparedREST) Patch(command PatchCommand) (PatchResult, error) {
	if command.hasAgenda() {
		project, err := p.service.mutations.GetProject(p.ctx, p.projectID)
		if err != nil {
			return PatchResult{}, err
		}
		if project.ExpiresAt != nil {
			return PatchResult{}, ErrDurableRequired
		}
	}
	updated, err := p.service.mutations.UpdateProjectBoardSettings(p.ctx, p.projectID, p.userID, store.ProjectBoardSettingsPatch{
		DefaultSprintWeeks: command.DefaultSprintWeeks,
		SprintsEnabled:     command.SprintsEnabled,
		AgendaEnabled:      command.AgendaEnabled,
		AgendaTimezone:     command.AgendaTimezone,
		AgendaTitle:        command.AgendaTitle,
		AgendaColor:        command.AgendaColor,
	})
	if err != nil {
		return PatchResult{}, err
	}
	p.service.refresh.PublishBoardRefresh(p.ctx, p.projectID, refreshReasonProjectSettingsUpdated, refresh.Entity{})
	return PatchResult{
		DefaultSprintWeeks: updated.DefaultSprintWeeks,
		SprintsEnabled:     updated.SprintsEnabled,
		AgendaEnabled:      updated.AgendaEnabled,
		AgendaTimezone:     updated.AgendaTimezone,
		AgendaTitle:        updated.AgendaTitle,
		AgendaColor:        updated.AgendaColor,
	}, nil
}
