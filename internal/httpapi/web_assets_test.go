package httpapi

import (
	"regexp"
	"strings"
	"testing"
)

// Web asset tests read embedded CSS/TS and assert alignment with documented contracts
// (canonical workflow keys, helper structure, columnsSpec keys). They do not run a browser
// or verify runtime DOM behavior after board refresh, workflow mutation, or drag/drop.
// Manual QA remains mandatory for those flows; do not treat these tests as UI/E2E coverage.

// Canonical default workflow column_key values (internal/store/types.go DefaultColumn*, workflows.go defaultWorkflowColumns).
var canonicalDefaultWorkflowKeys = []string{
	"backlog",
	"not_started",
	"doing",
	"testing",
	"done",
}

func TestWebStyles_ColListFillsColumn(t *testing.T) {
	css, err := embeddedWeb.ReadFile("web/styles.css")
	if err != nil {
		t.Fatalf("read embedded styles.css: %v", err)
	}

	re := regexp.MustCompile(`(?s)\.col__list\s*\{[^}]*\bflex\s*:\s*1\s*;`)
	if !re.Match(css) {
		t.Fatalf("expected .col__list to include flex: 1; (so the drop target fills the column)")
	}
}

func TestWebStyles_MobileTabFallbackSelectors_AlignWithCanonicalKeys(t *testing.T) {
	css, err := embeddedWeb.ReadFile("web/styles.css")
	if err != nil {
		t.Fatalf("read embedded styles.css: %v", err)
	}
	s := string(css)
	for _, k := range canonicalDefaultWorkflowKeys {
		needle := `.mobile-tab[data-tab="` + k + `"]`
		if !strings.Contains(s, needle) {
			t.Fatalf("expected styles.css to contain %q for default workflow lane CSS fallbacks", needle)
		}
	}
}

func TestWebStyles_MobileTabGenericRoundedCornerRule(t *testing.T) {
	css, err := embeddedWeb.ReadFile("web/styles.css")
	if err != nil {
		t.Fatalf("read embedded styles.css: %v", err)
	}
	s := string(css)
	if !strings.Contains(s, "Rounded outer edge for all lanes") {
		t.Fatalf("expected mobile .mobile-tab generic rounding comment")
	}
	// Generic rule (not only per-[data-tab] presets) so custom lane keys get the same radius.
	if !strings.Contains(s, ".mobile-tab {") || !strings.Contains(s, "border-radius: var(--radius-8) 0 0 8px") {
		t.Fatalf("expected .mobile-tab to define border-radius for mobile lane tabs")
	}
	if strings.Count(s, "border-radius: var(--radius-8) 0 0 8px") < 2 {
		t.Fatalf("expected at least tab + drop-zone rounded corners (mobile lane strip)")
	}
}

func TestWebMobileLaneTabs_BuildInnerHtml_ComposesTabsDropZonesAndDrops(t *testing.T) {
	ts, err := embeddedWeb.ReadFile("web/modules/views/mobile-lane-tabs.ts")
	if err != nil {
		t.Fatalf("read mobile-lane-tabs.ts: %v", err)
	}
	s := string(ts)
	if !strings.Contains(s, "export function buildMobileTabsInnerHtml") {
		t.Fatalf("expected buildMobileTabsInnerHtml export")
	}
	// Strip must concatenate tab buttons then #mobileTabDropZones filled with .mobile-tab-drop nodes.
	if !strings.Contains(s, `<div id="mobileTabDropZones">`) || !strings.Contains(s, "mobile-tab-drop") || !strings.Contains(s, "class=\"mobile-tab ") {
		t.Fatalf("expected buildMobileTabsInnerHtml to compose mobile tabs, #mobileTabDropZones, and drop overlays")
	}
	if !strings.Contains(s, "export function mobileLaneTabStyleAttrForHtml") || !strings.Contains(s, "export function applyMobileLaneTabStyles") {
		t.Fatalf("expected shared mobile lane style helpers to be exported")
	}
	if !strings.Contains(s, "sanitizeHexColor") {
		t.Fatalf("expected lane tab HTML styles to use sanitizeHexColor")
	}
}

func TestWebDragDrop_ColumnsSpec_UsesCanonicalWorkflowKeys(t *testing.T) {
	src, err := embeddedWeb.ReadFile("web/modules/features/drag-drop.ts")
	if err != nil {
		t.Fatalf("read drag-drop.ts: %v", err)
	}
	s := string(src)
	if !strings.Contains(s, "export function columnsSpec()") {
		t.Fatalf("expected columnsSpec export")
	}
	for _, k := range canonicalDefaultWorkflowKeys {
		if !strings.Contains(s, `key: "`+k+`"`) {
			t.Fatalf("expected columnsSpec to include canonical key %q (match store/API workflow)", k)
		}
	}
}

func TestWebStyles_CardDoingAliasForWorkflowKey(t *testing.T) {
	css, err := embeddedWeb.ReadFile("web/styles.css")
	if err != nil {
		t.Fatalf("read embedded styles.css: %v", err)
	}
	s := string(css)
	if !strings.Contains(s, ".card--doing") || !strings.Contains(s, "API column_key is `doing`") {
		t.Fatalf("expected .card--doing alias when todo.status is DOING (workflow column_key doing)")
	}
}

func TestWebStyles_SettingsDialog_RtlMobileCentering(t *testing.T) {
	css, err := embeddedWeb.ReadFile("web/styles.css")
	if err != nil {
		t.Fatalf("read embedded styles.css: %v", err)
	}
	s := string(css)
	if !strings.Contains(s, `[dir="rtl"] #settingsDialog.dialog`) {
		t.Fatalf("expected RTL mobile Settings dialog centering selector")
	}
	re := regexp.MustCompile(`(?s)@media \(max-width: 767px\)\s*\{\s*\[dir="rtl"\] #settingsDialog\.dialog\s*\{[^}]+\}`)
	if !re.Match(css) {
		t.Fatalf("expected @media (max-width: 767px) block for RTL Settings dialog centering")
	}
	block := re.FindString(s)
	for _, needle := range []string{
		"margin-left: auto",
		"margin-right: auto",
		"transform: translateY(-50%)",
		"max-width: calc(100vw - 24px)",
	} {
		if !strings.Contains(block, needle) {
			t.Fatalf("expected RTL Settings mobile centering block to contain %q", needle)
		}
	}
	re620 := regexp.MustCompile(`(?s)@media \(max-width: 620px\)\s*\{\s*\[dir="rtl"\] #settingsDialog\.dialog\s*\{[^}]+\}`)
	if !re620.Match(css) {
		t.Fatalf("expected @media (max-width: 620px) block for RTL Settings dialog width")
	}
	block620 := re620.FindString(s)
	if !strings.Contains(block620, "max-width: calc(100vw - 16px)") {
		t.Fatalf("expected narrow-mobile RTL Settings block to cap width at calc(100vw - 16px)")
	}
}

func TestWebBoard_MobileTabLegacyResolutionAndSync(t *testing.T) {
	boardTs, err := embeddedWeb.ReadFile("web/modules/views/board.ts")
	if err != nil {
		t.Fatalf("read board.ts: %v", err)
	}
	s := string(boardTs)
	for _, needle := range []string{
		"function syncMobileLaneTabsStrip",
		"function ensureMobileTabForBoard",
		"syncMobileLaneTabsStrip(board)",
		"bindMobileTabClickHandlersIfNeeded",
		"resolveMobileTabKeyFromStorage",
		"LEGACY_MOBILE_TAB_KEYS",
	} {
		if !strings.Contains(s, needle) {
			t.Fatalf("expected board.ts to contain %q", needle)
		}
	}
}

func TestWebAssets_CalendarHostBadgesAreEmbedded(t *testing.T) {
	for _, name := range []string{"google.webp", "apple.webp"} {
		data, err := embeddedWeb.ReadFile("web/assets/calendar/" + name)
		if err != nil {
			t.Fatalf("read embedded calendar badge %s: %v", name, err)
		}
		if len(data) == 0 {
			t.Fatalf("embedded calendar badge %s is empty", name)
		}
	}
	css, err := embeddedWeb.ReadFile("web/styles.css")
	if err != nil {
		t.Fatalf("read embedded styles.css: %v", err)
	}
	s := string(css)
	if !strings.Contains(s, ".col__agenda-host--apple") || !strings.Contains(s, "filter: invert(1)") {
		t.Fatal("expected Apple header host invert")
	}
	if !strings.Contains(s, ".col__agenda-hosts") {
		t.Fatal("expected agenda header host chips")
	}
}
