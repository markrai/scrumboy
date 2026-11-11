package drawrefs

import (
	"reflect"
	"strings"
	"testing"
)

const (
	asset = "/api/assets/a8b1c2d3-e4f5-4a6b-8c7d-9e0f1a2b3c4d"
	idA   = "drw_aaaaaaaa"
	idB   = "drw_bbbbbbbb"
	idN   = "drw_nnnnnnnn"
)

func TestAltDrawingIDGrammar(t *testing.T) {
	cases := map[string]string{
		"shot|draw:" + idA + "|480": idA,
		"shot|draw:" + idA:          idA,
		"shot|480":                  "",
		"shot|draw:" + idA + "|20":  "",
		"shot|draw:bad":             "",
		"a|b|draw:" + idA + "|1600": idA,
		"draw:" + idA:               "",
	}
	for alt, want := range cases {
		got, _ := AltDrawingID(alt)
		if got != want {
			t.Errorf("AltDrawingID(%q)=%q want %q", alt, got, want)
		}
	}
}

func TestExtractCountsOnlySupportedImageForms(t *testing.T) {
	body := strings.Join([]string{
		"![shot|draw:" + idA + "|480](" + asset + ")",
		"![sketch|draw:" + idB + "|640](/api/board/my-board/drawings/" + idB + ")",
		"`![code|draw:drw_cccccccc](" + asset + ")`",
		"```",
		"![fenced|draw:drw_dddddddd](" + asset + ")",
		"```",
		"![ext|draw:drw_eeeeeeee](https://example.com/x.png)",
		"[link|draw:drw_ffffffff](" + asset + ")",
		"plain draw:drw_gggggggg text",
		"![shot again|draw:" + idA + "](" + asset + ")",
	}, "\n\n")
	got := Extract(body)
	if !reflect.DeepEqual(got, []string{idA, idB}) {
		t.Fatalf("Extract=%v", got)
	}
}

func TestExtractStandaloneWithoutTag(t *testing.T) {
	got := Extract("![sketch](/api/board/b/drawings/" + idA + ")")
	if !reflect.DeepEqual(got, []string{idA}) {
		t.Fatalf("Extract=%v", got)
	}
}

func TestRemapRewritesTagsAndStandaloneDestinationsOnly(t *testing.T) {
	body := "Intro `draw:" + idA + "` kept\n\n![shot|draw:" + idA + "|480](" + asset + ")\n\n![s|draw:" + idB + "|640](/api/board/old-slug/drawings/" + idB + ")\n\n    ![indented|draw:" + idA + "](" + asset + ")\n"
	out, err := Remap(body, map[string]string{idA: idN, idB: "drw_mmmmmmmm"}, "new-slug")
	if err != nil {
		t.Fatal(err)
	}
	want := "Intro `draw:" + idA + "` kept\n\n![shot|draw:" + idN + "|480](" + asset + ")\n\n![s|draw:drw_mmmmmmmm|640](/api/board/new-slug/drawings/drw_mmmmmmmm)\n\n    ![indented|draw:" + idA + "](" + asset + ")\n"
	if out != want {
		t.Fatalf("remap\n got %q\nwant %q", out, want)
	}
}

func TestRemapLeavesUnmappedBodiesUntouched(t *testing.T) {
	body := "![x|draw:" + idA + "](" + asset + ")"
	out, err := Remap(body, map[string]string{idB: idN}, "s")
	if err != nil || out != body {
		t.Fatalf("out=%q err=%v", out, err)
	}
}

func TestRemapSharedReferenceMapsOnce(t *testing.T) {
	body := "![a|draw:" + idA + "](" + asset + ")\n\n![b|draw:" + idA + "|200](" + asset + ")"
	out, err := Remap(body, map[string]string{idA: idN}, "s")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, idN) != 2 || strings.Contains(out, idA) {
		t.Fatalf("out=%q", out)
	}
}

func TestRemapRejectsInvalidInput(t *testing.T) {
	if _, err := Remap("x", map[string]string{"bad": idN}, "s"); err == nil {
		t.Fatal("expected invalid mapping")
	}
	if _, err := Remap("x", map[string]string{idA: idN}, "bad/slug"); err == nil {
		t.Fatal("expected invalid slug")
	}
}

func TestNewIDIsValidAndRandom(t *testing.T) {
	a, b := NewID(), NewID()
	if !IsDrawingID(a) || a == b {
		t.Fatalf("ids %q %q", a, b)
	}
}
