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
func (s *Store) CreateOAuthAuthCode(ctx context.Context, clientID string, userID int64, redirectURI, codeChallenge, codeChallengeMethod string) (plaintext string, err error) {
	if clientID == "" || userID <= 0 || redirectURI == "" || codeChallenge == "" {
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
INSERT INTO oauth_auth_codes(code_hash, client_id, user_id, redirect_uri, code_challenge, code_challenge_method, created_at, expires_at, consumed_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL)
`, codeHash, clientID, userID, redirectURI, codeChallenge, codeChallengeMethod, nowMs, expiresMs)
	if err != nil {
		return "", fmt.Errorf("insert oauth auth code: %w", err)
	}
	return secret, nil
}

// ConsumeOAuthAuthCode atomically redeems a not-yet-consumed, not-yet-expired
// authorization code: single-use is enforced by the conditional UPDATE below,
// not merely by an in-memory check, so a replayed code can never redeem twice
// even under concurrent requests.
func (s *Store) ConsumeOAuthAuthCode(ctx context.Context, rawCode string) (OAuthAuthCode, error) {
	rawCode = strings.TrimSpace(rawCode)
	if rawCode == "" {
		return OAuthAuthCode{}, ErrNotFound
	}
	codeHash := hashToken(rawCode)
	nowMs := time.Now().UTC().UnixMilli()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OAuthAuthCode{}, fmt.Errorf("begin consume auth code tx: %w", err)
	}
	defer tx.Rollback()

	var ac OAuthAuthCode
	err = tx.QueryRowContext(ctx, `
SELECT client_id, user_id, redirect_uri, code_challenge, code_challenge_method
FROM oauth_auth_codes
WHERE code_hash = ? AND consumed_at IS NULL AND expires_at > ?
`, codeHash, nowMs).Scan(&ac.ClientID, &ac.UserID, &ac.RedirectURI, &ac.CodeChallenge, &ac.CodeChallengeMethod)
	if err != nil {
		if err == sql.ErrNoRows {
			return OAuthAuthCode{}, ErrNotFound
		}
		return OAuthAuthCode{}, fmt.Errorf("select oauth auth code: %w", err)
	}

	res, err := tx.ExecContext(ctx, `
UPDATE oauth_auth_codes SET consumed_at = ?
WHERE code_hash = ? AND consumed_at IS NULL AND expires_at > ?
`, nowMs, codeHash, nowMs)
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
	if err := tx.Commit(); err != nil {
		return OAuthAuthCode{}, fmt.Errorf("commit consume auth code tx: %w", err)
	}
	return ac, nil
}

// IssueOAuthTokenPair mints a new access token (1h TTL) and refresh token (30d
// TTL) for a client/user pair, e.g. after a successful authorization_code or
// refresh_token grant.
func (s *Store) IssueOAuthTokenPair(ctx context.Context, clientID string, userID int64) (OAuthTokenPair, error) {
	if clientID == "" || userID <= 0 {
		return OAuthTokenPair{}, fmt.Errorf("%w: missing client id or user id", ErrValidation)
	}
	accessSecret, err := oauth.GenerateOpaqueSecret()
	if err != nil {
		return OAuthTokenPair{}, err
	}
	refreshSecret, err := oauth.GenerateOpaqueSecret()
	if err != nil {
		return OAuthTokenPair{}, err
	}

	now := time.Now().UTC()
	nowMs := now.UnixMilli()
	accessExpiresMs := now.Add(accessTokenTTL).UnixMilli()
	refreshExpiresMs := now.Add(refreshTokenTTL).UnixMilli()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OAuthTokenPair{}, fmt.Errorf("begin issue token pair tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
INSERT INTO oauth_access_tokens(token_hash, client_id, user_id, created_at, expires_at, revoked_at)
VALUES (?, ?, ?, ?, ?, NULL)
`, hashToken(accessSecret), clientID, userID, nowMs, accessExpiresMs); err != nil {
		return OAuthTokenPair{}, fmt.Errorf("insert oauth access token: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO oauth_refresh_tokens(token_hash, client_id, user_id, created_at, expires_at, revoked_at)
VALUES (?, ?, ?, ?, ?, NULL)
`, hashToken(refreshSecret), clientID, userID, nowMs, refreshExpiresMs); err != nil {
		return OAuthTokenPair{}, fmt.Errorf("insert oauth refresh token: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return OAuthTokenPair{}, fmt.Errorf("commit issue token pair tx: %w", err)
	}

	return OAuthTokenPair{
		AccessToken:  accessSecret,
		RefreshToken: refreshSecret,
		ExpiresIn:    int64(accessTokenTTL.Seconds()),
	}, nil
}

// ConsumeOAuthRefreshToken validates and revokes (rotates away from) a
// refresh token, returning the client/user it was issued to so a new token
// pair can be minted. Revocation happens unconditionally on first successful
// use, which is what makes reuse of an already-rotated token fail.
func (s *Store) ConsumeOAuthRefreshToken(ctx context.Context, rawToken string) (clientID string, userID int64, err error) {
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" {
		return "", 0, ErrNotFound
	}
	tokenHash := hashToken(rawToken)
	nowMs := time.Now().UTC().UnixMilli()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", 0, fmt.Errorf("begin consume refresh token tx: %w", err)
	}
	defer tx.Rollback()

	err = tx.QueryRowContext(ctx, `
SELECT client_id, user_id
FROM oauth_refresh_tokens
WHERE token_hash = ? AND revoked_at IS NULL AND expires_at > ?
`, tokenHash, nowMs).Scan(&clientID, &userID)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", 0, ErrNotFound
		}
		return "", 0, fmt.Errorf("select oauth refresh token: %w", err)
	}

	res, err := tx.ExecContext(ctx, `
UPDATE oauth_refresh_tokens SET revoked_at = ?
WHERE token_hash = ? AND revoked_at IS NULL
`, nowMs, tokenHash)
	if err != nil {
		return "", 0, fmt.Errorf("revoke oauth refresh token: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return "", 0, fmt.Errorf("revoke oauth refresh token rows: %w", err)
	} else if n == 0 {
		return "", 0, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return "", 0, fmt.Errorf("commit consume refresh token tx: %w", err)
	}
	return clientID, userID, nil
}

// GetUserByOAuthAccessToken returns the user for an active (non-revoked,
// non-expired) OAuth access token. Mirrors GetUserByAPIToken's shape exactly
// so internal/mcp/adapter.go can use it as a drop-in fallback lookup.
func (s *Store) GetUserByOAuthAccessToken(ctx context.Context, rawToken string) (User, error) {
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" {
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
  AND t.revoked_at IS NULL
  AND t.expires_at > ?
`, tokenHash, nowMs).Scan(&u.ID, &u.Email, &u.Name, &isBootstrap, &systemRoleStr, &createdAt, &twoFactorEnabled)
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
