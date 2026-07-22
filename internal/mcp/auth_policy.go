package mcp

import (
	"context"
	"net/http"
)

type oauthBearerPolicyKey struct{}

// WithoutOAuthBearer returns a request whose authentication boundary accepts
// cookies and static API tokens, but never OAuth access tokens. Internal
// adapters use this when they delegate tool execution to /mcp/rpc without
// becoming additional OAuth protected resources themselves.
func WithoutOAuthBearer(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), oauthBearerPolicyKey{}, false))
}

func oauthBearerAllowed(r *http.Request) bool {
	allowed, present := r.Context().Value(oauthBearerPolicyKey{}).(bool)
	return !present || allowed
}
