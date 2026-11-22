package githubcredentials

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"scrumboy/internal/integration/githubmanifest"
)

var ErrAlreadyConfigured = errors.New("GitHub App credentials already exist")
var ErrInvalidCredentials = errors.New("GitHub App credentials are invalid")
var ErrInsecureStorage = errors.New("GitHub App credential storage is not private")

const directoryName = ".scrumboy-github-app"
const credentialsFileName = "credentials.json"
const maxCredentialsBytes = 96 * 1024

type Bundle struct {
	Version int `json:"version"`
	AppID int64 `json:"appId"`
	Slug string `json:"slug"`
	PrivateKey string `json:"privateKey"`
	WebhookSecret string `json:"webhookSecret"`
}

type Store struct {
	dataDir string
	mu sync.Mutex
}

func New(dataDir string) (*Store, error) {
	if dataDir == "" {
		return nil, ErrInsecureStorage
	}
	info, err := os.Stat(dataDir)
	if err != nil || !info.IsDir() {
		return nil, ErrInsecureStorage
	}
	return &Store{dataDir: dataDir}, nil
}

func (s *Store) directory() string {
	return filepath.Join(s.dataDir, directoryName)
}

func (s *Store) Load() (githubmanifest.Credentials, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	root := s.directory()
	info, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return githubmanifest.Credentials{}, false, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return githubmanifest.Credentials{}, false, ErrInsecureStorage
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return githubmanifest.Credentials{}, false, ErrInsecureStorage
	}
	name := filepath.Join(root, credentialsFileName)
	fileInfo, err := os.Lstat(name)
	if err != nil || !fileInfo.Mode().IsRegular() || fileInfo.Size() > maxCredentialsBytes ||
		(runtime.GOOS != "windows" && fileInfo.Mode().Perm()&0077 != 0) {
		return githubmanifest.Credentials{}, false, ErrInsecureStorage
	}
	f, err := os.Open(name)
	if err != nil {
		return githubmanifest.Credentials{}, false, ErrInsecureStorage
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxCredentialsBytes+1))
	if err != nil || len(data) > maxCredentialsBytes {
		return githubmanifest.Credentials{}, false, ErrInvalidCredentials
	}
	var bundle Bundle
	if err := json.Unmarshal(data, &bundle); err != nil || bundle.Version != 1 ||
		bundle.AppID <= 0 || bundle.Slug == "" || len(bundle.PrivateKey) == 0 ||
		len(bundle.PrivateKey) > 64*1024 || len(bundle.WebhookSecret) < 32 ||
		len(bundle.WebhookSecret) > 4096 {
		return githubmanifest.Credentials{}, false, ErrInvalidCredentials
	}
	return githubmanifest.Credentials{
		AppID: bundle.AppID,
		Slug: bundle.Slug,
		PrivateKey: []byte(bundle.PrivateKey),
		WebhookSecret: bundle.WebhookSecret,
	}, true, nil
}

func (s *Store) Save(c githubmanifest.Credentials) error {
	if c.AppID <= 0 || c.Slug == "" || len(c.PrivateKey) == 0 || len(c.PrivateKey) > 64*1024 ||
		len(c.WebhookSecret) < 32 || len(c.WebhookSecret) > 4096 {
		return ErrInvalidCredentials
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	dest := s.directory()
	_, err := os.Lstat(dest)
	if err == nil {
		return ErrAlreadyConfigured
	}
	if !errors.Is(err, os.ErrNotExist) {
		return ErrInsecureStorage
	}
	stage, err := os.MkdirTemp(s.dataDir, ".scrumboy-github-staging-*")
	if err != nil {
		return ErrInsecureStorage
	}
	defer os.RemoveAll(stage)
	if err := os.Chmod(stage, 0700); err != nil {
		return ErrInsecureStorage
	}
	bundle := Bundle{Version: 1, AppID: c.AppID, Slug: c.Slug, PrivateKey: string(c.PrivateKey), WebhookSecret: c.WebhookSecret}
	data, err := json.Marshal(bundle)
	if err != nil || len(data) > maxCredentialsBytes {
		return ErrInvalidCredentials
	}
	file, err := os.OpenFile(filepath.Join(stage, credentialsFileName), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return ErrInsecureStorage
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return ErrInsecureStorage
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return ErrInsecureStorage
	}
	if err := file.Close(); err != nil {
		return ErrInsecureStorage
	}
	if err := syncDirectory(stage); err != nil {
		return ErrInsecureStorage
	}
	if _, err := os.Lstat(dest); err == nil {
		return ErrAlreadyConfigured
	} else if !errors.Is(err, os.ErrNotExist) {
		return ErrInsecureStorage
	}
	if err := os.Rename(stage, dest); err != nil {
		return ErrInsecureStorage
	}
	if err := syncDirectory(s.dataDir); err != nil {
		return ErrInsecureStorage
	}
	return nil
}

func syncDirectory(path string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
