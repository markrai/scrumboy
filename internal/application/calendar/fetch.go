package calendar

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"scrumboy/internal/calendar/ics"
	"scrumboy/internal/safehttp"
)

const defaultFetchTimeout = 10 * time.Second

var (
	ErrFeedBlocked  = errors.New("calendar feed address is not allowed")
	ErrFeedTooLarge = errors.New("calendar feed too large")
	ErrFeedTimeout  = errors.New("calendar feed timed out")
	ErrFeedRequest  = errors.New("calendar feed request failed")
)

type FetchRequest struct {
	URL          string
	ETag         string
	LastModified string
}

type FetchResponse struct {
	NotModified  bool
	Body         []byte
	ETag         string
	LastModified string
	StatusCode   int
}

type FeedFetcher interface {
	Fetch(ctx context.Context, req FetchRequest) (FetchResponse, error)
}

type HTTPFetcher struct {
	AllowLoopback bool
	Timeout       time.Duration
	MaxBody       int
	LookupIP      func(ctx context.Context, host string) ([]net.IP, error)
}

func NewHTTPFetcher(allowLoopback bool) *HTTPFetcher {
	return &HTTPFetcher{
		AllowLoopback: allowLoopback,
		Timeout:       defaultFetchTimeout,
		MaxBody:       ics.MaxBodyBytes,
	}
}

func (f *HTTPFetcher) Fetch(ctx context.Context, req FetchRequest) (FetchResponse, error) {
	parsed, err := url.Parse(strings.TrimSpace(req.URL))
	if err != nil {
		return FetchResponse{}, ErrFeedRequest
	}
	if err := f.validateURL(parsed); err != nil {
		return FetchResponse{}, err
	}

	timeout := f.Timeout
	if timeout <= 0 {
		timeout = defaultFetchTimeout
	}
	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return ErrFeedRequest
			}
			return f.validateURL(next.URL)
		},
		Transport: &http.Transport{
			Proxy:                 nil,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: timeout,
			DialContext:           f.dialContext,
			TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
			DisableKeepAlives:     true,
		},
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return FetchResponse{}, ErrFeedRequest
	}
	httpReq.Header.Set("Accept", "text/calendar, text/plain, */*")
	if etag := strings.TrimSpace(req.ETag); etag != "" {
		httpReq.Header.Set("If-None-Match", etag)
	}
	if lm := strings.TrimSpace(req.LastModified); lm != "" {
		httpReq.Header.Set("If-Modified-Since", lm)
	}

	resp, err := client.Do(httpReq)
	if err != nil {
		return FetchResponse{}, sanitizeFetchError(err)
	}
	defer resp.Body.Close()

	maxBody := f.MaxBody
	if maxBody <= 0 {
		maxBody = ics.MaxBodyBytes
	}
	limited := io.LimitReader(resp.Body, int64(maxBody)+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return FetchResponse{}, sanitizeFetchError(err)
	}
	if len(body) > maxBody {
		return FetchResponse{}, ErrFeedTooLarge
	}

	out := FetchResponse{
		StatusCode:   resp.StatusCode,
		ETag:         strings.TrimSpace(resp.Header.Get("ETag")),
		LastModified: strings.TrimSpace(resp.Header.Get("Last-Modified")),
	}
	if resp.StatusCode == http.StatusNotModified {
		out.NotModified = true
		return out, nil
	}
	if resp.StatusCode != http.StatusOK {
		return FetchResponse{StatusCode: resp.StatusCode}, ErrFeedRequest
	}
	out.Body = body
	return out, nil
}

func (f *HTTPFetcher) validateURL(u *url.URL) error {
	if u == nil || u.Host == "" {
		return ErrFeedRequest
	}
	if u.User != nil {
		return ErrFeedBlocked
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	switch scheme {
	case "https":
	case "http":
		if !f.AllowLoopback || !isLoopbackHost(host) {
			return ErrFeedBlocked
		}
	default:
		return ErrFeedBlocked
	}
	if ip := net.ParseIP(host); ip != nil && f.blockedIP(ip) {
		return ErrFeedBlocked
	}
	return nil
}

func (f *HTTPFetcher) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	conn, err := safehttp.Dialer{
		LookupIP:  f.LookupIP,
		Forbidden: f.blockedIP,
	}.DialContext(ctx, network, address)
	if err == nil {
		return conn, nil
	}
	if errors.Is(err, safehttp.ErrForbidden) {
		return nil, ErrFeedBlocked
	}
	return nil, ErrFeedRequest
}

func (f *HTTPFetcher) blockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	classified := ip
	if v4 := ip.To4(); v4 != nil {
		classified = v4
	}
	if classified.IsLoopback() {
		return !f.AllowLoopback
	}
	return safehttp.IsForbiddenIP(ip)
}

func sanitizeFetchError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ErrFeedBlocked), errors.Is(err, safehttp.ErrForbidden):
		return ErrFeedBlocked
	case errors.Is(err, ErrFeedTooLarge):
		return ErrFeedTooLarge
	case errors.Is(err, ErrFeedTimeout):
		return ErrFeedTimeout
	case errors.Is(err, ErrFeedRequest):
		return ErrFeedRequest
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return ErrFeedTimeout
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		return ErrFeedTimeout
	}
	if strings.Contains(strings.ToLower(err.Error()), "timeout") {
		return ErrFeedTimeout
	}
	return ErrFeedRequest
}
