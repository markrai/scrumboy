package mcp

import "testing"

func TestWebhookToolsAreDiscoveredWithStrictSchemasAndNoAliases(t *testing.T) {
	adapter := New(nil, Options{Mode: "full"})
	wantRequired := map[string][]string{
		"webhooks_create": {"projectSlug", "url", "events"},
		"webhooks_list":   {"projectSlug"},
		"webhooks_delete": {"projectSlug", "webhookId"},
	}
	implemented := map[string]bool{}
	for _, name := range adapter.implementedTools() {
		implemented[name] = true
	}
	for name, required := range wantRequired {
		if !implemented[name] {
			t.Fatalf("%s is not advertised", name)
		}
		if _, ok := adapter.tools[name]; !ok {
			t.Fatalf("%s is not registered", name)
		}
		definition, ok := adapter.toolDefinition(name)
		if !ok {
			t.Fatalf("%s has no catalog definition", name)
		}
		schema := definition.InputSchema.(map[string]any)
		if schema["additionalProperties"] != false {
			t.Fatalf("%s permits unknown input fields", name)
		}
		got := requiredFieldNamesFromSchema(schema)
		if len(got) != len(required) {
			t.Fatalf("%s required fields=%v want=%v", name, got, required)
		}
		for index := range required {
			if got[index] != required[index] {
				t.Fatalf("%s required fields=%v want=%v", name, got, required)
			}
		}
	}
	for _, alias := range []string{"webhooks.create", "webhooks.list", "webhooks.delete"} {
		if _, ok := adapter.tools[alias]; ok {
			t.Fatalf("unexpected webhook alias %s", alias)
		}
	}
}
