package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

func TestParseJSONRPCMessage_NullIDPreservedOnStructuralError(t *testing.T) {
	_, err := parseJSONRPCMessage([]byte(`{"jsonrpc":"2.0","id":null,"method":123}`))
	var fail *jsonRPCParseFailure
	if !errors.As(err, &fail) {
		t.Fatalf("error=%v, want jsonRPCParseFailure", err)
	}
	if !fail.HasID {
		t.Fatal("expected HasID for null id")
	}
	if !bytes.Equal(bytes.TrimSpace(fail.ID), []byte("null")) {
		t.Fatalf("ID=%s, want null", fail.ID)
	}
	if fail.Code != jsonRPCInvalidRequest {
		t.Fatalf("code=%d, want %d", fail.Code, jsonRPCInvalidRequest)
	}
}

func TestParseJSONRPCMessage_InvalidIDHasNoRecoverableID(t *testing.T) {
	_, err := parseJSONRPCMessage([]byte(`{"jsonrpc":"2.0","id":{"bad":true},"method":"ping"}`))
	var fail *jsonRPCParseFailure
	if !errors.As(err, &fail) {
		t.Fatalf("error=%v, want jsonRPCParseFailure", err)
	}
	if fail.HasID {
		t.Fatalf("invalid id must not be recoverable; ID=%s", fail.ID)
	}
	if fail.Code != jsonRPCInvalidRequest {
		t.Fatalf("code=%d, want %d", fail.Code, jsonRPCInvalidRequest)
	}
}

func TestParseJSONRPCMessage_ParamsNullRejected(t *testing.T) {
	_, err := parseJSONRPCMessage([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":null}`))
	var fail *jsonRPCParseFailure
	if !errors.As(err, &fail) {
		t.Fatalf("error=%v, want jsonRPCParseFailure", err)
	}
	if fail.Code != jsonRPCInvalidRequest {
		t.Fatalf("code=%d, want %d", fail.Code, jsonRPCInvalidRequest)
	}
	if !fail.HasID {
		t.Fatal("expected recoverable id")
	}
	var id any
	if err := json.Unmarshal(fail.ID, &id); err != nil || id != float64(1) {
		t.Fatalf("ID=%s decoded=%v", fail.ID, id)
	}
}
