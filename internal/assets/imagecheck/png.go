package imagecheck

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
)

var pngSignature = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// pngDroppedChunks carry metadata (EXIF, text, timestamps) and are removed losslessly.
var pngDroppedChunks = map[string]bool{"eXIf": true, "tEXt": true, "iTXt": true, "zTXt": true, "tIME": true}

type apngState struct {
	hasACTL   bool
	numFrames uint32
	fcTLCount uint32
	nextSeq   uint32
	frameOpen bool // an fcTL is waiting for its frame data
	areaSum   int64
}

// sanitizePNG walks the chunks (CRC-verified), validates APNG structure, drops metadata chunks and
// rejects trailing data. It returns the rebuilt bytes and the frame count.
func sanitizePNG(data []byte, width, height int, lim Limits) ([]byte, int, error) {
	out := bytes.NewBuffer(make([]byte, 0, len(data)))
	out.Write(pngSignature)
	pos := len(pngSignature)
	first := true
	seenIDAT, seenIEND := false, false
	var st apngState
	for pos < len(data) {
		if seenIEND {
			return nil, 0, ErrTrailingData
		}
		if len(data)-pos < 12 {
			return nil, 0, ErrCorrupt
		}
		length := binary.BigEndian.Uint32(data[pos : pos+4])
		if uint64(length) > uint64(len(data)-pos-12) {
			return nil, 0, ErrCorrupt
		}
		typ := string(data[pos+4 : pos+8])
		body := data[pos+8 : pos+8+int(length)]
		crc := binary.BigEndian.Uint32(data[pos+8+int(length) : pos+12+int(length)])
		if crc32.ChecksumIEEE(data[pos+4:pos+8+int(length)]) != crc {
			return nil, 0, ErrCorrupt
		}
		chunk := data[pos : pos+12+int(length)]
		pos += 12 + int(length)

		if first {
			if typ != "IHDR" || length != 13 {
				return nil, 0, ErrCorrupt
			}
			first = false
		}
		switch typ {
		case "IDAT":
			if st.frameOpen {
				// IDAT carries the default image; it completes an fcTL that preceded the first IDAT.
				st.frameOpen = false
			}
			seenIDAT = true
		case "IEND":
			seenIEND = true
		case "acTL":
			if st.hasACTL || seenIDAT || length != 8 {
				return nil, 0, ErrInvalidAnimation
			}
			st.hasACTL = true
			st.numFrames = binary.BigEndian.Uint32(body[0:4])
			if st.numFrames == 0 {
				return nil, 0, ErrInvalidAnimation
			}
			if lim.MaxFrames > 0 && st.numFrames > uint32(lim.MaxFrames) {
				return nil, 0, ErrTooManyFrames
			}
		case "fcTL":
			if err := st.fcTL(body, seenIDAT, width, height, lim); err != nil {
				return nil, 0, err
			}
		case "fdAT":
			if !st.hasACTL || st.fcTLCount == 0 || !seenIDAT || length < 4 {
				return nil, 0, ErrInvalidAnimation
			}
			if binary.BigEndian.Uint32(body[0:4]) != st.nextSeq {
				return nil, 0, ErrInvalidAnimation
			}
			st.nextSeq++
			st.frameOpen = false
		}
		if pngDroppedChunks[typ] {
			continue
		}
		out.Write(chunk)
	}
	if !seenIEND || !seenIDAT {
		return nil, 0, ErrCorrupt
	}
	if !st.hasACTL {
		return out.Bytes(), 1, nil
	}
	if st.frameOpen || st.fcTLCount != st.numFrames {
		return nil, 0, ErrInvalidAnimation
	}
	return out.Bytes(), int(st.numFrames), nil
}

func (st *apngState) fcTL(body []byte, seenIDAT bool, width, height int, lim Limits) error {
	if !st.hasACTL || len(body) != 26 || st.frameOpen {
		return ErrInvalidAnimation
	}
	if binary.BigEndian.Uint32(body[0:4]) != st.nextSeq {
		return ErrInvalidAnimation
	}
	st.nextSeq++
	w := binary.BigEndian.Uint32(body[4:8])
	h := binary.BigEndian.Uint32(body[8:12])
	x := binary.BigEndian.Uint32(body[12:16])
	y := binary.BigEndian.Uint32(body[16:20])
	dispose, blend := body[24], body[25]
	if w == 0 || h == 0 || dispose > 2 || blend > 1 {
		return ErrInvalidAnimation
	}
	if uint64(x)+uint64(w) > uint64(width) || uint64(y)+uint64(h) > uint64(height) {
		return ErrInvalidAnimation
	}
	if !seenIDAT {
		// The default image is the first frame: it must cover the whole canvas.
		if st.fcTLCount != 0 || x != 0 || y != 0 || int(w) != width || int(h) != height {
			return ErrInvalidAnimation
		}
	}
	st.fcTLCount++
	if st.fcTLCount > st.numFrames {
		return ErrInvalidAnimation
	}
	st.areaSum += int64(w) * int64(h)
	if lim.MaxTotalFramePixels > 0 && st.areaSum > lim.MaxTotalFramePixels {
		return ErrTooManyFrames
	}
	st.frameOpen = true
	return nil
}
