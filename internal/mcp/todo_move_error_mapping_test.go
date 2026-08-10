package mcp

import (
	"net/http"
	"testing"

	todoapp "scrumboy/internal/application/todo"
	"scrumboy/internal/store"
)

func TestMapMCPMoveErrorPreservesAuthorizationSourceContract(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "anchor lane read uses generic mapping",
			err:        &todoapp.MCPMoveAnchorReadError{Err: store.ErrUnauthorized},
			wantStatus: http.StatusUnauthorized,
			wantCode:   CodeAuthRequired,
		},
		{
			name:       "todo lookup or move uses privileged mapping",
			err:        store.ErrUnauthorized,
			wantStatus: http.StatusForbidden,
			wantCode:   CodeForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapMCPMoveError(tt.err)
			if got.Status != tt.wantStatus || got.Code != tt.wantCode {
				t.Fatalf("mapMCPMoveError(%T) = status %d code %q, want %d %q", tt.err, got.Status, got.Code, tt.wantStatus, tt.wantCode)
			}
		})
	}
}
