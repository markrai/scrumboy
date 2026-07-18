package httpapi

import (
	"context"
	"embed"
	"encoding/json"
	"io"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"time"

	"scrumboy/internal/config"
	"scrumboy/internal/eventbus"
	"scrumboy/internal/httpapi/ratelimit"
	"scrumboy/internal/mailer"
	"scrumboy/internal/oidc"
	"scrumboy/internal/store"
	"scrumboy/internal/version"
)

type Options struct {
	Logger              *log.Logger
	MaxRequestBody      int64
	MaxTrelloImportBody int64
	ScrumboyMode        string // "full" or "anonymous"
	// DataDir is the instance data directory (SQLite lives here; also used for per-user wallpaper files).
	// Empty disables wallpaper upload/serve (returns 503 for those routes).
	DataDir             string
	AuthRateLimit       *ratelimit.Limiter
	OAuthDCRRateLimit   *ratelimit.Limiter
	OAuthTokenRateLimit *ratelimit.Limiter
	MCPHandler          http.Handler
	AgoraHandler        http.Handler
	// EncryptionKey is the HMAC secret for password reset tokens. Required for admin password reset.
	// Set from SCRUMBOY_ENCRYPTION_KEY (base64). If unset, password reset endpoints return 503.
	EncryptionKey []byte

	OIDCService *oidc.Service // nil when OIDC is not configured

	// Web Push (optional). Push is enabled only in full mode with both public/private VAPID keys set.
	VAPIDPublicKey  string
	VAPIDPrivateKey string
	VAPIDSubscriber string // VAPID JWT "sub" (e.g. mailto:ops@example.com); default in notifier if empty
	PushDebug       bool   // Log push send/skip (also SCRUMBOY_DEBUG_PUSH in config)

	// WallEnabled gates the Scrumbaby feature. When false, all /wall routes
	// return 404 and the frontend hides the Wall topbar button. Defaults on;
	// set SCRUMBOY_WALL_ENABLED=0 to disable (see config.FromEnv semantics).
	WallEnabled bool

	// MarkdownNotesEnabled gates the todo notes markdown/preview experience in the
	// SPA. When false, the frontend behaves exactly as before.
	MarkdownNotesEnabled bool

	// MermaidNotesEnabled gates Mermaid rendering within the markdown preview.
	// Effective only when MarkdownNotesEnabled is also true.
	MermaidNotesEnabled bool

	// SMTP (optional). Enables self-service "forgot password" email via
	// POST /api/auth/request-password-reset. Enabled when Host is set, Port
	// is in 1–65535 (defaults to 587 when omitted), and From is a parseable
	// RFC 5322 address (see SMTPConfigured). Also requires
	// SCRUMBOY_PUBLIC_BASE_URL for emailed links.
	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string
	SMTPTLSMode  string
	// SMTPDebug, if true, logs each send attempt's connection details (host,
	// port, TLS mode, whether auth is used) — never credentials or message
	// bodies. See SCRUMBOY_SMTP_DEBUG in docs/smtp.md.
	SMTPDebug bool

	// PublicBaseURL (SCRUMBOY_PUBLIC_BASE_URL). Required for self-service
	// password-reset emails; missing or invalid values fail closed. When set,
	// overrides the request-derived origin for reset links (see resetBaseURL)
	// and is the canonical OAuth discovery issuer.
	PublicBaseURL string

	// TrustProxy (SCRUMBOY_TRUST_PROXY). When true, clientIP honors
	// X-Forwarded-For for authentication and OAuth rate-limit IP keys. Default
	// false (RemoteAddr only). Enable only behind a reverse proxy that
	// overwrites or strips client-supplied XFF. Without PublicBaseURL, OAuth
	// discovery also requires forwarded HTTPS and an explicit X-Forwarded-Host.
	TrustProxy bool
}

type Server struct {
	store storeAPI

	logger              *log.Logger
	maxBody             int64
	maxTrelloImportBody int64
	mode                string // "full" or "anonymous"
	hub                 *Hub
	sink                EventSink
	fanout              *eventbus.Fanout
	webhookQueue        *webhookQueue
	webhookWorker       *webhookWorker
	webhookCancel       context.CancelFunc
	webhookDone         <-chan struct{} // closed once the webhook worker's shutdown flush completes
	mailQueue           *mailQueue
	mailWorker          *mailWorker // nil when SMTP isn't configured
	mailCancel          context.CancelFunc
	mailDone            <-chan struct{} // closed once the mail worker's shutdown flush completes; nil if SMTP isn't configured

	authRateLimit       *ratelimit.Limiter
	oauthDCRRateLimit   *ratelimit.Limiter
	oauthTokenRateLimit *ratelimit.Limiter

	encryptionKey []byte        // for password reset tokens; nil if not configured
	oidcService   *oidc.Service // nil when OIDC is not configured

	passwordResetAdminLimiter   *ratelimit.Limiter // 10 resets/min per admin
	passwordResetRequestLimiter *ratelimit.Limiter // 5/min per IP+email, self-service request
	firstPasswordStartLimiter   *ratelimit.Limiter
	firstPasswordFinishLimiter  *ratelimit.Limiter
	oidcLinkStartLimiter        *ratelimit.Limiter
	currentPasswordLimiter      *ratelimit.Limiter
	secondFactorLimiter         *ratelimit.Limiter
	totpLimiter                 *ratelimit.Limiter
	recoveryCodeLimiter         *ratelimit.Limiter

	smtpConfigured bool // Host+port+From statically valid; gates request-password-reset email sending

	publicBaseURL string // SCRUMBOY_PUBLIC_BASE_URL; reset-link origin and canonical OAuth issuer when set
	trustProxy    bool   // SCRUMBOY_TRUST_PROXY; gates forwarded client IP and OAuth origin signals

	webFS               fs.FS
	fileSrv             http.Handler
	indexHTML           []byte
	landingHTML         []byte
	landingHTMLByLocale map[string][]byte
	swJS                []byte // Service worker with version injected
	mcpHandler          http.Handler
	agoraHandler        http.Handler

	vapidPublicKey      string
	pushVapidConfigured bool // full mode + both VAPID keys present; subscribe and push notify use this
	pushDebug           bool

	dataDir string // user-wallpapers storage; empty = disabled

	wallEnabled          bool // Scrumbaby wall; default on (SCRUMBOY_WALL_ENABLED=0 to disable)
	markdownNotesEnabled bool // Todo notes markdown preview; default off unless explicitly enabled
	mermaidNotesEnabled  bool // Mermaid in todo notes preview; default off unless explicitly enabled
}

type storeAPI interface {
	Health(ctx context.Context) error

	CountUsers(ctx context.Context) (int, error)
	GetUser(ctx context.Context, userID int64) (store.User, error)
	GetUserPasswordHash(ctx context.Context, userID int64) (string, error)
	UpdateUserImage(ctx context.Context, userID int64, image *string) error
	UpdateUserPassword(ctx context.Context, userID int64, newPassword string) error
	ResetLocalPassword(ctx context.Context, userID int64, expectedHash, password string) error
	BootstrapUser(ctx context.Context, email, password, name string) (store.User, error)
	AuthenticateUser(ctx context.Context, email, password string) (store.User, error)
	CreateUser(ctx context.Context, email, password, name string) (store.User, error)
	ListUsers(ctx context.Context, requesterID int64) ([]store.User, error)
	UpdateUserRole(ctx context.Context, requesterID, targetUserID int64, newRole store.SystemRole) error
	DeleteUser(ctx context.Context, requesterID, targetUserID int64) error
	AssignUnownedDurableProjectsToUser(ctx context.Context, userID int64) error
	ClaimTemporaryBoard(ctx context.Context, projectID, userID int64) error
	CreateSession(ctx context.Context, userID int64, ttl time.Duration) (token string, expiresAt time.Time, err error)
	DeleteSession(ctx context.Context, token string) error
	DeleteSessionsByUserID(ctx context.Context, userID int64) error
	GetUserBySessionToken(ctx context.Context, token string) (store.User, error)
	CreateUserAPIToken(ctx context.Context, userID int64, name *string) (id int64, plaintext string, createdAt time.Time, err error)
	ListUserAPITokens(ctx context.Context, userID int64) ([]store.APITokenMeta, error)
	RevokeUserAPIToken(ctx context.Context, userID, tokenID int64) error

	// OAuth 2.1 authorization server (RFC 7591/6749/7636/7009) for MCP clients.
	CreateOAuthClient(ctx context.Context, clientID, clientName, redirectURI string) (store.OAuthClient, error)
	GetOAuthClient(ctx context.Context, clientID string) (store.OAuthClient, error)
	CreateOAuthAuthCode(ctx context.Context, clientID string, userID int64, redirectURI, codeChallenge, codeChallengeMethod string) (string, error)
	ConsumeOAuthAuthCode(ctx context.Context, rawCode string) (store.OAuthAuthCode, error)
	IssueOAuthTokenPair(ctx context.Context, clientID string, userID int64) (store.OAuthTokenPair, error)
	ConsumeOAuthRefreshToken(ctx context.Context, rawToken string) (clientID string, userID int64, err error)
	RevokeOAuthToken(ctx context.Context, rawToken, hint string) error

	GetUserByOIDCIdentity(ctx context.Context, issuer, subject string) (store.User, error)
	UpdateOIDCIdentityEmailAtLogin(ctx context.Context, userID int64, issuer, subject, email string) error
	GetUserByEmail(ctx context.Context, email string) (store.User, error)
	LinkOIDCIdentity(ctx context.Context, userID int64, issuer, subject, email string) error
	LinkOIDCIdentityExplicit(ctx context.Context, userID int64, issuer, subject, verifiedEmail string) error
	CreateUserOIDC(ctx context.Context, configuredIssuer, issuer, subject, email, name string) (store.User, error)
	CreateFirstPasswordGrant(ctx context.Context, userID int64, sessionToken string, ttl time.Duration) (string, time.Time, error)
	FirstPasswordGrantValid(ctx context.Context, rawGrant, sessionToken string, userID int64) (bool, error)
	SetFirstPassword(ctx context.Context, userID int64, rawGrant, sessionToken, password string) error
	SetFirstPasswordWithRecoveryCode(ctx context.Context, userID int64, rawGrant, sessionToken, password string, recoveryCodeID int64) error

	ListProjects(ctx context.Context) ([]store.ProjectListEntry, error)
	GetProject(ctx context.Context, projectID int64) (store.Project, error)
	GetProjectBySlug(ctx context.Context, slug string) (store.Project, error)
	GetProjectContextBySlug(ctx context.Context, slug string, mode store.Mode) (store.ProjectContext, error)
	GetProjectContextForRead(ctx context.Context, projectID int64, mode store.Mode) (store.ProjectContext, error)
	CreateProject(ctx context.Context, name string) (store.Project, error)
	CreateProjectWithWorkflow(ctx context.Context, name string, workflow []store.WorkflowColumn) (store.Project, error)
	DeleteProject(ctx context.Context, projectID int64, userID int64) error
	UpdateProjectImage(ctx context.Context, projectID int64, userID int64, image *string, dominantColor string) error
	UpdateProjectName(ctx context.Context, projectID int64, userID int64, name string) error
	UpdateProjectDefaultSprintWeeks(ctx context.Context, projectID int64, userID int64, weeks int) error
	AddWorkflowColumn(ctx context.Context, projectID int64, name string) (store.WorkflowColumn, error)
	DeleteWorkflowColumn(ctx context.Context, projectID int64, key string) error
	UpdateWorkflowColumn(ctx context.Context, projectID int64, key, name, color string) error
	CountTodosByColumnKey(ctx context.Context, projectID int64) (map[string]int, error)
	GetProjectRole(ctx context.Context, projectID int64, userID int64) (store.ProjectRole, error)
	CheckProjectRole(ctx context.Context, projectID int64, userID int64, requiredRole store.ProjectRole) error
	ListProjectMembers(ctx context.Context, projectID int64, userID int64) ([]store.ProjectMember, error)
	AddProjectMember(ctx context.Context, requesterID, projectID, targetUserID int64, role store.ProjectRole) error
	RemoveProjectMember(ctx context.Context, requesterID, projectID, targetUserID int64) error
	UpdateProjectMemberRole(ctx context.Context, requesterID, projectID, targetUserID int64, role store.ProjectRole) error
	ListAvailableUsersForProject(ctx context.Context, requesterID, projectID int64) ([]store.User, error)

	GetBoard(ctx context.Context, pc *store.ProjectContext, tagFilter string, searchFilter string, sprintFilter store.SprintFilter) (store.Project, []store.TagCount, []store.WorkflowColumn, map[string][]store.Todo, error)
	GetBoardPaged(ctx context.Context, pc *store.ProjectContext, tagFilter string, searchFilter string, sprintFilter store.SprintFilter, limitPerLane int) (store.Project, []store.TagCount, []store.WorkflowColumn, map[string][]store.Todo, map[string]store.LaneMeta, error)
	ListTagCounts(ctx context.Context, pc *store.ProjectContext) ([]store.TagCount, error)
	ListTodosForBoardLane(ctx context.Context, projectID int64, columnKey string, limit int, afterRank, afterID int64, tagFilter, searchFilter string, sprintFilter store.SprintFilter) ([]store.Todo, string, bool, error)
	GetDashboardSummary(ctx context.Context, userID int64, timezone string) (store.DashboardSummary, error)
	ListDashboardTodos(ctx context.Context, userID int64, limit int, cursor *string, sort string) ([]store.DashboardTodo, *string, error)
	GetBacklogSize(ctx context.Context, projectID int64, mode store.Mode) ([]store.BurndownPoint, error)
	GetRealBurndown(ctx context.Context, projectID int64, mode store.Mode) ([]store.RealBurndownPoint, error)
	GetRealBurndownForSprint(ctx context.Context, projectID, sprintID int64, mode store.Mode) ([]store.RealBurndownPoint, error)
	ListTags(ctx context.Context, projectID int64, mode store.Mode) ([]store.TagWithColor, error)
	ListUserTags(ctx context.Context, userID int64) ([]store.TagWithColor, error)
	ListUserTagsForProject(ctx context.Context, userID int64, projectID int64) ([]store.TagWithColor, error)
	ListBoardTagsForProject(ctx context.Context, projectID int64) ([]store.TagWithColor, error)
	GetTagIDByName(ctx context.Context, userID int64, tagName string) (int64, error)
	GetAnyTagIDByName(ctx context.Context, tagName string) (int64, error)
	GetBoardScopedTagIDByName(ctx context.Context, projectID int64, tagName string) (int64, error)
	ResolveTagForColorUpdate(ctx context.Context, projectID int64, viewerUserID *int64, tagName string, linkTemporaryBoard bool) (int64, error)
	UpdateTagColor(ctx context.Context, viewerUserID *int64, tagID int64, color *string) error
	UpdateTagColorForTemporaryBoard(ctx context.Context, projectID int64, viewerUserID *int64, tagID int64, color *string) error
	UpdateTagColorForProject(ctx context.Context, projectID int64, viewerUserID *int64, tagName string, color *string, linkTemporaryBoard bool) error
	GetTagColor(ctx context.Context, userID int64, tagID int64) (*string, error)
	DeleteTag(ctx context.Context, userID int64, tagID int64, isAnonymousBoard bool) error

	CreateTodo(ctx context.Context, projectID int64, in store.CreateTodoInput, mode store.Mode) (store.Todo, error)
	CreateSprint(ctx context.Context, projectID int64, name string, plannedStartAt, plannedEndAt time.Time) (store.Sprint, error)
	ListSprints(ctx context.Context, projectID int64) ([]store.Sprint, error)
	HasSprints(ctx context.Context, projectID int64) (bool, error)
	ListSprintsWithTodoCount(ctx context.Context, projectID int64) ([]store.SprintWithTodoCount, error)
	CountUnscheduledTodos(ctx context.Context, projectID int64) (int64, error)
	GetSprintByID(ctx context.Context, sprintID int64) (store.Sprint, error)
	GetSprintByProjectNumber(ctx context.Context, projectID, number int64) (store.Sprint, error)
	GetActiveSprintByProjectID(ctx context.Context, projectID int64) (*store.Sprint, error)
	ActivateSprint(ctx context.Context, projectID, sprintID int64) error
	CloseSprint(ctx context.Context, sprintID int64) error
	UpdateSprint(ctx context.Context, sprintID int64, in store.UpdateSprintInput) error
	DeleteSprint(ctx context.Context, projectID, sprintID int64) error
	UpdateTodo(ctx context.Context, todoID int64, in store.UpdateTodoInput, mode store.Mode) (store.Todo, error)
	DeleteTodo(ctx context.Context, todoID int64, mode store.Mode) error
	GetProjectIDForTodo(ctx context.Context, todoID int64) (int64, error)
	MoveTodo(ctx context.Context, todoID int64, toColumnKey string, afterID, beforeID *int64, mode store.Mode) (store.Todo, error)
	UpdateTodoByLocalID(ctx context.Context, projectID, localID int64, in store.UpdateTodoInput, mode store.Mode) (store.Todo, error)
	GetTodoByLocalID(ctx context.Context, projectID, localID int64, mode store.Mode) (store.Todo, error)
	DeleteTodoByLocalID(ctx context.Context, projectID, localID int64, mode store.Mode) error
	MoveTodoByLocalID(ctx context.Context, projectID, localID int64, toColumnKey string, afterLocalID, beforeLocalID *int64, mode store.Mode) (store.Todo, error)
	AddLink(ctx context.Context, projectID, fromLocalID, toLocalID int64, linkType string, mode store.Mode) error
	RemoveLink(ctx context.Context, projectID, fromLocalID, toLocalID int64, mode store.Mode) error
	ListLinksForTodo(ctx context.Context, projectID, localID int64, mode store.Mode) ([]store.TodoLinkTarget, error)
	ListBacklinksForTodo(ctx context.Context, projectID, localID int64, mode store.Mode) ([]store.TodoLinkTarget, error)
	SearchTodosForLinkPicker(ctx context.Context, projectID int64, q string, limit int, excludeLocalIDs []int64, mode store.Mode) ([]store.TodoLinkTarget, error)

	CreateAnonymousBoard(ctx context.Context) (store.Project, error)

	ExportAllProjects(ctx context.Context, mode store.Mode) (*store.ExportData, error)
	ImportProjects(ctx context.Context, data *store.ExportData, mode store.Mode, importMode string) (*store.ImportResult, error)
	ImportProjectsWithTarget(ctx context.Context, data *store.ExportData, mode store.Mode, importMode string, targetSlug string) (*store.ImportResult, error)
	PreviewImport(ctx context.Context, data *store.ExportData, mode store.Mode, importMode string) (*store.PreviewResult, error)
	ImportTrelloProject(ctx context.Context, data *store.ExportData, projectImportMetadata string, todoImportMetadataByLocalID map[int64]string, mode store.Mode) (store.Project, error)

	GetUserPreference(ctx context.Context, userID int64, key string) (string, error)
	SetUserPreference(ctx context.Context, userID int64, key, value string) error

	// 2FA
	CreateLogin2FAPending(ctx context.Context, userID int64, ttl time.Duration) (token string, expiresAt time.Time, err error)
	GetUserByLogin2FAPendingToken(ctx context.Context, token string) (store.User, int, error)
	IncrementLogin2FAPendingAttempt(ctx context.Context, token string) error
	DeleteLogin2FAPendingToken(ctx context.Context, token string) error
	CreateTwoFactorEnrollment(ctx context.Context, userID int64, secretEnc string, ttl time.Duration) (setupToken string, expiresAt time.Time, err error)
	GetTwoFactorEnrollmentByToken(ctx context.Context, token string) (userID int64, secretEnc string, err error)
	IncrementEnrollmentAttempt(ctx context.Context, token string) error
	DeleteTwoFactorEnrollmentByToken(ctx context.Context, token string) error
	GetUserTwoFactorSecret(ctx context.Context, userID int64) (string, error)
	SetUserTwoFactor(ctx context.Context, userID int64, encryptedSecret string) error
	ClearUserTwoFactor(ctx context.Context, userID int64) error
	AddRecoveryCodes(ctx context.Context, userID int64, codes []string) error
	ConsumeRecoveryCode(ctx context.Context, userID int64, code string) (bool, error)
	MatchRecoveryCode(ctx context.Context, userID int64, code string) (int64, error)
	ConsumeRecoveryCodeID(ctx context.Context, userID, recoveryCodeID int64) (bool, error)
	DeleteRecoveryCodesByUser(ctx context.Context, userID int64) error
	EncryptTOTPSecret(plaintext []byte) (string, error)
	DecryptTOTPSecret(encrypted string) ([]byte, error)

	// Webhooks
	CreateWebhook(ctx context.Context, userID int64, in store.CreateWebhookInput) (store.Webhook, error)
	ListWebhooks(ctx context.Context, userID int64) ([]store.Webhook, error)
	DeleteWebhook(ctx context.Context, userID, webhookID int64) error
	ListWebhooksByProject(ctx context.Context, projectID int64) ([]store.WebhookRow, error)

	UpsertPushSubscription(ctx context.Context, userID int64, endpoint, p256dh, auth string, userAgent *string) error
	DeletePushSubscription(ctx context.Context, userID int64, endpoint string) error
	DeletePushSubscriptionByEndpoint(ctx context.Context, endpoint string) error
	ListPushSubscriptionsByUser(ctx context.Context, userID int64) ([]store.PushSubscription, error)

	// Scrumbaby (sticky-note wall). Durable projects only.
	GetWall(ctx context.Context, projectID int64) (store.Wall, error)
	CreateNote(ctx context.Context, projectID int64, in store.CreateNoteInput) (store.WallNote, store.Wall, error)
	PatchNote(ctx context.Context, projectID int64, noteID string, in store.PatchNoteInput) (store.WallNote, store.Wall, error)
	DeleteNote(ctx context.Context, projectID int64, noteID string) (store.Wall, error)
	ReplaceWall(ctx context.Context, projectID int64, notes []store.WallNote) (store.Wall, error)
	CreateEdge(ctx context.Context, projectID int64, fromNoteID, toNoteID string) (store.WallEdge, store.Wall, error)
	DeleteEdge(ctx context.Context, projectID int64, edgeID string) (store.Wall, error)
}

//go:embed web/**
//go:embed web/vendor/**
var embeddedWeb embed.FS

func readLocalizedLandingHTML(webFS fs.FS) (map[string][]byte, error) {
	entries, err := fs.ReadDir(webFS, "landing.locales")
	if err != nil {
		return nil, err
	}

	landingByLocale := make(map[string][]byte)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".html") {
			continue
		}
		locale := strings.TrimSuffix(entry.Name(), ".html")
		if locale == "" || locale == "en" || locale == "pseudo" {
			continue
		}
		html, err := fs.ReadFile(webFS, "landing.locales/"+entry.Name())
		if err != nil {
			return nil, err
		}
		landingByLocale[locale] = []byte(strings.ReplaceAll(string(html), "{{VERSION}}", version.Version))
	}
	return landingByLocale, nil
}

func NewServer(st storeAPI, opts Options) *Server {
	logger := opts.Logger
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}

	webFS, err := fs.Sub(embeddedWeb, "web")
	if err != nil {
		panic(err)
	}
	indexHTML, err := fs.ReadFile(webFS, "index.html")
	if err != nil {
		panic(err)
	}
	// Inject version into index.html
	indexHTML = []byte(strings.ReplaceAll(string(indexHTML), "{{VERSION}}", version.Version))

	landingHTML, err := fs.ReadFile(webFS, "landing.html")
	if err != nil {
		panic(err)
	}
	// Inject version into landing.html
	landingHTML = []byte(strings.ReplaceAll(string(landingHTML), "{{VERSION}}", version.Version))

	landingHTMLByLocale, err := readLocalizedLandingHTML(webFS)
	if err != nil {
		panic(err)
	}

	swJS, err := fs.ReadFile(webFS, "sw.js")
	if err != nil {
		panic(err)
	}
	swJS = []byte(strings.ReplaceAll(string(swJS), "{{VERSION}}", version.Version))

	maxBody := opts.MaxRequestBody
	if maxBody <= 0 {
		maxBody = 1 << 20
	}
	maxTrelloImportBody := opts.MaxTrelloImportBody
	if maxTrelloImportBody <= 0 {
		maxTrelloImportBody = 32 << 20
	}

	mode := opts.ScrumboyMode
	if mode != "full" && mode != "anonymous" {
		mode = "full" // Default to full if invalid
	}

	// IMPORTANT: Anonymous mode disables all authentication, including valid session cookies.
	// When mode == "anonymous", requestContext() ignores session cookies and all requests
	// are treated as unauthenticated. This ensures anonymous temp boards have creator_user_id = NULL
	// and never appear in user listings. See requestContext() for implementation.

	authRateLimit := opts.AuthRateLimit
	if authRateLimit == nil {
		authRateLimit = ratelimit.New(10, time.Minute)
	}
	oauthDCRRateLimit := opts.OAuthDCRRateLimit
	if oauthDCRRateLimit == nil {
		oauthDCRRateLimit = ratelimit.New(10, time.Minute)
	}
	oauthTokenRateLimit := opts.OAuthTokenRateLimit
	if oauthTokenRateLimit == nil {
		oauthTokenRateLimit = ratelimit.New(60, time.Minute)
	}
	hub := NewHub(defaultSubscriberBuffer)
	sseBridgeConsumer := newSSEBridge(hub)
	whQueue := newWebhookQueue(logger)
	whDispatcher := newWebhookDispatcher(st, whQueue, logger)
	pushDebug := opts.PushDebug
	vapidPub := strings.TrimSpace(opts.VAPIDPublicKey)
	pushVapidConfigured := PushConfigured(mode, opts.VAPIDPublicKey, opts.VAPIDPrivateKey)
	pushNotifier := newPushNotifier(st, logger, opts.VAPIDPublicKey, opts.VAPIDPrivateKey, opts.VAPIDSubscriber, pushDebug)
	fanout := eventbus.NewFanout(sseBridgeConsumer, whDispatcher, pushNotifier)
	whWorker := newWebhookWorker(whQueue, logger)
	workerCtx, workerCancel := context.WithCancel(context.Background())
	webhookDone := whWorker.Done()
	go whWorker.Run(workerCtx)
	passwordResetAdminLimiter := ratelimit.New(10, time.Minute)
	passwordResetRequestLimiter := ratelimit.New(5, time.Minute)
	firstPasswordStartLimiter := ratelimit.New(5, time.Minute)
	firstPasswordFinishLimiter := ratelimit.New(5, time.Minute)
	oidcLinkStartLimiter := ratelimit.New(5, time.Minute)
	currentPasswordLimiter := ratelimit.New(5, time.Minute)
	secondFactorLimiter := ratelimit.New(5, time.Minute)
	totpLimiter := ratelimit.New(5, time.Minute)
	recoveryCodeLimiter := ratelimit.New(5, time.Minute)

	smtpConfigured := SMTPConfigured(opts.SMTPHost, opts.SMTPPort, opts.SMTPFrom)
	mQueue := newMailQueue(logger)
	var mWorker *mailWorker
	var mailCancel context.CancelFunc
	var mailDone <-chan struct{}
	if smtpConfigured {
		sender := mailer.New(mailer.Config{
			Host:     opts.SMTPHost,
			Port:     opts.SMTPPort,
			Username: opts.SMTPUsername,
			Password: opts.SMTPPassword,
			From:     opts.SMTPFrom,
			TLSMode:  opts.SMTPTLSMode,
			Debug:    opts.SMTPDebug,
			Logger:   logger,
		})
		mWorker = newMailWorker(mQueue, sender, logger)
		mailCtx, cancel := context.WithCancel(context.Background())
		mailCancel = cancel
		mailDone = mWorker.Done()
		go mWorker.Run(mailCtx)
	}

	var encKey []byte
	if opts.EncryptionKey != nil {
		encKey = opts.EncryptionKey
	}

	return &Server{
		store:                       st,
		logger:                      logger,
		maxBody:                     maxBody,
		maxTrelloImportBody:         maxTrelloImportBody,
		mode:                        mode,
		dataDir:                     strings.TrimSpace(opts.DataDir),
		hub:                         hub,
		sink:                        hub,
		fanout:                      fanout,
		webhookQueue:                whQueue,
		webhookWorker:               whWorker,
		webhookCancel:               workerCancel,
		webhookDone:                 webhookDone,
		mailQueue:                   mQueue,
		mailWorker:                  mWorker,
		mailCancel:                  mailCancel,
		mailDone:                    mailDone,
		authRateLimit:               authRateLimit,
		oauthDCRRateLimit:           oauthDCRRateLimit,
		oauthTokenRateLimit:         oauthTokenRateLimit,
		encryptionKey:               encKey,
		oidcService:                 opts.OIDCService,
		passwordResetAdminLimiter:   passwordResetAdminLimiter,
		passwordResetRequestLimiter: passwordResetRequestLimiter,
		firstPasswordStartLimiter:   firstPasswordStartLimiter,
		firstPasswordFinishLimiter:  firstPasswordFinishLimiter,
		oidcLinkStartLimiter:        oidcLinkStartLimiter,
		currentPasswordLimiter:      currentPasswordLimiter,
		secondFactorLimiter:         secondFactorLimiter,
		totpLimiter:                 totpLimiter,
		recoveryCodeLimiter:         recoveryCodeLimiter,
		smtpConfigured:              smtpConfigured,
		publicBaseURL:               config.NormalizeBaseURL(opts.PublicBaseURL),
		trustProxy:                  opts.TrustProxy,
		webFS:                       webFS,
		fileSrv:                     http.FileServer(http.FS(webFS)),
		indexHTML:                   indexHTML,
		landingHTML:                 landingHTML,
		landingHTMLByLocale:         landingHTMLByLocale,
		swJS:                        swJS,
		mcpHandler:                  opts.MCPHandler,
		agoraHandler:                opts.AgoraHandler,
		vapidPublicKey:              vapidPub,
		pushVapidConfigured:         pushVapidConfigured,
		pushDebug:                   pushDebug,
		wallEnabled:                 opts.WallEnabled,
		markdownNotesEnabled:        opts.MarkdownNotesEnabled,
		mermaidNotesEnabled:         opts.MermaidNotesEnabled && opts.MarkdownNotesEnabled,
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Apply proxy-aware scheme and host so cookies and redirects use the client-facing URL.
	if isSecureRequest(r) {
		if r.URL.Scheme != "https" {
			r.URL.Scheme = "https"
		}
		if h := strings.TrimSpace(r.Header.Get("X-Forwarded-Host")); h != "" {
			r.URL.Host = h
		}
	}

	start := time.Now()
	// Log immediately to catch requests that hang before completion
	if r.URL.Path == "/api/backup/import" {
		s.logger.Printf("INCOMING: %s %s (Content-Length: %s)", r.Method, r.URL.Path, r.Header.Get("Content-Length"))
	}
	defer func() {
		s.logger.Printf("%s %s %dms", r.Method, r.URL.Path, time.Since(start).Milliseconds())
	}()

	if r.URL.Path == "/healthz" {
		s.handleHealthz(w, r)
		return
	}

	if s.agoraHandler != nil && (r.URL.Path == "/agora/v1" || strings.HasPrefix(r.URL.Path, "/agora/v1/")) {
		s.agoraHandler.ServeHTTP(w, r)
		return
	}

	if s.mcpHandler != nil && (r.URL.Path == "/mcp" || strings.HasPrefix(r.URL.Path, "/mcp/")) {
		s.mcpHandler.ServeHTTP(w, r)
		return
	}

	if r.URL.Path == "/.well-known/oauth-protected-resource" {
		s.handleOAuthProtectedResourceMetadata(w, r)
		return
	}
	if r.URL.Path == "/.well-known/oauth-authorization-server" {
		s.handleOAuthASMetadata(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/oauth/") {
		s.handleOAuth(w, r)
		return
	}

	if strings.HasPrefix(r.URL.Path, "/api/") {
		s.handleAPI(w, r)
		return
	}

	s.handleSPA(w, r)
}

// requestContext is the HTTP credential-to-actor boundary.
// It may attach actor identity from a valid session cookie, but it does not
// authorize any operation on its own. Handlers may still do coarse
// auth-required checks for HTTP behavior, while store methods remain the
// authority for project/todo/user authorization.
func (s *Server) requestContext(r *http.Request) context.Context {
	ctx := r.Context()

	// Anonymous mode intentionally establishes no actor, even if a valid session
	// cookie is present. This keeps anonymous temp boards creator-less and out of
	// authenticated user listings.
	if s.mode == "anonymous" {
		return ctx // Do not extract user from session cookie
	}

	// Best-effort actor establishment only. Missing/invalid cookies fall through
	// as unauthenticated requests; later handler/store code decides whether that
	// is allowed for the specific operation.
	c, err := r.Cookie("scrumboy_session")
	if err != nil || c == nil || c.Value == "" {
		return ctx
	}
	u, err := s.store.GetUserBySessionToken(ctx, c.Value)
	if err != nil {
		return ctx
	}
	ctx = store.WithUserID(ctx, u.ID)
	ctx = store.WithUserEmail(ctx, u.Email)
	ctx = store.WithUserName(ctx, u.Name)
	return ctx
}

func (s *Server) storeMode() store.Mode {
	mode, _ := store.ParseMode(s.mode)
	if mode == "" {
		return store.ModeFull // Default
	}
	return mode
}

// Close stops accepting new delivery-queue entries, links each worker's
// retry context to ctx (so observing ctx cancellation stops further
// drain/retry work—including an already-running flush), cancels each
// worker's accept loop, and waits (also bounded by ctx) for the drain to
// finish. An in-flight send may complete under its own transport timeout.
// Once a worker observes close-context cancellation, it starts no further
// queued item or send attempt. Call from main on shutdown.
func (s *Server) Close(ctx context.Context) {
	if s.webhookQueue != nil {
		s.webhookQueue.Seal()
	}
	if s.mailQueue != nil {
		s.mailQueue.Seal()
	}
	if s.webhookWorker != nil {
		s.webhookWorker.beginShutdown(ctx)
	}
	if s.mailWorker != nil {
		s.mailWorker.beginShutdown(ctx)
	}
	if s.webhookCancel != nil {
		s.webhookCancel()
	}
	if s.mailCancel != nil {
		s.mailCancel()
	}

	for _, w := range []struct {
		name string
		done <-chan struct{}
	}{
		{"webhook", s.webhookDone},
		{"mail", s.mailDone},
	} {
		if w.done == nil {
			continue
		}
		select {
		case <-w.done:
		case <-ctx.Done():
			s.logger.Printf("shutdown: %s worker flush did not finish before deadline", w.name)
		}
	}
}

// PublishEvent sends an event through the fanout to all consumers (SSE bridge, webhooks, etc.).
// Best-effort: callers should not fail HTTP requests on publish errors.
func (s *Server) PublishEvent(ctx context.Context, e eventbus.Event) {
	if s.fanout == nil {
		return
	}
	_ = s.fanout.Publish(ctx, e)
}

// PublishTodoAssigned emits a "todo.assigned" event through the event bus.
// Designed to be passed to store.SetTodoAssignedPublisher.
func (s *Server) PublishTodoAssigned(ctx context.Context, projectID, todoID, localID int64, title, projectSlug string, from, to *int64, actorUserID int64) {
	payload, _ := json.Marshal(eventbus.TodoAssignedPayload{
		ProjectID:       projectID,
		ProjectSlug:     projectSlug,
		TodoID:          todoID,
		LocalID:         localID,
		Title:           title,
		FromAssigneeUID: from,
		ToAssigneeUID:   to,
		ActorUserID:     actorUserID,
	})
	s.PublishEvent(ctx, eventbus.Event{
		Type:      "todo.assigned",
		ProjectID: projectID,
		Payload:   payload,
	})
}
