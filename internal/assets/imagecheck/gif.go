package imagecheck

import "encoding/binary"

// scanGIF walks GIF blocks without decoding pixels, counting frames and summing frame areas so
// that gif.DecodeAll only runs on inputs within budget. Data after the trailer is rejected.
func scanGIF(data []byte, lim Limits) (int, error) {
	if len(data) < 13 {
		return 0, ErrCorrupt
	}
	screenW := int(binary.LittleEndian.Uint16(data[6:8]))
	screenH := int(binary.LittleEndian.Uint16(data[8:10]))
	packed := data[10]
	pos := 13
	if packed&0x80 != 0 {
		pos += 3 << ((packed & 7) + 1)
	}
	frames := 0
	var area int64
	skipSubBlocks := func() error {
		for {
			if pos >= len(data) {
				return ErrCorrupt
			}
			n := int(data[pos])
			pos++
			if n == 0 {
				return nil
			}
			if n > len(data)-pos {
				return ErrCorrupt
			}
			pos += n
		}
	}
	for {
		if pos >= len(data) {
			return 0, ErrCorrupt
		}
		switch data[pos] {
		case 0x21: // extension
			if pos+2 > len(data) {
				return 0, ErrCorrupt
			}
			pos += 2
			if err := skipSubBlocks(); err != nil {
				return 0, err
			}
		case 0x2C: // image descriptor
			if pos+10 > len(data) {
				return 0, ErrCorrupt
			}
			left := int(binary.LittleEndian.Uint16(data[pos+1 : pos+3]))
			top := int(binary.LittleEndian.Uint16(data[pos+3 : pos+5]))
			w := int(binary.LittleEndian.Uint16(data[pos+5 : pos+7]))
			h := int(binary.LittleEndian.Uint16(data[pos+7 : pos+9]))
			flags := data[pos+9]
			pos += 10
			if w == 0 || h == 0 || left+w > screenW || top+h > screenH {
				return 0, ErrCorrupt
			}
			if flags&0x80 != 0 {
				pos += 3 << ((flags & 7) + 1)
			}
			if pos >= len(data) {
				return 0, ErrCorrupt
			}
			if lzw := data[pos]; lzw < 2 || lzw > 8 {
				return 0, ErrCorrupt
			}
			pos++
			if err := skipSubBlocks(); err != nil {
				return 0, err
			}
			frames++
			area += int64(w) * int64(h)
			if (lim.MaxFrames > 0 && frames > lim.MaxFrames) || (lim.MaxTotalFramePixels > 0 && area > lim.MaxTotalFramePixels) {
				return 0, ErrTooManyFrames
			}
		case 0x3B: // trailer
			if pos+1 != len(data) {
				return 0, ErrTrailingData
			}
			if frames == 0 {
				return 0, ErrCorrupt
			}
			return frames, nil
		default:
			return 0, ErrCorrupt
		}
	}
}
