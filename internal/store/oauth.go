package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"scrumboy/internal/oauth"
)

const (
	authCodeTTL     = 60 * time.Second
	accessTokenTTL  = 1 * time.Hour
	refreshTokenTTL = 30 * 24 * time.Hour
)

// OAuthClient is a dynamically-registered public OAuth client (RFC 7591).
type OAuthClient struct {
	ID          string
	ClientName  string
	RedirectURI string
	CreatedAt   time.Time
}

// OAuthAuthCode is a resolved, not-yet-consumed authorization code, returned
// by ConsumeOAuthAuthCode so the caller can verify PKCE/redirect_uri/client_id
// before treating the code as spent.
type OAuthAuthCode struct {
	ClientID            string
	UserID              int64
	RedirectURI         string
	CodeChallenge       string
	CodeChallengeMethod string
	Resource            string
}

type OAuthRefreshGrant struct {
	ClientID string
	UserID   int64
	Resource string
}

// OAuthTokenPair is a freshly minted access+refresh token pair.
type OAuthTokenPair struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int64 // seconds
}

// CreateOAuthClient registers a new public OAuth client (RFC 7591 DCR).
func (s *Store) CreateOAuthClient(ctx context.Context, clientID, clientName, redirectURI string) (OAuthClient, error) {
	if clientID == "" || redirectURI == "" {
		return OAuthClient{}, fmt.Errorf("%w: client id and redirect uri are required", ErrValidation)
	}
	nowMs := time.Now().UTC().UnixMilli()
	_, err := s.db.ExecContext(ctx, `
INSERT INTO oauth_clients(id, client_name, redirect_uri, created_at)
VALUES (?, ?, ?, ?)
`, clientID, clientName, redirectURI, nowMs)
	if err != nil {
		return OAuthClient{}, fmt.Errorf("insert oauth client: %w", err)
	}
	return OAuthClient{
		ID:          clientID,
		ClientName:  clientName,
		RedirectURI: redirectURI,
		CreatedAt:   time.UnixMilli(nowMs).UTC(),
	}, nil
}

// GetOAuthClient looks up a registered client by id.
func (s *Store) GetOAuthClient(ctx context.Context, clientID string) (OAuthClient, error) {
	if clientID == "" {
		return OAuthClient{}, ErrNotFound
	}
	var (
		c           OAuthClient
		clientName  sql.NullString
		createdAtMs int64
	)
	err := s.db.QueryRowContext(ctx, `
SELECT id, client_name, redirect_uri, created_at
FROM oauth_clients
WHERE id = ?
`, clientID).Scan(&c.ID, &clientName, &c.RedirectURI, &createdAtMs)
	if err != nil {
		if err == sql.ErrNoRows {
			return OAuthClient{}, ErrNotFound
		}
		return OAuthClient{}, fmt.Errorf("get oauth client: %w", err)
	}
	c.ClientName = clientName.String
	c.CreatedAt = time.UnixMilli(createdAtMs).UTC()
	return c, nil
}

// CreateOAuthAuthCode issues a new single-use authorization code (plaintext
// returned once) with a 60s TTL, bound to the presented PKCE code_challenge.
func (s *Store) CreateOAuthAuthCode(ctx context.Context, clientID string, userID int64, redirectURI, codeChallenge, codeChallengeMethod, resource string) (plaintext string, err error) {
	if clientID == "" || userID <= 0 || redirectURI == "" || codeChallenge == "" || resource == "" {
		return "", fmt.Errorf("%w: missing required auth code fields", ErrValidation)
	}
	secret, err := oauth.GenerateOpaqueSecret()
	if err != nil {
		return "", err
	}
	codeHash := hashToken(secret)
	nowMs := time.Now().UTC().UnixMilli()
	expiresMs := time.Now().UTC().Add(authCodeTTL).UnixMilli()
	_, err = s.db.ExecContext(ctx, `
INSERT INTO oauth_auth_codes(code_hash, client_id, user_id, redirect_uri, code_challenge, code_challenge_method, resource, created_at, expires_at, consumed_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)
`, codeHash, clientID, userID, redirectURI, codeChallenge, codeChallengeMethod, resource, nowMs, expiresMs)
	if err != nil {
		return "", fmt.Errorf("insert oauth auth code: %w", err)
	}
	return secret, nil
}

// newOAuthTokenSecrets mints opaque access/refresh secrets. Tests may override
// it to force deterministic hashes (e.g. collision rollback coverage).
var newOAuthTokenSecrets = defaultNewOAuthTokenSecrets

func defaultNewOAuthTokenSecrets() (accessSecret, refreshSecret string, err error) {
	accessSecret, err = oauth.GenerateOpaqueSecret()
	if err != nil {
		return "", "", err
	}
	refreshSecret, err = oauth.GenerateOpaqueSecret()
	if err != nil {
		return "", "", err
	}
	return accessSecret, refreshSecret, nil
}

// RedeemOAuthAuthCode validates every request-bound property before consuming a
// code. Failed client, redirect, resource, or PKCE comparisons do not burn an
// otherwise valid grant. Prefer RedeemOAuthAuthCodeAndIssue on production token
// exchange paths so consume and token insert share one transaction.
func (s *Store) RedeemOAuthAuthCode(ctx context.Context, rawCode, expectedClientID, expectedRedirectURI, expectedResource, codeVerifier string) (OAuthAuthCode, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OAuthAuthCode{}, fmt.Errorf("begin redeem oauth auth code tx: %w", err)
	}
	defer tx.Rollback()

	ac, err := s.redeemOAuthAuthCodeTx(ctx, tx, rawCode, expectedClientID, expectedRedirectURI, expectedResource, codeVerifier)
	if err != nil {
		return OAuthAuthCode{}, err
	}
	if err := tx.Commit(); err != nil {
		return OAuthAuthCode{}, fmt.Errorf("commit redeem oauth auth code tx: %w", err)
	}
	return ac, nil
}

// RedeemOAuthAuthCodeAndIssue validates and consumes an authorization code and
// mints a new access/refresh pair in a single database transaction. Invalid
// client, redirect, resource, or PKCE comparisons do not burn the grant; if
// token insertion fails after a successful consume, the whole transaction rolls
// back so the code remains redeemable.
func (s *Store) RedeemOAuthAuthCodeAndIssue(ctx context.Context, rawCode, expectedClientID, expectedRedirectURI, expectedResource, codeVerifier string) (OAuthTokenPair, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OAuthTokenPair{}, fmt.Errorf("begin redeem auth code and issue tx: %w", err)
	}
	defer tx.Rollback()

	ac, err := s.redeemOAuthAuthCodeTx(ctx, tx, rawCode, expectedClientID, expectedRedirectURI, expectedResource, codeVerifier)
	if err != nil {
		return OAuthTokenPair{}, err
	}
	pair, err := s.issueOAuthTokenPairTx(ctx, tx, ac.ClientID, ac.UserID, ac.Resource)
	if err != nil {
		return OAuthTokenPair{}, err
	}
	if err := tx.Commit(); err != nil {
		return OAuthTokenPair{}, fmt.Errorf("commit redeem auth code and issue tx: %w", err)
	}
	return pair, nil
}

func (s *Store) redeemOAuthAuthCodeTx(ctx context.Context, tx *sql.Tx, rawCode, expectedClientID, expectedRedirectURI, expectedResource, codeVerifier string) (OAuthAuthCode, error) {
	rawCode = strings.TrimSpace(rawCode)
	if rawCode == "" || expectedClientID == "" || expectedRedirectURI == "" || expectedResource == "" || codeVerifier == "" {
		return OAuthAuthCode{}, ErrNotFound
	}
	codeHash := hashToken(rawCode)
	nowMs := time.Now().UTC().UnixMilli()

	var ac OAuthAuthCode
	err := tx.QueryRowContext(ctx, `
SELECT client_id, user_id, redirect_uri, code_challenge, code_challenge_method, resource
FROM oauth_auth_codes
WHERE code_hash = ? AND consumed_at IS NULL AND expires_at > ?
`, codeHash, nowMs).Scan(&ac.ClientID, &ac.UserID, &ac.RedirectURI, &ac.CodeChallenge, &ac.CodeChallengeMethod, &ac.Resource)
	if err != nil {
		if err == sql.ErrNoRows {
			return OAuthAuthCode{}, ErrNotFound
		}
		return OAuthAuthCode{}, fmt.Errorf("select oauth auth code: %w", err)
	}
	if ac.ClientID != expectedClientID || ac.RedirectURI != expectedRedirectURI || ac.Resource != expectedResource || !oauth.VerifyPKCE(ac.CodeChallengeMethod, codeVerifier, ac.CodeChallenge) {
		return OAuthAuthCode{}, ErrNotFound
	}

	res, err := tx.ExecContext(ctx, `
UPDATE oauth_auth_codes SET consumed_at = ?
WHERE code_hash = ? AND consumed_at IS NULL AND expires_at > ?
  AND client_id = ? AND redirect_uri = ? AND resource = ?
  AND code_challenge = ? AND code_challenge_method = ?
`, nowMs, codeHash, nowMs, ac.ClientID, ac.RedirectURI, ac.Resource, ac.CodeChallenge, ac.CodeChallengeMethod)
	if err != nil {
		return OAuthAuthCode{}, fmt.Errorf("consume oauth auth code: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return OAuthAuthCode{}, fmt.Errorf("consume oauth auth code rows: %w", err)
	}
	if n == 0 {
		// Consumed concurrently between the SELECT and UPDATE above.
		return OAuthAuthCode{}, ErrNotFound
	}
	return ac, nil
}

// IssueOAuthTokenPair mints a new access token (1h TTL) and refresh token (30d
// TTL) for a client/user pair. Production authorization_code and refresh_token
// exchanges use the combined redeem/consume-and-issue methods instead.
func (s *Store) IssueOAuthTokenPair(ctx context.Context, clientID string, userID int64, resource string) (OAuthTokenPair, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OAuthTokenPair{}, fmt.Errorf("begin issue token pair tx: %w", err)
	}
	defer tx.Rollback()

	pair, err := s.issueOAuthTokenPairTx(ctx, tx, clientID, userID, resource)
	if err != nil {
		return OAuthTokenPair{}, err
	}
	if err := tx.Commit(); err != nil {
		return OAuthTokenPair{}, fmt.Errorf("commit issue token pair tx: %w", err)
	}
	return pair, nil
}

func (s *Store) issueOAuthTokenPairTx(ctx context.Context, tx *sql.Tx, clientID string, userID int64, resource string) (OAuthTokenPair, error) {
	if clientID == "" || userID <= 0 || resource == "" {
		return OAuthTokenPair{}, fmt.Errorf("%w: missing client id, user id, or resource", ErrValidation)
	}
	accessSecret, refreshSecret, err := newOAuthTokenSecrets()
	if err != nil {
		return OAuthTokenPair{}, err
	}

	now := time.Now().UTC()
	nowMs := now.UnixMilli()
	accessExpiresMs := now.Add(accessTokenTTL).UnixMilli()
	refreshExpiresMs := now.Add(refreshTokenTTL).UnixMilli()

	if _, err := tx.ExecContext(ctx, `
INSERT INTO oauth_access_tokens(token_hash, client_id, user_id, resource, created_at, expires_at, revoked_at)
VALUES (?, ?, ?, ?, ?, ?, NULL)
`, hashToken(accessSecret), clientID, userID, resource, nowMs, accessExpiresMs); err != nil {
		return OAuthTokenPair{}, fmt.Errorf("insert oauth access token: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO oauth_refresh_tokens(token_hash, client_id, user_id, resource, created_at, expires_at, revoked_at)
VALUES (?, ?, ?, ?, ?, ?, NULL)
`, hashToken(refreshSecret), clientID, userID, resource, nowMs, refreshExpiresMs); err != nil {
		return OAuthTokenPair{}, fmt.Errorf("insert oauth refresh token: %w", err)
	}

	return OAuthTokenPair{
		AccessToken:  accessSecret,
		RefreshToken: refreshSecret,
		ExpiresIn:    int64(accessTokenTTL.Seconds()),
	}, nil
}

// ConsumeOAuthRefreshToken validates and revokes (rotates away from) a refresh
// token. Prefer ConsumeOAuthRefreshTokenAndIssue on production refresh paths so
// revoke and token insert share one transaction.
func (s *Store) ConsumeOAuthRefreshToken(ctx context.Context, rawToken, expectedClientID, expectedResource string) (grant OAuthRefreshGrant, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OAuthRefreshGrant{}, fmt.Errorf("begin consume oauth refresh token tx: %w", err)
	}
	defer tx.Rollback()

	grant, err = s.consumeOAuthRefreshTokenTx(ctx, tx, rawToken, expectedClientID, expectedResource)
	if err != nil {
		return OAuthRefreshGrant{}, err
	}
	if err := tx.Commit(); err != nil {
		return OAuthRefreshGrant{}, fmt.Errorf("commit consume oauth refresh token tx: %w", err)
	}
	return grant, nil
}

// ConsumeOAuthRefreshTokenAndIssue validates and revokes a refresh token and
// mints a new access/refresh pair in a single database transaction. Invalid
// client or resource comparisons do not revoke the grant; if token insertion
// fails after a successful revoke, the whole transaction rolls back so the
// refresh token remains usable.
func (s *Store) ConsumeOAuthRefreshTokenAndIssue(ctx context.Context, rawToken, expectedClientID, expectedResource string) (OAuthTokenPair, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OAuthTokenPair{}, fmt.Errorf("begin consume refresh and issue tx: %w", err)
	}
	defer tx.Rollback()

	grant, err := s.consumeOAuthRefreshTokenTx(ctx, tx, rawToken, expectedClientID, expectedResource)
	if err != nil {
		return OAuthTokenPair{}, err
	}
	pair, err := s.issueOAuthTokenPairTx(ctx, tx, grant.ClientID, grant.UserID, grant.Resource)
	if err != nil {
		return OAuthTokenPair{}, err
	}
	if err := tx.Commit(); err != nil {
		return OAuthTokenPair{}, fmt.Errorf("commit consume refresh and issue tx: %w", err)
	}
	return pair, nil
}

func (s *Store) consumeOAuthRefreshTokenTx(ctx context.Context, tx *sql.Tx, rawToken, expectedClientID, expectedResource string) (grant OAuthRefreshGrant, err error) {
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" || expectedClientID == "" || expectedResource == "" {
		return OAuthRefreshGrant{}, ErrNotFound
	}
	tokenHash := hashToken(rawToken)
	nowMs := time.Now().UTC().UnixMilli()

	err = tx.QueryRowContext(ctx, `
SELECT client_id, user_id, resource
FROM oauth_refresh_tokens
WHERE token_hash = ? AND revoked_at IS NULL AND expires_at > ?
`, tokenHash, nowMs).Scan(&grant.ClientID, &grant.UserID, &grant.Resource)
	if err != nil {
		if err == sql.ErrNoRows {
			return OAuthRefreshGrant{}, ErrNotFound
		}
		return OAuthRefreshGrant{}, fmt.Errorf("select oauth refresh token: %w", err)
	}
	if grant.ClientID != expectedClientID || grant.Resource != expectedResource {
		return OAuthRefreshGrant{}, ErrNotFound
	}

	res, err := tx.ExecContext(ctx, `
UPDATE oauth_refresh_tokens SET revoked_at = ?
WHERE token_hash = ? AND revoked_at IS NULL AND expires_at > ?
  AND client_id = ? AND resource = ?
`, nowMs, tokenHash, nowMs, grant.ClientID, grant.Resource)
	if err != nil {
		return OAuthRefreshGrant{}, fmt.Errorf("revoke oauth refresh token: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return OAuthRefreshGrant{}, fmt.Errorf("revoke oauth refresh token rows: %w", err)
	} else if n == 0 {
		return OAuthRefreshGrant{}, ErrNotFound
	}
	return grant, nil
}

// GetUserByOAuthAccessToken returns the user for an active (non-revoked,
// non-expired) OAuth access token. Mirrors GetUserByAPIToken's shape exactly
// so internal/mcp/adapter.go can use it as a drop-in fallback lookup.
func (s *Store) GetUserByOAuthAccessToken(ctx context.Context, rawToken, expectedResource string) (User, error) {
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" || expectedResource == "" {
		return User{}, ErrNotFound
	}
	tokenHash := hashToken(rawToken)
	nowMs := time.Now().UTC().UnixMilli()

	var (
		u                User
		isBootstrap      bool
		systemRoleStr    string
		createdAt        int64
		twoFactorEnabled bool
	)
	err := s.db.QueryRowContext(ctx, `
SELECT u.id, u.email, u.name, u.is_bootstrap, u.system_role, u.created_at, u.two_factor_enabled
FROM oauth_access_tokens t
JOIN users u ON u.id = t.user_id
WHERE t.token_hash = ?
	AND t.resource = ?
  AND t.revoked_at IS NULL
  AND t.expires_at > ?
`, tokenHash, expectedResource, nowMs).Scan(&u.ID, &u.Email, &u.Name, &isBootstrap, &systemRoleStr, &createdAt, &twoFactorEnabled)
	if err != nil {
		if err == sql.ErrNoRows {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("get oauth access token user: %w", err)
	}
	u.IsBootstrap = isBootstrap
	if role, ok := ParseSystemRole(systemRoleStr); ok {
		u.SystemRole = role
	} else {
		u.SystemRole = SystemRoleUser
	}
	u.CreatedAt = time.UnixMilli(createdAt).UTC()
	u.TwoFactorEnabled = twoFactorEnabled
	return u, nil
}

// DeleteExpiredOAuthArtifacts deletes spent/expired authorization codes and
// revoked/expired access and refresh tokens. Nothing else ever deletes these
// rows (consuming a code or rotating/revoking a token only flips a
// consumed_at/revoked_at column), so without a periodic sweep the three
// tables grow without bound on an otherwise-idle instance — most notably
// oauth_refresh_tokens, which gets one dead row per refresh-token rotation
// for every long-lived client. Called on the same hourly cadence as
// DeleteExpiredProjects (see cmd/scrumboy/main.go).
func (s *Store) DeleteExpiredOAuthArtifacts(ctx context.Context) (int64, error) {
	nowMs := time.Now().UTC().UnixMilli()
	var total int64
	res, err := s.db.ExecContext(ctx, `DELETE FROM oauth_auth_codes WHERE consumed_at IS NOT NULL OR expires_at < ?`, nowMs)
	if err != nil {
		return 0, fmt.Errorf("delete expired oauth auth codes: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected delete expired oauth auth codes: %w", err)
	}
	total += n

	res, err = s.db.ExecContext(ctx, `DELETE FROM oauth_access_tokens WHERE revoked_at IS NOT NULL OR expires_at < ?`, nowMs)
	if err != nil {
		return 0, fmt.Errorf("delete expired oauth access tokens: %w", err)
	}
	n, err = res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected delete expired oauth access tokens: %w", err)
	}
	total += n

	res, err = s.db.ExecContext(ctx, `DELETE FROM oauth_refresh_tokens WHERE revoked_at IS NOT NULL OR expires_at < ?`, nowMs)
	if err != nil {
		return 0, fmt.Errorf("delete expired oauth refresh tokens: %w", err)
	}
	n, err = res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected delete expired oauth refresh tokens: %w", err)
	}
	total += n

	return total, nil
}

// RevokeOAuthToken revokes an access or refresh token by its plaintext value
// (RFC 7009). If hint is "access_token" or "refresh_token" only that table is
// checked; otherwise both are tried. Always succeeds (no-op if the token
// isn't found) so callers can return 200 unconditionally per RFC 7009 §2.2,
// avoiding a token-existence oracle.
func (s *Store) RevokeOAuthToken(ctx context.Context, rawToken, hint string) error {
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" {
		return nil
	}
	tokenHash := hashToken(rawToken)
	nowMs := time.Now().UTC().UnixMilli()

	if hint != "refresh_token" {
		if _, err := s.db.ExecContext(ctx, `
UPDATE oauth_access_tokens SET revoked_at = ? WHERE token_hash = ? AND revoked_at IS NULL
`, nowMs, tokenHash); err != nil {
			return fmt.Errorf("revoke oauth access token: %w", err)
		}
	}
	if hint != "access_token" {
		if _, err := s.db.ExecContext(ctx, `
UPDATE oauth_refresh_tokens SET revoked_at = ? WHERE token_hash = ? AND revoked_at IS NULL
`, nowMs, tokenHash); err != nil {
			return fmt.Errorf("revoke oauth refresh token: %w", err)
		}
	}
	return nil
}
