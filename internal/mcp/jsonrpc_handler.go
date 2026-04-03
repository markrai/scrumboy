package mcp

import (
	"encoding/json"
	"io"
	"net/http"
)

const mcpProtocolVersion = "2024-11-05"

// JSON-RPC 2.0 wire types.

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      any           `json:"id"`
	Result  any           `json:"result,omitempty"`
	Error   *jsonRPCError `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

const (
	jsonRPCParseError     = -32700
	jsonRPCInvalidRequest = -32600
	jsonRPCMethodNotFound = -32601
	jsonRPCInvalidParams  = -32602
	jsonRPCInternalError  = -32603
)

// MCP initialize handshake types.

type mcpInitializeParams struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ClientInfo      mcpClientInfo  `json:"clientInfo"`
}

type mcpClientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type mcpInitializeResult struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Capabilities    mcpCapabilities `json:"capabilities"`
	ServerInfo      mcpServerInfo   `json:"serverInfo"`
	Instructions    string          `json:"instructions,omitempty"`
}

type mcpCapabilities struct {
	Tools *mcpToolsCapability `json:"tools,omitempty"`
}

type mcpToolsCapability struct {
	ListChanged bool `json:"listChanged,omitempty"`
}

type mcpServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// serveJSONRPC handles POST /mcp/rpc with JSON-RPC 2.0 framing.
// This is the spec-compliant MCP endpoint; the legacy /mcp endpoint is unchanged.
func (a *Adapter) serveJSONRPC(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	if r.Method != http.MethodPost {
		writeJSONRPCError(w, nil, jsonRPCInvalidRequest, "only POST is accepted")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSONRPCError(w, nil, jsonRPCParseError, "failed to read body")
		return
	}

	var req jsonRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSONRPCError(w, nil, jsonRPCParseError, "invalid JSON")
		return
	}

	if req.JSONRPC != "2.0" {
		writeJSONRPCError(w, req.ID, jsonRPCInvalidRequest, "jsonrpc must be \"2.0\"")
		return
	}
	if req.Method == "" {
		writeJSONRPCError(w, req.ID, jsonRPCInvalidRequest, "method is required")
		return
	}

	isNotification := req.ID == nil

	switch req.Method {
	case "initialize":
		a.handleJSONRPCInitialize(w, &req)
	case "notifications/initialized", "initialized":
		if !isNotification {
			writeJSONRPCError(w, req.ID, jsonRPCInvalidRequest, "initialized must be a notification (no id)")
			return
		}
		// Spec: notifications get no response body.
		w.WriteHeader(http.StatusNoContent)
	case "tools/list":
		a.handleJSONRPCToolsList(w, &req)
	case "tools/call":
		a.handleJSONRPCToolsCall(w, r, &req)
	default:
		writeJSONRPCError(w, req.ID, jsonRPCMethodNotFound, "method not found")
	}
}

func (a *Adapter) handleJSONRPCInitialize(w http.ResponseWriter, req *jsonRPCRequest) {
	if req.ID == nil {
		writeJSONRPCError(w, nil, jsonRPCInvalidRequest, "initialize must be a request (requires id)")
		return
	}

	var params mcpInitializeParams
	if req.Params != nil {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			writeJSONRPCError(w, req.ID, jsonRPCInvalidParams, "invalid initialize params")
			return
		}
	}

	result := mcpInitializeResult{
		ProtocolVersion: mcpProtocolVersion,
		Capabilities: mcpCapabilities{
			Tools: &mcpToolsCapability{ListChanged: false},
		},
		ServerInfo: mcpServerInfo{
			Name:    "scrumboy",
			Version: "1.0.0",
		},
		Instructions: "Scrumboy MCP server. Use tools/list to discover available tools.",
	}

	writeJSONRPCResult(w, req.ID, result)
}

func writeJSONRPCResult(w http.ResponseWriter, id any, result any) {
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	})
}

func (a *Adapter) handleJSONRPCToolsList(w http.ResponseWriter, req *jsonRPCRequest) {
	if req.ID == nil {
		writeJSONRPCError(w, nil, jsonRPCInvalidRequest, "tools/list must be a request (requires id)")
		return
	}
	writeJSONRPCResult(w, req.ID, map[string]any{
		"tools": a.toolCatalog(),
	})
}

// tools/call params shape per MCP spec.
type toolsCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// MCP text content block for tool results.
type mcpTextContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (a *Adapter) handleJSONRPCToolsCall(w http.ResponseWriter, r *http.Request, req *jsonRPCRequest) {
	if req.ID == nil {
		writeJSONRPCError(w, nil, jsonRPCInvalidRequest, "tools/call must be a request (requires id)")
		return
	}

	if req.Params == nil {
		writeJSONRPCError(w, req.ID, jsonRPCInvalidParams, "missing params")
		return
	}
	var params toolsCallParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeJSONRPCError(w, req.ID, jsonRPCInvalidParams, "invalid params")
		return
	}
	if params.Name == "" {
		writeJSONRPCError(w, req.ID, jsonRPCInvalidParams, "missing params.name")
		return
	}

	handler, ok := a.tools[params.Name]
	if !ok {
		writeJSONRPCToolErrorResult(w, req.ID, "tool not found")
		return
	}

	args := params.Arguments
	if args == nil {
		args = map[string]any{}
	}

	if err := validateRequiredFields(params.Name, args); err != "" {
		writeJSONRPCToolErrorResult(w, req.ID, err)
		return
	}

	authRes := a.resolveRequestAuth(r)
	if authRes.Err != nil {
		writeJSONRPCToolErrorResult(w, req.ID, "internal error")
		return
	}
	if authRes.BearerAuthFailed {
		writeJSONRPCToolErrorResult(w, req.ID, "authentication required")
		return
	}

	data, _, toolErr := handler(authRes.Ctx, args)
	if toolErr != nil {
		writeJSONRPCToolErrorResult(w, req.ID, toolErr.Message)
		return
	}

	writeJSONRPCToolSuccessResult(w, req.ID, data)
}

// requiredFieldNamesFromSchema extracts the "required" keyword from a JSON Schema object.
// Accepts []string (in-memory catalog) and []any (e.g. after JSON round-trip).
func requiredFieldNamesFromSchema(schema map[string]any) []string {
	raw := schema["required"]
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, el := range v {
			s, ok := el.(string)
			if !ok || s == "" {
				continue
			}
			out = append(out, s)
		}
		return out
	default:
		return nil
	}
}

// validateRequiredFields checks that required fields from the tool catalog are present.
// Returns an error message string, or "" if valid.
func validateRequiredFields(toolName string, args map[string]any) string {
	def, ok := toolCatalogDefinitions()[toolName]
	if !ok {
		return ""
	}
	schema, ok := def.InputSchema.(map[string]any)
	if !ok {
		return ""
	}
	required := requiredFieldNamesFromSchema(schema)
	if len(required) == 0 {
		return ""
	}
	for _, field := range required {
		if _, exists := args[field]; !exists {
			return "missing required field: " + field
		}
	}
	return ""
}

func toolResultText(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func writeJSONRPCToolSuccessResult(w http.ResponseWriter, id any, data any) {
	writeJSONRPCResult(w, id, map[string]any{
		"content": []mcpTextContent{
			{Type: "text", Text: toolResultText(data)},
		},
		"structuredContent": data,
	})
}

func writeJSONRPCToolErrorResult(w http.ResponseWriter, id any, message string) {
	writeJSONRPCResult(w, id, map[string]any{
		"content": []mcpTextContent{
			{Type: "text", Text: message},
		},
		"isError": true,
	})
}

func writeJSONRPCErrorWithData(w http.ResponseWriter, id any, code int, message string, data any) {
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &jsonRPCError{
			Code:    code,
			Message: message,
			Data:    data,
		},
	})
}

func writeJSONRPCError(w http.ResponseWriter, id any, code int, message string) {
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error: &jsonRPCError{
			Code:    code,
			Message: message,
		},
	})
}
