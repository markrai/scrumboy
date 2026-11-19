package githubbroker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"scrumboy/internal/integration"
)

var ErrDenied = errors.New("broker access denied")
var ErrUnavailable = errors.New("broker authorization unavailable")

const maxRequestBytes int64 = 4096

var repositoryNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

type Authenticator interface {
	Identify(*http.Request) (string, error)
}

type Grant struct {
	InstanceID string
	InstallationID int64
	RepositoryID int64
	Repository string
	ExpiresAt time.Time
}

type Grants interface {
	Lookup(context.Context, string, int64, int64) (Grant, error)
}

type Provider interface {
	integration.IssuePullRequestProvider
}

type Broker struct {
	auth Authenticator
	grants Grants
	provider Provider
	now func() time.Time
	rate *rateLimiter
}

func New(auth Authenticator, grants Grants, provider Provider) (*Broker, error) {
	if auth == nil || grants == nil || provider == nil {
		return nil, ErrUnavailable
	}
	return &Broker{
		auth: auth, grants: grants, provider: provider, now: time.Now,
		rate: &rateLimiter{entries: make(map[string]rateEntry), limit: 120, globalLimit: 3000, window: time.Minute},
	}, nil
}

type requestData struct {
	InstallationID int64 `json:"installationId"`
	RepositoryID int64 `json:"repositoryId"`
	Kind string `json:"kind"`
	Number int64 `json:"number,omitempty"`
	Query string `json:"query,omitempty"`
}

func (b *Broker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Path == "/healthz" && r.Method == http.MethodGet {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.URL.Path != "/v1/github/items/resolve" && r.URL.Path != "/v1/github/items/search" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed)
		return
	}
	instance, err := b.auth.Identify(r)
	if err != nil || !validInstance(instance) {
		writeError(w, http.StatusUnauthorized)
		return
	}
	if !b.rate.allow(instance, b.now()) {
		writeError(w, http.StatusTooManyRequests)
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType)
		return
	}
	var in requestData
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest)
		return
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) || in.InstallationID <= 0 || in.RepositoryID <= 0 ||
		(in.Kind != "issue" && in.Kind != "pr") {
		writeError(w, http.StatusBadRequest)
		return
	}
	resolve := r.URL.Path == "/v1/github/items/resolve"
	if resolve {
		if in.Number <= 0 || in.Number > 2147483647 || in.Query != "" {
			writeError(w, http.StatusBadRequest)
			return
		}
	} else if in.Number != 0 || len(in.Query) < 2 || len(in.Query) > 100 ||
		len(strings.TrimSpace(in.Query)) < 2 {
		writeError(w, http.StatusBadRequest)
		return
	}
	scope, err := b.grants.Lookup(r.Context(), instance, in.InstallationID, in.RepositoryID)
	if err != nil || !validScope(scope, instance, in.InstallationID, in.RepositoryID, b.now()) {
		writeError(w, http.StatusForbidden)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	installationID := strconv.FormatInt(scope.InstallationID, 10)
	repositoryID := strconv.FormatInt(scope.RepositoryID, 10)
	if resolve {
		item, err := b.provider.ResolveItem(ctx, installationID, repositoryID, scope.Repository, in.Kind, in.Number)
		if err != nil || !validItem(item, scope.Repository, in.Kind, in.Number) {
			writeError(w, http.StatusBadGateway)
			return
		}
		writeJSON(w, map[string]any{"item": item})
		return
	}
	result, err := b.provider.SearchItems(ctx, installationID, repositoryID, scope.Repository, in.Kind, in.Query)
	if err != nil || len(result) > 20 {
		writeError(w, http.StatusBadGateway)
		return
	}
	for _, item := range result {
		if !validItem(item, scope.Repository, in.Kind, item.Number) {
			writeError(w, http.StatusBadGateway)
			return
		}
	}
	if result == nil {
		result = []integration.GitHubItem{}
	}
	writeJSON(w, map[string]any{"items": result})
}

func validInstance(instance string) bool {
	if len(instance) < 1 || len(instance) > 128 {
		return false
	}
	for _, ch := range instance {
		if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
			(ch >= '0' && ch <= '9') || ch == '_' || ch == '-' || ch == '.') {
			return false
		}
	}
	return true
}

func validScope(scope Grant, instance string, installationID, repositoryID int64, now time.Time) bool {
	return scope.InstanceID == instance && scope.InstallationID == installationID &&
		scope.RepositoryID == repositoryID && installationID > 0 && repositoryID > 0 &&
		now.Before(scope.ExpiresAt) && len(scope.Repository) <= 200 &&
		repositoryNamePattern.MatchString(scope.Repository) &&
		!strings.Contains(scope.Repository, "..")
}

func validItem(item integration.GitHubItem, repository, kind string, number int64) bool {
	if item.Kind != kind || item.Number != number || number <= 0 || number > 2147483647 ||
		(item.State != "open" && item.State != "closed") ||
		len(item.Title) == 0 || len(item.Title) > 500 || strings.ContainsRune(item.Title, 0) {
		return false
	}
	segment := "issues"
	if kind == "pr" {
		segment = "pull"
	}
	return strings.EqualFold(item.URL, "https://github.com/"+repository+"/"+segment+"/"+strconv.FormatInt(number, 10))
}

func writeError(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "request rejected"})
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

type rateEntry struct {
	period time.Time
	count int
}

type rateLimiter struct {
	mu sync.Mutex
	entries map[string]rateEntry
	global rateEntry
	limit int
	globalLimit int
	window time.Duration
}

func (r *rateLimiter) allow(instance string, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if now.Sub(r.global.period) >= r.window {
		r.global = rateEntry{period: now}
		for id, entry := range r.entries {
			if now.Sub(entry.period) >= r.window {
				delete(r.entries, id)
			}
		}
	}
	if r.global.count >= r.globalLimit {
		return false
	}
	entry, ok := r.entries[instance]
	if !ok && len(r.entries) >= 4096 {
		return false
	}
	if !ok || now.Sub(entry.period) >= r.window {
		entry = rateEntry{period: now}
	}
	if entry.count >= r.limit {
		return false
	}
	entry.count++
	r.entries[instance] = entry
	r.global.count++
	return true
}
