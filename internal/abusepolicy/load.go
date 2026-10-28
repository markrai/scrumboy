package abusepolicy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const maxLimit = 1_000_000

// Load reads path and merges it onto Default(). An empty path returns Default.
// Any validation failure is an error; callers should fail startup.
func Load(path string) (Policy, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return Default(), nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return Policy{}, fmt.Errorf("abuse policy: resolve %q: %w", path, err)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return Policy{}, fmt.Errorf("abuse policy: read %s: %w", abs, err)
	}
	policy, err := Parse(data)
	if err != nil {
		return Policy{}, fmt.Errorf("abuse policy: %s: %w", abs, err)
	}
	policy.Source = abs
	return policy, nil
}

// Parse decodes a JSON policy document onto defaults.
func Parse(data []byte) (Policy, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return Policy{}, fmt.Errorf("file is empty")
	}
	root, err := decodeObject(trimmed, map[string]struct{}{
		"version":        {},
		"rateLimits":     {},
		"safetyCeilings": {},
	})
	if err != nil {
		return Policy{}, err
	}
	rawVersion, ok := root["version"]
	if !ok {
		return Policy{}, fmt.Errorf("missing required key \"version\"")
	}
	version, err := parseInt(rawVersion, "version")
	if err != nil {
		return Policy{}, err
	}
	if version != CurrentVersion {
		return Policy{}, fmt.Errorf("unsupported version %d (supported: %d)", version, CurrentVersion)
	}

	out := Default()
	out.Version = version
	if raw, ok := root["rateLimits"]; ok {
		rl, err := parseRateLimits(raw, out.RateLimits)
		if err != nil {
			return Policy{}, err
		}
		out.RateLimits = rl
	}
	if raw, ok := root["safetyCeilings"]; ok {
		safety, err := parseSafety(raw, out.Safety)
		if err != nil {
			return Policy{}, err
		}
		out.Safety = safety
	}
	return out, nil
}

func parseRateLimits(raw json.RawMessage, base RateLimits) (RateLimits, error) {
	obj, err := decodeObject(raw, map[string]struct{}{
		"enabled":                 {},
		"anonymousBoardCreation":  {},
		"mutations":               {},
		"bulkImports":             {},
		"exports":                 {},
	})
	if err != nil {
		return RateLimits{}, fmt.Errorf("rateLimits: %w", err)
	}
	if rawEnabled, ok := obj["enabled"]; ok {
		enabled, err := parseBool(rawEnabled, "rateLimits.enabled")
		if err != nil {
			return RateLimits{}, err
		}
		base.Enabled = enabled
	}
	if raw, ok := obj["anonymousBoardCreation"]; ok {
		created, err := decodeObject(raw, map[string]struct{}{
			"perIP":         {},
			"perIPDaily":    {},
			"instance":      {},
			"instanceDaily": {},
		})
		if err != nil {
			return RateLimits{}, fmt.Errorf("rateLimits.anonymousBoardCreation: %w", err)
		}
		if err := assignWindow(created, "perIP", "rateLimits.anonymousBoardCreation.perIP", &base.CreatePerIP); err != nil {
			return RateLimits{}, err
		}
		if err := assignWindow(created, "perIPDaily", "rateLimits.anonymousBoardCreation.perIPDaily", &base.CreatePerIPDaily); err != nil {
			return RateLimits{}, err
		}
		if err := assignWindow(created, "instance", "rateLimits.anonymousBoardCreation.instance", &base.CreateInstance); err != nil {
			return RateLimits{}, err
		}
		if err := assignWindow(created, "instanceDaily", "rateLimits.anonymousBoardCreation.instanceDaily", &base.CreateInstanceDaily); err != nil {
			return RateLimits{}, err
		}
	}
	if raw, ok := obj["mutations"]; ok {
		mut, err := decodeObject(raw, map[string]struct{}{
			"perIP":      {},
			"perIPBurst": {},
			"perBoard":   {},
		})
		if err != nil {
			return RateLimits{}, fmt.Errorf("rateLimits.mutations: %w", err)
		}
		if err := assignWindow(mut, "perIP", "rateLimits.mutations.perIP", &base.MutationPerIP); err != nil {
			return RateLimits{}, err
		}
		if err := assignWindow(mut, "perIPBurst", "rateLimits.mutations.perIPBurst", &base.MutationPerIPBurst); err != nil {
			return RateLimits{}, err
		}
		if err := assignWindow(mut, "perBoard", "rateLimits.mutations.perBoard", &base.MutationPerBoard); err != nil {
			return RateLimits{}, err
		}
	}
	if raw, ok := obj["bulkImports"]; ok {
		imp, err := decodeObject(raw, map[string]struct{}{"perIP": {}})
		if err != nil {
			return RateLimits{}, fmt.Errorf("rateLimits.bulkImports: %w", err)
		}
		if err := assignWindow(imp, "perIP", "rateLimits.bulkImports.perIP", &base.BulkImportPerIP); err != nil {
			return RateLimits{}, err
		}
	}
	if raw, ok := obj["exports"]; ok {
		exp, err := decodeObject(raw, map[string]struct{}{"perIP": {}})
		if err != nil {
			return RateLimits{}, fmt.Errorf("rateLimits.exports: %w", err)
		}
		if err := assignWindow(exp, "perIP", "rateLimits.exports.perIP", &base.ExportPerIP); err != nil {
			return RateLimits{}, err
		}
	}
	return base, nil
}

func parseSafety(raw json.RawMessage, base SafetyCeilings) (SafetyCeilings, error) {
	obj, err := decodeObject(raw, map[string]struct{}{
		"maxTodosPerImport":           {},
		"maxProjectsPerImport":        {},
		"maxInFlightAnonymousExports": {},
		"anonymousSSEGlobal":          {},
		"anonymousSSEPerIP":           {},
		"anonymousSSEPerBoard":        {},
	})
	if err != nil {
		return SafetyCeilings{}, fmt.Errorf("safetyCeilings: %w", err)
	}
	if err := assignPositiveInt(obj, "maxTodosPerImport", "safetyCeilings.maxTodosPerImport", &base.MaxTodosPerImport); err != nil {
		return SafetyCeilings{}, err
	}
	if err := assignPositiveInt(obj, "maxProjectsPerImport", "safetyCeilings.maxProjectsPerImport", &base.MaxProjectsPerImport); err != nil {
		return SafetyCeilings{}, err
	}
	if err := assignPositiveInt(obj, "maxInFlightAnonymousExports", "safetyCeilings.maxInFlightAnonymousExports", &base.MaxInFlightAnonymousExports); err != nil {
		return SafetyCeilings{}, err
	}
	if err := assignPositiveInt(obj, "anonymousSSEGlobal", "safetyCeilings.anonymousSSEGlobal", &base.AnonymousSSEGlobal); err != nil {
		return SafetyCeilings{}, err
	}
	if err := assignPositiveInt(obj, "anonymousSSEPerIP", "safetyCeilings.anonymousSSEPerIP", &base.AnonymousSSEPerIP); err != nil {
		return SafetyCeilings{}, err
	}
	if err := assignPositiveInt(obj, "anonymousSSEPerBoard", "safetyCeilings.anonymousSSEPerBoard", &base.AnonymousSSEPerBoard); err != nil {
		return SafetyCeilings{}, err
	}
	return base, nil
}

func assignWindow(obj map[string]json.RawMessage, key, path string, dest *WindowLimit) error {
	raw, ok := obj[key]
	if !ok {
		return nil
	}
	limit, err := parseWindowLimit(raw, path)
	if err != nil {
		return err
	}
	*dest = limit
	return nil
}

func assignPositiveInt(obj map[string]json.RawMessage, key, path string, dest *int) error {
	raw, ok := obj[key]
	if !ok {
		return nil
	}
	n, err := parseInt(raw, path)
	if err != nil {
		return err
	}
	if n <= 0 {
		return fmt.Errorf("%s must be a positive integer", path)
	}
	if n > maxLimit {
		return fmt.Errorf("%s exceeds maximum %d", path, maxLimit)
	}
	*dest = n
	return nil
}

func parseWindowLimit(raw json.RawMessage, path string) (WindowLimit, error) {
	obj, err := decodeObject(raw, map[string]struct{}{"limit": {}, "window": {}})
	if err != nil {
		return WindowLimit{}, fmt.Errorf("%s: %w", path, err)
	}
	rawLimit, ok := obj["limit"]
	if !ok {
		return WindowLimit{}, fmt.Errorf("%s.limit is required", path)
	}
	rawWindow, ok := obj["window"]
	if !ok {
		return WindowLimit{}, fmt.Errorf("%s.window is required", path)
	}
	limit, err := parseInt(rawLimit, path+".limit")
	if err != nil {
		return WindowLimit{}, err
	}
	if limit <= 0 {
		return WindowLimit{}, fmt.Errorf("%s.limit must be a positive integer (use rateLimits.enabled=false to disable throttling)", path)
	}
	if limit > maxLimit {
		return WindowLimit{}, fmt.Errorf("%s.limit exceeds maximum %d", path, maxLimit)
	}
	window, err := parseWindowDuration(rawWindow, path+".window")
	if err != nil {
		return WindowLimit{}, err
	}
	return WindowLimit{Limit: limit, Window: window}, nil
}

func decodeObject(raw json.RawMessage, allowed map[string]struct{}) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var obj map[string]json.RawMessage
	if err := dec.Decode(&obj); err != nil {
		return nil, fmt.Errorf("invalid JSON object: %w", err)
	}
	if obj == nil {
		return nil, fmt.Errorf("must not be null")
	}
	for key, value := range obj {
		if _, ok := allowed[key]; !ok {
			return nil, fmt.Errorf("unknown key %q", key)
		}
		trimmed := bytes.TrimSpace(value)
		if bytes.Equal(trimmed, []byte("null")) {
			return nil, fmt.Errorf("%q must not be null", key)
		}
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("unexpected extra JSON")
		}
		return nil, fmt.Errorf("invalid JSON object: %w", err)
	}
	return obj, nil
}

func parseInt(raw json.RawMessage, path string) (int, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return 0, fmt.Errorf("%s is empty", path)
	}
	if bytes.ContainsAny(trimmed, ".eE") {
		return 0, fmt.Errorf("%s must be an integer", path)
	}
	n, err := strconv.ParseInt(string(trimmed), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", path, err)
	}
	if n > math.MaxInt || n < math.MinInt {
		return 0, fmt.Errorf("%s overflows", path)
	}
	return int(n), nil
}

func parseBool(raw json.RawMessage, path string) (bool, error) {
	var v bool
	if err := json.Unmarshal(raw, &v); err != nil {
		return false, fmt.Errorf("%s must be a boolean", path)
	}
	return v, nil
}

func parseWindowDuration(raw json.RawMessage, path string) (time.Duration, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, fmt.Errorf("%s must be a duration string such as \"10s\", \"1m\", or \"1h\"", path)
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("%s is empty", path)
	}
	if strings.ContainsAny(s, "un") || strings.HasSuffix(s, "ms") {
		return 0, fmt.Errorf("%s must use s, m, or h units", path)
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("%s is not a valid duration: %w", path, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s must be positive", path)
	}
	switch {
	case d%time.Hour == 0, d%time.Minute == 0, d%time.Second == 0:
		// ok
	default:
		return 0, fmt.Errorf("%s must be a whole number of seconds", path)
	}
	if d > 168*time.Hour {
		return 0, fmt.Errorf("%s exceeds maximum 168h", path)
	}
	return d, nil
}
