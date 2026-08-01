package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	boardapp "scrumboy/internal/application/board"
	todoapp "scrumboy/internal/application/todo"
	"scrumboy/internal/publicorigin"
	"scrumboy/internal/store"
)

type storeAPI interface {
	CountUsers(ctx context.Context) (int, error)
	GetUserBySessionToken(ctx context.Context, token string) (store.User, error)
	GetUserByAPIToken(ctx context.Context, rawToken string) (store.User, error)
	GetUserByOAuthAccessToken(ctx context.Context, rawToken, expectedResource string) (store.User, error)
	ListProjects(ctx context.Context) ([]store.ProjectListEntry, error)
	todoapp.MCPMoveAccessStore
	CreateTodo(ctx context.Context, projectID int64, in store.CreateTodoInput, mode store.Mode) (store.Todo, error)
	todoapp.MCPMoveLookupStore
	SearchTodosForLinkPicker(ctx context.Context, projectID int64, q string, limit int, excludeLocalIDs []int64, mode store.Mode) ([]store.TodoLinkTarget, error)
	AddLink(ctx context.Context, projectID, fromLocalID, toLocalID int64, linkType string, mode store.Mode) error
	RemoveLink(ctx context.Context, projectID, fromLocalID, toLocalID int64, mode store.Mode) error
	ListLinksForTodo(ctx context.Context, projectID, localID int64, mode store.Mode) ([]store.TodoLinkTarget, error)
	ListBacklinksForTodo(ctx context.Context, projectID, localID int64, mode store.Mode) ([]store.TodoLinkTarget, error)
	todoapp.UpdateStore
	DeleteTodoByLocalID(ctx context.Context, projectID, localID int64, mode store.Mode) error
	todoapp.MoveStore
	todoapp.MCPMoveLaneStore
	ListSprintsWithTodoCount(ctx context.Context, projectID int64) ([]store.SprintWithTodoCount, error)
	CountUnscheduledTodos(ctx context.Context, projectID int64) (int64, error)
	GetSprintByID(ctx context.Context, sprintID int64) (store.Sprint, error)
	GetActiveSprintByProjectID(ctx context.Context, projectID int64) (*store.Sprint, error)
	CreateSprint(ctx context.Context, projectID int64, name string, plannedStartAt, plannedEndAt time.Time) (store.Sprint, error)
	GetProjectRole(ctx context.Context, projectID int64, userID int64) (store.ProjectRole, error)
	ActivateSprint(ctx context.Context, projectID, sprintID int64) error
	CloseSprint(ctx context.Context, sprintID int64) error
	UpdateSprint(ctx context.Context, sprintID int64, in store.UpdateSprintInput) error
	DeleteSprint(ctx context.Context, projectID, sprintID int64) error
	ListTagCounts(ctx context.Context, pc *store.ProjectContext) ([]store.TagCount, error)
	ListUserTags(ctx context.Context, userID int64) ([]store.TagWithColor, error)
	UpdateTagColor(ctx context.Context, viewerUserID *int64, tagID int64, color *string) error
	UpdateTagColorForDurableProjectByID(ctx context.Context, projectID int64, viewerUserID int64, tagID int64, color *string) error
	UpdateTagColorForTemporaryBoard(ctx context.Context, projectID int64, viewerUserID *int64, tagID int64, color *string) error
	SetViewerTagColorByName(ctx context.Context, projectID int64, viewerUserID int64, name string, color *string) error
	DeleteTag(ctx context.Context, userID int64, tagID int64, isAnonymousBoard bool) error
	GetProjectScopedTagByID(ctx context.Context, projectID, tagID int64) (store.TagWithColor, error)
	ListProjectMembers(ctx context.Context, projectID int64, userID int64) ([]store.ProjectMember, error)
	ListAvailableUsersForProject(ctx context.Context, requesterID, projectID int64) ([]store.User, error)
	AddProjectMember(ctx context.Context, requesterID, projectID, targetUserID int64, role store.ProjectRole) error
	UpdateProjectMemberRole(ctx context.Context, requesterID, projectID, targetUserID int64, role store.ProjectRole) error
	RemoveProjectMember(ctx context.Context, requesterID, projectID, targetUserID int64) error
	GetProjectWorkflow(ctx context.Context, projectID int64) ([]store.WorkflowColumn, error)
	AddWorkflowColumn(ctx context.Context, projectID int64, name string) (store.WorkflowColumn, error)
	UpdateWorkflowColumn(ctx context.Context, projectID int64, key, name, color string) error
	DeleteWorkflowColumn(ctx context.Context, projectID int64, key string) error
	CountTodosForBoardLane(ctx context.Context, projectID int64, columnKey string, tagFilter string, searchFilter string, assigneeFilter store.AssigneeFilter, sprintFilter store.SprintFilter) (int, error)
	UpdateBoardActivity(ctx context.Context, projectID int64) error
	CreateProject(ctx context.Context, name string) (store.Project, error)
	GetProject(ctx context.Context, projectID int64) (store.Project, error)
	UpdateProjectName(ctx context.Context, projectID int64, userID int64, name string) error
	UpdateProjectDefaultSprintWeeks(ctx context.Context, projectID int64, userID int64, weeks int) error
	UpdateProjectPatch(ctx context.Context, projectID int64, userID int64, patch store.UpdateProjectPatch) error
	CheckCanManageProject(ctx context.Context, projectID int64, userID int64) error
	DeleteProject(ctx context.Context, projectID int64, userID int64) (store.DeletedProjectSnapshot, error)
	GetDashboardSummary(ctx context.Context, userID int64, timezone string) (store.DashboardSummary, error)
	ListDashboardTodos(ctx context.Context, userID int64, limit int, cursor *string, sort string) ([]store.DashboardTodo, *string, error)
	GetRealBurndown(ctx context.Context, projectID int64, mode store.Mode) ([]store.RealBurndownPoint, error)
	GetRealBurndownForSprint(ctx context.Context, projectID, sprintID int64, mode store.Mode) ([]store.RealBurndownPoint, error)
	GetBacklogSize(ctx context.Context, projectID int64, mode store.Mode) ([]store.BurndownPoint, error)
	ListUsers(ctx context.Context, requesterID int64) ([]store.User, error)
	GetUser(ctx context.Context, userID int64) (store.User, error)
	UpdateUserRole(ctx context.Context, requesterID, targetUserID int64, newRole store.SystemRole) error
	DeleteUser(ctx context.Context, requesterID, targetUserID int64) error
}

type Options struct {
	Mode         string
	PublicOrigin *publicorigin.Resolver
	Logger       *log.Logger
}

type Adapter struct {
	store        storeAPI
	boardReads   *boardapp.MCPBoardReadService
	todoMoves    *todoapp.MCPMoveService
	todoUpdates  *todoapp.MCPUpdateService
	mode         string
	tools        toolRegistry
	publicOrigin *publicorigin.Resolver
	logger       *log.Logger
}

func New(st storeAPI, opts Options) *Adapter {
	mode := opts.Mode
	if mode != "full" && mode != "anonymous" {
		mode = "full"
	}
	resolver := opts.PublicOrigin
	if resolver == nil {
		resolver = publicorigin.New("", false)
	}
	logger := opts.Logger
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}

	a := &Adapter{
		store: st,
		boardReads: boardapp.NewMCPBoardReadService(boardapp.MCPBoardReadServiceDependencies{
			Access:   st,
			Sprints:  st,
			Workflow: st,
			Lanes:    st,
			Activity: st,
			ReportActivityRefreshFailure: func(_ context.Context, projectID int64, err error) {
				logger.Printf("mcp: board activity refresh failed project_id=%d: %v", projectID, err)
			},
		}),
		todoMoves: todoapp.NewMCPMoveService(todoapp.MCPMoveServiceDependencies{
			Access: st,
			Lookup: st,
			Lanes:  st,
			Move:   st,
		}),
		todoUpdates: todoapp.NewMCPUpdateService(todoapp.MCPUpdateServiceDependencies{
			Access: st,
			Lookup: st,
			Update: st,
		}),
		mode:         mode,
		tools:        make(toolRegistry),
		publicOrigin: resolver,
		logger:       logger,
	}
	a.registerTools()
	return a
}

func (a *Adapter) logAdapterError(transport, tool string, err *adapterError) {
	if err == nil || err.Code != CodeInternal {
		return
	}
	if err.Cause != nil {
		a.logger.Printf("mcp: internal error transport=%s tool=%s: %v", transport, tool, err.Cause)
		return
	}
	a.logger.Printf("mcp: internal error transport=%s tool=%s: %s", transport, tool, err.Message)
}

// parseBearerAuthorization parses Authorization: Bearer. The credential is the segment after the first
// ASCII space following "Bearer"; it is trimmed with strings.TrimSpace only (not the full header value).
// If the scheme is Bearer (case-insensitive), ok is true and credential is "" when missing/blank after trim.
// RFC 9110 expects at least one space between the scheme and token; a single run-on "Authorization:Bearer x"
// is therefore not treated as Bearer here (optional future leniency could accept it).
func parseBearerAuthorization(headerValue string) (ok bool, credential string) {
	v := strings.TrimSpace(headerValue)
	if v == "" {
		return false, ""
	}
	i := strings.IndexByte(v, ' ')
	if i < 0 {
		if strings.EqualFold(v, "Bearer") {
			return true, ""
		}
		return false, ""
	}
	if !strings.EqualFold(v[:i], "Bearer") {
		return false, ""
	}
	return true, strings.TrimSpace(v[i+1:])
}

// requestAuthResult is the outcome of MCP credential-to-actor resolution.
type requestAuthResult struct {
	Ctx              context.Context
	Authenticated    bool
	BearerAuthFailed bool
	Err              error // non-nil store failure (caller should map to 500)
}

// resolveRequestAuth is the MCP credential-to-actor boundary.
// It establishes actor identity from bearer token or session cookie when
// present, but it does not authorize access to any specific resource. MCP
// tools may gate capability/UX early; store methods remain the authority for
// resource authorization.
func (a *Adapter) resolveRequestAuth(r *http.Request, allowOAuth bool) requestAuthResult {
	ctx := r.Context()

	// Anonymous mode intentionally establishes no actor, matching the HTTP API
	// boundary for anonymous deployments.
	if a.mode == "anonymous" {
		return requestAuthResult{Ctx: ctx}
	}

	isBearer, cred := parseBearerAuthorization(r.Header.Get("Authorization"))
	if isBearer {
		if cred == "" {
			return requestAuthResult{Ctx: ctx, BearerAuthFailed: true}
		}
		u, err := a.store.GetUserByAPIToken(ctx, cred)
		if allowOAuth && errors.Is(err, store.ErrNotFound) {
			// OAuth-issued access tokens (RFC 6749) live in a separate table
			// from manually-created API tokens; fall back to that lookup
			// before giving up. This never falls back to the session cookie.
			resource, resourceErr := a.publicOrigin.MCPResource(r)
			if resourceErr != nil {
				return requestAuthResult{Ctx: ctx, Err: resourceErr}
			}
			u, err = a.store.GetUserByOAuthAccessToken(ctx, cred, resource)
		}
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return requestAuthResult{Ctx: ctx, BearerAuthFailed: true}
			}
			return requestAuthResult{Ctx: ctx, Err: err}
		}
		ctx = store.WithUserID(ctx, u.ID)
		ctx = store.WithUserEmail(ctx, u.Email)
		ctx = store.WithUserName(ctx, u.Name)
		return requestAuthResult{Ctx: ctx, Authenticated: true}
	}

	c, err := r.Cookie("scrumboy_session")
	if err != nil || c == nil || c.Value == "" {
		return requestAuthResult{Ctx: ctx}
	}

	u, err := a.store.GetUserBySessionToken(ctx, c.Value)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return requestAuthResult{Ctx: ctx}
		}
		return requestAuthResult{Ctx: ctx, Err: err}
	}

	ctx = store.WithUserID(ctx, u.ID)
	ctx = store.WithUserEmail(ctx, u.Email)
	ctx = store.WithUserName(ctx, u.Name)
	return requestAuthResult{Ctx: ctx, Authenticated: true}
}

// authState reports whether authenticated MCP tools are usable for this
// request. It is a transport/tool capability gate layered on top of
// resolveRequestAuth, not the source of truth for store-level authorization.
func (a *Adapter) authState(ctx context.Context) (authCapabilities, bool, *adapterError) {
	if a.mode == "anonymous" {
		reason := "server mode anonymous disables authenticated MCP tools"
		return authCapabilities{
			Mode:                     "disabled",
			Authenticated:            false,
			AuthenticatedToolsUsable: false,
			Reason:                   &reason,
		}, false, nil
	}

	n, err := a.store.CountUsers(ctx)
	if err != nil {
		return authCapabilities{}, false, newAdapterError(http.StatusInternalServerError, CodeInternal, "internal error", map[string]any{"detail": err.Error()})
	}

	_, authenticated := store.UserIDFromContext(ctx)
	bootstrapAvailable := n == 0
	authUsable := n > 0

	var reason *string
	if bootstrapAvailable {
		msg := "bootstrap required before authenticated MCP tools are available"
		reason = &msg
	}

	return authCapabilities{
		Mode:                     "sessionCookie",
		Authenticated:            authenticated,
		AuthenticatedToolsUsable: authUsable,
		Reason:                   reason,
		AuthMethods:              []string{"sessionCookie", "bearer"},
	}, bootstrapAvailable, nil
}

func (a *Adapter) implementedTools() []string {
	return []string{
		"system_getCapabilities",
		"projects_list",
		"projects_create",
		"projects_update",
		"projects_delete",
		"todos_create",
		"todos_get",
		"todos_search",
		"todos_update",
		"todos_delete",
		"todos_move",
		"todos_linksList",
		"todos_linkAdd",
		"todos_linkRemove",
		"sprints_list",
		"sprints_get",
		"sprints_getActive",
		"sprints_create",
		"sprints_activate",
		"sprints_close",
		"sprints_update",
		"sprints_delete",
		"tags_listProject",
		"tags_listMine",
		"tags_updateMineColor",
		"tags_deleteMine",
		"tags_updateProjectColor",
		"tags_deleteProject",
		"members_list",
		"members_listAvailable",
		"members_add",
		"members_updateRole",
		"members_remove",
		"board_get",
		"workflow_list",
		"workflow_create",
		"workflow_update",
		"workflow_delete",
		"dashboard_getSummary",
		"dashboard_listTodos",
		"metrics_getBurndown",
		"metrics_getBacklogSize",
		"admin_listUsers",
		"admin_updateUserRole",
		"admin_deleteUser",
	}
}

func (a *Adapter) plannedTools() []string {
	return nil
}

func (a *Adapter) storeMode() store.Mode {
	mode, _ := store.ParseMode(a.mode)
	if mode == "" {
		return store.ModeFull
	}
	return mode
}

func decodeInput(input any, dst any) error {
	b, err := json.Marshal(input)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	return nil
}

func normalizeColumnKey(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "":
		return ""
	case "backlog":
		return store.DefaultColumnBacklog
	case "not_started", "not-started":
		return store.DefaultColumnNotStarted
	case "doing", "in_progress", "in-progress":
		return store.DefaultColumnDoing
	case "testing":
		return store.DefaultColumnTesting
	case "done":
		return store.DefaultColumnDone
	default:
		return strings.ToLower(strings.TrimSpace(v))
	}
}
