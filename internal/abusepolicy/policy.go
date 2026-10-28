// Package abusepolicy loads the optional JSON abuse-protection file pointed to
// by SCRUMBOY_ABUSE_POLICY_FILE.
//
// A missing file yields built-in defaults. rateLimits.enabled=false disables
// request throttling only; safety ceilings and access-boundary fixes stay on.
package abusepolicy

import (
	"fmt"
	"time"
)

const CurrentVersion = 1

const (
	DefaultMaxImportProjects  = 20
	DefaultMaxImportTodos     = 2000
	DefaultMaxInFlightExports = 1
	DefaultAnonSSEGlobal      = 500
	DefaultAnonSSEPerIP       = 10
	DefaultAnonSSEPerBoard    = 50
)

// WindowLimit is one fixed-window budget.
type WindowLimit struct {
	Limit  int
	Window time.Duration
}

// RateLimits are process-local request budgets. Disabled throttling must not
// weaken CSRF, authorization, payload limits, or SafetyCeilings.
type RateLimits struct {
	Enabled bool

	CreatePerIP         WindowLimit
	CreatePerIPDaily    WindowLimit
	CreateInstance      WindowLimit
	CreateInstanceDaily WindowLimit

	MutationPerIP      WindowLimit
	MutationPerIPBurst WindowLimit
	MutationPerBoard   WindowLimit

	BulkImportPerIP WindowLimit
	ExportPerIP     WindowLimit
}

// SafetyCeilings are always enforced, including when RateLimits.Enabled is false.
type SafetyCeilings struct {
	MaxTodosPerImport           int
	MaxProjectsPerImport        int
	MaxInFlightAnonymousExports int
	AnonymousSSEGlobal          int
	AnonymousSSEPerIP           int
	AnonymousSSEPerBoard        int
}

// Policy is the fully resolved configuration used at runtime.
type Policy struct {
	Source     string // "defaults" or the absolute file path
	Version    int
	RateLimits RateLimits
	Safety     SafetyCeilings
}

// DisabledThrottling returns Default with request budgets off. Safety ceilings
// and access-boundary rules stay in force.
func DisabledThrottling() Policy {
	p := Default()
	p.RateLimits.Enabled = false
	return p
}

// Default returns the built-in P0 policy for a single-instance self-hosted
// deployment. Workshop operators raise creation windows in the JSON file.
func Default() Policy {
	return Policy{
		Source:  "defaults",
		Version: CurrentVersion,
		RateLimits: RateLimits{
			Enabled:             true,
			CreatePerIP:         WindowLimit{Limit: 5, Window: time.Hour},
			CreatePerIPDaily:    WindowLimit{Limit: 10, Window: 24 * time.Hour},
			CreateInstance:      WindowLimit{Limit: 20, Window: time.Hour},
			CreateInstanceDaily: WindowLimit{Limit: 50, Window: 24 * time.Hour},
			MutationPerIP:       WindowLimit{Limit: 120, Window: time.Minute},
			MutationPerIPBurst:  WindowLimit{Limit: 40, Window: 10 * time.Second},
			MutationPerBoard:    WindowLimit{Limit: 240, Window: time.Minute},
			BulkImportPerIP:     WindowLimit{Limit: 10, Window: time.Hour},
			ExportPerIP:         WindowLimit{Limit: 10, Window: time.Hour},
		},
		Safety: SafetyCeilings{
			MaxTodosPerImport:           DefaultMaxImportTodos,
			MaxProjectsPerImport:        DefaultMaxImportProjects,
			MaxInFlightAnonymousExports: DefaultMaxInFlightExports,
			AnonymousSSEGlobal:          DefaultAnonSSEGlobal,
			AnonymousSSEPerIP:           DefaultAnonSSEPerIP,
			AnonymousSSEPerBoard:        DefaultAnonSSEPerBoard,
		},
	}
}

// LogSummary is a single-line operator-facing description with no client data.
func (p Policy) LogSummary() string {
	rl := p.RateLimits
	s := p.Safety
	enabled := "true"
	if !rl.Enabled {
		enabled = "false"
	}
	return fmt.Sprintf(
		"source=%s version=%d rateLimits.enabled=%s creation perIP=%s perIPDaily=%s instance=%s instanceDaily=%s mutations perIP=%s burst=%s perBoard=%s imports perIP=%s exports perIP=%s ceilings todos=%d projects=%d inFlightExports=%d sse global=%d perIP=%d perBoard=%d",
		p.Source,
		p.Version,
		enabled,
		formatWindow(rl.CreatePerIP),
		formatWindow(rl.CreatePerIPDaily),
		formatWindow(rl.CreateInstance),
		formatWindow(rl.CreateInstanceDaily),
		formatWindow(rl.MutationPerIP),
		formatWindow(rl.MutationPerIPBurst),
		formatWindow(rl.MutationPerBoard),
		formatWindow(rl.BulkImportPerIP),
		formatWindow(rl.ExportPerIP),
		s.MaxTodosPerImport,
		s.MaxProjectsPerImport,
		s.MaxInFlightAnonymousExports,
		s.AnonymousSSEGlobal,
		s.AnonymousSSEPerIP,
		s.AnonymousSSEPerBoard,
	)
}

func formatWindow(w WindowLimit) string {
	return fmt.Sprintf("%d/%s", w.Limit, formatDuration(w.Window))
}

func formatDuration(d time.Duration) string {
	if d < 0 {
		d = -d
	}
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", d/time.Hour)
	}
	if d%time.Minute == 0 {
		return fmt.Sprintf("%dm", d/time.Minute)
	}
	if d < time.Minute && d%time.Second == 0 {
		return fmt.Sprintf("%ds", d/time.Second)
	}
	return d.String()
}
