package publicorigin

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode"
)

const (
	MCPResourcePath         = "/mcp/rpc"
	MCPResourceMetadataPath = "/.well-known/oauth-protected-resource/mcp/rpc"
)

// ErrUnavailable means no public origin can be derived from inputs the
// server is configured to trust. Callers must fail closed rather than guess.
var ErrUnavailable = errors.New("no trustworthy public origin for this request")

// Resolver constructs public OAuth/MCP URLs from the same trusted inputs.
type Resolver struct {
	publicBaseURL string
	trustProxy    bool
}

func New(publicBaseURL string, trustProxy bool) *Resolver {
	return &Resolver{publicBaseURL: strings.TrimSpace(publicBaseURL), trustProxy: trustProxy}
}

// ConfiguredOrigin validates and canonicalizes a configured public base URL
// using the same OAuth-safe rules as Resolver.Origin.
func ConfiguredOrigin(base string) (string, error) {
	return configuredOrigin(strings.TrimSpace(base))
}

// Origin returns the trusted, canonical public origin for r. Scheme and host
// are lowercased; an explicit port is preserved exactly.
func (r *Resolver) Origin(req *http.Request) (string, error) {
	if r != nil && r.publicBaseURL != "" {
		return configuredOrigin(r.publicBaseURL)
	}
	authority, hostname, ok := ParseHTTPAuthority(req.Host)
	if req.TLS != nil && ok {
		return "https://" + canonicalAuthority(authority, hostname), nil
	}
	if r != nil && r.trustProxy {
		if origin, ok := forwardedOrigin(req); ok {
			return origin, nil
		}
	}
	if ok && IsLoopbackHostname(hostname) {
		return "http://" + canonicalAuthority(authority, hostname), nil
	}
	return "", ErrUnavailable
}

func (r *Resolver) MCPResource(req *http.Request) (string, error) {
	origin, err := r.Origin(req)
	if err != nil {
		return "", err
	}
	return origin + MCPResourcePath, nil
}

func (r *Resolver) MCPResourceMetadataURL(req *http.Request) (string, error) {
	origin, err := r.Origin(req)
	if err != nil {
		return "", err
	}
	return origin + MCPResourceMetadataPath, nil
}

// ValidateMCPResource accepts only the canonical MCP resource. It
// canonicalizes scheme and hostname case, but deliberately does not normalize
// ports, paths, escaping, queries, fragments, or trailing slashes.
func (r *Resolver) ValidateMCPResource(req *http.Request, raw string) (string, error) {
	got, err := canonicalAbsoluteMCPResource(raw)
	if err != nil {
		return "", err
	}
	want, err := r.MCPResource(req)
	if err != nil {
		return "", err
	}
	if got != want {
		return "", errors.New("resource does not identify this MCP server")
	}
	return want, nil
}

// OriginAllowed validates a supplied Origin header against the same trusted
// public origin. Requests without Origin are allowed for non-browser clients.
func (r *Resolver) OriginAllowed(req *http.Request) (bool, error) {
	values := req.Header.Values("Origin")
	if len(values) == 0 {
		return true, nil
	}
	if len(values) != 1 || strings.Contains(values[0], ",") {
		return false, nil
	}
	got, err := canonicalAbsoluteOrigin(values[0])
	if err != nil {
		return false, nil
	}
	want, err := r.Origin(req)
	if err != nil {
		return false, err
	}
	return got == want, nil
}

func configuredOrigin(base string) (string, error) {
	u, err := url.Parse(base)
	if err != nil || !u.IsAbs() || u.Opaque != "" || u.Host == "" || u.User != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(base, "?#%") {
		return "", ErrUnavailable
	}
	scheme := strings.ToLower(u.Scheme)
	authority, hostname, ok := ParseHTTPAuthority(u.Host)
	if !ok {
		return "", ErrUnavailable
	}
	switch scheme {
	case "https":
	case "http":
		if !IsLoopbackHostname(hostname) {
			return "", ErrUnavailable
		}
	default:
		return "", ErrUnavailable
	}
	return scheme + "://" + canonicalAuthority(authority, hostname), nil
}

func canonicalAbsoluteMCPResource(raw string) (string, error) {
	if raw == "" || strings.ContainsAny(raw, "?#%") || containsSpaceOrControl(raw) {
		return "", errors.New("invalid resource URI")
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Opaque != "" || u.Host == "" || u.User != nil || u.Path != MCPResourcePath || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", errors.New("invalid resource URI")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" && scheme != "http" {
		return "", errors.New("unsupported resource URI scheme")
	}
	authority, hostname, ok := ParseHTTPAuthority(u.Host)
	if !ok {
		return "", errors.New("invalid resource URI authority")
	}
	return scheme + "://" + canonicalAuthority(authority, hostname) + MCPResourcePath, nil
}

func canonicalAbsoluteOrigin(raw string) (string, error) {
	if raw == "" || strings.ContainsAny(raw, "?#%") || containsSpaceOrControl(raw) {
		return "", errors.New("invalid origin")
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.Opaque != "" || u.Host == "" || u.User != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", errors.New("invalid origin")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" && scheme != "http" {
		return "", errors.New("unsupported origin scheme")
	}
	authority, hostname, ok := ParseHTTPAuthority(u.Host)
	if !ok {
		return "", errors.New("invalid origin authority")
	}
	return scheme + "://" + canonicalAuthority(authority, hostname), nil
}

func forwardedOrigin(req *http.Request) (string, bool) {
	proto, ok := forwardedScheme(req)
	if !ok || proto != "https" {
		return "", false
	}
	hostValues := req.Header.Values("X-Forwarded-Host")
	if len(hostValues) != 1 {
		return "", false
	}
	authority, hostname, ok := ParseHTTPAuthority(hostValues[0])
	if !ok {
		return "", false
	}
	return "https://" + canonicalAuthority(authority, hostname), true
}

func forwardedScheme(req *http.Request) (string, bool) {
	protoValues := req.Header.Values("X-Forwarded-Proto")
	if len(protoValues) > 0 {
		if len(protoValues) != 1 {
			return "", false
		}
		proto := strings.ToLower(strings.TrimSpace(protoValues[0]))
		if proto == "" || strings.Contains(proto, ",") {
			return "", false
		}
		return proto, true
	}
	cfValues := req.Header.Values("CF-Visitor")
	if len(cfValues) != 1 {
		return "", false
	}
	var visitor struct {
		Scheme string `json:"scheme"`
	}
	if err := json.Unmarshal([]byte(cfValues[0]), &visitor); err != nil {
		return "", false
	}
	proto := strings.ToLower(strings.TrimSpace(visitor.Scheme))
	if proto == "" || strings.Contains(proto, ",") {
		return "", false
	}
	return proto, true
}

func canonicalAuthority(authority, hostname string) string {
	host := strings.ToLower(hostname)
	if strings.HasPrefix(authority, "[") {
		closeBracket := strings.IndexByte(authority, ']')
		return "[" + host + "]" + authority[closeBracket+1:]
	}
	if colon := strings.LastIndexByte(authority, ':'); colon >= 0 {
		return host + authority[colon:]
	}
	return host
}

func containsSpaceOrControl(raw string) bool {
	for _, char := range raw {
		if unicode.IsControl(char) || unicode.IsSpace(char) {
			return true
		}
	}
	return false
}

// ParseHTTPAuthority validates an HTTP authority consisting of a hostname and
// an optional explicit port. The returned hostname omits IPv6 brackets.
func ParseHTTPAuthority(raw string) (authority, hostname string, ok bool) {
	if raw == "" || containsSpaceOrControl(raw) || strings.ContainsAny(raw, `,/?#@%\`) {
		return "", "", false
	}
	if raw[0] == '[' {
		closeBracket := strings.IndexByte(raw, ']')
		if closeBracket <= 1 {
			return "", "", false
		}
		hostname = raw[1:closeBracket]
		if ip := net.ParseIP(hostname); ip == nil || !strings.Contains(hostname, ":") {
			return "", "", false
		}
		remainder := raw[closeBracket+1:]
		if remainder == "" {
			return raw, hostname, true
		}
		if remainder[0] != ':' || !validPort(remainder[1:]) {
			return "", "", false
		}
		return raw, hostname, true
	}
	if strings.ContainsAny(raw, "[]") || strings.Count(raw, ":") > 1 {
		return "", "", false
	}
	hostname = raw
	if colon := strings.LastIndexByte(raw, ':'); colon >= 0 {
		hostname = raw[:colon]
		if !validPort(raw[colon+1:]) {
			return "", "", false
		}
	}
	if hostname == "" || !validRegName(hostname) {
		return "", "", false
	}
	return raw, hostname, true
}

func validPort(raw string) bool {
	if raw == "" {
		return false
	}
	for i := range raw {
		if raw[i] < '0' || raw[i] > '9' {
			return false
		}
	}
	port, err := strconv.Atoi(raw)
	return err == nil && port >= 1 && port <= 65535
}

func validRegName(raw string) bool {
	for i := range raw {
		char := raw[i]
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' {
			continue
		}
		switch char {
		case '-', '.', '_', '~', '!', '$', '&', '\'', '(', ')', '*', '+', ';', '=':
			continue
		default:
			return false
		}
	}
	return true
}

func IsLoopbackHostname(hostname string) bool {
	if strings.EqualFold(hostname, "localhost") {
		return true
	}
	ip := net.ParseIP(hostname)
	return ip != nil && ip.IsLoopback()
}
