package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	BindAddr             string
	DataDir              string
	DBPath               string
	MaxRequestBodyBytes  int64
	MaxTrelloImportBytes int64

	SQLiteBusyTimeout int
	SQLiteJournalMode string
	SQLiteSynchronous string

	ScrumboyMode string // "full" or "anonymous", default "full"

	// PublicProjectsEnabled is the operator-controlled capability gate for
	// durable-project publication management. It defaults off.
	PublicProjectsEnabled bool

	// LandingPageEnabled is the independent presentation gate for a future
	// Full Mode landing page. Phase 1 only propagates the value; routing is
	// unchanged. It defaults off.
	LandingPageEnabled bool

	// TwoFactorEncryptionKey is a base64-encoded 32-byte key for AES-256-GCM encryption of TOTP secrets.
	// Set via SCRUMBOY_ENCRYPTION_KEY. Generate with: openssl rand -base64 32
	TwoFactorEncryptionKey string

	// TLS (optional). If both TLSCertFile and TLSKeyFile exist, server uses HTTPS. Used by f.bat/a.bat with mkcert.
	TLSCertFile string // default ./cert.pem
	TLSKeyFile  string // default ./key.pem
	// IntranetIP is an optional LAN IP for startup intranet URL / mkcert hints
	// (e.g. 192.0.2.10). Set via SCRUMBOY_INTRANET_IP; empty when unset. Does not bind.
	IntranetIP string

	// OIDC (optional). All four required fields must be set to enable OIDC login.
	OIDCIssuer            string // Raw issuer URL from SCRUMBOY_OIDC_ISSUER
	OIDCIssuerCanonical   string // Normalized once: trimmed, no trailing slash
	OIDCClientID          string
	OIDCClientSecret      string
	OIDCRedirectURL       string // Absolute callback URL
	OIDCLocalAuthDisabled bool   // If true, disable password login/bootstrap when OIDC is configured
	// OIDCAllowedEmailDomains restricts which email domains may auto-provision a new
	// account on first SSO login. Empty means unrestricted. Set via
	// SCRUMBOY_OIDC_ALLOWED_EMAIL_DOMAINS as a comma-separated list (e.g. "example.com,example.org").
	// Only gates signup of brand-new users; existing users can still log in regardless of domain.
	OIDCAllowedEmailDomains []string

	// Web Push VAPID (optional). Both public and private must be set for push subscribe and assignment notifications.
	VAPIDPublicKey  string
	VAPIDPrivateKey string
	VAPIDSubscriber string // mailto: or https: URL for VAPID JWT sub; plain email normalized to mailto:
	PushDebug       bool   // SCRUMBOY_DEBUG_PUSH=1

	// Scrumbaby (sticky-note wall). Defaults to on for new installs. Set
	// SCRUMBOY_WALL_ENABLED=0 (or false/off/no, case-insensitive) to disable.
	// Durable projects only; anonymous/temp boards never expose the wall.
	WallEnabled bool

	// Markdown notes preview. Defaults on; opt out with
	// SCRUMBOY_MARKDOWN_NOTES_ENABLED=0 (see defaultOnFeatureFromEnv).
	MarkdownNotesEnabled bool

	// Mermaid notes preview. Defaults on; opt out with
	// SCRUMBOY_MERMAID_NOTES_ENABLED=0. Already false when MarkdownNotesEnabled is false.
	MermaidNotesEnabled bool

	// Inline images in story notes. Defaults on; opt out with
	// SCRUMBOY_INLINE_IMAGES_ENABLED=0. This is the operator setting; the effective
	// value is InlineImagesActive (Full mode with markdown notes enabled).
	InlineImagesEnabled bool
	// Byte budgets for stored note images (0 disables that budget).
	// SCRUMBOY_ASSET_PROJECT_BUDGET_MB / SCRUMBOY_ASSET_INSTANCE_BUDGET_MB.
	AssetProjectBudgetBytes  int64
	AssetInstanceBudgetBytes int64

	// SMTP (optional). Enables self-service "forgot password" emails. Host and
	// From are required; Port defaults to 587 when omitted. Invalid explicit
	// port values become 0 (SMTP stays off). Username/Password are optional
	// (some relays allow trusted-network submission without auth).
	SMTPHost         string // SCRUMBOY_SMTP_HOST
	SMTPPort         int    // SCRUMBOY_SMTP_PORT, default defaultSMTPPort when unset
	SMTPPortExplicit bool   // true when SCRUMBOY_SMTP_PORT key is present in the environment
	SMTPUsername     string // SCRUMBOY_SMTP_USERNAME (optional)
	SMTPPassword     string // SCRUMBOY_SMTP_PASSWORD (optional; never logged)
	SMTPFrom         string // SCRUMBOY_SMTP_FROM, e.g. "Scrumboy <no-reply@example.com>"
	SMTPTLSMode      string // SCRUMBOY_SMTP_TLS_MODE: "starttls" (default) | "implicit" | "none"
	SMTPDebug        bool   // SCRUMBOY_SMTP_DEBUG=1 — log send attempts (never credentials/body)

	// PublicBaseURL (SCRUMBOY_PUBLIC_BASE_URL). Required for self-service
	// password-reset emails: missing or invalid values fail closed (no email
	// sent). When set to a valid absolute http/https origin, reset links use
	// this origin for both self-service email and admin-generated links, and
	// OAuth discovery uses it as the canonical issuer origin.
	// Example: "https://scrumboy.example.com".
	PublicBaseURL string

	// TrustProxy (SCRUMBOY_TRUST_PROXY). When true, authentication and OAuth
	// rate-limit IP keys honor X-Forwarded-For (first hop). Default false: use
	// RemoteAddr only so clients cannot spoof the per-IP limiter. Enable only
	// when a reverse proxy is the sole network path and overwrites/strips XFF.
	// Without PublicBaseURL, OAuth discovery also requires forwarded HTTPS and
	// an explicit X-Forwarded-Host; it never falls back to the request Host.
	TrustProxy bool

	// Warnings lists configuration values FromEnv did not recognize and how it
	// treated them, for the caller to log at startup.
	Warnings []string
}

func FromEnv() Config {
	dataDir, dbPath, err := ResolveDataDir("")
	if err != nil {
		panic(err)
	}

	mode := getenv("SCRUMBOY_MODE", "full")
	if mode != "full" && mode != "anonymous" {
		mode = "full" // Default to full if invalid
	}

	var warnings []string
	markdownNotesEnabled := defaultOnFeatureFromEnv("SCRUMBOY_MARKDOWN_NOTES_ENABLED", &warnings)
	mermaidNotesEnabled := defaultOnFeatureFromEnv("SCRUMBOY_MERMAID_NOTES_ENABLED", &warnings) && markdownNotesEnabled
	inlineImagesEnabled := defaultOnFeatureFromEnv("SCRUMBOY_INLINE_IMAGES_ENABLED", &warnings)
	smtpPort, smtpPortExplicit := smtpPortFromEnv()

	return Config{
		BindAddr:             getenv("BIND_ADDR", ":8080"),
		DataDir:              dataDir,
		DBPath:               dbPath,
		MaxRequestBodyBytes:  int64(getenvInt("MAX_REQUEST_BODY_BYTES", 1<<20)),   // 1 MiB
		MaxTrelloImportBytes: int64(getenvInt("MAX_TRELLO_IMPORT_BYTES", 32<<20)), // 32 MiB

		SQLiteBusyTimeout: getenvInt("SQLITE_BUSY_TIMEOUT_MS", 30000), // 30 seconds for write-heavy operations
		SQLiteJournalMode: getenv("SQLITE_JOURNAL_MODE", "WAL"),
		SQLiteSynchronous: getenv("SQLITE_SYNCHRONOUS", "FULL"),

		ScrumboyMode:          mode,
		PublicProjectsEnabled: optInFeatureEnabledFromEnv("SCRUMBOY_PUBLIC_PROJECTS_ENABLED"),
		LandingPageEnabled:    optInFeatureEnabledFromEnv("SCRUMBOY_LANDING_PAGE_ENABLED"),
		// Trim whitespace so keys from .env / copy-paste decode (base64 is sensitive to newlines).
		TwoFactorEncryptionKey: strings.TrimSpace(os.Getenv("SCRUMBOY_ENCRYPTION_KEY")),

		TLSCertFile: getenv("SCRUMBOY_TLS_CERT", "./cert.pem"),
		TLSKeyFile:  getenv("SCRUMBOY_TLS_KEY", "./key.pem"),
		IntranetIP:  getenv("SCRUMBOY_INTRANET_IP", ""),

		OIDCIssuer:              strings.TrimSpace(os.Getenv("SCRUMBOY_OIDC_ISSUER")),
		OIDCIssuerCanonical:     normalizeIssuer(os.Getenv("SCRUMBOY_OIDC_ISSUER")),
		OIDCClientID:            strings.TrimSpace(os.Getenv("SCRUMBOY_OIDC_CLIENT_ID")),
		OIDCClientSecret:        strings.TrimSpace(os.Getenv("SCRUMBOY_OIDC_CLIENT_SECRET")),
		OIDCRedirectURL:         strings.TrimSpace(os.Getenv("SCRUMBOY_OIDC_REDIRECT_URL")),
		OIDCLocalAuthDisabled:   strings.TrimSpace(strings.ToLower(os.Getenv("SCRUMBOY_OIDC_LOCAL_AUTH_DISABLED"))) == "true",
		OIDCAllowedEmailDomains: oidcAllowedEmailDomainsFromEnv(),

		VAPIDPublicKey:  strings.TrimSpace(os.Getenv("SCRUMBOY_VAPID_PUBLIC_KEY")),
		VAPIDPrivateKey: strings.TrimSpace(os.Getenv("SCRUMBOY_VAPID_PRIVATE_KEY")),
		VAPIDSubscriber: NormalizeVAPIDSubscriber(os.Getenv("SCRUMBOY_VAPID_SUBSCRIBER")),
		PushDebug:       strings.TrimSpace(os.Getenv("SCRUMBOY_DEBUG_PUSH")) == "1",

		WallEnabled:          wallEnabledFromEnv(),
		MarkdownNotesEnabled: markdownNotesEnabled,
		MermaidNotesEnabled:  mermaidNotesEnabled,

		InlineImagesEnabled:      inlineImagesEnabled,
		AssetProjectBudgetBytes:  assetBudgetBytesFromEnv("SCRUMBOY_ASSET_PROJECT_BUDGET_MB", 1024),
		AssetInstanceBudgetBytes: assetBudgetBytesFromEnv("SCRUMBOY_ASSET_INSTANCE_BUDGET_MB", 10240),

		SMTPHost:         strings.TrimSpace(os.Getenv("SCRUMBOY_SMTP_HOST")),
		SMTPPort:         smtpPort,
		SMTPPortExplicit: smtpPortExplicit,
		SMTPUsername:     strings.TrimSpace(os.Getenv("SCRUMBOY_SMTP_USERNAME")),
		SMTPPassword:     strings.TrimSpace(os.Getenv("SCRUMBOY_SMTP_PASSWORD")),
		SMTPFrom:         strings.TrimSpace(os.Getenv("SCRUMBOY_SMTP_FROM")),
		SMTPTLSMode:      normalizeSMTPTLSMode(os.Getenv("SCRUMBOY_SMTP_TLS_MODE")),
		SMTPDebug:        strings.TrimSpace(os.Getenv("SCRUMBOY_SMTP_DEBUG")) == "1",

		PublicBaseURL: NormalizeBaseURL(os.Getenv("SCRUMBOY_PUBLIC_BASE_URL")),
		TrustProxy:    trustProxyFromEnv(),

		Warnings: warnings,
	}
}

// NormalizeBaseURL parses SCRUMBOY_PUBLIC_BASE_URL into a canonical public
// origin (scheme://host[:port]). Empty or invalid input returns "" so
// self-service password-reset email fails closed. Valid: absolute http or
// https, hostname required, optional port in 1..65535, no userinfo, path only
// empty or "/", no query (including bare ?) or fragment.
func NormalizeBaseURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}

	if u.Opaque != "" ||
		(!strings.EqualFold(u.Scheme, "http") &&
			!strings.EqualFold(u.Scheme, "https")) ||
		u.Hostname() == "" ||
		u.User != nil ||
		u.ForceQuery ||
		u.RawQuery != "" ||
		u.Fragment != "" {
		return ""
	}

	escapedPath := u.EscapedPath()
	if escapedPath != "" && escapedPath != "/" {
		return ""
	}

	// Reject dangling colon in authority (e.g. https://host:, http://[::1]:).
	// Valid IPv6 without a port ends in ], not :.
	if strings.HasSuffix(u.Host, ":") {
		return ""
	}

	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return ""
		}
	}

	return strings.ToLower(u.Scheme) + "://" + u.Host
}

// normalizeSMTPTLSMode validates SCRUMBOY_SMTP_TLS_MODE. Unrecognized or empty
// values default to "starttls" (correct for the conventional port 587) —
// the mode is always explicit config, never inferred from the port number.
func normalizeSMTPTLSMode(raw string) string {
	v := strings.ToLower(strings.TrimSpace(raw))
	switch v {
	case "implicit", "none":
		return v
	default:
		return "starttls"
	}
}

// wallEnabledFromEnv returns whether the Scrumbaby wall is enabled. Default
// is true when the variable is unset or empty so fresh installs get the
// feature without extra configuration. Explicit opt-out: SCRUMBOY_WALL_ENABLED=0
// (also accepts false, off, no — trimmed, case-insensitive).
func wallEnabledFromEnv() bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv("SCRUMBOY_WALL_ENABLED")))
	switch v {
	case "0", "false", "off", "no":
		return false
	default:
		return true
	}
}

// defaultOnFeatureFromEnv parses enabled-by-default feature flags (Markdown
// notes, Mermaid, inline images). Unset or empty enables; 0/false/off/no
// disables; 1/true/on/yes enables (trimmed, case-insensitive). Any other
// nonempty value disables the feature rather than guessing, and is reported in
// warnings so the operator sees it at startup.
func defaultOnFeatureFromEnv(key string, warnings *[]string) bool {
	raw := os.Getenv(key)
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "", "1", "true", "on", "yes":
		return true
	case "0", "false", "off", "no":
		return false
	default:
		*warnings = append(*warnings, fmt.Sprintf("%s=%q is not recognized (use 1 or 0); feature disabled", key, raw))
		return false
	}
}

// optInFeatureEnabledFromEnv parses disabled-by-default feature flags. Only
// the repository's explicit truthy values enable a feature; every other value
// fails closed.
func optInFeatureEnabledFromEnv(key string) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	switch v {
	case "1", "true", "on", "yes":
		return true
	default:
		return false
	}
}

// trustProxyFromEnv returns whether rate-limit IP keys may honor
// X-Forwarded-For. Default false unless explicitly opted in with
// 1/true/on/yes (trimmed, case-insensitive).
func trustProxyFromEnv() bool {
	return optInFeatureEnabledFromEnv("SCRUMBOY_TRUST_PROXY")
}

// InlineImagesActive reports whether inline images are effective: enabled,
// with markdown notes enabled, in Full mode. The HTTP server additionally
// requires a data directory and asset storage.
func (c Config) InlineImagesActive() bool {
	return c.InlineImagesEnabled && c.MarkdownNotesEnabled && c.ScrumboyMode == "full"
}

// OIDCEnabled returns true if all required OIDC env vars are set.
func (c Config) OIDCEnabled() bool {
	return c.OIDCIssuerCanonical != "" && c.OIDCClientID != "" && c.OIDCClientSecret != "" && c.OIDCRedirectURL != ""
}

func normalizeIssuer(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimRight(s, "/")
	return s
}

// oidcAllowedEmailDomainsFromEnv parses SCRUMBOY_OIDC_ALLOWED_EMAIL_DOMAINS as
// a comma-separated list of domains, trimmed, lowercased, and with any leading
// "@" stripped. Empty entries are dropped. An unset/empty variable returns nil,
// meaning no domain restriction.
func oidcAllowedEmailDomainsFromEnv() []string {
	raw := os.Getenv("SCRUMBOY_OIDC_ALLOWED_EMAIL_DOMAINS")
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var domains []string
	for _, part := range strings.Split(raw, ",") {
		d := strings.ToLower(strings.TrimSpace(part))
		d = strings.TrimPrefix(d, "@")
		if d != "" {
			domains = append(domains, d)
		}
	}
	return domains
}

// ResolveDataDir returns the resolved data directory and db path.
// DATA_DIR overrides the default ./data for local development.
func ResolveDataDir(dataDirOverride string) (string, string, error) {
	dataDir := dataDirOverride
	sqlitePath := os.Getenv("SQLITE_PATH")
	if dataDir == "" {
		if sqlitePath != "" {
			dataDir = filepath.Dir(sqlitePath)
		} else {
			dataDir = getenv("DATA_DIR", "./data")
		}
	}

	if dataDir == "" {
		return "", "", fmt.Errorf("data dir is empty")
	}

	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return "", "", fmt.Errorf("create data dir: %w", err)
	}

	// Fail fast if the directory is not writable.
	f, err := os.CreateTemp(dataDir, ".writetest-*")
	if err != nil {
		return "", "", fmt.Errorf("data dir not writable: %w", err)
	}
	_ = f.Close()
	_ = os.Remove(f.Name())

	dbPath := sqlitePath
	if dbPath == "" || dataDirOverride != "" {
		dbPath = filepath.Join(dataDir, "app.db")
	}

	return dataDir, dbPath, nil
}

func getenv(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

const defaultSMTPPort = 587

// smtpPortFromEnv parses SCRUMBOY_SMTP_PORT. When unset, returns the default
// port and explicit=false. When set, explicit=true; invalid or out-of-range
// values fail closed to port 0.
func smtpPortFromEnv() (port int, explicit bool) {
	raw, ok := os.LookupEnv("SCRUMBOY_SMTP_PORT")
	if !ok {
		return defaultSMTPPort, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 1 || n > 65535 {
		return 0, true
	}
	return n, true
}

// assetBudgetBytesFromEnv reads a budget in MiB. 0 disables the budget; negative or invalid
// values fall back to the default.
func assetBudgetBytesFromEnv(key string, defaultMB int) int64 {
	mb := getenvInt(key, defaultMB)
	if mb < 0 {
		mb = defaultMB
	}
	return int64(mb) << 20
}

func getenvInt(key string, defaultValue int) int {
	v := os.Getenv(key)
	if v == "" {
		return defaultValue
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return defaultValue
	}
	return n
}
