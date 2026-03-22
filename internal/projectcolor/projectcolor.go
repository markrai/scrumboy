package projectcolor

import (
	"bytes"
	"encoding/base64"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"strconv"
	"strings"
)

const fallbackColor = "#888888"

func ExtractFromDataURL(dataURL string) string {
	raw := strings.TrimSpace(dataURL)
	if raw == "" {
		return fallbackColor
	}
	payload, ok := splitDataURL(raw)
	if !ok {
		return fallbackColor
	}
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return fallbackColor
	}
	img, _, err := image.Decode(bytes.NewReader(decoded))
	if err != nil {
		return fallbackColor
	}
	return colorToHex(clampColor(computeDominant(img)))
}

func splitDataURL(v string) (string, bool) {
	if !strings.HasPrefix(v, "data:") {
		return "", false
	}
	parts := strings.SplitN(v, ",", 2)
	if len(parts) != 2 {
		return "", false
	}
	meta := parts[0]
	if !strings.Contains(meta, ";base64") {
		return "", false
	}
	return parts[1], true
}

func computeDominant(img image.Image) rgb {
	b := img.Bounds()
	w := b.Dx()
	h := b.Dy()
	if w <= 0 || h <= 0 {
		return rgb{136, 136, 136}
	}
	stepX := maxInt(1, w/48)
	stepY := maxInt(1, h/48)
	var totalW float64
	var sr float64
	var sg float64
	var sb float64
	for y := b.Min.Y; y < b.Max.Y; y += stepY {
		for x := b.Min.X; x < b.Max.X; x += stepX {
			r16, g16, b16, a16 := img.At(x, y).RGBA()
			if a16 == 0 {
				continue
			}
			alpha := float64(a16) / 65535.0
			r := float64(r16>>8) / 255.0
			g := float64(g16>>8) / 255.0
			bl := float64(b16>>8) / 255.0
			maxC := math.Max(r, math.Max(g, bl))
			minC := math.Min(r, math.Min(g, bl))
			sat := maxC - minC
			wgt := alpha * (0.35 + sat)
			sr += r * wgt
			sg += g * wgt
			sb += bl * wgt
			totalW += wgt
		}
	}
	if totalW == 0 {
		return rgb{136, 136, 136}
	}
	return rgb{
		R: toByte(sr / totalW),
		G: toByte(sg / totalW),
		B: toByte(sb / totalW),
	}
}

type rgb struct {
	R uint8
	G uint8
	B uint8
}

func clampColor(in rgb) rgb {
	h, s, l := rgbToHsl(in)
	if l < 0.23 {
		l = 0.23
	}
	if l > 0.78 {
		l = 0.78
	}
	if s < 0.25 {
		s = 0.25
	}
	out := hslToRgb(h, s, l)
	if out.R > 246 && out.G > 246 && out.B > 246 {
		out = hslToRgb(h, math.Max(s, 0.3), 0.78)
	}
	if out.R < 20 && out.G < 20 && out.B < 20 {
		out = hslToRgb(h, math.Max(s, 0.3), 0.25)
	}
	return out
}

func colorToHex(c rgb) string {
	return "#" + strings.ToUpper(hex2(c.R)+hex2(c.G)+hex2(c.B))
}

func hex2(v uint8) string {
	s := strconv.FormatUint(uint64(v), 16)
	if len(s) == 1 {
		return "0" + s
	}
	return s
}

func rgbToHsl(c rgb) (float64, float64, float64) {
	r := float64(c.R) / 255.0
	g := float64(c.G) / 255.0
	b := float64(c.B) / 255.0
	maxC := math.Max(r, math.Max(g, b))
	minC := math.Min(r, math.Min(g, b))
	l := (maxC + minC) / 2
	if maxC == minC {
		return 0, 0, l
	}
	d := maxC - minC
	var s float64
	if l > 0.5 {
		s = d / (2.0 - maxC - minC)
	} else {
		s = d / (maxC + minC)
	}
	var h float64
	switch maxC {
	case r:
		h = (g - b) / d
		if g < b {
			h += 6
		}
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	h /= 6
	return h, s, l
}

func hslToRgb(h, s, l float64) rgb {
	if s == 0 {
		v := toByte(l)
		return rgb{v, v, v}
	}
	var q float64
	if l < 0.5 {
		q = l * (1 + s)
	} else {
		q = l + s - l*s
	}
	p := 2*l - q
	r := hueToRGB(p, q, h+1.0/3.0)
	g := hueToRGB(p, q, h)
	b := hueToRGB(p, q, h-1.0/3.0)
	return rgb{toByte(r), toByte(g), toByte(b)}
}

func hueToRGB(p, q, t float64) float64 {
	if t < 0 {
		t += 1
	}
	if t > 1 {
		t -= 1
	}
	if t < 1.0/6.0 {
		return p + (q-p)*6*t
	}
	if t < 1.0/2.0 {
		return q
	}
	if t < 2.0/3.0 {
		return p + (q-p)*(2.0/3.0-t)*6
	}
	return p
}

func toByte(v float64) uint8 {
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	return uint8(math.Round(v * 255))
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
