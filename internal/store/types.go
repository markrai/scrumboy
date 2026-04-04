package store

import (
	"time"

	"scrumboy/internal/errs"
)

var (
	ErrNotFound                   = errs.ErrNotFound
	ErrConflict                   = errs.ErrConflict
	ErrValidation                 = errs.ErrValidation
	ErrUnauthorized               = errs.ErrUnauthorized
	ErrTooManyAttempts            = errs.ErrTooManyAttempts
	Err2FAEncryptionNotConfigured = errs.Err2FAEncryptionNotConfigured
)

const (
	DefaultColumnBacklog    = "backlog"
	DefaultColumnNotStarted = "not_started"
	DefaultColumnDoing      = "doing"
	DefaultColumnTesting    = "testing"
	DefaultColumnDone       = "done"
)

// Deprecated compatibility model kept for import/export transitions.
type Status int

const (
	StatusBacklog    Status = 0
	StatusNotStarted Status = 1
	StatusInProgress Status = 2
	StatusTesting    Status = 3
	StatusDone       Status = 4
)

func (s Status) String() string {
	switch s {
	case StatusBacklog:
		return "BACKLOG"
	case StatusNotStarted:
		return "NOT_STARTED"
	case StatusInProgress:
		return "IN_PROGRESS"
	case StatusTesting:
		return "TESTING"
	case StatusDone:
		return "DONE"
	default:
		return "UNKNOWN"
	}
}

func ParseStatus(v string) (Status, bool) {
	switch v {
	case "BACKLOG":
		return StatusBacklog, true
	case "NOT_STARTED":
		return StatusNotStarted, true
	case "IN_PROGRESS":
		return StatusInProgress, true
	case "TESTING":
		return StatusTesting, true
	case "DONE":
		return StatusDone, true
	default:
		return 0, false
	}
}

// StatusToColumnKey maps Status (legacy int) to column_key (persisted representation).
// Used for import/export compatibility with the column_key schema.
func StatusToColumnKey(s Status) string {
	switch s {
	case StatusBacklog:
		return DefaultColumnBacklog
	case StatusNotStarted:
		return DefaultColumnNotStarted
	case StatusInProgress:
		return DefaultColumnDoing
	case StatusTesting:
		return DefaultColumnTesting
	case StatusDone:
		return DefaultColumnDone
	default:
		return DefaultColumnBacklog
	}
}

// WorkflowColumn defines one ordered workflow lane for a project.
type WorkflowColumn struct {
	ID       int64
	ProjectID int64
	Key      string
	Name     string
	Color    string
	Position int
	IsDone   bool
	System   bool
}

// SprintFilter represents the sprint filter for board queries.
// Mode:
// - "none" = no filter
// - "scheduled" = sprint_id IS NOT NULL
// - "sprint" = filter by internal SprintID
// - "sprint_number" = filter by project-local sprint number
// - "unscheduled" = sprint_id IS NULL
type SprintFilter struct {
	Mode         string // "none" | "scheduled" | "sprint" | "sprint_number" | "unscheduled"
	SprintID     int64  // only when Mode == "sprint"
	SprintNumber int64  // only when Mode == "sprint_number"
}

// ProjectContext bundles project, role, and auth state computed once per request.
// Pass to GetBoard, GetBoardPaged, listTagCounts to avoid redundant project/auth queries.
type ProjectContext struct {
	Project     Project
	Role        ProjectRole
	AuthEnabled bool
}

type Project struct {
	ID                 int64
	Name               string
	Image              *string // Base64 encoded image data URL
	DominantColor      string
	EstimationMode     string
	DefaultSprintWeeks int
	Slug               string
	OwnerUserID        *int64 // NULL for unowned (e.g., temporary/share boards)
	// CreatorUserID represents who created the project at creation time.
	// This is immutable historical metadata, not a permission source.
	// - NULL for anonymous temp boards (created without authentication)
	// - Set for authenticated temp boards (created by logged-in user)
	// - Once project sharing exists, the creator will be inserted as initial project maintainer
	// - Do not use creator_user_id for authorization checks; use project_members instead
	CreatorUserID  *int64
	LastActivityAt time.Time  // NOT NULL in DB
	ExpiresAt      *time.Time // NULL for full mode, set for anonymous mode
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// ProjectMember represents a project membership
type ProjectMember struct {
	UserID    int64
	Email     string
	Name      string
	Image     *string // User avatar (base64 data URL), same as users.image
	Role      ProjectRole
	CreatedAt time.Time
}

// ProjectListEntry is a project with the current user's role for list responses.
// Used by ListProjects so the UI can hide Delete/Rename for non-maintainers.
type ProjectListEntry struct {
	Project Project
	Role   ProjectRole
}

// SystemRole represents a user's system-wide role (Owner, Admin, User).
// System roles govern system-level permissions (user management, admin APIs).
// System roles are completely separate from ProjectRole and do not grant
// project-level permissions. Project permissions are governed by project_members.
type SystemRole string

const (
	SystemRoleOwner SystemRole = "owner"
	SystemRoleAdmin SystemRole = "admin"
	SystemRoleUser  SystemRole = "user"
)

func (r SystemRole) String() string {
	return string(r)
}

func ParseSystemRole(v string) (SystemRole, bool) {
	switch v {
	case "owner":
		return SystemRoleOwner, true
	case "admin":
		return SystemRoleAdmin, true
	case "user":
		return SystemRoleUser, true
	default:
		return "", false
	}
}

type User struct {
	ID               int64
	Email            string
	Name             string
	Image            *string // Base64 data URL, same as project image
	IsBootstrap      bool    // Deprecated for authorization; kept for bootstrap initialization only
	SystemRole       SystemRole
	CreatedAt        time.Time
	TwoFactorEnabled bool // Store-only: use IsTwoFactorActive() for "is 2FA on?"
	// two_factor_secret_enc is never loaded into User. Fetched only in GetUserTwoFactorSecret when verifying TOTP.
}

// IsTwoFactorActive returns true when 2FA is enabled. Use this for all "is 2FA on?" checks.
// Invariant: enabling sets both two_factor_enabled and two_factor_secret_enc; disabling clears both.
func (u User) IsTwoFactorActive() bool {
	return u.TwoFactorEnabled
}

type Todo struct {
	ID        int64
	ProjectID int64
	// LocalID is a project-scoped, user-facing todo number (1-based, monotonically increasing, write-once).
	LocalID          int64
	Title            string
	Body             string
	Status           Status
	ColumnKey        string
	Rank             int64
	EstimationPoints *int64
	AssigneeUserID   *int64
	SprintID         *int64 // NULL = backlog; non-NULL = in that sprint
	Tags             []string
	CreatedAt        time.Time
	UpdatedAt        time.Time
	DoneAt           *time.Time // Last completion time (Unix ms). Set on transition into DONE; never cleared on reopen.

	// AssignmentChanged is set by UpdateTodo when the assignee was modified in this mutation.
	// Not persisted; used by callers to gate SSE emissions.
	AssignmentChanged bool `json:"-"`
}

// Sprint time terminology (see Sprint struct in sprints.go):
// - planned_start_at / planned_end_at: user-planned schedule, never overwritten by lifecycle transitions.
// - started_at: actual activation timestamp (PLANNED -> ACTIVE).
// - closed_at: actual closure timestamp (ACTIVE -> CLOSED).

const EstimationModeModifiedFibonacci = "MODIFIED_FIBONACCI"

// TodoLinkTarget holds minimal todo info for link API responses.
type TodoLinkTarget struct {
	LocalID  int64
	Title    string
	LinkType string
}

type TagCount struct {
	TagID     int64 // One row per tag_id; authority and mutations are tag_id-based
	Name      string
	Count     int
	Color     *string // Hex color code (e.g., "#FF5733"), nil if no custom color
	CanDelete bool    // Computed from tag.project_id/user_id and requester role/ownership; never from name groups
}

// LaneMeta holds pagination info for a board lane. NextCursor is "rank:id" (DB id); empty when !HasMore.
// TotalCount is the total number of todos in the lane (with same tag/search filters); 0 when not set.
type LaneMeta struct {
	HasMore    bool
	NextCursor string
	TotalCount int
}

type Mode string

const (
	ModeFull      Mode = "full"
	ModeAnonymous Mode = "anonymous"
)

func (m Mode) String() string {
	return string(m)
}

func ParseMode(s string) (Mode, bool) {
	switch s {
	case "full":
		return ModeFull, true
	case "anonymous":
		return ModeAnonymous, true
	default:
		return "", false
	}
}

// ProjectRole represents a user's role within a specific project.
// This is distinct from SystemRole (owner/admin/user) which applies system-wide.
//
// IMPORTANT: System roles (Owner, Admin, User) and project roles are completely separate.
// A user's system role does not grant project permissions. Project permissions are
// governed by project_members table entries.
//
// Current roles (in use):
//   - RoleOwner: Deprecated (Phase 2); maps to Maintainer; kept for ParseProjectRole compat
//   - RoleMaintainer: Project authority role (used in project_members)
//   - RoleContributor: Project contributor role; canonical write role (Editor deprecated)
//   - RoleViewer: Project viewer role (used in project_members)
//   - RoleEditor: Deprecated; merged into Contributor (Phase 1); kept for ParseProjectRole
//
// Once project sharing is implemented, the creator will be inserted as the initial
// project maintainer. Project roles will govern all project-level permissions.
type ProjectRole string

const (
	RoleOwner       ProjectRole = "owner"       // Deprecated: Phase 2 - maps to Maintainer; kept for ParseProjectRole compat
	RoleEditor      ProjectRole = "editor"      // Deprecated: merged into Contributor; kept for ParseProjectRole
	RoleViewer      ProjectRole = "viewer"      // Legacy: used in project_members
	RoleMaintainer  ProjectRole = "maintainer"  // Project authority role
	RoleContributor ProjectRole = "contributor"  // Canonical write role (Editor deprecated)
)

var validProjectRoleSet = map[ProjectRole]struct{}{
	RoleViewer:      {},
	RoleContributor: {},
	RoleMaintainer:  {},
}

func IsValidProjectRole(r ProjectRole) bool {
	_, ok := validProjectRoleSet[r]
	return ok
}

func (r ProjectRole) String() string {
	return string(r)
}

func ParseProjectRole(s string) (ProjectRole, bool) {
	switch s {
	case "owner":
		return RoleMaintainer, true // Deprecated: owner maps to maintainer (Phase 2)
	case "editor":
		return RoleEditor, true
	case "viewer":
		return RoleViewer, true
	case "maintainer":
		return RoleMaintainer, true
	case "contributor":
		return RoleContributor, true
	default:
		return "", false
	}
}

// Rank returns the permission rank for a project role.
// Higher rank means greater authority.
//
// Hierarchy:
// viewer < contributor < maintainer (editor deprecated, same rank as contributor; owner deprecated, same rank as maintainer)
// Unknown/empty roles have rank 0.
func (r ProjectRole) Rank() int {
	switch r {
	case RoleViewer:
		return 1
	case RoleEditor, RoleContributor:
		return 2
	case RoleMaintainer, RoleOwner:
		return 3
	default:
		return 0
	}
}

// HasMinimumRole reports whether role r has at least the required role
// according to Rank() ordering.
func (r ProjectRole) HasMinimumRole(required ProjectRole) bool {
	return r.Rank() >= required.Rank()
}
