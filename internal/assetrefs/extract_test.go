package assetrefs

import (
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

type extractFixture struct {
	Name     string   `json:"name"`
	Markdown string   `json:"markdown"`
	Expected []string `json:"expected"`
	Note     string   `json:"note,omitempty"`
}

type remapFixture struct {
	Name     string `json:"name"`
	Markdown string `json:"markdown"`
	Expected string `json:"expected"`
	Error    string `json:"error,omitempty"`
}

const (
	u1 = "11111111-1111-4111-8111-111111111111"
	u2 = "22222222-2222-4222-9222-222222222222"
	n1 = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	n2 = "bbbbbbbb-bbbb-4bbb-abbb-bbbbbbbbbbbb"
)

var fixtureMapping = map[string]string{u1: n1, u2: n2}

func loadJSON[T any](t testing.TB, path string) []T {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out []T
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return out
}

func TestExtractFixtures(t *testing.T) {
	fixtures := loadJSON[extractFixture](t, "testdata/fixtures.json")
	if len(fixtures) < 35 {
		t.Fatalf("fixture corpus has %d cases, want >= 35", len(fixtures))
	}
	for _, f := range fixtures {
		t.Run(f.Name, func(t *testing.T) {
			got := Extract(f.Markdown)
			if !slices.Equal(got, f.Expected) && !(len(got) == 0 && len(f.Expected) == 0) {
				t.Fatalf("Extract = %v, want %v", got, f.Expected)
			}
		})
	}
}

func TestExtractLongBody(t *testing.T) {
	body := "![a](/api/assets/" + u1 + ")\n\n" + strings.Repeat("filler text ", 1700) + "\n\n![b](/api/assets/" + u2 + ")"
	if len(body) < 20000 {
		t.Fatalf("body too short: %d", len(body))
	}
	if got := Extract(body); !slices.Equal(got, []string{u1, u2}) {
		t.Fatalf("Extract = %v", got)
	}
}

func TestExtractNoBangFastPath(t *testing.T) {
	if got := Extract("/api/assets/" + u1); got != nil {
		t.Fatalf("Extract = %v", got)
	}
}

func FuzzExtract(f *testing.F) {
	for _, fx := range loadJSON[extractFixture](f, "testdata/fixtures.json") {
		f.Add(fx.Markdown)
	}
	f.Fuzz(func(t *testing.T, body string) {
		got := Extract(body)
		if len(got) > strings.Count(body, "!") {
			t.Fatalf("more references than '!' characters")
		}
		for _, id := range got {
			if !IsCanonicalUUID(id) {
				t.Fatalf("non-canonical id %q", id)
			}
		}
	})
}

func TestRemapFixtures(t *testing.T) {
	for _, f := range loadJSON[remapFixture](t, "testdata/remap_fixtures.json") {
		t.Run(f.Name, func(t *testing.T) {
			got, err := RemapImageDestinations(f.Markdown, fixtureMapping)
			if f.Error != "" {
				want := map[string]error{"unmapped": ErrUnmappedReference, "shared": ErrSharedLinkDestination}[f.Error]
				if want == nil || !errors.Is(err, want) {
					t.Fatalf("want %v, got %q, %v", want, got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("RemapImageDestinations: %v", err)
			}
			if got != f.Expected {
				t.Fatalf("got  %q\nwant %q", got, f.Expected)
			}
		})
	}
}

// Every image Extract reports must come out remapped: no mapped ID may survive a successful remap.
func TestRemapFixturesLeaveNoMappedEmbed(t *testing.T) {
	for _, f := range loadJSON[remapFixture](t, "testdata/remap_fixtures.json") {
		got, err := RemapImageDestinations(f.Markdown, fixtureMapping)
		if err != nil {
			continue
		}
		for _, id := range Extract(got) {
			if _, ok := fixtureMapping[id]; ok {
				t.Errorf("%s: %s still embedded after remap", f.Name, id)
			}
		}
	}
}

func TestRemapRejectsNonCanonicalMapping(t *testing.T) {
	if _, err := RemapImageDestinations("x", map[string]string{"nope": n1}); err == nil {
		t.Fatal("want error")
	}
}

func TestRemapTooManyCandidates(t *testing.T) {
	body := "![a](/api/assets/" + u1 + ") " + strings.Repeat("`/api/assets/"+u1+"` ", MaxRemapCandidates+1)
	if _, err := RemapImageDestinations(body, fixtureMapping); err != ErrTooManyReferences {
		t.Fatalf("want ErrTooManyReferences, got %v", err)
	}
}

func FuzzRemap(f *testing.F) {
	for _, fx := range loadJSON[remapFixture](f, "testdata/remap_fixtures.json") {
		f.Add(fx.Markdown)
	}
	f.Fuzz(func(t *testing.T, body string) {
		out, err := RemapImageDestinations(body, fixtureMapping)
		if err != nil {
			return
		}
		for _, id := range Extract(out) {
			if _, ok := fixtureMapping[id]; ok {
				t.Fatalf("mapped id %s still embedded", id)
			}
		}
		if strings.ContainsAny(body, `&\`) {
			return
		}
		if len(out) != len(body) {
			t.Fatalf("length changed")
		}
		// Without escapes or references, bytes may differ only inside rewritten 36-byte UUID spans.
		for i := 0; i < len(body); i++ {
			if body[i] == out[i] {
				continue
			}
			start := strings.LastIndex(out[:i+1], CanonicalPathPrefix)
			if start < 0 || i >= start+len(CanonicalPathPrefix)+36 || i < start+len(CanonicalPathPrefix) {
				t.Fatalf("byte %d changed outside a UUID span", i)
			}
		}
	})
}
