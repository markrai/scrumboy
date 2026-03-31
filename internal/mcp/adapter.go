package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"scrumboy/internal/store"
)

type storeAPI interface {
	CountUsers(ctx context.Context) (int, error)
	GetUserBySessionToken(ctx context.Context, token string) (store.User, error)
	ListProjects(ctx context.Context) ([]store.ProjectListEntry, error)
	GetProjectContextBySlug(ctx context.Context, slug string, mode store.Mode) (store.ProjectContext, error)
	CreateTodo(ctx context.Context, projectID int64, in store.CreateTodoInput, mode store.Mode) (store.Todo, error)
	GetTodoByLocalID(ctx context.Context, projectID, localID int64, mode store.Mode) (store.Todo, error)
	SearchTodosForLinkPicker(ctx context.Context, projectID int64, q string, limit int, excludeLocalIDs []int64, mode store.Mode) ([]store.TodoLinkTarget, error)
	UpdateTodoByLocalID(ctx context.Context, projectID, localID int64, in store.UpdateTodoInput, mode store.Mode) (store.Todo, error)
	DeleteTodoByLocalID(ctx context.Context, projectID, localID int64, mode store.Mode) error
	MoveTodoByLocalID(ctx context.Context, projectID, localID int64, toColumnKey string, afterLocalID, beforeLocalID *int64, mode store.Mode) (store.Todo, error)
	ListTodosForBoardLane(ctx context.Context, projectID int64, columnKey string, limit int, afterRank, afterID int64, tagFilter, searchFilter string, sprintFilter store.SprintFilter) ([]store.Todo, string, bool, error)
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
}

type Options struct {
	Mode string
}

type Adapter struct {
	store storeAPI
	mode  string
	tools toolRegistry
}

func New(st storeAPI, opts Options) *Adapter {
	mode := opts.Mode
	if mode != "full" && mode != "anonymous" {
		mode = "full"
	}

	a := &Adapter{
		store: st,
		mode:  mode,
		tools: make(toolRegistry),
	}
	a.registerTools()
	return a
}

func (a *Adapter) requestContext(r *http.Request) context.Context {
	ctx := r.Context()

	// Keep anonymous mode aligned with the existing HTTP API boundary:
	// valid session cookies are ignored entirely.
	if a.mode == "anonymous" {
		return ctx
	}

	c, err := r.Cookie("scrumboy_session")
	if err != nil || c == nil || c.Value == "" {
		return ctx
	}

	u, err := a.store.GetUserBySessionToken(ctx, c.Value)
	if err != nil {
		return ctx
	}

	ctx = store.WithUserID(ctx, u.ID)
	ctx = store.WithUserEmail(ctx, u.Email)
	ctx = store.WithUserName(ctx, u.Name)
	return ctx
}

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
	}, bootstrapAvailable, nil
}

func (a *Adapter) implementedTools() []string {
	return []string{
		"system.getCapabilities",
		"projects.list",
		"todos.create",
		"todos.get",
		"todos.search",
		"todos.update",
		"todos.delete",
		"todos.move",
		"sprints.list",
		"sprints.get",
		"sprints.getActive",
		"sprints.create",
		"sprints.activate",
		"sprints.close",
		"sprints.update",
		"sprints.delete",
		"tags.listProject",
		"tags.listMine",
		"tags.updateMineColor",
	}
}

func (a *Adapter) plannedTools() []string {
	return []string{
		"board.get",
	}
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
