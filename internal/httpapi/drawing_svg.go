package httpapi

import (
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"

	"scrumboy/internal/store"
)

var svgColorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func svgNum(v float64) string {
	r := math.Round(v*10) / 10
	if r == 0 {
		return "0"
	}
	return strconv.FormatFloat(r, 'f', -1, 64)
}

func svgCenterlinePath(points []store.DrawingPoint) string {
	var b strings.Builder
	switch len(points) {
	case 0:
		return ""
	case 1:
		x, y := points[0][0], points[0][1]
		b.WriteString("M " + svgNum(x) + " " + svgNum(y) + " L " + svgNum(x+0.1) + " " + svgNum(y+0.1))
		return b.String()
	case 2:
		return "M " + svgNum(points[0][0]) + " " + svgNum(points[0][1]) + " L " + svgNum(points[1][0]) + " " + svgNum(points[1][1])
	}
	b.WriteString("M " + svgNum(points[0][0]) + " " + svgNum(points[0][1]))
	n := len(points)
	for i := 0; i < n-1; i++ {
		p0 := points[max(i-1, 0)]
		p1 := points[i]
		p2 := points[i+1]
		p3 := points[min(i+2, n-1)]
		c1x := p1[0] + (p2[0]-p0[0])/6
		c1y := p1[1] + (p2[1]-p0[1])/6
		c2x := p2[0] - (p3[0]-p1[0])/6
		c2y := p2[1] - (p3[1]-p1[1])/6
		b.WriteString(" C " + svgNum(c1x) + " " + svgNum(c1y) + ", " + svgNum(c2x) + " " + svgNum(c2y) + ", " + svgNum(p2[0]) + " " + svgNum(p2[1]))
	}
	return b.String()
}

func pressureFactor(p float64) float64 {
	return 0.4 + 1.2*math.Min(1, math.Max(0, p))
}

func strokeHasPressure(s store.DrawingStroke) bool {
	if s.Tool != "pen" {
		return false
	}
	for _, p := range s.Points {
		if math.Abs(p[2]-0.5) > 0.005 {
			return true
		}
	}
	return false
}

type outlineSample struct{ x, y, r float64 }

func svgOutlinePath(points []store.DrawingPoint, size float64) string {
	half := size / 2
	var samples []outlineSample
	if len(points) <= 2 {
		for _, p := range points {
			samples = append(samples, outlineSample{p[0], p[1], half * pressureFactor(p[2])})
		}
	} else {
		samples = append(samples, outlineSample{points[0][0], points[0][1], half * pressureFactor(points[0][2])})
		n := len(points)
		for i := 0; i < n-1; i++ {
			p0 := points[max(i-1, 0)]
			p1 := points[i]
			p2 := points[i+1]
			p3 := points[min(i+2, n-1)]
			c1x := p1[0] + (p2[0]-p0[0])/6
			c1y := p1[1] + (p2[1]-p0[1])/6
			c2x := p2[0] - (p3[0]-p1[0])/6
			c2y := p2[1] - (p3[1]-p1[1])/6
			for k := 1; k <= 4; k++ {
				t := float64(k) / 4
				u := 1 - t
				x := u*u*u*p1[0] + 3*u*u*t*c1x + 3*u*t*t*c2x + t*t*t*p2[0]
				y := u*u*u*p1[1] + 3*u*u*t*c1y + 3*u*t*t*c2y + t*t*t*p2[1]
				samples = append(samples, outlineSample{x, y, half * pressureFactor(p1[2]+(p2[2]-p1[2])*t)})
			}
		}
	}
	var dedup []outlineSample
	for _, s := range samples {
		if l := len(dedup); l > 0 && math.Hypot(s.x-dedup[l-1].x, s.y-dedup[l-1].y) < 1e-6 {
			dedup[l-1].r = math.Max(dedup[l-1].r, s.r)
			continue
		}
		dedup = append(dedup, s)
	}
	if len(dedup) == 0 {
		return ""
	}
	if len(dedup) == 1 {
		s := dedup[0]
		r := math.Max(s.r, 0.05)
		return "M " + svgNum(s.x-r) + " " + svgNum(s.y) + " A " + svgNum(r) + " " + svgNum(r) + " 0 1 0 " + svgNum(s.x+r) + " " + svgNum(s.y) +
			" A " + svgNum(r) + " " + svgNum(r) + " 0 1 0 " + svgNum(s.x-r) + " " + svgNum(s.y) + " Z"
	}
	left := make([][2]float64, len(dedup))
	right := make([][2]float64, len(dedup))
	for i := range dedup {
		prev := dedup[max(i-1, 0)]
		next := dedup[min(i+1, len(dedup)-1)]
		tx, ty := next.x-prev.x, next.y-prev.y
		l := math.Hypot(tx, ty)
		if l == 0 {
			l = 1
		}
		nx, ny := -ty/l, tx/l
		s := dedup[i]
		left[i] = [2]float64{s.x + nx*s.r, s.y + ny*s.r}
		right[i] = [2]float64{s.x - nx*s.r, s.y - ny*s.r}
	}
	var b strings.Builder
	b.WriteString("M " + svgNum(left[0][0]) + " " + svgNum(left[0][1]))
	for i := 1; i < len(left); i++ {
		b.WriteString(" L " + svgNum(left[i][0]) + " " + svgNum(left[i][1]))
	}
	endR := math.Max(dedup[len(dedup)-1].r, 0.05)
	last := right[len(right)-1]
	b.WriteString(" A " + svgNum(endR) + " " + svgNum(endR) + " 0 0 1 " + svgNum(last[0]) + " " + svgNum(last[1]))
	for i := len(right) - 2; i >= 0; i-- {
		b.WriteString(" L " + svgNum(right[i][0]) + " " + svgNum(right[i][1]))
	}
	startR := math.Max(dedup[0].r, 0.05)
	b.WriteString(" A " + svgNum(startR) + " " + svgNum(startR) + " 0 0 1 " + svgNum(left[0][0]) + " " + svgNum(left[0][1]) + " Z")
	return b.String()
}

func renderDrawingSVG(raw json.RawMessage) ([]byte, error) {
	doc, _, err := store.ParseDrawingDocument(raw)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	w, h := svgNum(doc.Width), svgNum(doc.Height)
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ` + w + ` ` + h + `" width="` + w + `" height="` + h + `">`)
	for _, s := range doc.Strokes {
		if !svgColorRe.MatchString(s.Color) || len(s.Points) == 0 {
			continue
		}
		opacity := strconv.FormatFloat(math.Min(1, math.Max(0, s.Opacity)), 'f', -1, 64)
		if strokeHasPressure(s) {
			b.WriteString(`<path d="` + svgOutlinePath(s.Points, s.Size) + `" fill="` + s.Color + `" stroke="none" opacity="` + opacity + `"/>`)
			continue
		}
		b.WriteString(`<path d="` + svgCenterlinePath(s.Points) + `" fill="none" stroke="` + s.Color + `" stroke-width="` + strconv.FormatFloat(s.Size, 'f', -1, 64) +
			`" stroke-linecap="round" stroke-linejoin="round" opacity="` + opacity + `"/>`)
	}
	b.WriteString(`</svg>`)
	return []byte(b.String()), nil
}
