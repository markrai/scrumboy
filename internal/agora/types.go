package agora

import "encoding/json"

type Options struct {
	MaxRequestBytes int64
}

type invokeEnvelope struct {
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
}

type emptyDiscover struct{}

type jsonRPCWire struct {
	JSONRPC string            `json:"jsonrpc"`
	ID      json.RawMessage   `json:"id"`
	Result  json.RawMessage   `json:"result"`
	Error   *jsonRPCErrorWire `json:"error"`
}

type jsonRPCErrorWire struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type toolsListResult struct {
	Tools []json.RawMessage `json:"tools"`
}

type toolsCallResult struct {
	Content           []mcpTextBlock  `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent"`
	IsError           bool            `json:"isError"`
}

type mcpTextBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}
