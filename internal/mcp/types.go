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
	PriorityKey      *string    `json:"priorityKey"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
	DoneAt           *time.Time `json:"doneAt"`
}

type todoSearchItem struct {
	ProjectSlug string `json:"projectSlug"`
	LocalID     int64  `json:"localId"`
	Title       string `json:"title"`
}

type todoLinkItem struct {
	LocalID  int64  `json:"localId"`
	Title    string `json:"title"`
	LinkType string `json:"linkType"`
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
	// TagID is omitted for grouped personal labels (no representative row).
	TagID int64   `json:"tagId,omitempty"`
	Name  string  `json:"name"`
	Count int     `json:"count"`
	Color *string `json:"color"`
	// DeleteScope is one of "mine", "project", "none". A personal-label group is
	// never "project", so clients must not infer tags_deleteProject eligibility from it.
	DeleteScope      string `json:"deleteScope"`
	CanDeleteMine    bool   `json:"canDeleteMine"`
	CanDeleteProject bool   `json:"canDeleteProject"`
	// CanUpdateColor is false for durable board-scoped tags when the caller is
	// below Maintainer. Personal labels and temporary-board rows report true when
	// the caller may change the color.
	CanUpdateColor bool `json:"canUpdateColor"`
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

type workflowColumnItem struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Color    string `json:"color"`
	Position int    `json:"position"`
	IsDone   bool   `json:"isDone"`
	System   bool   `json:"system"`
}

type priorityTierItem struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Color    string `json:"color"`
	Position int    `json:"position"`
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

// membersAddInput is the input for members_add and members_updateRole (projectSlug + userId + canonical role only).
type membersAddInput struct {
	ProjectSlug string `json:"projectSlug"`
	UserID      int64  `json:"userId"`
	Role        string `json:"role"`
}

// membersRemoveInput is the input for members_remove (projectSlug + userId only).
type membersRemoveInput struct {
	ProjectSlug string `json:"projectSlug"`
	UserID      int64  `json:"userId"`
}

// availableUserItem is the shape for members_listAvailable only (users not yet in the project).
// It intentionally omits fields the store does not load for that query (e.g. image).
type availableUserItem struct {
	UserID      int64     `json:"userId"`
	Email       string    `json:"email"`
	Name        string    `json:"name"`
	SystemRole  string    `json:"systemRole"`
	IsBootstrap bool      `json:"isBootstrap"`
	CreatedAt   time.Time `json:"createdAt"`
}

type activeSprintInfoItem struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	StartAt int64  `json:"startAt"`
	EndAt   int64  `json:"endAt"`
}

type sprintSectionInfoItem struct {
	ID      *int64 `json:"id,omitempty"`
	Name    string `json:"name"`
	State   string `json:"state,omitempty"`
	StartAt int64  `json:"startAt,omitempty"`
	EndAt   int64  `json:"endAt,omitempty"`
}

type dashboardProjectItem struct {
	ProjectID      int64                   `json:"projectId"`
	ProjectName    string                  `json:"projectName"`
	ProjectSlug    string                  `json:"projectSlug"`
	ActiveSprint   *activeSprintInfoItem   `json:"activeSprint"`
	SprintSections []sprintSectionInfoItem `json:"sprintSections"`
}

type assignedSplitItem struct {
	SprintStories  int   `json:"sprintStories"`
	SprintPoints   int64 `json:"sprintPoints"`
	BacklogStories int   `json:"backlogStories"`
	BacklogPoints  int64 `json:"backlogPoints"`
}

type sprintCompletionItem struct {
	TotalStories int   `json:"totalStories"`
	DoneStories  int   `json:"doneStories"`
	TotalPoints  int64 `json:"totalPoints"`
	DonePoints   int64 `json:"donePoints"`
}

type weeklyThroughputPointItem struct {
	WeekStart string `json:"weekStart"`
	Stories   int    `json:"stories"`
	Points    int64  `json:"points"`
}

type oldestWipItem struct {
	LocalID     int64  `json:"localId"`
	Title       string `json:"title"`
	AgeDays     int    `json:"ageDays"`
	ProjectName string `json:"projectName"`
	ProjectSlug string `json:"projectSlug"`
}

type dashboardSummaryItem struct {
	AssignedCount            int                         `json:"assignedCount"`
	TotalAssignedStoryPoints int64                       `json:"totalAssignedStoryPoints"`
	PointsCompletedThisWeek  int64                       `json:"pointsCompletedThisWeek"`
	StoriesCompletedThisWeek int                         `json:"storiesCompletedThisWeek"`
	Projects                 []dashboardProjectItem      `json:"projects"`
	AssignedSplit            *assignedSplitItem          `json:"assignedSplit,omitempty"`
	SprintCompletion         *sprintCompletionItem       `json:"sprintCompletion,omitempty"`
	SprintCompletionAllUsers *sprintCompletionItem       `json:"sprintCompletionAllUsers,omitempty"`
	WipCount                 int                         `json:"wipCount"`
	WipInProgressCount       int                         `json:"wipInProgressCount"`
	WipTestingCount          int                         `json:"wipTestingCount"`
	WeeklyThroughput         []weeklyThroughputPointItem `json:"weeklyThroughput"`
	AvgLeadTimeDays          *float64                    `json:"avgLeadTimeDays,omitempty"`
	OldestWip                *oldestWipItem              `json:"oldestWip,omitempty"`
}

type dashboardTodoItem struct {
	ID                   int64     `json:"id"`
	LocalID              int64     `json:"localId"`
	Title                string    `json:"title"`
	ProjectID            int64     `json:"projectId"`
	ProjectName          string    `json:"projectName"`
	ProjectSlug          string    `json:"projectSlug"`
	ProjectImage         *string   `json:"projectImage,omitempty"`
	ProjectDominantColor string    `json:"projectDominantColor"`
	EstimationPoints     *int64    `json:"estimationPoints,omitempty"`
	SprintId             *int64    `json:"sprintId,omitempty"`
	Status               string    `json:"status"`
	StatusName           string    `json:"statusName"`
	StatusColor          string    `json:"statusColor"`
	ColumnKey            string    `json:"columnKey"`
	UpdatedAt            time.Time `json:"updatedAt"`
}

type burndownPointItem struct {
	Date             time.Time `json:"date"`
	IncompleteCount  *int      `json:"incompleteCount,omitempty"`
	TotalScope       *int      `json:"totalScope,omitempty"`
	IncompletePoints *int      `json:"incompletePoints,omitempty"`
	TotalScopePoints *int      `json:"totalScopePoints,omitempty"`
	NewTodosCount    int       `json:"newTodosCount"`
}

type realBurndownPointItem struct {
	Date               time.Time `json:"date"`
	RemainingWork      *int      `json:"remainingWork,omitempty"`
	InitialScope       int       `json:"initialScope"`
	RemainingPoints    *int      `json:"remainingPoints,omitempty"`
	InitialScopePoints *int      `json:"initialScopePoints,omitempty"`
}

// adminUserItem is the shape for admin_listUsers / admin_updateUserRole. It
// intentionally mirrors availableUserItem's field set (system-level, not
// project-scoped) and omits sensitive fields (password hash, 2FA secret).
type adminUserItem struct {
	UserID      int64     `json:"userId"`
	Email       string    `json:"email"`
	Name        string    `json:"name"`
	SystemRole  string    `json:"systemRole"`
	IsBootstrap bool      `json:"isBootstrap"`
	CreatedAt   time.Time `json:"createdAt"`
}
