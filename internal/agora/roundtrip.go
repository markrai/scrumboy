package agora

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
)

func roundTrip(mcp http.Handler, r *http.Request, rpcBody []byte) (int, []byte) {
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
	mcp.ServeHTTP(rec, sub)
	return rec.Code, rec.Body.Bytes()
}
