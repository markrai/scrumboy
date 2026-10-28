package useradmin

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"scrumboy/internal/auth/tokens"
	"scrumboy/internal/store"
)

type restPasswordResetContextKey struct{}

type restPasswordResetReadFake struct {
	label  string
	trace  *[]string
	user   store.User
	err    error
	calls  int
	userID int64
	marker any
}

func (f *restPasswordResetReadFake) GetUser(ctx context.Context, userID int64) (store.User, error) {
	*f.trace = append(*f.trace, f.label)
	f.calls++
	f.userID = userID
	f.marker = ctx.Value(restPasswordResetContextKey{})
	return f.user, f.err
}

type restPasswordResetHashFake struct {
	trace  *[]string
	hash   string
	err    error
	calls  int
	userID int64
	marker any
}

func (f *restPasswordResetHashFake) GetUserPasswordHash(ctx context.Context, userID int64) (string, error) {
	*f.trace = append(*f.trace, "password-hash")
	f.calls++
	f.userID = userID
	f.marker = ctx.Value(restPasswordResetContextKey{})
	return f.hash, f.err
}

type restPasswordResetTokenFake struct {
	trace   *[]string
	token   string
	expires time.Time
	err     error
	calls   int
	secret  []byte
	userID  int64
	hash    string
}

func (f *restPasswordResetTokenFake) GeneratePasswordResetToken(
	secret []byte,
	userID int64,
	passwordHash string,
) (string, time.Time, error) {
	*f.trace = append(*f.trace, "token")
	f.calls++
	f.secret = append([]byte(nil), secret...)
	f.userID = userID
	f.hash = passwordHash
	return f.token, f.expires, f.err
}

func newRESTPasswordResetTestService(
	trace *[]string,
) (*RESTPasswordResetService, *restPasswordResetReadFake, *restPasswordResetReadFake, *restPasswordResetHashFake, *restPasswordResetTokenFake) {
	requester := &restPasswordResetReadFake{
		label: "requester-read",
		trace: trace,
		user:  store.User{ID: 17, SystemRole: store.SystemRoleOwner},
	}
	target := &restPasswordResetReadFake{
		label: "target-read",
		trace: trace,
		user:  store.User{ID: 41, SystemRole: store.SystemRoleUser},
	}
	hashes := &restPasswordResetHashFake{trace: trace, hash: "current-hash"}
	issuer := &restPasswordResetTokenFake{
		trace:   trace,
		token:   "issued-token",
		expires: time.Date(2026, time.October, 26, 16, 0, 0, 0, time.UTC),
	}
	service := NewRESTPasswordResetService(RESTPasswordResetServiceDependencies{
		RequesterRead: requester,
		TargetRead:    target,
		Hashes:        hashes,
		Tokens:        issuer,
	})
	return service, requester, target, hashes, issuer
}

func TestRESTPasswordResetPrepareRequiresActor(t *testing.T) {
	trace := []string{}
	service, requester, target, hashes, issuer := newRESTPasswordResetTestService(&trace)

	prepared, err := service.Prepare(context.Background(), PasswordResetCommand{TargetUserID: 41})
	if prepared != nil || !errors.Is(err, ErrActorRequired) {
		t.Fatalf("Prepare() = (%v, %v), want (nil, ErrActorRequired)", prepared, err)
	}
	if requester.calls != 0 || target.calls != 0 || hashes.calls != 0 || issuer.calls != 0 || len(trace) != 0 {
		t.Fatalf("missing actor reached dependencies: trace=%v", trace)
	}
}

func TestRESTPasswordResetPrepareRequesterReadFailure(t *testing.T) {
	trace := []string{}
	service, requester, target, hashes, issuer := newRESTPasswordResetTestService(&trace)
	wantErr := errors.New("requester read failed")
	requester.err = wantErr

	prepared, err := service.Prepare(
		store.WithUserID(context.Background(), 17),
		PasswordResetCommand{TargetUserID: 41},
	)
	if prepared != nil || err != wantErr {
		t.Fatalf("Prepare() = (%v, %v), want (nil, original error)", prepared, err)
	}
	if requester.calls != 1 || target.calls != 0 || hashes.calls != 0 || issuer.calls != 0 ||
		!reflect.DeepEqual(trace, []string{"requester-read"}) {
		t.Fatalf("requester failure calls=%d/%d/%d/%d trace=%v", requester.calls, target.calls, hashes.calls, issuer.calls, trace)
	}
}

func TestRESTPasswordResetPrepareRequiresOwner(t *testing.T) {
	for _, role := range []store.SystemRole{store.SystemRoleUser, store.SystemRoleAdmin} {
		t.Run(role.String(), func(t *testing.T) {
			trace := []string{}
			service, requester, target, hashes, issuer := newRESTPasswordResetTestService(&trace)
			requester.user.SystemRole = role

			prepared, err := service.Prepare(
				store.WithUserID(context.Background(), 17),
				PasswordResetCommand{TargetUserID: 41},
			)
			if prepared != nil || !errors.Is(err, ErrOwnerRequired) {
				t.Fatalf("Prepare() = (%v, %v), want (nil, ErrOwnerRequired)", prepared, err)
			}
			if requester.calls != 1 || target.calls != 0 || hashes.calls != 0 || issuer.calls != 0 ||
				!reflect.DeepEqual(trace, []string{"requester-read"}) {
				t.Fatalf("role gate calls=%d/%d/%d/%d trace=%v", requester.calls, target.calls, hashes.calls, issuer.calls, trace)
			}
		})
	}
}

func TestRESTPasswordResetPrepareSelfResetStopsBeforeTargetRead(t *testing.T) {
	trace := []string{}
	service, requester, target, hashes, issuer := newRESTPasswordResetTestService(&trace)

	prepared, err := service.Prepare(
		store.WithUserID(context.Background(), 17),
		PasswordResetCommand{TargetUserID: 17},
	)
	if prepared != nil || !errors.Is(err, ErrSelfPasswordReset) {
		t.Fatalf("Prepare() = (%v, %v), want (nil, ErrSelfPasswordReset)", prepared, err)
	}
	if requester.calls != 1 || target.calls != 0 || hashes.calls != 0 || issuer.calls != 0 ||
		!reflect.DeepEqual(trace, []string{"requester-read"}) {
		t.Fatalf("self-reset calls=%d/%d/%d/%d trace=%v", requester.calls, target.calls, hashes.calls, issuer.calls, trace)
	}
}

func TestRESTPasswordResetPrepareTargetReadFailure(t *testing.T) {
	trace := []string{}
	service, requester, target, hashes, issuer := newRESTPasswordResetTestService(&trace)
	wantErr := errors.New("target read failed")
	target.err = wantErr

	prepared, err := service.Prepare(
		store.WithUserID(context.Background(), 17),
		PasswordResetCommand{TargetUserID: 41},
	)
	if prepared != nil || err != wantErr {
		t.Fatalf("Prepare() = (%v, %v), want (nil, original error)", prepared, err)
	}
	if requester.calls != 1 || target.calls != 1 || hashes.calls != 0 || issuer.calls != 0 ||
		!reflect.DeepEqual(trace, []string{"requester-read", "target-read"}) {
		t.Fatalf("target failure calls=%d/%d/%d/%d trace=%v", requester.calls, target.calls, hashes.calls, issuer.calls, trace)
	}
}

func TestRESTPasswordResetPrepareRejectsOwnerTarget(t *testing.T) {
	trace := []string{}
	service, requester, target, hashes, issuer := newRESTPasswordResetTestService(&trace)
	target.user.SystemRole = store.SystemRoleOwner

	prepared, err := service.Prepare(
		store.WithUserID(context.Background(), 17),
		PasswordResetCommand{TargetUserID: 41},
	)
	if prepared != nil || !errors.Is(err, ErrOwnerPasswordResetTarget) {
		t.Fatalf("Prepare() = (%v, %v), want (nil, ErrOwnerPasswordResetTarget)", prepared, err)
	}
	if requester.calls != 1 || target.calls != 1 || hashes.calls != 0 || issuer.calls != 0 ||
		!reflect.DeepEqual(trace, []string{"requester-read", "target-read"}) {
		t.Fatalf("owner-target calls=%d/%d/%d/%d trace=%v", requester.calls, target.calls, hashes.calls, issuer.calls, trace)
	}
}

func TestRESTPasswordResetIssueOrderHashAndContext(t *testing.T) {
	trace := []string{}
	service, requester, target, hashes, issuer := newRESTPasswordResetTestService(&trace)
	hashes.hash = "bound-hash"
	secret := []byte("0123456789abcdef0123456789abcdef")
	ctx := store.WithUserID(
		context.WithValue(context.Background(), restPasswordResetContextKey{}, "reset-context"),
		17,
	)

	prepared, err := service.Prepare(ctx, PasswordResetCommand{TargetUserID: 41})
	if err != nil {
		t.Fatalf("Prepare(): %v", err)
	}
	if prepared.RequesterID() != 17 {
		t.Fatalf("RequesterID() = %d, want 17", prepared.RequesterID())
	}
	if hashes.calls != 0 || issuer.calls != 0 {
		t.Fatalf("Prepare issued a token: hash/token=%d/%d", hashes.calls, issuer.calls)
	}

	got, err := prepared.Issue(secret)
	if err != nil {
		t.Fatalf("Issue(): %v", err)
	}
	if got.Token != "issued-token" || !got.ExpiresAt.Equal(issuer.expires) {
		t.Fatalf("Issue() = %+v", got)
	}
	if wantTrace := []string{"requester-read", "target-read", "password-hash", "token"}; !reflect.DeepEqual(trace, wantTrace) {
		t.Fatalf("trace = %v, want %v", trace, wantTrace)
	}
	if requester.userID != 17 || target.userID != 41 || hashes.userID != 41 || issuer.userID != 41 {
		t.Fatalf("ids requester=%d target=%d hash=%d token=%d", requester.userID, target.userID, hashes.userID, issuer.userID)
	}
	if hashes.hash != "bound-hash" || issuer.hash != "bound-hash" {
		t.Fatalf("hash binding hash-store=%q issuer=%q", hashes.hash, issuer.hash)
	}
	if !reflect.DeepEqual(issuer.secret, secret) {
		t.Fatalf("issuer secret = %q, want %q", issuer.secret, secret)
	}
	if requester.marker != "reset-context" || target.marker != "reset-context" || hashes.marker != "reset-context" {
		t.Fatalf("context markers requester=%v target=%v hash=%v", requester.marker, target.marker, hashes.marker)
	}
}

func TestRESTPasswordResetHashFailureStopsTokenIssuance(t *testing.T) {
	trace := []string{}
	service, _, _, hashes, issuer := newRESTPasswordResetTestService(&trace)
	wantErr := errors.New("hash read failed")
	hashes.err = wantErr

	prepared, err := service.Prepare(
		store.WithUserID(context.Background(), 17),
		PasswordResetCommand{TargetUserID: 41},
	)
	if err != nil {
		t.Fatalf("Prepare(): %v", err)
	}
	got, err := prepared.Issue([]byte("secret"))
	if got != (IssuedPasswordReset{}) || err != wantErr {
		t.Fatalf("Issue() = (%+v, %v), want (zero, original error)", got, err)
	}
	if errors.Is(err, ErrResetTokenGeneration) {
		t.Fatal("hash failure classified as token generation")
	}
	if hashes.calls != 1 || issuer.calls != 0 ||
		!reflect.DeepEqual(trace, []string{"requester-read", "target-read", "password-hash"}) {
		t.Fatalf("hash failure calls hash/token=%d/%d trace=%v", hashes.calls, issuer.calls, trace)
	}
}

func TestRESTPasswordResetTokenGenerationIsDistinguished(t *testing.T) {
	trace := []string{}
	service, _, _, hashes, issuer := newRESTPasswordResetTestService(&trace)
	wantErr := errors.New("forced token failure")
	issuer.err = wantErr

	prepared, err := service.Prepare(
		store.WithUserID(context.Background(), 17),
		PasswordResetCommand{TargetUserID: 41},
	)
	if err != nil {
		t.Fatalf("Prepare(): %v", err)
	}
	got, err := prepared.Issue([]byte("secret"))
	if got != (IssuedPasswordReset{}) || !errors.Is(err, ErrResetTokenGeneration) {
		t.Fatalf("Issue() = (%+v, %v), want token-generation sentinel", got, err)
	}
	if err.Error() != wantErr.Error() {
		t.Fatalf("Issue() error text = %q, want original %q", err.Error(), wantErr.Error())
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("Issue() error does not unwrap to original cause: %v", err)
	}
	if hashes.calls != 1 || issuer.calls != 1 ||
		!reflect.DeepEqual(trace, []string{"requester-read", "target-read", "password-hash", "token"}) {
		t.Fatalf("token failure calls hash/token=%d/%d trace=%v", hashes.calls, issuer.calls, trace)
	}
}

func TestRESTPasswordResetDefaultIssuerUsesCurrentHash(t *testing.T) {
	trace := []string{}
	requester := &restPasswordResetReadFake{
		label: "requester-read",
		trace: &trace,
		user:  store.User{ID: 17, SystemRole: store.SystemRoleOwner},
	}
	target := &restPasswordResetReadFake{
		label: "target-read",
		trace: &trace,
		user:  store.User{ID: 41, SystemRole: store.SystemRoleAdmin},
	}
	hashes := &restPasswordResetHashFake{trace: &trace, hash: "live-password-hash"}
	service := NewRESTPasswordResetService(RESTPasswordResetServiceDependencies{
		RequesterRead: requester,
		TargetRead:    target,
		Hashes:        hashes,
	})
	secret := []byte("0123456789abcdef0123456789abcdef")

	prepared, err := service.Prepare(
		store.WithUserID(context.Background(), 17),
		PasswordResetCommand{TargetUserID: 41},
	)
	if err != nil {
		t.Fatalf("Prepare(): %v", err)
	}
	issued, err := prepared.Issue(secret)
	if err != nil {
		t.Fatalf("Issue(): %v", err)
	}
	userID, timestamp, signature, err := tokens.ParsePasswordResetToken(issued.Token)
	if err != nil {
		t.Fatalf("ParsePasswordResetToken: %v", err)
	}
	if userID != 41 {
		t.Fatalf("token user = %d, want 41", userID)
	}
	if err := tokens.VerifyPasswordResetToken(secret, userID, timestamp, signature, "live-password-hash"); err != nil {
		t.Fatalf("VerifyPasswordResetToken: %v", err)
	}
	until := time.Until(issued.ExpiresAt)
	if until < 29*time.Minute || until > 31*time.Minute {
		t.Fatalf("expires delta = %s", until)
	}
	if hashes.calls != 1 || hashes.userID != 41 {
		t.Fatalf("hash calls=%d user=%d", hashes.calls, hashes.userID)
	}
}

func TestRESTPasswordResetPreparedValuesAndContextAreOwned(t *testing.T) {
	trace := []string{}
	service, requester, target, hashes, issuer := newRESTPasswordResetTestService(&trace)
	ctx := store.WithUserID(
		context.WithValue(context.Background(), restPasswordResetContextKey{}, "owned-context"),
		17,
	)
	command := PasswordResetCommand{TargetUserID: 41}

	prepared, err := service.Prepare(ctx, command)
	if err != nil {
		t.Fatalf("Prepare(): %v", err)
	}
	command.TargetUserID = 999
	ctx = context.Background()

	if _, err := prepared.Issue([]byte("secret")); err != nil {
		t.Fatalf("Issue(): %v", err)
	}
	if requester.userID != 17 || target.userID != 41 || hashes.userID != 41 || issuer.userID != 41 {
		t.Fatalf("bound ids requester=%d target=%d hash=%d token=%d", requester.userID, target.userID, hashes.userID, issuer.userID)
	}
	if requester.marker != "owned-context" || target.marker != "owned-context" || hashes.marker != "owned-context" {
		t.Fatalf("bound context markers requester=%v target=%v hash=%v", requester.marker, target.marker, hashes.marker)
	}
}

func TestRESTPasswordResetDoesNotExposePasswordMutation(t *testing.T) {
	trace := []string{}
	service, _, _, hashes, issuer := newRESTPasswordResetTestService(&trace)

	prepared, err := service.Prepare(
		store.WithUserID(context.Background(), 17),
		PasswordResetCommand{TargetUserID: 41},
	)
	if err != nil {
		t.Fatalf("Prepare(): %v", err)
	}
	if _, err := prepared.Issue([]byte("secret")); err != nil {
		t.Fatalf("Issue(): %v", err)
	}
	if !reflect.DeepEqual(trace, []string{"requester-read", "target-read", "password-hash", "token"}) {
		t.Fatalf("Issue reached unexpected capabilities: %v", trace)
	}
	if hashes.calls != 1 || issuer.calls != 1 {
		t.Fatalf("hash/token calls = %d/%d, want 1/1", hashes.calls, issuer.calls)
	}
}
