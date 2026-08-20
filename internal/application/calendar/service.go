package calendar

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"scrumboy/internal/application/refresh"
	"scrumboy/internal/store"
)

const refreshReasonProjectSettingsUpdated = "project_settings_updated"

var (
	ErrActorRequired      = errors.New("calendar mutation actor required")
	ErrMaintainerRequired = errors.New("calendar mutation maintainer required")
	ErrDurableRequired    = errors.New("calendar requires a durable project")
)

type SourceView struct {
	ID            int64
	Name          string
	Type          string
	Enabled       bool
	URLConfigured bool
	URLPreview    string
}

type AgendaSettingsView struct {
	Enabled  bool
	Timezone string
	Title    string
	Color    string
	Sources  []SourceView
}

type CreateSourceCommand struct {
	Name    string
	Type    string
	URL     string
	Enabled *bool
}

type UpdateSourceCommand struct {
	SourceID int64
	Name     *string
	Enabled  *bool
	URL      *string
}

type PatchSettingsCommand struct {
	Enabled  *bool
	Timezone *string
	Title    *string
	Color    *string
}

type ProjectLookup interface {
	GetProject(ctx context.Context, projectID int64) (store.Project, error)
}

type RoleStore interface {
	GetProjectRole(ctx context.Context, projectID int64, userID int64) (store.ProjectRole, error)
}

type SecretCipher interface {
	EncryptSecret(plaintext []byte) (string, error)
	DecryptSecret(encrypted string) ([]byte, error)
}

type SourceStore interface {
	GetProjectAgendaSettings(ctx context.Context, projectID int64) (store.ProjectAgendaSettings, error)
	UpdateProjectAgendaSettings(ctx context.Context, projectID int64, enabled *bool, timezone *string, title *string, color *string) (store.ProjectAgendaSettings, error)
	ListCalendarSources(ctx context.Context, projectID int64) ([]store.CalendarSource, error)
	CountCalendarSources(ctx context.Context, projectID int64) (int, error)
	GetCalendarSource(ctx context.Context, projectID, sourceID int64) (store.CalendarSource, error)
	CreateCalendarSource(ctx context.Context, projectID int64, in store.CreateCalendarSourceInput) (store.CalendarSource, error)
	UpdateCalendarSource(ctx context.Context, projectID, sourceID int64, in store.UpdateCalendarSourceInput) (store.CalendarSource, error)
	UpdateCalendarSourceHostKindIfURLHashCurrent(ctx context.Context, sourceID int64, expectedURLHash, hostKind string) (changed bool, err error)
	DeleteCalendarSource(ctx context.Context, projectID, sourceID int64) error
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
	Projects      ProjectLookup
	Roles         RoleStore
	Cipher        SecretCipher
	Sources       SourceStore
	Refresh       BoardRefreshPublisher
	AllowLoopback bool
}

type RESTService struct {
	projects      ProjectLookup
	roles         RoleStore
	cipher        SecretCipher
	sources       SourceStore
	refresh       BoardRefreshPublisher
	allowLoopback bool
}

func NewRESTService(deps RESTServiceDependencies) *RESTService {
	refresh := deps.Refresh
	if refresh == nil {
		refresh = nopBoardRefreshPublisher{}
	}
	return &RESTService{
		projects:      deps.Projects,
		roles:         deps.Roles,
		cipher:        deps.Cipher,
		sources:       deps.Sources,
		refresh:       refresh,
		allowLoopback: deps.AllowLoopback,
	}
}

type ResolvedRESTTarget struct {
	ProjectID int64
}

type PreparedREST struct {
	ctx       context.Context
	service   *RESTService
	projectID int64
}

func (s *RESTService) Prepare(ctx context.Context, target ResolvedRESTTarget) (*PreparedREST, error) {
	userID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return nil, ErrActorRequired
	}
	project, err := s.projects.GetProject(ctx, target.ProjectID)
	if err != nil {
		return nil, err
	}
	if project.ExpiresAt != nil {
		return nil, ErrDurableRequired
	}
	role, err := s.roles.GetProjectRole(ctx, target.ProjectID, userID)
	if err != nil || !role.HasMinimumRole(store.RoleMaintainer) {
		return nil, ErrMaintainerRequired
	}
	return &PreparedREST{ctx: ctx, service: s, projectID: target.ProjectID}, nil
}

func (p *PreparedREST) PatchSettings(command PatchSettingsCommand) (AgendaSettingsView, error) {
	if command.Timezone != nil {
		tz, err := validateAgendaTimezone(*command.Timezone)
		if err != nil {
			return AgendaSettingsView{}, fmt.Errorf("%w: %s", store.ErrValidation, err.Error())
		}
		command.Timezone = &tz
	}
	if _, err := p.service.sources.UpdateProjectAgendaSettings(p.ctx, p.projectID, command.Enabled, command.Timezone, command.Title, command.Color); err != nil {
		return AgendaSettingsView{}, err
	}
	p.service.refresh.PublishBoardRefresh(p.ctx, p.projectID, refreshReasonProjectSettingsUpdated, refresh.Entity{})
	return p.List()
}

func (p *PreparedREST) List() (AgendaSettingsView, error) {
	settings, err := p.service.sources.GetProjectAgendaSettings(p.ctx, p.projectID)
	if err != nil {
		return AgendaSettingsView{}, err
	}
	rows, err := p.service.sources.ListCalendarSources(p.ctx, p.projectID)
	if err != nil {
		return AgendaSettingsView{}, err
	}
	views := make([]SourceView, 0, len(rows))
	for _, row := range rows {
		view, err := p.service.toSourceView(row)
		if err != nil {
			return AgendaSettingsView{}, err
		}
		views = append(views, view)
	}
	return AgendaSettingsView{
		Enabled:  settings.Enabled,
		Timezone: settings.Timezone,
		Title:    settings.Title,
		Color:    settings.Color,
		Sources:  views,
	}, nil
}

func (p *PreparedREST) Create(command CreateSourceCommand) (SourceView, error) {
	name := strings.TrimSpace(command.Name)
	if name == "" {
		return SourceView{}, fmt.Errorf("%w: invalid calendar source name", store.ErrValidation)
	}
	srcType := strings.TrimSpace(command.Type)
	if srcType == "" {
		srcType = SourceTypeICSFeed
	}
	if srcType != SourceTypeICSFeed {
		return SourceView{}, fmt.Errorf("%w: invalid calendar source type", store.ErrValidation)
	}
	canonical, err := canonicalCalendarURL(command.URL, p.service.allowLoopback)
	if err != nil {
		return SourceView{}, fmt.Errorf("%w: invalid calendar URL", store.ErrValidation)
	}
	count, err := p.service.sources.CountCalendarSources(p.ctx, p.projectID)
	if err != nil {
		return SourceView{}, err
	}
	if count >= store.MaxCalendarSources {
		return SourceView{}, fmt.Errorf("%w: calendar source limit reached", store.ErrValidation)
	}
	secretEnc, err := p.service.cipher.EncryptSecret([]byte(canonical))
	if err != nil {
		return SourceView{}, err
	}
	enabled := true
	if command.Enabled != nil {
		enabled = *command.Enabled
	}
	created, err := p.service.sources.CreateCalendarSource(p.ctx, p.projectID, store.CreateCalendarSourceInput{
		Type:      srcType,
		Name:      name,
		Enabled:   enabled,
		SecretEnc: secretEnc,
		URLHash:   hashCalendarURL(canonical),
		HostKind:  string(calendarHostKind(canonical)),
	})
	if err != nil {
		return SourceView{}, err
	}
	p.service.refresh.PublishBoardRefresh(p.ctx, p.projectID, refreshReasonProjectSettingsUpdated, refresh.Entity{})
	return p.service.toSourceView(created)
}

func (p *PreparedREST) Update(command UpdateSourceCommand) (SourceView, error) {
	in := store.UpdateCalendarSourceInput{
		Name:    command.Name,
		Enabled: command.Enabled,
	}
	if command.URL != nil {
		canonical, err := canonicalCalendarURL(*command.URL, p.service.allowLoopback)
		if err != nil {
			return SourceView{}, fmt.Errorf("%w: invalid calendar URL", store.ErrValidation)
		}
		secretEnc, err := p.service.cipher.EncryptSecret([]byte(canonical))
		if err != nil {
			return SourceView{}, err
		}
		hash := hashCalendarURL(canonical)
		kind := string(calendarHostKind(canonical))
		in.SecretEnc = &secretEnc
		in.URLHash = &hash
		in.HostKind = &kind
	}
	updated, err := p.service.sources.UpdateCalendarSource(p.ctx, p.projectID, command.SourceID, in)
	if err != nil {
		return SourceView{}, err
	}
	p.service.refresh.PublishBoardRefresh(p.ctx, p.projectID, refreshReasonProjectSettingsUpdated, refresh.Entity{})
	return p.service.toSourceView(updated)
}

func (p *PreparedREST) Delete(sourceID int64) error {
	if err := p.service.sources.DeleteCalendarSource(p.ctx, p.projectID, sourceID); err != nil {
		return err
	}
	p.service.refresh.PublishBoardRefresh(p.ctx, p.projectID, refreshReasonProjectSettingsUpdated, refresh.Entity{})
	return nil
}

func (s *RESTService) toSourceView(src store.CalendarSource) (SourceView, error) {
	view := SourceView{
		ID:            src.ID,
		Name:          src.Name,
		Type:          src.Type,
		Enabled:       src.Enabled,
		URLConfigured: strings.TrimSpace(src.SecretEnc) != "",
		URLPreview:    "…",
	}
	if !view.URLConfigured {
		return view, nil
	}
	plain, err := s.cipher.DecryptSecret(src.SecretEnc)
	if err != nil {
		return SourceView{}, err
	}
	view.URLPreview = urlPreview(string(plain))
	return view, nil
}
