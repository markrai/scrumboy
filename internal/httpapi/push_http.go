package httpapi

import (
	"crypto/tls"
	"errors"
	"net/http"
	"time"

	"scrumboy/internal/safehttp"
)

var errPushRedirect = errors.New("push delivery redirect not allowed")

func newPushHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errPushRedirect
		},
		Transport: &http.Transport{
			Proxy:                 nil,
			ForceAttemptHTTP2:     true,
			DialContext:           safehttp.NewDialContext(safehttp.Dialer{}),
			TLSHandshakeTimeout:   5 * time.Second,
			TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
			ResponseHeaderTimeout: 8 * time.Second,
			MaxIdleConns:          32,
			MaxIdleConnsPerHost:   8,
			IdleConnTimeout:       90 * time.Second,
		},
	}
}
