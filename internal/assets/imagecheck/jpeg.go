package imagecheck

import (
	"bytes"
	"encoding/binary"
)

const (
	jpegSOI   = 0xD8
	jpegEOI   = 0xD9
	jpegSOS   = 0xDA
	jpegAPP0  = 0xE0
	jpegAPP1  = 0xE1
	jpegAPP13 = 0xED
	jpegCOM   = 0xFE
)

var exifHeader = []byte("Exif\x00\x00")

// sanitizeJPEG walks marker segments up to the first SOS, drops APP1 (EXIF/XMP), APP13 (IPTC)
// and COM, re-synthesizes a minimal EXIF segment holding only a non-default Orientation, copies
// the entropy-coded data verbatim, and rejects data after EOI.
func sanitizeJPEG(data []byte) ([]byte, int, error) {
	if len(data) < 4 || data[0] != 0xFF || data[1] != jpegSOI {
		return nil, 0, ErrCorrupt
	}
	type segment struct {
		marker byte
		raw    []byte
	}
	var kept []segment
	orientation := 1
	exifSeen := false
	pos := 2
	sosAt := -1
	for pos < len(data) {
		if data[pos] != 0xFF {
			return nil, 0, ErrCorrupt
		}
		start := pos
		for pos < len(data) && data[pos] == 0xFF {
			pos++
		}
		if pos >= len(data) {
			return nil, 0, ErrCorrupt
		}
		marker := data[pos]
		pos++
		switch {
		case marker == jpegSOI || marker == jpegEOI || marker == 0x00:
			return nil, 0, ErrCorrupt
		case marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7):
			kept = append(kept, segment{marker, data[start:pos]})
			continue
		}
		if len(data)-pos < 2 {
			return nil, 0, ErrCorrupt
		}
		length := int(binary.BigEndian.Uint16(data[pos : pos+2]))
		if length < 2 || length > len(data)-pos {
			return nil, 0, ErrCorrupt
		}
		payload := data[pos+2 : pos+length]
		end := pos + length
		if marker == jpegSOS {
			sosAt = start
			break
		}
		pos = end
		switch marker {
		case jpegAPP1:
			if !exifSeen && bytes.HasPrefix(payload, exifHeader) {
				exifSeen = true
				orientation = exifOrientation(payload[len(exifHeader):])
			}
			continue
		case jpegAPP13, jpegCOM:
			continue
		}
		kept = append(kept, segment{marker, data[start:end]})
	}
	if sosAt < 0 {
		return nil, 0, ErrCorrupt
	}
	scan := data[sosAt:]
	trimmed := bytes.TrimRight(scan, "\x00")
	if len(trimmed) < 2 || trimmed[len(trimmed)-2] != 0xFF || trimmed[len(trimmed)-1] != jpegEOI {
		return nil, 0, ErrTrailingData
	}

	out := bytes.NewBuffer(make([]byte, 0, len(data)))
	out.Write([]byte{0xFF, jpegSOI})
	inserted := orientation == 1
	for i, seg := range kept {
		if !inserted && !(i == 0 && seg.marker == jpegAPP0) {
			out.Write(orientationSegment(orientation))
			inserted = true
		}
		out.Write(seg.raw)
	}
	if !inserted {
		out.Write(orientationSegment(orientation))
	}
	out.Write(scan)
	return out.Bytes(), orientation, nil
}

// orientationSegment is a complete APP1 segment: Exif header, little-endian TIFF header, IFD0
// with exactly one entry (Orientation, SHORT, count 1) and no next IFD.
func orientationSegment(orientation int) []byte {
	payload := []byte{
		'E', 'x', 'i', 'f', 0, 0,
		'I', 'I', 0x2A, 0x00, 0x08, 0x00, 0x00, 0x00,
		0x01, 0x00,
		0x12, 0x01, 0x03, 0x00, 0x01, 0x00, 0x00, 0x00, byte(orientation), 0x00, 0x00, 0x00,
		0x00, 0x00, 0x00, 0x00,
	}
	seg := []byte{0xFF, jpegAPP1, 0, 0}
	binary.BigEndian.PutUint16(seg[2:], uint16(len(payload)+2))
	return append(seg, payload...)
}

// exifOrientation reads tag 0x0112 from IFD0 of a TIFF structure with bounded reads. Anything
// malformed, absent or out of range yields 1 (never an error).
func exifOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	if order.Uint16(tiff[2:4]) != 42 {
		return 1
	}
	ifd := uint64(order.Uint32(tiff[4:8]))
	if ifd < 8 || ifd+2 > uint64(len(tiff)) {
		return 1
	}
	count := uint64(order.Uint16(tiff[ifd : ifd+2]))
	if ifd+2+count*12 > uint64(len(tiff)) {
		return 1
	}
	for i := uint64(0); i < count; i++ {
		e := tiff[ifd+2+i*12 : ifd+2+(i+1)*12]
		if order.Uint16(e[0:2]) != 0x0112 {
			continue
		}
		if order.Uint16(e[2:4]) != 3 || order.Uint32(e[4:8]) != 1 {
			return 1
		}
		v := int(order.Uint16(e[8:10]))
		if v < 1 || v > 8 {
			return 1
		}
		return v
	}
	return 1
}
