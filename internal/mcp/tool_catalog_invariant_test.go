package mcp

import (
	"regexp"
	"testing"
)

// claudeToolNamePattern mirrors the regex Claude's MCP client validates every
// tools/list name against (^[a-zA-Z0-9_-]{1,64}$). A single name that fails
// this breaks tool-calling for every MCP server in the session, not just
// Scrumboy -- see the PR that introduced this test for the incident writeup.
var claudeToolNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// TestImplementedTools_UniqueAndClaudeCompatible is a catalog invariant: every
// name returned by implementedTools() (and therefore advertised via
// tools/list and system_getCapabilities) must be unique and satisfy Claude's
// tool-name regex. This guards against reintroducing a dotted or otherwise
// incompatible name in the future.
func TestImplementedTools_UniqueAndClaudeCompatible(t *testing.T) {
	a := New(nil, Options{Mode: "full"})
	names := a.implementedTools()
	if len(names) == 0 {
		t.Fatal("implementedTools() returned no tools")
	}

	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if seen[name] {
			t.Errorf("duplicate tool name in implementedTools(): %q", name)
		}
		seen[name] = true

		if !claudeToolNamePattern.MatchString(name) {
			t.Errorf("tool name %q does not match Claude's tool-name pattern %s", name, claudeToolNamePattern.String())
		}
	}
}

// TestToolCatalog_NamesUniqueAndClaudeCompatible checks the same invariant
// against the actual tools/list payload (toolCatalog()), which is built from
// toolCatalogDefinitions() rather than implementedTools() directly.
func TestToolCatalog_NamesUniqueAndClaudeCompatible(t *testing.T) {
	a := New(nil, Options{Mode: "full"})
	catalog := a.toolCatalog()
	if len(catalog) == 0 {
		t.Fatal("toolCatalog() returned no tools")
	}

	seen := make(map[string]bool, len(catalog))
	for _, def := range catalog {
		if def.Name == "" {
			t.Fatalf("tool definition missing name: %#v", def)
		}
		if seen[def.Name] {
			t.Errorf("duplicate tool name in toolCatalog(): %q", def.Name)
		}
		seen[def.Name] = true

		if !claudeToolNamePattern.MatchString(def.Name) {
			t.Errorf("tool name %q does not match Claude's tool-name pattern %s", def.Name, claudeToolNamePattern.String())
		}
	}
}

// TestLegacyToolAliases_AreDottedAndDisjointFromCatalog verifies the
// dispatch-only compatibility shim (registerLegacyToolAliases) stays
// dispatch-only: every alias key is a legacy dotted name distinct from any
// current catalog name, and every alias resolves to a real implemented tool.
// If this ever fails, a dotted name has leaked into (or a canonical name has
// been shadowed by) the alias table, which is exactly the class of bug the
// underscore rename fixed.
func TestLegacyToolAliases_AreDottedAndDisjointFromCatalog(t *testing.T) {
	a := New(nil, Options{Mode: "full"})
	canonical := make(map[string]bool)
	for _, name := range a.implementedTools() {
		canonical[name] = true
	}

	if len(legacyToolAliases) == 0 {
		t.Fatal("legacyToolAliases is empty")
	}

	for oldName, newName := range legacyToolAliases {
		if claudeToolNamePattern.MatchString(oldName) {
			t.Errorf("legacy alias %q unexpectedly satisfies the underscore-only pattern; it should be a dotted legacy name", oldName)
		}
		if canonical[oldName] {
			t.Errorf("legacy alias key %q collides with a current canonical tool name", oldName)
		}
		if !canonical[newName] {
			t.Errorf("legacy alias %q -> %q does not resolve to an implemented tool", oldName, newName)
		}
	}
}
