package githubbroker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type grantKey struct {
	instance string
	installation int64
	repository int64
}

type Snapshot struct {
	clients map[[32]byte]string
	grants map[grantKey]Grant
}

type snapshotDocument struct {
	Clients []struct {
		InstanceID string `json:"instanceId"`
		CertificateSHA256 string `json:"certificateSha256"`
	} `json:"clients"`
	Grants []struct {
		InstanceID string `json:"instanceId"`
		InstallationID int64 `json:"installationId"`
		RepositoryID int64 `json:"repositoryId"`
		Repository string `json:"repository"`
		ExpiresAt time.Time `json:"expiresAt"`
	} `json:"grants"`
}

func LoadSnapshot(path string) (*Snapshot, error) {
	if path == "" {
		return nil, ErrUnavailable
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, 1<<20+1))
	if err != nil || len(content) > 1<<20 {
		return nil, ErrUnavailable
	}
	var document snapshotDocument
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&document) != nil {
		return nil, ErrUnavailable
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) || len(document.Clients) == 0 ||
		len(document.Clients) > 256 || len(document.Grants) == 0 || len(document.Grants) > 2048 {
		return nil, ErrUnavailable
	}
	clients := make(map[[32]byte]string, len(document.Clients))
	instances := make(map[string]bool, len(document.Clients))
	for _, entry := range document.Clients {
		if !validInstance(entry.InstanceID) || instances[entry.InstanceID] ||
			len(entry.CertificateSHA256) != 64 {
			return nil, ErrUnavailable
		}
		bytes, err := hex.DecodeString(entry.CertificateSHA256)
		if err != nil || len(bytes) != sha256.Size {
			return nil, ErrUnavailable
		}
		var digest [32]byte
		copy(digest[:], bytes)
		if clients[digest] != "" {
			return nil, ErrUnavailable
		}
		clients[digest] = entry.InstanceID
		instances[entry.InstanceID] = true
	}
	grants := make(map[grantKey]Grant, len(document.Grants))
	now := time.Now()
	for _, entry := range document.Grants {
		grant := Grant{
			InstanceID: entry.InstanceID,
			InstallationID: entry.InstallationID,
			RepositoryID: entry.RepositoryID,
			Repository: entry.Repository,
			ExpiresAt: entry.ExpiresAt,
		}
		if !instances[entry.InstanceID] ||
			!validScope(grant, entry.InstanceID, entry.InstallationID, entry.RepositoryID, now) ||
			entry.ExpiresAt.After(now.Add(30*24*time.Hour)) {
			return nil, ErrUnavailable
		}
		key := grantKey{entry.InstanceID, entry.InstallationID, entry.RepositoryID}
		if _, exists := grants[key]; exists {
			return nil, ErrUnavailable
		}
		grants[key] = grant
	}
	return &Snapshot{clients: clients, grants: grants}, nil
}

func (s *Snapshot) Identify(r *http.Request) (string, error) {
	if s == nil || r == nil || r.TLS == nil || len(r.TLS.PeerCertificates) == 0 ||
		len(r.TLS.VerifiedChains) == 0 {
		return "", ErrDenied
	}
	digest := sha256.Sum256(r.TLS.PeerCertificates[0].Raw)
	instance, ok := s.clients[digest]
	if !ok || !validInstance(instance) {
		return "", ErrDenied
	}
	return instance, nil
}

func (s *Snapshot) Lookup(ctx context.Context, instance string, installationID, repositoryID int64) (Grant, error) {
	if s == nil || ctx.Err() != nil {
		return Grant{}, ErrDenied
	}
	grant, ok := s.grants[grantKey{instance, installationID, repositoryID}]
	if !ok || !validScope(grant, instance, installationID, repositoryID, time.Now()) {
		return Grant{}, ErrDenied
	}
	return grant, nil
}

var _ Authenticator = (*Snapshot)(nil)
var _ Grants = (*Snapshot)(nil)
