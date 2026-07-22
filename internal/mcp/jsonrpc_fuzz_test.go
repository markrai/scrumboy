package mcp

import (
	"bytes"
	"errors"
	"testing"
)

func FuzzParseJSONRPCMessage(f *testing.F) {
	seeds := []string{
		`{"jsonrpc":"2.0","id":1,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":"abc","method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":null,"method":"ping"}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":[1,2]}`,
		`{"jsonrpc":"2.0","id":1,"result":{}}`,
		`{"jsonrpc":"2.0","id":null,"method":123}`,
		`{"jsonrpc":"2.0","id":{"bad":true},"method":"ping"}`,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":null}`,
		`{"jsonrpc":"1.0","id":1,"method":"ping"}`,
		`{"id":1,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":1}`,
		`{"jsonrpc":"2.0","id":1,"method":"ping","result":{}}`,
		`[]`,
		`"hello"`,
		``,
		`{`,
	}
	for _, seed := range seeds {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxJSONRPCBodyBytes {
			return
		}

		req, err := parseJSONRPCMessage(data)

		if err != nil {
			if req != nil {
				t.Fatalf("error result returned non-nil request: %q", data)
			}
			var fail *jsonRPCParseFailure
			if !errors.As(err, &fail) {
				t.Fatalf("error is not *jsonRPCParseFailure: %T for %q", err, data)
			}
			return
		}

		if req == nil {
			t.Fatalf("nil request without error: %q", data)
		}
		if req.JSONRPC != "2.0" {
			t.Fatalf("accepted request with jsonrpc=%q: %q", req.JSONRPC, data)
		}
		if req.IsReply {
			if !req.HasID {
				t.Fatalf("reply without id: %q", data)
			}
			if req.Method != "" {
				t.Fatalf("reply carried method %q: %q", req.Method, data)
			}
		} else if req.Method == "" {
			t.Fatalf("accepted non-reply request without method: %q", data)
		}
		if req.HasID && !validJSONRPCID(req.ID) {
			t.Fatalf("accepted request with invalid id %q: %q", req.ID, data)
		}
		if len(req.Params) > 0 {
			trimmed := bytes.TrimSpace(req.Params)
			if len(trimmed) == 0 || (trimmed[0] != '{' && trimmed[0] != '[') {
				t.Fatalf("accepted params that are not object or array: %q", req.Params)
			}
		}
	})
}
