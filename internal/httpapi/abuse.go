package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"scrumboy/internal/abusepolicy"
	"scrumboy/internal/httpapi/ratelimit"
	"scrumboy/internal/store"
)

func (s *Server) abuseEnabled() bool {
	return s.abusePolicy.RateLimits.Enabled && s.abuseAdmission != nil
}

func (s *Server) writeRateLimited(w http.ResponseWriter, rejected *ratelimit.Rejected) {
	retry := time.Second
	budget := ""
	if rejected != nil {
		if rejected.RetryAfter > 0 {
			retry = rejected.RetryAfter
		}
		budget = rejected.Budget
	}
	seconds := retryAfterSeconds(retry)
	if budget != "" {
		s.logger.Printf("abuse rate limit exceeded budget=%s", budget)
	} else {
		s.logger.Printf("abuse rate limit exceeded")
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many requests", nil)
}

func (s *Server) admitCosts(w http.ResponseWriter, costs []ratelimit.Cost) bool {
	if !s.abuseEnabled() {
		return true
	}
	if rejected := s.abuseAdmission.Admit(costs); rejected != nil {
		s.writeRateLimited(w, rejected)
		return false
	}
	return true
}

func (s *Server) creationCosts(ip string, n int) []ratelimit.Cost {
	rl := s.abusePolicy.RateLimits
	if n <= 0 {
		n = 1
	}
	return []ratelimit.Cost{
		{Key: "create-ip:" + ip, Limit: rl.CreatePerIP.Limit, Window: rl.CreatePerIP.Window, N: n},
		{Key: "create-ip-day:" + ip, Limit: rl.CreatePerIPDaily.Limit, Window: rl.CreatePerIPDaily.Window, N: n},
		{Key: "create-global", Limit: rl.CreateInstance.Limit, Window: rl.CreateInstance.Window, N: n},
		{Key: "create-global-day", Limit: rl.CreateInstanceDaily.Limit, Window: rl.CreateInstanceDaily.Window, N: n},
	}
}

func (s *Server) mutationCosts(ip string, projectID int64) []ratelimit.Cost {
	rl := s.abusePolicy.RateLimits
	return []ratelimit.Cost{
		{Key: "mut-ip:" + ip, Limit: rl.MutationPerIP.Limit, Window: rl.MutationPerIP.Window},
		{Key: "mut-burst-ip:" + ip, Limit: rl.MutationPerIPBurst.Limit, Window: rl.MutationPerIPBurst.Window},
		{Key: "mut-board:" + strconv.FormatInt(projectID, 10), Limit: rl.MutationPerBoard.Limit, Window: rl.MutationPerBoard.Window},
	}
}

func (s *Server) importCost(ip string) ratelimit.Cost {
	rl := s.abusePolicy.RateLimits
	return ratelimit.Cost{Key: "import-ip:" + ip, Limit: rl.BulkImportPerIP.Limit, Window: rl.BulkImportPerIP.Window}
}

func (s *Server) exportCost(ip string) ratelimit.Cost {
	rl := s.abusePolicy.RateLimits
	return ratelimit.Cost{Key: "export-ip:" + ip, Limit: rl.ExportPerIP.Limit, Window: rl.ExportPerIP.Window}
}

func (s *Server) admitAnonymousCreation(w http.ResponseWriter, r *http.Request) bool {
	return s.admitCosts(w, s.creationCosts(s.clientIP(r), 1))
}

func (s *Server) admitExpiringMutation(w http.ResponseWriter, r *http.Request, project store.Project) bool {
	if project.ExpiresAt == nil {
		return true
	}
	return s.admitCosts(w, s.mutationCosts(s.clientIP(r), project.ID))
}

// deferExpiringMutationAdmission is true when slug access is not the route's
// authorization: owner/maintainer/creator checks still run in the handler.
// Those handlers must call admitExpiringMutation after that check succeeds.
func deferExpiringMutationAdmission(method string, rest []string) bool {
	if !isMutatingMethod(method) || len(rest) == 0 {
		return false
	}
	if len(rest) == 1 {
		return method == http.MethodPatch
	}
	switch rest[1] {
	case "settings", "claim", "workflow", "priorities", "custom-fields", "sprints",
		"calendar-sources", "publication", "wall":
		return true
	default:
		return false
	}
}

func retryAfterSeconds(d time.Duration) int {
	if d <= 0 {
		return 1
	}
	sec := int((d + time.Second - 1) / time.Second)
	if sec < 1 {
		return 1
	}
	return sec
}

func (s *Server) admitAnonymousImport(w http.ResponseWriter, r *http.Request, newProjects int) bool {
	ip := s.clientIP(r)
	costs := []ratelimit.Cost{s.importCost(ip)}
	if newProjects > 0 {
		costs = append(costs, s.creationCosts(ip, newProjects)...)
	}
	return s.admitCosts(w, costs)
}

func (s *Server) admitAnonymousTargetedImport(w http.ResponseWriter, r *http.Request, project store.Project) bool {
	ip := s.clientIP(r)
	costs := append([]ratelimit.Cost{s.importCost(ip)}, s.mutationCosts(ip, project.ID)...)
	return s.admitCosts(w, costs)
}

func (s *Server) admitAnonymousExport(w http.ResponseWriter, r *http.Request) bool {
	return s.admitCosts(w, []ratelimit.Cost{s.exportCost(s.clientIP(r))})
}

func (s *Server) acquireAnonymousExportSlot(w http.ResponseWriter) bool {
	limit := s.abusePolicy.Safety.MaxInFlightAnonymousExports
	if limit <= 0 {
		limit = abusepolicy.DefaultMaxInFlightExports
	}
	for {
		cur := s.anonExportsInFlight.Load()
		if int(cur) >= limit {
			w.Header().Set("Retry-After", "10")
			writeError(w, http.StatusTooManyRequests, "RATE_LIMITED", "too many requests", nil)
			return false
		}
		if s.anonExportsInFlight.CompareAndSwap(cur, cur+1) {
			return true
		}
	}
}

func (s *Server) releaseAnonymousExportSlot() {
	for {
		cur := s.anonExportsInFlight.Load()
		if cur <= 0 {
			return
		}
		if s.anonExportsInFlight.CompareAndSwap(cur, cur-1) {
			return
		}
	}
}

func (s *Server) admitMCPExpiringMutation(project store.Project, ip string) error {
	if project.ExpiresAt == nil || !s.abuseEnabled() {
		return nil
	}
	if ip == "" {
		ip = "unknown"
	}
	rejected := s.abuseAdmission.Admit(s.mutationCosts(ip, project.ID))
	if rejected == nil {
		return nil
	}
	return rejected
}

func isMutatingMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}
