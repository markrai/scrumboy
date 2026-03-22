package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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

		ScrumboyMode:           mode,
		TwoFactorEncryptionKey: getenv("SCRUMBOY_ENCRYPTION_KEY", ""),

		TLSCertFile: getenv("SCRUMBOY_TLS_CERT", "./cert.pem"),
		TLSKeyFile:  getenv("SCRUMBOY_TLS_KEY", "./key.pem"),
		IntranetIP:  getenv("SCRUMBOY_INTRANET_IP", "192.168.1.250"),
	}
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
