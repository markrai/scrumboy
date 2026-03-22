package httpapi

import (
	"regexp"
	"testing"
)

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

