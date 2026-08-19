package calendar

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"scrumboy/internal/calendar/ics"
)

func TestHTTPFetcherAllowsLoopbackHTTPWhenConfigured(t *testing.T) {
	var gotMatch string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMatch = r.Header.Get("If-None-Match")
		w.Header().Set("ETag", `"abc"`)
		w.Header().Set("Last-Modified", "Mon, 17 Aug 2026 12:00:00 GMT")
		_, _ = io.WriteString(w, "BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n")
	}))
	defer ts.Close()

	fetcher := NewHTTPFetcher(true)
	resp, err := fetcher.Fetch(context.Background(), FetchRequest{URL: ts.URL, ETag: `"prev"`})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if gotMatch != `"prev"` || resp.NotModified || !strings.Contains(string(resp.Body), "BEGIN:VCALENDAR") {
		t.Fatalf("resp=%+v match=%q", resp, gotMatch)
	}
	if resp.ETag != `"abc"` {
		t.Fatalf("etag=%q", resp.ETag)
	}
}

func TestHTTPFetcherNotModified(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	defer ts.Close()

	resp, err := NewHTTPFetcher(true).Fetch(context.Background(), FetchRequest{URL: ts.URL, ETag: `"abc"`})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !resp.NotModified || len(resp.Body) != 0 {
		t.Fatalf("resp=%+v", resp)
	}
}

func TestHTTPFetcherRejectsServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, "nope")
	}))
	defer ts.Close()

	_, err := NewHTTPFetcher(true).Fetch(context.Background(), FetchRequest{URL: ts.URL})
	if !errors.Is(err, ErrFeedRequest) {
		t.Fatalf("err=%v, want ErrFeedRequest", err)
	}
}

func TestHTTPFetcherEnforcesSizeCap(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("a", 64))
	}))
	defer ts.Close()

	fetcher := NewHTTPFetcher(true)
	fetcher.MaxBody = 8
	_, err := fetcher.Fetch(context.Background(), FetchRequest{URL: ts.URL})
	if !errors.Is(err, ErrFeedTooLarge) {
		t.Fatalf("err=%v, want ErrFeedTooLarge", err)
	}
}

func TestHTTPFetcherAcceptsFeedAboveOldTwoMiBCeiling(t *testing.T) {
	const oldCeiling = 2 << 20
	body := paddedFeedICS(oldCeiling + 1)
	if len(body) <= oldCeiling || len(body) > ics.MaxBodyBytes {
		t.Fatalf("padded size=%d", len(body))
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer ts.Close()

	resp, err := NewHTTPFetcher(true).Fetch(context.Background(), FetchRequest{URL: ts.URL})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(resp.Body) != len(body) {
		t.Fatalf("body len=%d want %d", len(resp.Body), len(body))
	}
}

func TestHTTPFetcherRejectsAboveNewCeiling(t *testing.T) {
	fetcher := NewHTTPFetcher(true)
	if fetcher.MaxBody != ics.MaxBodyBytes || ics.MaxBodyBytes != 32<<20 {
		t.Fatalf("MaxBody=%d, want 32 MiB", fetcher.MaxBody)
	}
	var served atomicInt64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		buf := make([]byte, 32*1024)
		for {
			n, err := w.Write(buf)
			served.add(int64(n))
			if err != nil {
				return
			}
		}
	}))
	defer ts.Close()

	_, err := fetcher.Fetch(context.Background(), FetchRequest{URL: ts.URL})
	if !errors.Is(err, ErrFeedTooLarge) {
		t.Fatalf("err=%v, want ErrFeedTooLarge", err)
	}
	got := served.load()
	max := int64(fetcher.MaxBody) + 1
	if got < max {
		t.Fatalf("served %d bytes, want at least the LimitReader cap %d", got, max)
	}
}

func paddedFeedICS(minBytes int) []byte {
	prefix := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nBEGIN:VEVENT\r\nUID:pad\r\nDTSTART:20260817T150000Z\r\nDTEND:20260817T160000Z\r\nSUMMARY:Pad\r\nDESCRIPTION:"
	suffix := "\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	n := minBytes - len(prefix) - len(suffix)
	if n < 1 {
		n = 1
	}
	return []byte(prefix + strings.Repeat("x", n) + suffix)
}

type atomicInt64 struct {
	mu sync.Mutex
	n  int64
}

func (a *atomicInt64) add(n int64) {
	a.mu.Lock()
	a.n += n
	a.mu.Unlock()
}

func (a *atomicInt64) load() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.n
}

func TestHTTPFetcherTimeout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	fetcher := NewHTTPFetcher(true)
	fetcher.Timeout = 50 * time.Millisecond
	_, err := fetcher.Fetch(context.Background(), FetchRequest{URL: ts.URL})
	if !errors.Is(err, ErrFeedTimeout) {
		t.Fatalf("err=%v, want ErrFeedTimeout", err)
	}
}

func TestHTTPFetcherBlocksLoopbackWithoutAllow(t *testing.T) {
	_, err := NewHTTPFetcher(false).Fetch(context.Background(), FetchRequest{URL: "http://127.0.0.1/feed.ics"})
	if !errors.Is(err, ErrFeedBlocked) {
		t.Fatalf("err=%v, want ErrFeedBlocked", err)
	}
	_, err = NewHTTPFetcher(false).Fetch(context.Background(), FetchRequest{URL: "https://127.0.0.1/feed.ics"})
	if !errors.Is(err, ErrFeedBlocked) {
		t.Fatalf("https loopback err=%v, want ErrFeedBlocked", err)
	}
}

func TestHTTPFetcherBlocksPrivateResolution(t *testing.T) {
	fetcher := NewHTTPFetcher(false)
	fetcher.LookupIP = func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("10.1.2.3")}, nil
	}
	_, err := fetcher.Fetch(context.Background(), FetchRequest{URL: "https://calendar.example.com/feed.ics"})
	if !errors.Is(err, ErrFeedBlocked) {
		t.Fatalf("err=%v, want ErrFeedBlocked", err)
	}
}

func TestHTTPFetcherBlocksCGNATAndAllowsPublic(t *testing.T) {
	blocked := NewHTTPFetcher(false)
	if !blocked.blockedIP(net.ParseIP("100.64.0.1")) {
		t.Fatal("100.64.0.1 must be blocked")
	}
	if !blocked.blockedIP(net.ParseIP("100.127.255.254")) {
		t.Fatal("100.127.255.254 must be blocked")
	}
	if blocked.blockedIP(net.ParseIP("8.8.8.8")) {
		t.Fatal("8.8.8.8 must remain allowed by address classification")
	}
	if blocked.blockedIP(net.ParseIP("1.1.1.1")) {
		t.Fatal("1.1.1.1 must remain allowed by address classification")
	}
	if !blocked.blockedIP(net.ParseIP("10.1.2.3")) || !blocked.blockedIP(net.ParseIP("192.168.1.1")) || !blocked.blockedIP(net.ParseIP("172.16.0.1")) {
		t.Fatal("RFC1918 must remain blocked")
	}
	if !blocked.blockedIP(net.ParseIP("127.0.0.1")) {
		t.Fatal("loopback must remain blocked when not allowed")
	}
	if NewHTTPFetcher(true).blockedIP(net.ParseIP("127.0.0.1")) {
		t.Fatal("loopback must remain allowed when configured")
	}
	if !blocked.blockedIP(net.ParseIP("169.254.1.1")) {
		t.Fatal("link-local must remain blocked")
	}

	_, err := blocked.Fetch(context.Background(), FetchRequest{URL: "https://100.64.1.8/feed.ics"})
	if !errors.Is(err, ErrFeedBlocked) {
		t.Fatalf("literal CGNAT URL err=%v, want ErrFeedBlocked", err)
	}

	fetcher := NewHTTPFetcher(false)
	fetcher.LookupIP = func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("100.64.12.34")}, nil
	}
	_, err = fetcher.Fetch(context.Background(), FetchRequest{URL: "https://calendar.example.com/feed.ics"})
	if !errors.Is(err, ErrFeedBlocked) {
		t.Fatalf("CGNAT hostname err=%v, want ErrFeedBlocked", err)
	}
}

func TestHTTPFetcherBlocksRedirectToHTTP(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "http://example.com/feed.ics", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	_, err := NewHTTPFetcher(true).Fetch(context.Background(), FetchRequest{URL: ts.URL + "/start"})
	if !errors.Is(err, ErrFeedBlocked) {
		t.Fatalf("err=%v, want ErrFeedBlocked", err)
	}
}

func TestHTTPFetcherErrorDoesNotIncludeURL(t *testing.T) {
	fetcher := NewHTTPFetcher(false)
	fetcher.LookupIP = func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("10.0.0.1")}, nil
	}
	_, err := fetcher.Fetch(context.Background(), FetchRequest{
		URL: "https://calendar.example.com/private/super-secret-token.ics",
	})
	if !errors.Is(err, ErrFeedBlocked) {
		t.Fatalf("err=%v, want ErrFeedBlocked", err)
	}
	if strings.Contains(err.Error(), "super-secret-token") || strings.Contains(err.Error(), "calendar.example.com") {
		t.Fatalf("error leaked URL: %v", err)
	}
}
