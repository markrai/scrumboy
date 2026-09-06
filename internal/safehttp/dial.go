package safehttp

import (
	"context"
	"errors"
	"net"
	"time"
)

// ErrForbidden is returned when every resolved address is unsafe to dial.
var ErrForbidden = errors.New("destination address is not allowed")

const defaultDialTimeout = 5 * time.Second

// LookupIPFunc resolves host to IP addresses. Host may already be a literal IP.
type LookupIPFunc func(ctx context.Context, host string) ([]net.IP, error)

// Dialer resolves address, drops forbidden IPs, and dials a vetted IP directly
// so the HTTP transport cannot perform a second DNS lookup.
type Dialer struct {
	// LookupIP defaults to the process resolver (literal IPs are not looked up).
	LookupIP LookupIPFunc
	// Forbidden defaults to IsForbiddenIP.
	Forbidden func(net.IP) bool
	// Timeout is the TCP dial timeout when Dial is nil. Zero means 5s.
	Timeout time.Duration
	// Dial is the TCP connect function. The address argument is always host:port
	// with a vetted IP host (never the original hostname). Nil uses net.Dialer.
	Dial func(ctx context.Context, network, address string) (net.Conn, error)
}

// NewDialContext returns a DialContext suitable for http.Transport.
func NewDialContext(d Dialer) func(ctx context.Context, network, address string) (net.Conn, error) {
	return d.DialContext
}

// DialContext resolves address, skips forbidden IPs, and connects to a remaining
// vetted address. Mixed DNS answers may only produce connections to allowed IPs.
func (d Dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	lookup := d.LookupIP
	if lookup == nil {
		lookup = defaultLookupIP
	}
	forbidden := d.Forbidden
	if forbidden == nil {
		forbidden = IsForbiddenIP
	}
	ips, err := lookup(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, errors.New("no IP addresses")
	}
	dial := d.Dial
	if dial == nil {
		timeout := d.Timeout
		if timeout <= 0 {
			timeout = defaultDialTimeout
		}
		inner := &net.Dialer{Timeout: timeout}
		dial = inner.DialContext
	}

	var lastDial error
	triedPermitted := false
	for _, ip := range ips {
		if forbidden(ip) {
			continue
		}
		triedPermitted = true
		conn, err := dial(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastDial = err
	}
	if !triedPermitted {
		return nil, ErrForbidden
	}
	return nil, lastDial
}

func defaultLookupIP(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	out := make([]net.IP, 0, len(addrs))
	for _, addr := range addrs {
		if addr.IP != nil {
			out = append(out, addr.IP)
		}
	}
	return out, nil
}
