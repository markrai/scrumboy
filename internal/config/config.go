package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	BindAddr            string
	DataDir             string
	DBPath              string
	MaxRequestBodyBytes int64

	SQLiteBusyTimeout int
	SQLiteJournalMode string
	SQLiteSynchronous string

	ScrumboyMode string // "full" or "anonymous", default "full"

	// TwoFactorEncryptionKey is a base64-encoded 32-byte key for AES-256-GCM encryption of TOTP secrets.
	// Set via SCRUMBOY_ENCRYPTION_KEY. Generate with: openssl rand -base64 32
	TwoFactorEncryptionKey string

	// TLS (optional). If both TLSCertFile and TLSKeyFile exist, server uses HTTPS. Used by f.bat/a.bat with mkcert.
	TLSCertFile string // default ./cert.pem
	TLSKeyFile  string // default ./key.pem
	// IntranetIP is the LAN IP to log for intranet access (e.g. 192.168.1.250). Set via SCRUMBOY_INTRANET_IP.
	IntranetIP string

	// OIDC (optional). All four required fields must be set to enable OIDC login.
	OIDCIssuer            string // Raw issuer URL from SCRUMBOY_OIDC_ISSUER
	OIDCIssuerCanonical   string // Normalized once: trimmed, no trailing slash
	OIDCClientID          string
	OIDCClientSecret      string
	OIDCRedirectURL       string // Absolute callback URL
	OIDCLocalAuthDisabled bool   // If true, disable password login/bootstrap when OIDC is configured

	// Web Push VAPID (optional). Both public and private must be set for push subscribe and assignment notifications.
	VAPIDPublicKey  string
	VAPIDPrivateKey string
	VAPIDSubscriber string // mailto: or https: URL for VAPID JWT sub; plain email normalized to mailto:
	PushDebug       bool   // SCRUMBOY_DEBUG_PUSH=1
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

	return Config{
		BindAddr:            getenv("BIND_ADDR", ":8080"),
		DataDir:             dataDir,
		DBPath:              dbPath,
		MaxRequestBodyBytes: int64(getenvInt("MAX_REQUEST_BODY_BYTES", 1<<20)), // 1 MiB

		SQLiteBusyTimeout: getenvInt("SQLITE_BUSY_TIMEOUT_MS", 30000), // 30 seconds for write-heavy operations
		SQLiteJournalMode: getenv("SQLITE_JOURNAL_MODE", "WAL"),
		SQLiteSynchronous: getenv("SQLITE_SYNCHRONOUS", "FULL"),

		ScrumboyMode: mode,
		// Trim whitespace so keys from .env / copy-paste decode (base64 is sensitive to newlines).
		TwoFactorEncryptionKey: strings.TrimSpace(os.Getenv("SCRUMBOY_ENCRYPTION_KEY")),

		TLSCertFile: getenv("SCRUMBOY_TLS_CERT", "./cert.pem"),
		TLSKeyFile:  getenv("SCRUMBOY_TLS_KEY", "./key.pem"),
		IntranetIP:  getenv("SCRUMBOY_INTRANET_IP", "192.168.1.250"),

		OIDCIssuer:            strings.TrimSpace(os.Getenv("SCRUMBOY_OIDC_ISSUER")),
		OIDCIssuerCanonical:   normalizeIssuer(os.Getenv("SCRUMBOY_OIDC_ISSUER")),
		OIDCClientID:          strings.TrimSpace(os.Getenv("SCRUMBOY_OIDC_CLIENT_ID")),
		OIDCClientSecret:      strings.TrimSpace(os.Getenv("SCRUMBOY_OIDC_CLIENT_SECRET")),
		OIDCRedirectURL:       strings.TrimSpace(os.Getenv("SCRUMBOY_OIDC_REDIRECT_URL")),
		OIDCLocalAuthDisabled: strings.TrimSpace(strings.ToLower(os.Getenv("SCRUMBOY_OIDC_LOCAL_AUTH_DISABLED"))) == "true",

		VAPIDPublicKey:       strings.TrimSpace(os.Getenv("SCRUMBOY_VAPID_PUBLIC_KEY")),
		VAPIDPrivateKey:      strings.TrimSpace(os.Getenv("SCRUMBOY_VAPID_PRIVATE_KEY")),
		VAPIDSubscriber:      NormalizeVAPIDSubscriber(os.Getenv("SCRUMBOY_VAPID_SUBSCRIBER")),
		PushDebug: strings.TrimSpace(os.Getenv("SCRUMBOY_DEBUG_PUSH")) == "1",
	}
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
