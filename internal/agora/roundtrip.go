package agora

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"

	"scrumboy/internal/mcp"
)

type roundTripResponse struct {
	Status int
	Body   []byte
}

func roundTrip(mcpHandler http.Handler, r *http.Request, rpcBody []byte) roundTripResponse {
	rec := httptest.NewRecorder()
	u := url.URL{Scheme: "http", Host: "127.0.0.1", Path: "/mcp/rpc"}
	sub := r.Clone(r.Context())
	sub.Method = http.MethodPost
	sub.URL = &u
	sub.RequestURI = ""
	sub.ContentLength = int64(len(rpcBody))
	sub.Body = io.NopCloser(bytes.NewReader(rpcBody))
	sub.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(rpcBody)), nil
	}
	sub.Header = r.Header.Clone()
	sub.Header.Set("Content-Type", "application/json; charset=utf-8")
	sub.Header.Set("Accept", "application/json, text/event-stream")
	sub.Header.Set("MCP-Protocol-Version", "2025-11-25")
	sub = mcp.WithoutOAuthBearer(sub)
	mcpHandler.ServeHTTP(rec, sub)
	return roundTripResponse{Status: rec.Code, Body: rec.Body.Bytes()}
}
