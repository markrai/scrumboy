package useradmin

import (
	"context"
	"errors"
	"time"

	"scrumboy/internal/auth/tokens"
	"scrumboy/internal/store"
)

var (
	// ErrSelfPasswordReset reports that the requester attempted to generate a
	// privileged reset link for their own account.
	ErrSelfPasswordReset = errors.New("cannot generate reset link for yourself")

	// ErrOwnerPasswordResetTarget reports that the target of a privileged reset
	// link is itself a system Owner.
	ErrOwnerPasswordResetTarget = errors.New("cannot reset password for another owner")

	// ErrResetTokenGeneration classifies a token-utility failure after the
	// password hash was read, while retaining the original cause text.
	ErrResetTokenGeneration = errors.New("user administration password reset token generation failed")
)

// resetTokenGenerationError preserves the original token-utility cause and its
// exact text while marking the issuance stage for the REST adapter mapper.
type resetTokenGenerationError struct {
	cause error
}

func (e *resetTokenGenerationError) Error() string {
	return e.cause.Error()
}

func (e *resetTokenGenerationError) Unwrap() error {
	return e.cause
}

func (e *resetTokenGenerationError) Is(target error) bool {
	return target == ErrResetTokenGeneration
}

// PasswordResetTokenIssuer signs one privileged password-reset token from an
// already-authorized target identity and current password hash.
type PasswordResetTokenIssuer interface {
	GeneratePasswordResetToken(secret []byte, userID int64, passwordHash string) (token string, expiresAt time.Time, err error)
}

type passwordResetTokenIssuerFunc func(secret []byte, userID int64, passwordHash string) (string, time.Time, error)

func (f passwordResetTokenIssuerFunc) GeneratePasswordResetToken(
	secret []byte,
	userID int64,
	passwordHash string,
) (string, time.Time, error) {
	return f(secret, userID, passwordHash)
}

// RESTPasswordResetServiceDependencies separates requester authority reads,
// target authority reads, password-hash retrieval, and token issuance even
// when production supplies one concrete store and the shared token utility.
type RESTPasswordResetServiceDependencies struct {
	RequesterRead UserReadStore
	TargetRead    UserReadStore
	Hashes        UserPasswordHashStore
	Tokens        PasswordResetTokenIssuer
}

// RESTPasswordResetService owns REST's requester Owner check, self-reset
// prohibition, Owner-target protection, password-hash retrieval, and signed
// token issuance. It does not construct URLs, apply rate limits, or mutate
// passwords.
type RESTPasswordResetService struct {
	requesterRead UserReadStore
	targetRead    UserReadStore
	hashes        UserPasswordHashStore
	tokens        PasswordResetTokenIssuer
}

// NewRESTPasswordResetService constructs the REST password-reset service.
func NewRESTPasswordResetService(deps RESTPasswordResetServiceDependencies) *RESTPasswordResetService {
	issuer := deps.Tokens
	if issuer == nil {
		issuer = passwordResetTokenIssuerFunc(tokens.GeneratePasswordResetToken)
	}
	return &RESTPasswordResetService{
		requesterRead: deps.RequesterRead,
		targetRead:    deps.TargetRead,
		hashes:        deps.Hashes,
		tokens:        issuer,
	}
}

// PreparedRESTPasswordReset binds the authorized actor, target, and exact
// request context for one privileged token issuance. Fields are unexported so
// callers cannot bypass Prepare.
type PreparedRESTPasswordReset struct {
	ctx          context.Context
	service      *RESTPasswordResetService
	requesterID  int64
	targetUserID int64
}

// IssuedPasswordReset is the signed token and expiry produced after
// authorization and rate limiting. The adapter owns URL encoding and JSON.
type IssuedPasswordReset struct {
	Token     string
	ExpiresAt time.Time
}

// Prepare preserves the characterized REST order: trusted actor extraction,
// requester read, exact Owner classification, self-reset prohibition, target
// read, and Owner-target protection. It performs no hash read or token
// issuance so the adapter can apply its existing rate limit in between.
func (s *RESTPasswordResetService) Prepare(
	ctx context.Context,
	command PasswordResetCommand,
) (*PreparedRESTPasswordReset, error) {
	requesterID, ok := store.UserIDFromContext(ctx)
	if !ok {
		return nil, ErrActorRequired
	}

	requester, err := s.requesterRead.GetUser(ctx, requesterID)
	if err != nil {
		return nil, err
	}
	if requester.SystemRole != store.SystemRoleOwner {
		return nil, ErrOwnerRequired
	}

	if requesterID == command.TargetUserID {
		return nil, ErrSelfPasswordReset
	}

	target, err := s.targetRead.GetUser(ctx, command.TargetUserID)
	if err != nil {
		return nil, err
	}
	if target.SystemRole == store.SystemRoleOwner {
		return nil, ErrOwnerPasswordResetTarget
	}

	return &PreparedRESTPasswordReset{
		ctx:          ctx,
		service:      s,
		requesterID:  requesterID,
		targetUserID: command.TargetUserID,
	}, nil
}

// RequesterID returns the trusted actor bound during Prepare so the adapter
// can apply its existing per-requester rate-limit key.
func (p *PreparedRESTPasswordReset) RequesterID() int64 {
	return p.requesterID
}

// Issue reads the target's current password hash and signs one reset token.
// Persistence failures are returned unmodified; token-utility failures are
// classified as ErrResetTokenGeneration while keeping the original Error text.
func (p *PreparedRESTPasswordReset) Issue(secret []byte) (IssuedPasswordReset, error) {
	passwordHash, err := p.service.hashes.GetUserPasswordHash(p.ctx, p.targetUserID)
	if err != nil {
		return IssuedPasswordReset{}, err
	}

	token, expiresAt, err := p.service.tokens.GeneratePasswordResetToken(secret, p.targetUserID, passwordHash)
	if err != nil {
		return IssuedPasswordReset{}, &resetTokenGenerationError{cause: err}
	}

	return IssuedPasswordReset{Token: token, ExpiresAt: expiresAt}, nil
}
