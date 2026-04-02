package mcp

import "time"

type successResponse struct {
	OK   bool           `json:"ok"`
	Data any            `json:"data"`
	Meta map[string]any `json:"meta"`
}

type errorResponse struct {
	OK    bool              `json:"ok"`
	Error errorResponseBody `json:"error"`
}

type errorResponseBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details"`
}

type requestEnvelope struct {
	Tool  string `json:"tool"`
	Input any    `json:"input"`
}

type authCapabilities struct {
	Mode                     string   `json:"mode"`
	Authenticated            bool     `json:"authenticated"`
	AuthenticatedToolsUsable bool     `json:"authenticatedToolsUsable"`
	Reason                   *string  `json:"reason,omitempty"`
	AuthMethods              []string `json:"authMethods,omitempty"`
}

type identityCapabilities struct {
	Project       string   `json:"project"`
	Todo          []string `json:"todo"`
	ProjectMember []string `json:"projectMember,omitempty"`
	AvailableUser []string `json:"availableUser,omitempty"`
}

type paginationCapabilities struct {
	DefaultInput       []string `json:"defaultInput"`
	DefaultOutput      []string `json:"defaultOutput"`
	FutureSpecialCases []string `json:"futureSpecialCases,omitempty"`
}

type capabilitiesData struct {
	ServerMode         string                 `json:"serverMode"`
	Auth               authCapabilities       `json:"auth"`
	BootstrapAvailable bool                   `json:"bootstrapAvailable"`
	Identity           identityCapabilities   `json:"identity"`
	Pagination         paginationCapabilities `json:"pagination"`
	ImplementedTools   []string               `json:"implementedTools"`
	PlannedTools       []string               `json:"plannedTools,omitempty"`
}

type projectItem struct {
	ProjectSlug        string     `json:"projectSlug"`
	ProjectID          int64      `json:"projectId"`
	Name               string     `json:"name"`
	Image              *string    `json:"image"`
	DominantColor      string     `json:"dominantColor"`
	DefaultSprintWeeks int        `json:"defaultSprintWeeks"`
	ExpiresAt          *time.Time `json:"expiresAt"`
	CreatedAt          time.Time  `json:"createdAt"`
	UpdatedAt          time.Time  `json:"updatedAt"`
	Role               string     `json:"role,omitempty"`
}

type todoItem struct {
	ProjectSlug      string     `json:"projectSlug"`
	LocalID          int64      `json:"localId"`
	Title            string     `json:"title"`
	Body             string     `json:"body"`
	ColumnKey        string     `json:"columnKey"`
	Tags             []string   `json:"tags"`
	EstimationPoints *int64     `json:"estimationPoints"`
	AssigneeUserId   *int64     `json:"assigneeUserId"`
	SprintId         *int64     `json:"sprintId"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
	DoneAt           *time.Time `json:"doneAt"`
}

type todoSearchItem struct {
	ProjectSlug string `json:"projectSlug"`
	LocalID     int64  `json:"localId"`
	Title       string `json:"title"`
}

type sprintItem struct {
	ProjectSlug    string `json:"projectSlug"`
	SprintID       int64  `json:"sprintId"`
	Number         int64  `json:"number"`
	Name           string `json:"name"`
	PlannedStartAt int64  `json:"plannedStartAt"`
	PlannedEndAt   int64  `json:"plannedEndAt"`
	StartedAt      *int64 `json:"startedAt"`
	ClosedAt       *int64 `json:"closedAt"`
	State          string `json:"state"`
	TodoCount      *int64 `json:"todoCount"`
}

type projectTagItem struct {
	TagID     int64   `json:"tagId"`
	Name      string  `json:"name"`
	Count     int     `json:"count"`
	Color     *string `json:"color"`
	CanDelete bool    `json:"canDelete"`
}

type mineTagItem struct {
	TagID     int64   `json:"tagId"`
	Name      string  `json:"name"`
	Color     *string `json:"color"`
	CanDelete bool    `json:"canDelete"`
}

type boardProjectItem struct {
	ProjectSlug string `json:"projectSlug"`
	Name        string `json:"name"`
	Role        string `json:"role,omitempty"`
}

type boardColumnItem struct {
	Key    string     `json:"key"`
	Name   string     `json:"name"`
	IsDone bool       `json:"isDone"`
	Items  []todoItem `json:"items"`
}

type projectMemberItem struct {
	ProjectSlug string    `json:"projectSlug"`
	UserID      int64     `json:"userId"`
	Email       string    `json:"email"`
	Name        string    `json:"name"`
	Image       *string   `json:"image,omitempty"`
	Role        string    `json:"role"`
	CreatedAt   time.Time `json:"createdAt"`
}

// membersAddInput is the input for members.add and members.updateRole (projectSlug + userId + canonical role only).
type membersAddInput struct {
	ProjectSlug string `json:"projectSlug"`
	UserID      int64  `json:"userId"`
	Role        string `json:"role"`
}

// membersRemoveInput is the input for members.remove (projectSlug + userId only).
type membersRemoveInput struct {
	ProjectSlug string `json:"projectSlug"`
	UserID      int64  `json:"userId"`
}

// availableUserItem is the shape for members.listAvailable only (users not yet in the project).
// It intentionally omits fields the store does not load for that query (e.g. image).
type availableUserItem struct {
	UserID      int64     `json:"userId"`
	Email       string    `json:"email"`
	Name        string    `json:"name"`
	SystemRole  string    `json:"systemRole"`
	IsBootstrap bool      `json:"isBootstrap"`
	CreatedAt   time.Time `json:"createdAt"`
}
