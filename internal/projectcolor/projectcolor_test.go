package projectcolor

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"strconv"
	"testing"
)

func solidPNGDataURL(t *testing.T, c color.RGBA) string {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: c}, image.Point{}, draw.Src)
	return pngDataURL(t, img)
}

func pngDataURL(t *testing.T, img image.Image) string {
	t.Helper()

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func parseHexColor(t *testing.T, got string) (r, g, b uint8) {
	t.Helper()
	assertHexColor(t, got)

	r64, err := strconv.ParseUint(got[1:3], 16, 8)
	if err != nil {
		t.Fatalf("parse red from %q: %v", got, err)
	}
	g64, err := strconv.ParseUint(got[3:5], 16, 8)
	if err != nil {
		t.Fatalf("parse green from %q: %v", got, err)
	}
	b64, err := strconv.ParseUint(got[5:7], 16, 8)
	if err != nil {
		t.Fatalf("parse blue from %q: %v", got, err)
	}
	return uint8(r64), uint8(g64), uint8(b64)
}

func assertHexColor(t *testing.T, got string) {
	t.Helper()

	if len(got) != 7 || got[0] != '#' {
		t.Fatalf("color = %q, want #RRGGBB", got)
	}
	for _, c := range got[1:] {
		if (c >= '0' && c <= '9') || (c >= 'A' && c <= 'F') {
			continue
		}
		t.Fatalf("color = %q, want uppercase #RRGGBB", got)
	}
}

func TestExtractFromDataURL_InvalidInputsReturnFallback(t *testing.T) {
	notImage := base64.StdEncoding.EncodeToString([]byte("not an image"))
	cases := []struct {
		name  string
		input string
	}{
		{name: "empty", input: ""},
		{name: "whitespace", input: " \t\n "},
		{name: "non data url", input: "https://example.test/image.png"},
		{name: "data url without comma", input: "data:image/png;base64"},
		{name: "data url without base64", input: "data:image/png," + notImage},
		{name: "malformed base64", input: "data:image/png;base64,not-base64!!!"},
		{name: "base64 non image", input: "data:text/plain;base64," + notImage},
		{name: "empty base64 image payload", input: "data:image/png;base64,"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExtractFromDataURL(tc.input); got != fallbackColor {
				t.Fatalf("ExtractFromDataURL(%q) = %q, want %q", tc.input, got, fallbackColor)
			}
		})
	}
}

func TestExtractFromDataURL_SolidPNGPreservesDominantChannel(t *testing.T) {
	cases := []struct {
		name     string
		color    color.RGBA
		dominant string
	}{
		{name: "red", color: color.RGBA{R: 255, A: 255}, dominant: "red"},
		{name: "green", color: color.RGBA{G: 255, A: 255}, dominant: "green"},
		{name: "blue", color: color.RGBA{B: 255, A: 255}, dominant: "blue"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractFromDataURL(solidPNGDataURL(t, tc.color))
			if got == fallbackColor {
				t.Fatalf("ExtractFromDataURL returned fallback color %q", got)
			}
			r, g, b := parseHexColor(t, got)
			switch tc.dominant {
			case "red":
				if r <= g || r <= b {
					t.Fatalf("color %q channels r=%d g=%d b=%d, want red dominant", got, r, g, b)
				}
			case "green":
				if g <= r || g <= b {
					t.Fatalf("color %q channels r=%d g=%d b=%d, want green dominant", got, r, g, b)
				}
			case "blue":
				if b <= r || b <= g {
					t.Fatalf("color %q channels r=%d g=%d b=%d, want blue dominant", got, r, g, b)
				}
			}
		})
	}
}

func TestExtractFromDataURL_ClampsBlackAndWhite(t *testing.T) {
	black := ExtractFromDataURL(solidPNGDataURL(t, color.RGBA{A: 255}))
	if black == "#000000" {
		t.Fatal("black image returned #000000, want lifted display color")
	}
	blackR, blackG, blackB := parseHexColor(t, black)
	if blackR == 0 && blackG == 0 && blackB == 0 {
		t.Fatalf("black image channels r=%d g=%d b=%d, want at least one channel above zero", blackR, blackG, blackB)
	}

	white := ExtractFromDataURL(solidPNGDataURL(t, color.RGBA{R: 255, G: 255, B: 255, A: 255}))
	if white == "#FFFFFF" {
		t.Fatal("white image returned #FFFFFF, want clamped display color")
	}
	whiteR, whiteG, whiteB := parseHexColor(t, white)
	if whiteR == 255 && whiteG == 255 && whiteB == 255 {
		t.Fatalf("white image channels r=%d g=%d b=%d, want at least one channel below 255", whiteR, whiteG, whiteB)
	}
}

func TestExtractFromDataURL_TransparentPNGReturnsValidColor(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	got := ExtractFromDataURL(pngDataURL(t, img))
	assertHexColor(t, got)
}
