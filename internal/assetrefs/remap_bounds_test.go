package assetrefs

import (
	"errors"
	"runtime"
	"strings"
	"testing"
)

// totalAlloc returns the bytes allocated while f runs (an upper bound on its peak).
func totalAlloc(f func()) uint64 {
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

// adversarialBodies are highly compressible note bodies of about n bytes, each ending with one
// embedded image, shaped to stress the decoder: plain prose, dense character references, dense
// "/api/assets/" anchors, a run with no safe chunk boundary, and dense backslash escapes.
func adversarialBodies(n int) map[string]string {
	img := "\n\n![a](/api/assets/" + hexRef(u1) + ")\n"
	return map[string]string{
		"prose":    strings.Repeat("lorem ipsum ", n/12) + img,
		"entities": strings.Repeat("&#x31;", n/6) + img,
		"anchors":  strings.Repeat("/api/assets/", n/12) + img,
		"alnum":    strings.Repeat("a", n) + img,
		"escapes":  strings.Repeat(`\*`, n/2) + img,
	}
}

func hexRef(id string) string { return "&#x" + hexDigits(id[0]) + ";" + id[1:] }

func hexDigits(c byte) string {
	const d = "0123456789abcdef"
	return string([]byte{d[c>>4], d[c&0xf]})
}

// A huge archive note with one embedded image is refused after a single parse, without
// allocating per byte of the note.
func TestRemapOversizedNoteFailsClosedCheaply(t *testing.T) {
	if testing.Short() {
		t.Skip("allocates 32 MiB bodies")
	}
	for name, body := range adversarialBodies(32 << 20) {
		var err error
		alloc := totalAlloc(func() { _, err = RemapImageDestinations(body, fixtureMapping) })
		if !errors.Is(err, ErrNoteTooLarge) {
			t.Fatalf("%s: want ErrNoteTooLarge, got %v", name, err)
		}
		if limit := uint64(2*len(body)) + 8<<20; alloc > limit {
			t.Fatalf("%s: %d MiB allocated for a %d MiB note (limit %d MiB)", name, alloc>>20, len(body)>>20, limit>>20)
		}
	}
}

// At the size ceiling the full remap path runs with allocation bounded by a small multiple of
// the note, not by a per-byte decoded copy of it.
func TestRemapAtCeilingIsBounded(t *testing.T) {
	for name, body := range adversarialBodies(MaxRemapBodyBytes - 64) {
		var out string
		var err error
		alloc := totalAlloc(func() { out, err = RemapImageDestinations(body, fixtureMapping) })
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.HasSuffix(out, "![a](/api/assets/"+n1+")\n") {
			t.Fatalf("%s: image not remapped", name)
		}
		if limit := uint64(32 << 20); alloc > limit {
			t.Fatalf("%s: %d MiB allocated for a 1 MiB note (limit %d MiB)", name, alloc>>20, limit>>20)
		}
	}
}

// Oversized notes that embed no mapped image (legacy imports) are returned unchanged.
func TestRemapOversizedNoteWithoutMappedImageUnchanged(t *testing.T) {
	body := strings.Repeat("plain text ", (MaxRemapBodyBytes/11)+10) + "![x](/api/assets/33333333-3333-4333-8333-333333333333)"
	out, err := RemapImageDestinations(body, fixtureMapping)
	if err != nil || out != body {
		t.Fatalf("unchanged body expected, got err %v", err)
	}
}

// Destinations straddling decoder chunk boundaries are found at every offset, whether the
// boundary falls on a safe split point or must be forced inside a long alphanumeric run.
func TestRemapAcrossChunkBoundaries(t *testing.T) {
	// A character reference and a backslash escape inside the UUID.
	encoded := `![a](/api/assets/&#x31;1111111\-1111-4111-8111-111111111111)`
	for _, filler := range []string{" ", "a"} {
		for shift := -80; shift <= 8; shift++ {
			pad := decodeChunk + shift
			body := strings.Repeat(filler, pad) + "\n\n" + encoded
			got, err := RemapImageDestinations(body, fixtureMapping)
			if filler == "a" && errors.Is(err, ErrUnmappedReference) {
				continue // a forced split may fail closed; it must never mis-rewrite
			}
			if err != nil {
				t.Fatalf("filler %q shift %d: %v", filler, shift, err)
			}
			if want := strings.Repeat(filler, pad) + "\n\n![a](/api/assets/" + n1 + ")"; got != want {
				t.Fatalf("filler %q shift %d: wrong rewrite", filler, shift)
			}
		}
	}
}

// The one-at-a-time fallback is limited by a parse budget, so a long note that needs it fails
// closed instead of being parsed dozens of times.
func TestRemapFallbackProbeBudget(t *testing.T) {
	label := "![/api/assets/" + u1 + "]\n\n[/api/assets/" + u1 + "]: /api/assets/" + u1 + "\n"
	short, err := RemapImageDestinations(label, fixtureMapping)
	if err != nil || !strings.Contains(short, "]: /api/assets/"+n1) {
		t.Fatalf("short label case: %q %v", short, err)
	}
	long := strings.Repeat("filler ", (MaxRemapBodyBytes-len(label))/7) + "\n\n" + label
	var out string
	alloc := totalAlloc(func() { out, err = RemapImageDestinations(long, fixtureMapping) })
	if err != nil && !errors.Is(err, ErrUnmappedReference) {
		t.Fatalf("long label case: %v", err)
	}
	if err == nil && !strings.Contains(out, "]: /api/assets/"+n1) {
		t.Fatal("long label case: wrong rewrite")
	}
	if alloc > 64<<20 {
		t.Fatalf("fallback allocated %d MiB", alloc>>20)
	}
}
