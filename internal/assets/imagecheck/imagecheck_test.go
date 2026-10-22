package imagecheck

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/color/palette"
	"image/gif"
	"image/jpeg"
	"image/png"
	"runtime"
	"testing"
)

// ---- fixture builders ----

func gradient(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(x * 7), uint8(y * 11), uint8(x + y), 200})
		}
	}
	return img
}

func encodePNG(t testing.TB, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func encodeJPEG(t testing.TB, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func pngChunk(typ string, data []byte) []byte {
	out := make([]byte, 8, 12+len(data))
	binary.BigEndian.PutUint32(out[0:4], uint32(len(data)))
	copy(out[4:8], typ)
	out = append(out, data...)
	crc := crc32.ChecksumIEEE(out[4:])
	return binary.BigEndian.AppendUint32(out, crc)
}

type rawChunk struct {
	typ  string
	data []byte
}

func splitPNG(t testing.TB, b []byte) []rawChunk {
	t.Helper()
	var out []rawChunk
	pos := 8
	for pos < len(b) {
		n := int(binary.BigEndian.Uint32(b[pos:]))
		out = append(out, rawChunk{string(b[pos+4 : pos+8]), b[pos+8 : pos+8+n]})
		pos += 12 + n
	}
	return out
}

func joinPNG(chunks []rawChunk) []byte {
	out := append([]byte(nil), pngSignature...)
	for _, c := range chunks {
		out = append(out, pngChunk(c.typ, c.data)...)
	}
	return out
}

// insertBeforeIEND adds chunks just before IEND.
func insertBeforeIEND(t testing.TB, b []byte, extra ...rawChunk) []byte {
	chunks := splitPNG(t, b)
	last := chunks[len(chunks)-1]
	chunks = append(append(chunks[:len(chunks)-1:len(chunks)-1], extra...), last)
	return joinPNG(chunks)
}

func idatData(t testing.TB, b []byte) []byte {
	var out []byte
	for _, c := range splitPNG(t, b) {
		if c.typ == "IDAT" {
			out = append(out, c.data...)
		}
	}
	return out
}

func fcTLData(seq uint32, w, h, x, y uint32, dispose, blend byte) []byte {
	d := make([]byte, 26)
	binary.BigEndian.PutUint32(d[0:], seq)
	binary.BigEndian.PutUint32(d[4:], w)
	binary.BigEndian.PutUint32(d[8:], h)
	binary.BigEndian.PutUint32(d[12:], x)
	binary.BigEndian.PutUint32(d[16:], y)
	binary.BigEndian.PutUint16(d[20:], 1)
	binary.BigEndian.PutUint16(d[22:], 10)
	d[24], d[25] = dispose, blend
	return d
}

func acTLData(n uint32) []byte {
	d := make([]byte, 8)
	binary.BigEndian.PutUint32(d, n)
	return d
}

func fdATData(seq uint32, frame []byte) []byte {
	return append(binary.BigEndian.AppendUint32(nil, seq), frame...)
}

// apngChunks builds a valid 3-frame 8x8 APNG whose default image is frame 0.
func apngChunks(t testing.TB) []rawChunk {
	t.Helper()
	frames := make([][]byte, 3)
	var ihdr []byte
	for i := range frames {
		enc := encodePNG(t, gradientShift(8, 8, i))
		frames[i] = idatData(t, enc)
		ihdr = splitPNG(t, enc)[0].data
	}
	return []rawChunk{
		{"IHDR", ihdr},
		{"acTL", acTLData(3)},
		{"fcTL", fcTLData(0, 8, 8, 0, 0, 0, 0)},
		{"IDAT", frames[0]},
		{"fcTL", fcTLData(1, 8, 8, 0, 0, 1, 0)},
		{"fdAT", fdATData(2, frames[1])},
		{"fcTL", fcTLData(3, 8, 8, 0, 0, 0, 1)},
		{"fdAT", fdATData(4, frames[2])},
		{"IEND", nil},
	}
}

func gradientShift(w, h, shift int) *image.RGBA {
	img := gradient(w, h)
	for i := range img.Pix {
		img.Pix[i] += uint8(shift * 40)
	}
	return img
}

// exifTIFF builds a TIFF block (little or big endian) with Orientation plus noise tags.
func exifTIFF(order binary.ByteOrder, orientation uint16, withGPS bool) []byte {
	type entry struct {
		tag, typ uint16
		count    uint32
		value    uint32
	}
	entries := []entry{{0x010F, 2, 4, 0x6b6e4d41}, {0x0112, 3, 1, uint32(orientation)}}
	if withGPS {
		entries = append(entries, entry{0x8825, 4, 1, 0})
	}
	var b bytes.Buffer
	if order == binary.LittleEndian {
		b.WriteString("II")
	} else {
		b.WriteString("MM")
	}
	_ = binary.Write(&b, order, uint16(42))
	_ = binary.Write(&b, order, uint32(8))
	_ = binary.Write(&b, order, uint16(len(entries)))
	for _, e := range entries {
		_ = binary.Write(&b, order, e.tag)
		_ = binary.Write(&b, order, e.typ)
		_ = binary.Write(&b, order, e.count)
		if e.typ == 3 {
			_ = binary.Write(&b, order, uint16(e.value))
			_ = binary.Write(&b, order, uint16(0))
		} else {
			_ = binary.Write(&b, order, e.value)
		}
	}
	_ = binary.Write(&b, order, uint32(0))
	b.WriteString("GPSLatitude=51.5N;SECRET-LOCATION")
	return b.Bytes()
}

func jpegSegment(marker byte, payload []byte) []byte {
	seg := []byte{0xFF, marker, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	return append(seg, payload...)
}

// insertAfterSOI places segments right after SOI (before APP0, as many cameras do).
func insertAfterSOI(j []byte, segs ...[]byte) []byte {
	out := append([]byte(nil), j[:2]...)
	for _, s := range segs {
		out = append(out, s...)
	}
	return append(out, j[2:]...)
}

func appSegments(t testing.TB, b []byte, marker byte) [][]byte {
	t.Helper()
	var out [][]byte
	pos := 2
	for pos+4 <= len(b) && b[pos] == 0xFF && b[pos+1] != jpegSOS {
		n := int(binary.BigEndian.Uint16(b[pos+2:]))
		if b[pos+1] == marker {
			out = append(out, b[pos:pos+2+n])
		}
		pos += 2 + n
	}
	return out
}

func samePixels(t testing.TB, a, b []byte) {
	t.Helper()
	ia, _, err := image.Decode(bytes.NewReader(a))
	if err != nil {
		t.Fatal(err)
	}
	ib, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if ia.Bounds() != ib.Bounds() {
		t.Fatal("bounds differ")
	}
	for y := ia.Bounds().Min.Y; y < ia.Bounds().Max.Y; y++ {
		for x := ia.Bounds().Min.X; x < ia.Bounds().Max.X; x++ {
			if ia.At(x, y) != ib.At(x, y) {
				t.Fatalf("pixel (%d,%d) differs", x, y)
			}
		}
	}
}

func mustValidate(t *testing.T, b []byte) Result {
	t.Helper()
	r, err := Validate(b, DefaultLimits)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	again, err := Validate(r.Data, DefaultLimits)
	if err != nil || !bytes.Equal(again.Data, r.Data) || again.SHA256 != r.SHA256 {
		t.Fatalf("not idempotent: %v", err)
	}
	return r
}

// ---- tests ----

func TestAcceptPNGVariants(t *testing.T) {
	rgba64 := image.NewRGBA64(image.Rect(0, 0, 5, 4))
	for i := range rgba64.Pix {
		rgba64.Pix[i] = uint8(i * 13)
	}
	pal := image.NewPaletted(image.Rect(0, 0, 6, 6), color.Palette{color.Transparent, color.RGBA{255, 0, 0, 255}})
	pal.SetColorIndex(2, 2, 1)
	gray := image.NewGray(image.Rect(0, 0, 3, 3))
	for name, img := range map[string]image.Image{"rgba": gradient(9, 7), "rgba64": rgba64, "paletted": pal, "gray": gray} {
		t.Run(name, func(t *testing.T) {
			in := encodePNG(t, img)
			r := mustValidate(t, in)
			if r.MIME != "image/png" || r.Ext != "png" || r.Frames != 1 || r.Animated || !bytes.Equal(r.Data, in) {
				t.Fatalf("result %+v", r)
			}
			samePixels(t, in, r.Data)
		})
	}
}

func TestPNGMetadataStripped(t *testing.T) {
	base := encodePNG(t, gradient(4, 4))
	in := insertBeforeIEND(t, base,
		rawChunk{"tEXt", []byte("Comment\x00secret")},
		rawChunk{"eXIf", exifTIFF(binary.LittleEndian, 1, true)},
		rawChunk{"tIME", []byte{7, 0xE8, 1, 1, 0, 0, 0}},
		rawChunk{"prVt", []byte("private ancillary kept")})
	// iCCP must survive; put it after IHDR.
	chunks := splitPNG(t, in)
	chunks = append([]rawChunk{chunks[0], {"iCCP", []byte("sRGB\x00\x00" + "\x78\x9c\x03\x00\x00\x00\x00\x01")}}, chunks[1:]...)
	in = joinPNG(chunks)
	r := mustValidate(t, in)
	for _, c := range splitPNG(t, r.Data) {
		if pngDroppedChunks[c.typ] {
			t.Fatalf("%s survived", c.typ)
		}
	}
	if !bytes.Contains(r.Data, []byte("iCCP")) || !bytes.Contains(r.Data, []byte("private ancillary kept")) || bytes.Contains(r.Data, []byte("SECRET")) {
		t.Fatal("wrong chunks kept")
	}
	samePixels(t, base, r.Data)
}

func TestAcceptJPEGAndStripMetadata(t *testing.T) {
	base := encodeJPEG(t, gradient(16, 16))
	in := insertAfterSOI(base,
		jpegSegment(jpegAPP1, append([]byte("Exif\x00\x00"), exifTIFF(binary.LittleEndian, 1, true)...)),
		jpegSegment(jpegAPP1, []byte("http://ns.adobe.com/xap/1.0/\x00<x:xmpmeta>SECRET</x:xmpmeta>")),
		jpegSegment(jpegAPP13, []byte("Photoshop 3.0\x00SECRET")),
		jpegSegment(jpegCOM, []byte("SECRET comment")),
		jpegSegment(0xE2, []byte("ICC_PROFILE\x00\x01\x01fakeprofile")))
	r := mustValidate(t, in)
	if r.MIME != "image/jpeg" || r.Ext != "jpg" || r.Orientation != 1 {
		t.Fatalf("result %+v", r)
	}
	if bytes.Contains(r.Data, []byte("SECRET")) || len(appSegments(t, r.Data, jpegAPP1)) != 0 {
		t.Fatal("metadata survived")
	}
	if !bytes.Contains(r.Data, []byte("ICC_PROFILE")) {
		t.Fatal("ICC profile dropped")
	}
	samePixels(t, base, r.Data)
}

func TestJPEGOrientationPreserved(t *testing.T) {
	base := encodeJPEG(t, gradient(16, 8))
	for _, order := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		for o := uint16(2); o <= 8; o++ {
			in := insertAfterSOI(base, jpegSegment(jpegAPP1, append([]byte("Exif\x00\x00"), exifTIFF(order, o, true)...)))
			r := mustValidate(t, in)
			if r.Orientation != int(o) {
				t.Fatalf("orientation %d -> %d", o, r.Orientation)
			}
			segs := appSegments(t, r.Data, jpegAPP1)
			if len(segs) != 1 || !bytes.Equal(segs[0], orientationSegment(int(o))) {
				t.Fatalf("orientation %d: APP1 segments %x", o, segs)
			}
			if bytes.Contains(r.Data, []byte("SECRET")) || bytes.Contains(r.Data, []byte{0x25, 0x88}) {
				t.Fatal("GPS data survived")
			}
			if len(orientationSegment(int(o))) != 36 || binary.BigEndian.Uint16(orientationSegment(int(o))[2:]) != 34 {
				t.Fatal("synthesized segment length")
			}
			samePixels(t, base, r.Data)
			if r.Width != 16 || r.Height != 8 {
				t.Fatal("dimensions must be the encoded ones")
			}
		}
	}
	// The synthesized segment goes after APP0 (JFIF), keeping JFIF first.
	if base[2] != 0xFF || base[3] != jpegAPP0 {
		t.Skip("encoder emitted no APP0")
	}
}

func TestJPEGOrientationOneOrMalformedDropsAPP1(t *testing.T) {
	base := encodeJPEG(t, gradient(8, 8))
	truncated := exifTIFF(binary.LittleEndian, 6, false)[:12]
	loop := exifTIFF(binary.LittleEndian, 6, false)
	binary.LittleEndian.PutUint32(loop[4:], 0xFFFFFF00) // IFD offset out of range
	overflow := exifTIFF(binary.LittleEndian, 6, false)
	binary.LittleEndian.PutUint16(overflow[8:], 0xFFFF) // entry count beyond data
	badType := exifTIFF(binary.LittleEndian, 6, false)
	binary.LittleEndian.PutUint16(badType[8+2+12+2:], 4) // orientation entry type LONG
	for name, tiff := range map[string][]byte{
		"orientation 1":  exifTIFF(binary.LittleEndian, 1, false),
		"out of range":   exifTIFF(binary.LittleEndian, 9, false),
		"truncated":      truncated,
		"bad offset":     loop,
		"count overflow": overflow,
		"bad type":       badType,
		"bad header":     []byte("XX*\x00\x08\x00\x00\x00"),
	} {
		t.Run(name, func(t *testing.T) {
			in := insertAfterSOI(base, jpegSegment(jpegAPP1, append([]byte("Exif\x00\x00"), tiff...)))
			r := mustValidate(t, in)
			if r.Orientation != 1 || len(appSegments(t, r.Data, jpegAPP1)) != 0 {
				t.Fatalf("orientation %d, APP1s %d", r.Orientation, len(appSegments(t, r.Data, jpegAPP1)))
			}
		})
	}
}

func TestAcceptGIFStaticAndAnimated(t *testing.T) {
	frames := make([]*image.Paletted, 3)
	for i := range frames {
		frames[i] = image.NewPaletted(image.Rect(0, 0, 10, 10), palette.Plan9)
		frames[i].SetColorIndex(i, i, uint8(i+1))
	}
	var anim bytes.Buffer
	if err := gif.EncodeAll(&anim, &gif.GIF{Image: frames, Delay: []int{5, 5, 5}}); err != nil {
		t.Fatal(err)
	}
	r := mustValidate(t, anim.Bytes())
	if r.MIME != "image/gif" || r.Frames != 3 || !r.Animated || !bytes.Equal(r.Data, anim.Bytes()) {
		t.Fatalf("animated result %+v", r)
	}
	var static bytes.Buffer
	if err := gif.Encode(&static, frames[0], nil); err != nil {
		t.Fatal(err)
	}
	if r := mustValidate(t, static.Bytes()); r.Frames != 1 || r.Animated {
		t.Fatalf("static result %+v", r)
	}
}

func TestAcceptAPNG(t *testing.T) {
	in := joinPNG(apngChunks(t))
	r := mustValidate(t, in)
	if r.Frames != 3 || !r.Animated || !bytes.Equal(r.Data, in) {
		t.Fatalf("APNG result frames=%d animated=%v identical=%v", r.Frames, r.Animated, bytes.Equal(r.Data, in))
	}
	// Default image outside the animation: acTL, IDAT, then fcTL+fdAT frames only.
	c := apngChunks(t)
	alt := []rawChunk{c[0], {"acTL", acTLData(2)}, c[3],
		{"fcTL", fcTLData(0, 8, 8, 0, 0, 0, 0)}, {"fdAT", fdATData(1, c[5].data[4:])},
		{"fcTL", fcTLData(2, 4, 4, 2, 2, 0, 0)}, {"fdAT", fdATData(3, c[7].data[4:])}, c[8]}
	if r := mustValidate(t, joinPNG(alt)); r.Frames != 2 {
		t.Fatalf("separate default image frames = %d", r.Frames)
	}
}

func TestRejectInvalidAPNG(t *testing.T) {
	mutate := func(fn func([]rawChunk) []rawChunk) []byte { return joinPNG(fn(apngChunks(t))) }
	cases := map[string][]byte{
		"acTL after IDAT": mutate(func(c []rawChunk) []rawChunk {
			return []rawChunk{c[0], c[2], c[3], c[1], c[4], c[5], c[6], c[7], c[8]}
		}),
		"duplicate acTL": mutate(func(c []rawChunk) []rawChunk {
			return append([]rawChunk{c[0], c[1], c[1]}, c[2:]...)
		}),
		"fdAT before first fcTL": mutate(func(c []rawChunk) []rawChunk {
			return []rawChunk{c[0], c[1], c[3], {"fdAT", fdATData(0, c[3].data)}, c[8]}
		}),
		"sequence gap": mutate(func(c []rawChunk) []rawChunk {
			c[5] = rawChunk{"fdAT", fdATData(3, c[5].data[4:])}
			return c
		}),
		"duplicate sequence": mutate(func(c []rawChunk) []rawChunk {
			c[6] = rawChunk{"fcTL", fcTLData(2, 8, 8, 0, 0, 0, 0)}
			return c
		}),
		"fcTL count mismatch": mutate(func(c []rawChunk) []rawChunk {
			c[1] = rawChunk{"acTL", acTLData(4)}
			return c
		}),
		"frame exceeds canvas": mutate(func(c []rawChunk) []rawChunk {
			c[4] = rawChunk{"fcTL", fcTLData(1, 8, 8, 4, 0, 0, 0)}
			return c
		}),
		"pre-IDAT fcTL not full canvas": mutate(func(c []rawChunk) []rawChunk {
			c[2] = rawChunk{"fcTL", fcTLData(0, 4, 4, 0, 0, 0, 0)}
			return c
		}),
		"dispose_op 3": mutate(func(c []rawChunk) []rawChunk {
			c[4] = rawChunk{"fcTL", fcTLData(1, 8, 8, 0, 0, 3, 0)}
			return c
		}),
		"fcTL without acTL": mutate(func(c []rawChunk) []rawChunk {
			return append([]rawChunk{c[0]}, c[2:]...)
		}),
		"fcTL without frame data": mutate(func(c []rawChunk) []rawChunk {
			return []rawChunk{c[0], {"acTL", acTLData(2)}, c[2], c[3], c[4], c[8]}
		}),
	}
	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Validate(b, DefaultLimits); !errors.Is(err, ErrInvalidAnimation) {
				t.Fatalf("err = %v", err)
			}
		})
	}
	tooMany := mutate(func(c []rawChunk) []rawChunk {
		c[1] = rawChunk{"acTL", acTLData(301)}
		return c
	})
	if _, err := Validate(tooMany, DefaultLimits); !errors.Is(err, ErrTooManyFrames) {
		t.Fatalf("301 frames: %v", err)
	}
	lim := DefaultLimits
	lim.MaxTotalFramePixels = 150
	if _, err := Validate(joinPNG(apngChunks(t)), lim); !errors.Is(err, ErrTooManyFrames) {
		t.Fatalf("frame area budget: %v", err)
	}
}

func TestRejectFormatsAndCorruption(t *testing.T) {
	png1 := encodePNG(t, gradient(4, 4))
	jpg := encodeJPEG(t, gradient(8, 8))
	var g bytes.Buffer
	_ = gif.Encode(&g, image.NewPaletted(image.Rect(0, 0, 2, 2), palette.Plan9), nil)
	badCRC := append([]byte(nil), png1...)
	badCRC[30] ^= 0xFF
	cases := map[string]struct {
		in   []byte
		want error
	}{
		"svg":              {[]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`), ErrUnsupportedFormat},
		"html":             {[]byte("<html><body>x</body></html>"), ErrUnsupportedFormat},
		"webp":             {[]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), ErrUnsupportedFormat},
		"bmp":              {[]byte("BM\x00\x00\x00\x00\x00\x00"), ErrUnsupportedFormat},
		"empty":            {nil, ErrCorrupt},
		"one byte":         {[]byte{0x89}, ErrUnsupportedFormat},
		"png missing IEND": {png1[:len(png1)-12], ErrCorrupt},
		"png bad crc":      {badCRC, ErrCorrupt},
		"png + html":       {append(append([]byte(nil), png1...), []byte("<html>")...), ErrTrailingData},
		"jpeg + zip":       {append(append([]byte(nil), jpg...), []byte("PK\x03\x04trailer")...), ErrTrailingData},
		"gif + bytes":      {append(append([]byte(nil), g.Bytes()...), 'x'), ErrTrailingData},
		"truncated jpeg":   {jpg[:len(jpg)/2], ErrCorrupt},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Validate(c.in, DefaultLimits)
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}
}

func TestJPEGTrailingZeroPaddingAccepted(t *testing.T) {
	jpg := encodeJPEG(t, gradient(8, 8))
	mustValidate(t, append(append([]byte(nil), jpg...), 0, 0, 0))
}

func TestRejectHugeDimensionsWithoutAllocating(t *testing.T) {
	chunks := splitPNG(t, encodePNG(t, gradient(2, 2)))
	ihdr := append([]byte(nil), chunks[0].data...)
	binary.BigEndian.PutUint32(ihdr[0:], 50000)
	binary.BigEndian.PutUint32(ihdr[4:], 50000)
	chunks[0] = rawChunk{"IHDR", ihdr}
	bomb := joinPNG(chunks)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err := Validate(bomb, DefaultLimits)
	runtime.ReadMemStats(&after)
	if !errors.Is(err, ErrDimensions) {
		t.Fatalf("err = %v", err)
	}
	if after.TotalAlloc-before.TotalAlloc > 10<<20 {
		t.Fatalf("allocated %d bytes rejecting a header-only bomb", after.TotalAlloc-before.TotalAlloc)
	}
}

func TestRejectGIFFrameBombs(t *testing.T) {
	frames := make([]*image.Paletted, 301)
	delays := make([]int, len(frames))
	for i := range frames {
		frames[i] = image.NewPaletted(image.Rect(0, 0, 1, 1), palette.Plan9)
	}
	var b bytes.Buffer
	if err := gif.EncodeAll(&b, &gif.GIF{Image: frames, Delay: delays}); err != nil {
		t.Fatal(err)
	}
	if _, err := Validate(b.Bytes(), DefaultLimits); !errors.Is(err, ErrTooManyFrames) {
		t.Fatalf("301 frames: %v", err)
	}
	lim := DefaultLimits
	lim.MaxTotalFramePixels = 100
	big := []*image.Paletted{image.NewPaletted(image.Rect(0, 0, 10, 10), palette.Plan9), image.NewPaletted(image.Rect(0, 0, 10, 10), palette.Plan9)}
	b.Reset()
	_ = gif.EncodeAll(&b, &gif.GIF{Image: big, Delay: []int{1, 1}})
	if _, err := Validate(b.Bytes(), lim); !errors.Is(err, ErrTooManyFrames) {
		t.Fatalf("frame area budget: %v", err)
	}
}

func TestSizeLimit(t *testing.T) {
	in := encodePNG(t, gradient(4, 4))
	lim := DefaultLimits
	lim.MaxBytes = int64(len(in))
	if _, err := Validate(in, lim); err != nil {
		t.Fatalf("exactly MaxBytes: %v", err)
	}
	lim.MaxBytes--
	if _, err := Validate(in, lim); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("MaxBytes+1: %v", err)
	}
}

func FuzzValidate(f *testing.F) {
	f.Add(encodePNG(f, gradient(3, 3)))
	f.Add(encodeJPEG(f, gradient(8, 8)))
	f.Add(joinPNG(apngChunks(f)))
	f.Add(insertAfterSOI(encodeJPEG(f, gradient(8, 8)), jpegSegment(jpegAPP1, append([]byte("Exif\x00\x00"), exifTIFF(binary.BigEndian, 6, true)...))))
	var g bytes.Buffer
	_ = gif.Encode(&g, image.NewPaletted(image.Rect(0, 0, 2, 2), palette.Plan9), nil)
	f.Add(g.Bytes())
	f.Add([]byte("<svg/>"))
	lim := DefaultLimits
	lim.MaxPixels = 1 << 16
	f.Fuzz(func(t *testing.T, data []byte) {
		r, err := Validate(data, lim)
		if err != nil {
			return
		}
		again, err := Validate(r.Data, lim)
		if err != nil || !bytes.Equal(again.Data, r.Data) {
			t.Fatalf("accepted output not idempotent: %v", err)
		}
		if _, _, err := image.Decode(bytes.NewReader(r.Data)); err != nil {
			t.Fatalf("accepted output does not decode: %v", err)
		}
	})
}

func FuzzExifOrientation(f *testing.F) {
	f.Add(exifTIFF(binary.LittleEndian, 6, true))
	f.Add(exifTIFF(binary.BigEndian, 3, false))
	f.Add([]byte("II*\x00"))
	f.Fuzz(func(t *testing.T, tiff []byte) {
		if o := exifOrientation(tiff); o < 1 || o > 8 {
			t.Fatalf("orientation %d", o)
		}
	})
}
