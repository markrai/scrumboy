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

// renamedToolsWithLegacyAliases is the fixed set of tools that existed when
// MCP wire names were renamed from dots to underscores. Each must keep a
// permanent dotted alias. Tools introduced after that migration must not invent
// legacy dotted names, so this list is intentionally frozen rather than derived
// from implementedTools().
var renamedToolsWithLegacyAliases = []string{
	"system_getCapabilities",
	"projects_list",
	"todos_create",
	"todos_get",
	"todos_search",
	"todos_update",
	"todos_delete",
	"todos_move",
	"sprints_list",
	"sprints_get",
	"sprints_getActive",
	"sprints_create",
	"sprints_activate",
	"sprints_close",
	"sprints_update",
	"sprints_delete",
	"tags_listProject",
	"tags_listMine",
	"tags_updateMineColor",
	"tags_deleteMine",
	"tags_updateProjectColor",
	"tags_deleteProject",
	"members_list",
	"members_listAvailable",
	"members_add",
	"members_updateRole",
	"members_remove",
	"board_get",
}

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

// TestLegacyToolAliases_CoverRenamedToolSetAndUniqueTargets asserts the fixed
// rename-era set (28 tools) each has exactly one dotted alias with unique
// targets. New tools added after the migration are intentionally excluded.
func TestLegacyToolAliases_CoverRenamedToolSetAndUniqueTargets(t *testing.T) {
	if len(renamedToolsWithLegacyAliases) != 28 {
		t.Fatalf("renamedToolsWithLegacyAliases should list the 28 rename-era tools, got %d", len(renamedToolsWithLegacyAliases))
	}
	if len(legacyToolAliases) != len(renamedToolsWithLegacyAliases) {
		t.Fatalf("legacyToolAliases has %d entries, want %d (one per rename-era tool)", len(legacyToolAliases), len(renamedToolsWithLegacyAliases))
	}

	a := New(nil, Options{Mode: "full"})
	canonical := make(map[string]bool, len(a.implementedTools()))
	for _, name := range a.implementedTools() {
		canonical[name] = true
	}

	expected := make(map[string]bool, len(renamedToolsWithLegacyAliases))
	for _, name := range renamedToolsWithLegacyAliases {
		if expected[name] {
			t.Errorf("duplicate entry in renamedToolsWithLegacyAliases: %q", name)
		}
		expected[name] = true
		if !canonical[name] {
			t.Errorf("rename-era tool %q is missing from implementedTools()", name)
		}
	}

	targets := make(map[string]string, len(legacyToolAliases))
	for oldName, newName := range legacyToolAliases {
		if !expected[newName] {
			t.Errorf("legacy alias %q -> %q targets a tool outside the rename-era set; new tools should not invent dotted aliases", oldName, newName)
		}
		if prev, ok := targets[newName]; ok {
			t.Errorf("alias target %q is not unique: claimed by both %q and %q", newName, prev, oldName)
		}
		targets[newName] = oldName
	}

	for name := range expected {
		if _, ok := targets[name]; !ok {
			t.Errorf("rename-era tool %q has no legacy dotted alias", name)
		}
	}

	// Discovery must never advertise alias keys (covered end-to-end in
	// legacy_alias_test.go); assert the catalog construction path here too.
	for _, def := range a.toolCatalog() {
		if _, ok := legacyToolAliases[def.Name]; ok {
			t.Errorf("toolCatalog() advertised legacy alias key %q", def.Name)
		}
	}
	for _, name := range a.implementedTools() {
		if _, ok := legacyToolAliases[name]; ok {
			t.Errorf("implementedTools() advertised legacy alias key %q", name)
		}
	}
}
