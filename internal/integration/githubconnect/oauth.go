package githubconnect

import (
    "context"
    "crypto/rand"
    "crypto/sha256"
    "encoding/base64"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "net/http"
    "net/url"
    "strconv"
    "strings"
    "sync"
    "time"
)

var ErrUnavailable = errors.New("github authorization unavailable")
var ErrInvalidFlow = errors.New("github authorization request invalid")
var ErrNotAuthorized = errors.New("github installation not authorized")
var ErrCapacity = errors.New("github authorization capacity reached")

const (
    oauthOrigin = "https://github.com"
    apiOrigin = "https://api.github.com"
    flowTTL = 10 * time.Minute
    maxPendingFlows = 256
    maxResponseBytes = 1 << 20
    maxInstallationPages = 20
)

type Config struct {
    AppID int64
    ClientID string
    ClientSecret string
    RedirectURI string
}

type Verification struct {
    Subject string
    GitHubUserID int64
    GitHubLogin string
    InstallationID int64
    AccountID int64
    AccountLogin string
    AccountType string
}

type challenge struct {
    subject string
    verifier string
    expires time.Time
}

type Service struct {
    config Config
    client *http.Client
    authBase string
    apiBase string
    now func() time.Time
    mu sync.Mutex
    pending map[string]challenge
}

func New(config Config, client *http.Client) (*Service, error) {
    return newService(config, client, oauthOrigin, apiOrigin)
}

func newService(config Config, client *http.Client, authBase, apiBase string) (*Service, error) {
    if config.AppID <= 0 || len(config.ClientID) < 8 || len(config.ClientID) > 256 ||
        len(config.ClientSecret) < 16 || len(config.ClientSecret) > 1024 {
        return nil, fmt.Errorf("%w: incomplete GitHub App OAuth configuration", ErrInvalidFlow)
    }
    redirect, err := url.Parse(config.RedirectURI)
    if err != nil || redirect.Host == "" || redirect.User != nil || redirect.Fragment != "" ||
        redirect.RawQuery != "" || (redirect.Scheme != "https" && !(redirect.Scheme == "http" &&
        (redirect.Hostname() == "localhost" || redirect.Hostname() == "127.0.0.1" || redirect.Hostname() == "::1"))) {
        return nil, fmt.Errorf("%w: redirect URI must use HTTPS or local loopback", ErrInvalidFlow)
    }
    if client == nil {
        client = &http.Client{Timeout: 10 * time.Second}
    }
    copyClient := *client
    if copyClient.Timeout == 0 || copyClient.Timeout > 20*time.Second {
        copyClient.Timeout = 10 * time.Second
    }
    copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
    return &Service{
        config: config, client: &copyClient, authBase: authBase, apiBase: apiBase,
        now: time.Now, pending: map[string]challenge{},
    }, nil
}

func validSubject(subject string) bool {
    return len(subject) > 0 && len(subject) <= 256 && !strings.ContainsAny(subject, "\r\n\x00")
}

func randomValue() (string, error) {
    data := make([]byte, 32)
    if _, err := rand.Read(data); err != nil { return "", ErrUnavailable }
    return base64.RawURLEncoding.EncodeToString(data), nil
}

func (s *Service) Begin(subject string) (string, error) {
    if !validSubject(subject) { return "", ErrInvalidFlow }
    state, err := randomValue()
    if err != nil { return "", err }
    verifier, err := randomValue()
    if err != nil { return "", err }
    digest := sha256.Sum256([]byte(verifier))
    challenge := base64.RawURLEncoding.EncodeToString(digest[:])
    s.mu.Lock()
    now := s.now()
    for key, entry := range s.pending {
        if !now.Before(entry.expires) { delete(s.pending, key) }
    }
    if len(s.pending) >= maxPendingFlows {
        s.mu.Unlock()
        return "", ErrCapacity
    }
    s.pending[state] = challengeEntry(subject, verifier, now)
    s.mu.Unlock()
    query := url.Values{
        "client_id": {s.config.ClientID},
        "redirect_uri": {s.config.RedirectURI},
        "state": {state},
        "code_challenge": {challenge},
        "code_challenge_method": {"S256"},
    }
    return s.authBase + "/login/oauth/authorize?" + query.Encode(), nil
}

func challengeEntry(subject, verifier string, now time.Time) challenge {
    return challenge{subject: subject, verifier: verifier, expires: now.Add(flowTTL)}
}

func (s *Service) consume(subject, state string) (string, error) {
    if !validSubject(subject) || len(state) != 43 { return "", ErrInvalidFlow }
    s.mu.Lock()
    defer s.mu.Unlock()
    entry, ok := s.pending[state]
    if !ok || !s.now().Before(entry.expires) {
        delete(s.pending, state)
        return "", ErrInvalidFlow
    }
    if entry.subject != subject { return "", ErrInvalidFlow }
    delete(s.pending, state)
    return entry.verifier, nil
}

func (s *Service) Complete(ctx context.Context, subject, state, code string, installationID int64) (Verification, error) {
    result := Verification{}
    verifier, err := s.consume(subject, state)
    if err != nil { return result, err }
    if len(code) == 0 || len(code) > 1024 || strings.ContainsAny(code, "\r\n\x00") ||
        installationID <= 0 {
        return result, ErrInvalidFlow
    }
    token, err := s.exchange(ctx, code, verifier)
    if err != nil { return result, err }
    identity, err := s.user(ctx, token)
    if err != nil { return result, err }
    installation, err := s.findInstallation(ctx, token, installationID)
    if err != nil { return result, err }
    return Verification{
        Subject: subject, GitHubUserID: identity.ID, GitHubLogin: identity.Login,
        InstallationID: installation.ID, AccountID: installation.Account.ID,
        AccountLogin: installation.Account.Login, AccountType: installation.TargetType,
    }, nil
}

func (s *Service) request(ctx context.Context, method, path, bearer, body, base string, target any) error {
    var input io.Reader
    if body != "" { input = strings.NewReader(body) }
    request, err := http.NewRequestWithContext(ctx, method, base+path, input)
    if err != nil { return ErrUnavailable }
    request.Header.Set("Accept", "application/json")
    if body != "" { request.Header.Set("Content-Type", "application/x-www-form-urlencoded") }
    if bearer != "" { request.Header.Set("Authorization", "Bearer "+bearer) }
    response, err := s.client.Do(request)
    if err != nil { return ErrUnavailable }
    defer response.Body.Close()
    if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden ||
        response.StatusCode == http.StatusNotFound {
        return ErrNotAuthorized
    }
    if response.StatusCode != http.StatusOK { return ErrUnavailable }
    data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
    if err != nil || len(data) > maxResponseBytes { return ErrUnavailable }
    if err := json.Unmarshal(data, target); err != nil { return ErrUnavailable }
    return nil
}

func (s *Service) exchange(ctx context.Context, code, verifier string) (string, error) {
    body := url.Values{
        "client_id": {s.config.ClientID},
        "client_secret": {s.config.ClientSecret},
        "code": {code},
        "redirect_uri": {s.config.RedirectURI},
        "code_verifier": {verifier},
    }.Encode()
    var response struct {
        AccessToken string `json:"access_token"`
        TokenType string `json:"token_type"`
        Error string `json:"error"`
    }
    if err := s.request(ctx, http.MethodPost, "/login/oauth/access_token", "", body, s.authBase, &response); err != nil {
        return "", err
    }
    if response.Error != "" || response.TokenType != "bearer" ||
        len(response.AccessToken) < 16 || len(response.AccessToken) > 2048 {
        return "", ErrNotAuthorized
    }
    return response.AccessToken, nil
}

type githubIdentity struct {
    ID int64 `json:"id"`
    Login string `json:"login"`
}

type githubInstallation struct {
    ID int64 `json:"id"`
    AppID int64 `json:"app_id"`
    TargetType string `json:"target_type"`
    SuspendedAt *time.Time `json:"suspended_at"`
    Account githubIdentity `json:"account"`
}

func (s *Service) user(ctx context.Context, token string) (githubIdentity, error) {
    var result githubIdentity
    if err := s.request(ctx, http.MethodGet, "/user", token, "", s.apiBase, &result); err != nil {
        return githubIdentity{}, err
    }
    if result.ID <= 0 || len(result.Login) == 0 || len(result.Login) > 200 {
        return githubIdentity{}, ErrUnavailable
    }
    return result, nil
}

func (s *Service) findInstallation(ctx context.Context, token string, installationID int64) (githubInstallation, error) {
    seen := make(map[int64]bool)
    var found githubInstallation
    count := 0
    total := -1
    for page := 1; page <= maxInstallationPages; page++ {
        var response struct {
            TotalCount int `json:"total_count"`
            Installations []githubInstallation `json:"installations"`
        }
        path := "/user/installations?per_page=100&page=" + strconv.Itoa(page)
        if err := s.request(ctx, http.MethodGet, path, token, "", s.apiBase, &response); err != nil {
            return githubInstallation{}, err
        }
        if response.TotalCount < 0 || response.TotalCount > maxInstallationPages*100 ||
            len(response.Installations) > 100 || (total != -1 && total != response.TotalCount) {
            return githubInstallation{}, ErrUnavailable
        }
        total = response.TotalCount
        for _, inst := range response.Installations {
            if inst.ID <= 0 || inst.AppID <= 0 || seen[inst.ID] {
                return githubInstallation{}, ErrUnavailable
            }
            seen[inst.ID] = true
            if inst.ID == installationID && inst.AppID == s.config.AppID && inst.SuspendedAt == nil &&
                inst.Account.ID > 0 && len(inst.Account.Login) > 0 && len(inst.Account.Login) <= 200 &&
                (inst.TargetType == "User" || inst.TargetType == "Organization") {
                found = inst
            }
            count++
        }
        if count == total {
            if found.ID == 0 { return githubInstallation{}, ErrNotAuthorized }
            return found, nil
        }
        if count > total || len(response.Installations) != 100 { return githubInstallation{}, ErrUnavailable }
    }
    return githubInstallation{}, ErrUnavailable
}
