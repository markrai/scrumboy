package imagecheck

// Limits bounds what Validate accepts.
//
// Memory: a full decode allocates about MaxPixels x 8 bytes in the worst case (16-bit RGBA PNG),
// roughly 200 MB at the default 25 MP. Uploads run at most two decodes at once (see
// internal/application/asset), so peak decode memory stays near 400 MB. APNG non-default frames
// are not decoded here, and GIF frame data is bounded by MaxTotalFramePixels (1 byte per pixel).
type Limits struct {
	MaxBytes            int64
	MaxPixels           int64 // per frame / canvas
	MaxSide             int
	MaxFrames           int   // GIF and APNG
	MaxTotalFramePixels int64 // sum of frame areas, GIF and APNG
}

// DefaultLimits are the limits for story-note images.
var DefaultLimits = Limits{
	MaxBytes:            10 << 20,
	MaxPixels:           25_000_000,
	MaxSide:             16384,
	MaxFrames:           300,
	MaxTotalFramePixels: 100_000_000,
}

// Result describes an accepted image.
type Result struct {
	MIME, Ext string // image/png|image/jpeg|image/gif ; png|jpg|gif (APNG is image/png, .png)
	// Width and Height are the encoded dimensions (JPEG orientation 5-8 displays them swapped).
	Width, Height int
	// Frames is 1 for static images, acTL num_frames for APNG, image descriptors for GIF.
	Frames int
	// Orientation is the JPEG EXIF orientation preserved in Data (1-8); always 1 for PNG/GIF.
	Orientation int
	Animated    bool
	// Data is the bytes to store: the input with metadata removed.
	Data   []byte
	SHA256 string // lowercase hex of Data
}
