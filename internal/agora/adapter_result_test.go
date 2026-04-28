package agora

import "testing"

func TestInvokeSuccessNormalized_invalidStructuredContent_fallsBackToTextJSON(t *testing.T) {
	t.Parallel()
	tcr := toolsCallResult{
		Content: []mcpTextBlock{
			{Type: "text", Text: `{"x":2}`},
		},
		StructuredContent: []byte(`{not-valid-json`),
	}
	v := invokeSuccessNormalized(tcr)
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T %v", v, v)
	}
	if x, _ := m["x"].(float64); x != 2 {
		t.Fatalf("expected x=2, got %v", m)
	}
}

func TestInvokeSuccessNormalized_invalidStructuredContent_fallsBackToRawText(t *testing.T) {
	t.Parallel()
	tcr := toolsCallResult{
		Content: []mcpTextBlock{
			{Type: "text", Text: "plain not json"},
		},
		StructuredContent: []byte(`{broken`),
	}
	v := invokeSuccessNormalized(tcr)
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T %v", v, v)
	}
	if s, _ := m["rawText"].(string); s != "plain not json" {
		t.Fatalf("rawText: %v", m)
	}
}
