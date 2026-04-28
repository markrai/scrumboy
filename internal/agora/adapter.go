package agora

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

type handler struct {
	mcp  http.Handler
	opts Options
}

func New(mcp http.Handler, opts Options) http.Handler {
	if opts.MaxRequestBytes <= 0 {
		opts.MaxRequestBytes = 1 << 20
	}
	if mcp == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		})
	}
	return &handler{mcp: mcp, opts: opts}
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/agora/v1" {
		h.writeNotFound(w)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/agora/v1/") {
		h.writeNotFound(w)
		return
	}
	switch r.URL.Path {
	case "/agora/v1/discover":
		if r.Method != http.MethodPost {
			h.writeMethodNotAllowed(w)
			return
		}
		h.serveDiscover(w, r)
	case "/agora/v1/invoke":
		if r.Method != http.MethodPost {
			h.writeMethodNotAllowed(w)
			return
		}
		h.serveInvoke(w, r)
	default:
		h.writeNotFound(w)
	}
}

func (h *handler) writeMethodNotAllowed(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusMethodNotAllowed)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":     false,
		"result": nil,
		"error": map[string]any{
			"message": "method not allowed",
		},
	})
}

func (h *handler) writeNotFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNotFound)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":     false,
		"result": nil,
		"error": map[string]any{
			"message": "not found",
		},
	})
}

func (h *handler) serveDiscover(w http.ResponseWriter, r *http.Request) {
	body, err := readLimitedBody(r, h.opts.MaxRequestBytes)
	if err != nil {
		if errors.Is(err, errTooLarge) {
			h.writeAdapterError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		h.writeAdapterError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(bytes.TrimSpace(body)) == 0 {
	} else {
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.DisallowUnknownFields()
		var eb emptyDiscover
		if err := dec.Decode(&eb); err != nil {
			h.writeAdapterError(w, http.StatusBadRequest, "invalid discover body: "+err.Error())
			return
		}
		if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			if err == nil {
				h.writeAdapterError(w, http.StatusBadRequest, "invalid discover body: extra json data")
			} else {
				h.writeAdapterError(w, http.StatusBadRequest, "invalid discover body: "+err.Error())
			}
			return
		}
	}
	rpc := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	_, out := roundTrip(h.mcp, r, rpc)
	if err := h.writeDiscoverFromMCP(w, out); err != nil {
		h.writeAdapterError(w, http.StatusBadGateway, err.Error())
	}
}

func (h *handler) serveInvoke(w http.ResponseWriter, r *http.Request) {
	body, err := readLimitedBody(r, h.opts.MaxRequestBytes)
	if err != nil {
		if errors.Is(err, errTooLarge) {
			h.writeAdapterError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		h.writeAdapterError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(bytes.TrimSpace(body)) == 0 {
		h.writeAdapterError(w, http.StatusBadRequest, "missing tool")
		return
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var env invokeEnvelope
	if err := dec.Decode(&env); err != nil {
		h.writeAdapterError(w, http.StatusBadRequest, "invalid invoke body: "+err.Error())
		return
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			h.writeAdapterError(w, http.StatusBadRequest, "invalid invoke body: extra json data")
		} else {
			h.writeAdapterError(w, http.StatusBadRequest, "invalid invoke body: "+err.Error())
		}
		return
	}
	if strings.TrimSpace(env.Tool) == "" {
		h.writeAdapterError(w, http.StatusBadRequest, "missing tool")
		return
	}
	if len(env.Arguments) == 0 {
		h.writeAdapterError(w, http.StatusBadRequest, "missing arguments")
		return
	}
	if err := validateArgumentsOuterShape(env.Arguments); err != nil {
		h.writeAdapterError(w, http.StatusBadRequest, err.Error())
		return
	}
	argBytes := normalizeArgumentsRaw(env.Arguments)
	paramsObj := struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}{Name: env.Tool, Arguments: argBytes}
	params, err := json.Marshal(paramsObj)
	if err != nil {
		h.writeAdapterError(w, http.StatusInternalServerError, "failed to build params")
		return
	}
	var rpc bytes.Buffer
	rpc.WriteString(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":`)
	rpc.Write(params)
	rpc.WriteByte('}')
	_, out := roundTrip(h.mcp, r, rpc.Bytes())
	if err := h.writeInvokeFromMCP(w, out); err != nil {
		h.writeAdapterError(w, http.StatusBadGateway, err.Error())
	}
}

var errTooLarge = errors.New("body too large")

func readLimitedBody(r *http.Request, max int64) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	lr := io.LimitReader(r.Body, max+1)
	b, err := io.ReadAll(lr)
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, errTooLarge
	}
	return b, nil
}

var errArgumentsMustBeObject = errors.New("arguments must be a JSON object")

func validateArgumentsOuterShape(rm json.RawMessage) error {
	if len(rm) == 0 {
		return nil
	}
	t := bytes.TrimSpace(rm)
	if len(t) == 0 {
		return nil
	}
	if string(t) == "null" {
		return nil
	}
	var v any
	if err := json.Unmarshal(rm, &v); err != nil {
		return errArgumentsMustBeObject
	}
	if v == nil {
		return nil
	}
	if _, ok := v.(map[string]any); !ok {
		return errArgumentsMustBeObject
	}
	return nil
}

func normalizeArgumentsRaw(rm json.RawMessage) json.RawMessage {
	if len(rm) == 0 {
		return []byte("{}")
	}
	s := string(bytes.TrimSpace(rm))
	if s == "" || s == "null" {
		return []byte("{}")
	}
	return rm
}

func (h *handler) writeAdapterError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":     false,
		"result": nil,
		"error": map[string]any{
			"message": message,
		},
	})
}

func (h *handler) writeDiscoverFromMCP(w http.ResponseWriter, mcpBytes []byte) error {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	var wire jsonRPCWire
	if err := json.Unmarshal(mcpBytes, &wire); err != nil {
		return err
	}
	if wire.Error != nil {
		h.writeJSONRPCErrorEnvelope(w, wire.Error)
		return nil
	}
	var listResult struct {
		Tools json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(wire.Result, &listResult); err != nil {
		return err
	}
	w.WriteHeader(http.StatusOK)
	enc := struct {
		OK     bool `json:"ok"`
		Result struct {
			Tools json.RawMessage `json:"tools"`
		} `json:"result"`
		Error any `json:"error"`
	}{
		OK:    true,
		Error: nil,
	}
	enc.Result.Tools = listResult.Tools
	return json.NewEncoder(w).Encode(&enc)
}

func (h *handler) writeInvokeFromMCP(w http.ResponseWriter, mcpBytes []byte) error {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	var wire jsonRPCWire
	if err := json.Unmarshal(mcpBytes, &wire); err != nil {
		return err
	}
	if wire.Error != nil {
		h.writeJSONRPCErrorEnvelope(w, wire.Error)
		return nil
	}
	var tcr toolsCallResult
	if err := json.Unmarshal(wire.Result, &tcr); err != nil {
		return err
	}
	if tcr.IsError {
		msg := extractTextContent(tcr.Content)
		w.WriteHeader(http.StatusOK)
		return json.NewEncoder(w).Encode(map[string]any{
			"ok":     false,
			"result": nil,
			"error": map[string]any{
				"message": msg,
			},
		})
	}
	resultValue := invokeSuccessNormalized(tcr)
	w.WriteHeader(http.StatusOK)
	return json.NewEncoder(w).Encode(map[string]any{
		"ok":     true,
		"result": resultValue,
		"error":  nil,
	})
}

func invokeSuccessNormalized(tcr toolsCallResult) any {
	var resultValue any
	sc := bytes.TrimSpace(tcr.StructuredContent)
	useStructured := false
	if len(sc) > 0 && string(sc) != "null" {
		var v any
		if err := json.Unmarshal(tcr.StructuredContent, &v); err == nil {
			if v != nil {
				resultValue = v
				useStructured = true
			}
		}
	}
	if !useStructured {
		txt := extractTextContent(tcr.Content)
		if txt != "" {
			var parsed any
			if err := json.Unmarshal([]byte(txt), &parsed); err != nil {
				resultValue = map[string]any{"rawText": txt}
			} else {
				resultValue = parsed
			}
		} else {
			resultValue = map[string]any{"rawText": ""}
		}
	}
	return resultValue
}

func extractTextContent(blocks []mcpTextBlock) string {
	if len(blocks) == 0 {
		return ""
	}
	return blocks[0].Text
}

func (h *handler) writeJSONRPCErrorEnvelope(w http.ResponseWriter, e *jsonRPCErrorWire) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	errObj := map[string]any{
		"code":    e.Code,
		"message": e.Message,
	}
	if len(e.Data) > 0 {
		var dataVal any
		if json.Unmarshal(e.Data, &dataVal) == nil {
			errObj["data"] = dataVal
		} else {
			errObj["data"] = json.RawMessage(e.Data)
		}
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":     false,
		"result": nil,
		"error":  errObj,
	})
}
