package mcp

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"scrumboy/internal/publicorigin"
)

const (
	mcpLatestProtocolVersion  = "2025-11-25"
	mcpDefaultProtocolVersion = "2025-03-26"
	maxJSONRPCBodyBytes       = 1 << 20
)

var supportedMCPProtocolVersions = map[string]bool{
	"2025-03-26": true,
	"2025-06-18": true,
	"2025-11-25": true,
}

type jsonRPCRequest struct {
	JSONRPC string
	ID      json.RawMessage
	HasID   bool
	Method  string
	Params  json.RawMessage
	IsReply bool
}

// jsonRPCParseFailure is a structurally invalid JSON-RPC object (-32600).
// Malformed JSON remains a separate -32700 path in serveJSONRPC and must not
// be routed through this type.
type jsonRPCParseFailure struct {
	ID      json.RawMessage
	HasID   bool
	Code    int
	Message string
}

func (e *jsonRPCParseFailure) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func invalidJSONRPCRequest(req *jsonRPCRequest, message string) error {
	fail := &jsonRPCParseFailure{
		Code:    jsonRPCInvalidRequest,
		Message: message,
	}
	if req != nil && req.HasID {
		fail.ID = append(json.RawMessage(nil), req.ID...)
		fail.HasID = true
	}
	return fail
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
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

type mcpInitializeParams struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ClientInfo      *mcpClientInfo `json:"clientInfo"`
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

func (a *Adapter) serveJSONRPC(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")

	allowed, err := a.publicOrigin.OriginAllowed(r)
	if err != nil {
		writeEmptyStatus(w, http.StatusServiceUnavailable)
		return
	}
	if !allowed {
		writeEmptyStatus(w, http.StatusForbidden)
		return
	}

	if a.mode != "anonymous" {
		authRes := a.resolveRequestAuth(r, oauthBearerAllowed(r))
		if authRes.Err != nil {
			if errors.Is(authRes.Err, publicorigin.ErrUnavailable) {
				writeEmptyStatus(w, http.StatusServiceUnavailable)
			} else {
				writeEmptyStatus(w, http.StatusInternalServerError)
			}
			return
		}
		if !authRes.Authenticated || authRes.BearerAuthFailed {
			metadataURL, err := a.publicOrigin.MCPResourceMetadataURL(r)
			if err != nil {
				writeEmptyStatus(w, http.StatusServiceUnavailable)
				return
			}
			challenge := `Bearer resource_metadata="` + metadataURL + `"`
			if authRes.BearerAuthFailed {
				challenge = `Bearer error="invalid_token", resource_metadata="` + metadataURL + `"`
			}
			w.Header().Set("WWW-Authenticate", challenge)
			writeEmptyStatus(w, http.StatusUnauthorized)
			return
		}
		r = r.WithContext(authRes.Ctx)
	}

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeEmptyStatus(w, http.StatusMethodNotAllowed)
		return
	}
	if !acceptsJSONAndSSE(r.Header.Values("Accept")) {
		writeEmptyStatus(w, http.StatusNotAcceptable)
		return
	}
	if !isJSONContentType(r.Header.Values("Content-Type")) {
		writeEmptyStatus(w, http.StatusUnsupportedMediaType)
		return
	}

	body, tooLarge, err := readJSONRPCBody(r.Body)
	if err != nil {
		writeEmptyStatus(w, http.StatusBadRequest)
		return
	}
	if tooLarge {
		writeEmptyStatus(w, http.StatusRequestEntityTooLarge)
		return
	}
	if !utf8.Valid(body) || !json.Valid(body) {
		writeJSONRPCError(w, nil, jsonRPCParseError, "invalid JSON")
		return
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		writeEmptyStatus(w, http.StatusBadRequest)
		return
	}

	req, err := parseJSONRPCMessage(trimmed)
	if err != nil {
		var fail *jsonRPCParseFailure
		if errors.As(err, &fail) {
			if fail.HasID {
				writeJSONRPCError(w, fail.ID, fail.Code, fail.Message)
			} else {
				writeJSONRPCErrorStatus(w, http.StatusBadRequest, nil, fail.Code, fail.Message)
			}
			return
		}
		writeJSONRPCErrorStatus(w, http.StatusBadRequest, nil, jsonRPCInvalidRequest, err.Error())
		return
	}
	if req.IsReply {
		writeEmptyStatus(w, http.StatusBadRequest)
		return
	}

	if !req.HasID {
		if isMCPRequestOnlyMethod(req.Method) {
			writeJSONRPCErrorStatus(w, http.StatusBadRequest, nil, jsonRPCInvalidRequest, "method requires a request id")
			return
		}
		if !validMCPProtocolHeader(r.Header.Values("MCP-Protocol-Version")) {
			writeEmptyStatus(w, http.StatusBadRequest)
			return
		}
		writeEmptyStatus(w, http.StatusAccepted)
		return
	}
	if req.Method != "initialize" && !validMCPProtocolHeader(r.Header.Values("MCP-Protocol-Version")) {
		writeEmptyStatus(w, http.StatusBadRequest)
		return
	}

	switch req.Method {
	case "initialize":
		a.handleJSONRPCInitialize(w, req)
	case "notifications/initialized", "initialized":
		writeJSONRPCError(w, req.ID, jsonRPCInvalidRequest, "initialized must be a notification (no id)")
	case "ping":
		writeJSONRPCResult(w, req.ID, map[string]any{})
	case "tools/list":
		a.handleJSONRPCToolsList(w, req)
	case "tools/call":
		a.handleJSONRPCToolsCall(w, r, req)
	default:
		writeJSONRPCError(w, req.ID, jsonRPCMethodNotFound, "method not found")
	}
}

func isMCPRequestOnlyMethod(method string) bool {
	switch method {
	case "initialize", "ping", "tools/list", "tools/call":
		return true
	default:
		return false
	}
}

func parseJSONRPCMessage(body []byte) (*jsonRPCRequest, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return nil, invalidJSONRPCRequest(nil, "JSON-RPC message must be an object")
	}
	var req jsonRPCRequest
	if raw, ok := fields["jsonrpc"]; !ok || json.Unmarshal(raw, &req.JSONRPC) != nil || req.JSONRPC != "2.0" {
		return nil, invalidJSONRPCRequest(nil, `jsonrpc must be "2.0"`)
	}
	if raw, ok := fields["id"]; ok {
		if !validJSONRPCID(raw) {
			return nil, invalidJSONRPCRequest(nil, "id must be a string, number, or null")
		}
		req.ID = append(json.RawMessage(nil), raw...)
		req.HasID = true
	}
	if raw, ok := fields["method"]; ok {
		if json.Unmarshal(raw, &req.Method) != nil || req.Method == "" {
			return nil, invalidJSONRPCRequest(&req, "method must be a non-empty string")
		}
	}
	if raw, ok := fields["params"]; ok {
		trimmedParams := bytes.TrimSpace(raw)
		if len(trimmedParams) == 0 || (trimmedParams[0] != '{' && trimmedParams[0] != '[') {
			return nil, invalidJSONRPCRequest(&req, "params must be an object or array")
		}
		req.Params = append(json.RawMessage(nil), raw...)
	}
	_, hasResult := fields["result"]
	_, hasError := fields["error"]
	if req.Method == "" && req.HasID && hasResult != hasError {
		req.IsReply = true
		return &req, nil
	}
	if req.Method == "" {
		return nil, invalidJSONRPCRequest(&req, "method is required")
	}
	if hasResult || hasError {
		return nil, invalidJSONRPCRequest(&req, "request must not contain result or error")
	}
	return &req, nil
}

func validJSONRPCID(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if bytes.Equal(trimmed, []byte("null")) {
		return true
	}
	if len(trimmed) == 0 {
		return false
	}
	if trimmed[0] == '"' {
		var value string
		return json.Unmarshal(trimmed, &value) == nil
	}
	var number json.Number
	return json.Unmarshal(trimmed, &number) == nil
}

func readJSONRPCBody(body io.Reader) ([]byte, bool, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxJSONRPCBodyBytes+1))
	if err != nil {
		return nil, false, err
	}
	if len(data) > maxJSONRPCBodyBytes {
		return nil, true, nil
	}
	return data, false, nil
}

func isJSONContentType(values []string) bool {
	if len(values) != 1 || strings.Contains(values[0], ",") {
		return false
	}
	mediaType, params, err := mime.ParseMediaType(values[0])
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		return false
	}
	charset := params["charset"]
	return charset == "" || strings.EqualFold(charset, "utf-8")
}

func acceptsJSONAndSSE(values []string) bool {
	if len(values) == 0 {
		return true
	}
	hasJSON, hasSSE := false, false
	for _, field := range values {
		for _, item := range strings.Split(field, ",") {
			mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(item))
			if err != nil {
				return false
			}
			if q, ok := params["q"]; ok {
				quality, err := strconv.ParseFloat(q, 64)
				if err != nil || quality < 0 || quality > 1 {
					return false
				}
				if quality == 0 {
					continue
				}
			}
			switch strings.ToLower(mediaType) {
			case "*/*":
				return true
			case "application/json":
				hasJSON = true
			case "text/event-stream":
				hasSSE = true
			}
		}
	}
	return hasJSON && hasSSE
}

func validMCPProtocolHeader(values []string) bool {
	if len(values) == 0 {
		return supportedMCPProtocolVersions[mcpDefaultProtocolVersion]
	}
	if len(values) != 1 || strings.Contains(values[0], ",") {
		return false
	}
	return supportedMCPProtocolVersions[strings.TrimSpace(values[0])]
}

func (a *Adapter) handleJSONRPCInitialize(w http.ResponseWriter, req *jsonRPCRequest) {
	var params mcpInitializeParams
	if len(req.Params) == 0 || bytes.Equal(bytes.TrimSpace(req.Params), []byte("null")) || json.Unmarshal(req.Params, &params) != nil || params.ProtocolVersion == "" || params.Capabilities == nil || params.ClientInfo == nil || strings.TrimSpace(params.ClientInfo.Name) == "" || strings.TrimSpace(params.ClientInfo.Version) == "" {
		writeJSONRPCError(w, req.ID, jsonRPCInvalidParams, "initialize requires protocolVersion, capabilities, clientInfo.name, and clientInfo.version")
		return
	}
	selected := params.ProtocolVersion
	if !supportedMCPProtocolVersions[selected] {
		selected = mcpLatestProtocolVersion
	}
	result := mcpInitializeResult{
		ProtocolVersion: selected,
		Capabilities:    mcpCapabilities{Tools: &mcpToolsCapability{ListChanged: false}},
		ServerInfo:      mcpServerInfo{Name: "scrumboy", Version: "1.0.0"},
		Instructions:    "Scrumboy MCP server. Use tools/list to discover available tools.",
	}
	writeJSONRPCResult(w, req.ID, result)
}

func (a *Adapter) handleJSONRPCToolsList(w http.ResponseWriter, req *jsonRPCRequest) {
	writeJSONRPCResult(w, req.ID, map[string]any{"tools": a.toolCatalog()})
}

type toolsCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

type mcpTextContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (a *Adapter) handleJSONRPCToolsCall(w http.ResponseWriter, r *http.Request, req *jsonRPCRequest) {
	if len(req.Params) == 0 {
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
	data, _, toolErr := handler(r.Context(), args)
	if toolErr != nil {
		writeJSONRPCToolErrorResult(w, req.ID, toolErr.Message)
		return
	}
	writeJSONRPCToolSuccessResult(w, req.ID, data)
}

func requiredFieldNamesFromSchema(schema map[string]any) []string {
	raw := schema["required"]
	switch value := raw.(type) {
	case []string:
		return value
	case []any:
		out := make([]string, 0, len(value))
		for _, element := range value {
			if field, ok := element.(string); ok && field != "" {
				out = append(out, field)
			}
		}
		return out
	default:
		return nil
	}
}

func validateRequiredFields(toolName string, args map[string]any) string {
	definition, ok := toolCatalogDefinitions()[toolName]
	if !ok {
		return ""
	}
	schema, ok := definition.InputSchema.(map[string]any)
	if !ok {
		return ""
	}
	for _, field := range requiredFieldNamesFromSchema(schema) {
		if _, exists := args[field]; !exists {
			return "missing required field: " + field
		}
	}
	return ""
}

func toolResultText(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func writeJSONRPCResult(w http.ResponseWriter, id json.RawMessage, result any) {
	writeJSONRPCResponse(w, jsonRPCResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func writeJSONRPCToolSuccessResult(w http.ResponseWriter, id json.RawMessage, data any) {
	writeJSONRPCResult(w, id, map[string]any{
		"content":           []mcpTextContent{{Type: "text", Text: toolResultText(data)}},
		"structuredContent": data,
	})
}

func writeJSONRPCToolErrorResult(w http.ResponseWriter, id json.RawMessage, message string) {
	writeJSONRPCResult(w, id, map[string]any{
		"content": []mcpTextContent{{Type: "text", Text: message}},
		"isError": true,
	})
}

func writeJSONRPCErrorWithData(w http.ResponseWriter, id json.RawMessage, code int, message string, data any) {
	writeJSONRPCResponse(w, jsonRPCResponse{JSONRPC: "2.0", ID: id, Error: &jsonRPCError{Code: code, Message: message, Data: data}})
}

func writeJSONRPCError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	writeJSONRPCErrorStatus(w, http.StatusOK, id, code, message)
}

func writeJSONRPCErrorStatus(w http.ResponseWriter, status int, id json.RawMessage, code int, message string) {
	writeJSONRPCResponseStatus(w, status, jsonRPCResponse{JSONRPC: "2.0", ID: id, Error: &jsonRPCError{Code: code, Message: message}})
}

func writeJSONRPCResponse(w http.ResponseWriter, response jsonRPCResponse) {
	writeJSONRPCResponseStatus(w, http.StatusOK, response)
}

func writeJSONRPCResponseStatus(w http.ResponseWriter, status int, response jsonRPCResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response)
}

func writeEmptyStatus(w http.ResponseWriter, status int) {
	w.Header().Del("Content-Type")
	w.WriteHeader(status)
}
