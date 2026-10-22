// Package imagecheck decides whether bytes are an acceptable PNG (including APNG), JPEG or GIF
// for story-note images and returns the exact bytes to store.
//
// Pixels are never re-encoded. Privacy metadata is removed losslessly: PNG eXIf/tEXt/iTXt/zTXt/
// tIME chunks and JPEG APP1 (EXIF/XMP), APP13 (IPTC) and COM segments are dropped. A JPEG EXIF
// Orientation of 2-8 is preserved by synthesizing a minimal EXIF segment that contains only that
// tag, so photos keep their displayed orientation. Width and Height are the encoded dimensions;
// orientations 5-8 display them swapped.
//
// APNG animation is preserved byte for byte after structural validation (acTL/fcTL/fdAT order,
// sequence numbers, frame bounds and budgets). Only the default image is pixel-decoded here;
// other APNG frames are decoded solely by the viewer's browser.
//
// Validate is pure: no I/O, no globals, safe for concurrent use. Callers bound concurrency.
package imagecheck

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
)

var (
	ErrTooLarge          = errors.New("imagecheck: image exceeds size limit")
	ErrUnsupportedFormat = errors.New("imagecheck: unsupported image type")
	ErrDimensions        = errors.New("imagecheck: image dimensions out of range")
	ErrCorrupt           = errors.New("imagecheck: invalid image data")
	ErrTrailingData      = errors.New("imagecheck: unexpected data after image end")
	ErrTooManyFrames     = errors.New("imagecheck: animation exceeds frame limits")
	ErrInvalidAnimation  = errors.New("imagecheck: invalid animation structure")
)

// Validate checks data and returns the bytes to store with their metadata.
func Validate(data []byte, lim Limits) (Result, error) {
	if len(data) == 0 {
		return Result{}, ErrCorrupt
	}
	if lim.MaxBytes > 0 && int64(len(data)) > lim.MaxBytes {
		return Result{}, ErrTooLarge
	}
	var (
		res Result
		err error
	)
	switch {
	case bytes.HasPrefix(data, pngSignature):
		res, err = validatePNG(data, lim)
	case len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF:
		res, err = validateJPEG(data, lim)
	case bytes.HasPrefix(data, []byte("GIF87a")) || bytes.HasPrefix(data, []byte("GIF89a")):
		res, err = validateGIF(data, lim)
	default:
		return Result{}, ErrUnsupportedFormat
	}
	if err != nil {
		return Result{}, err
	}
	sum := sha256.Sum256(res.Data)
	res.SHA256 = hex.EncodeToString(sum[:])
	return res, nil
}

func checkDims(w, h int, lim Limits) error {
	if w <= 0 || h <= 0 {
		return ErrDimensions
	}
	if lim.MaxSide > 0 && (w > lim.MaxSide || h > lim.MaxSide) {
		return ErrDimensions
	}
	if lim.MaxPixels > 0 && int64(w)*int64(h) > lim.MaxPixels {
		return ErrDimensions
	}
	return nil
}

func decodeConfig(data []byte, fn func(*bytes.Reader) (image.Config, error), lim Limits) (image.Config, error) {
	cfg, err := fn(bytes.NewReader(data))
	if err != nil {
		return image.Config{}, ErrCorrupt
	}
	if err := checkDims(cfg.Width, cfg.Height, lim); err != nil {
		return image.Config{}, err
	}
	return cfg, nil
}

func validatePNG(data []byte, lim Limits) (Result, error) {
	cfg, err := decodeConfig(data, func(r *bytes.Reader) (image.Config, error) { return png.DecodeConfig(r) }, lim)
	if err != nil {
		return Result{}, err
	}
	out, frames, err := sanitizePNG(data, cfg.Width, cfg.Height, lim)
	if err != nil {
		return Result{}, err
	}
	if _, err := png.Decode(bytes.NewReader(out)); err != nil {
		return Result{}, ErrCorrupt
	}
	return Result{MIME: "image/png", Ext: "png", Width: cfg.Width, Height: cfg.Height, Frames: frames, Orientation: 1, Animated: frames > 1, Data: out}, nil
}

func validateJPEG(data []byte, lim Limits) (Result, error) {
	cfg, err := decodeConfig(data, func(r *bytes.Reader) (image.Config, error) { return jpeg.DecodeConfig(r) }, lim)
	if err != nil {
		return Result{}, err
	}
	out, orientation, err := sanitizeJPEG(data)
	if err != nil {
		return Result{}, err
	}
	if _, err := jpeg.Decode(bytes.NewReader(out)); err != nil {
		return Result{}, ErrCorrupt
	}
	return Result{MIME: "image/jpeg", Ext: "jpg", Width: cfg.Width, Height: cfg.Height, Frames: 1, Orientation: orientation, Data: out}, nil
}

func validateGIF(data []byte, lim Limits) (Result, error) {
	cfg, err := decodeConfig(data, func(r *bytes.Reader) (image.Config, error) { return gif.DecodeConfig(r) }, lim)
	if err != nil {
		return Result{}, err
	}
	frames, err := scanGIF(data, lim)
	if err != nil {
		return Result{}, err
	}
	if _, err := gif.DecodeAll(bytes.NewReader(data)); err != nil {
		return Result{}, ErrCorrupt
	}
	out := append([]byte(nil), data...)
	return Result{MIME: "image/gif", Ext: "gif", Width: cfg.Width, Height: cfg.Height, Frames: frames, Orientation: 1, Animated: frames > 1, Data: out}, nil
}
