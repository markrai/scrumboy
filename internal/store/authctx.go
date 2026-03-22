package store

import "context"

type ctxKey int

const (
	ctxKeyUserID ctxKey = iota
	ctxKeyUserEmail
	ctxKeyUserName
)

// WithUserID attaches the authenticated user id to the context.
func WithUserID(ctx context.Context, userID int64) context.Context {
	return context.WithValue(ctx, ctxKeyUserID, userID)
}

// WithUserEmail attaches the authenticated user email to the context.
func WithUserEmail(ctx context.Context, email string) context.Context {
	return context.WithValue(ctx, ctxKeyUserEmail, email)
}

// WithUserName attaches the authenticated user name to the context.
func WithUserName(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, ctxKeyUserName, name)
}

// UserIDFromContext retrieves the authenticated user id from the context.
func UserIDFromContext(ctx context.Context) (int64, bool) {
	v := ctx.Value(ctxKeyUserID)
	id, ok := v.(int64)
	return id, ok && id > 0
}

// UserEmailFromContext retrieves the authenticated user email from the context.
func UserEmailFromContext(ctx context.Context) (string, bool) {
	v := ctx.Value(ctxKeyUserEmail)
	s, ok := v.(string)
	return s, ok && s != ""
}

// UserNameFromContext retrieves the authenticated user name from the context.
func UserNameFromContext(ctx context.Context) (string, bool) {
	v := ctx.Value(ctxKeyUserName)
	s, ok := v.(string)
	return s, ok && s != ""
}
