// Package image provides bounded standard-library raster image ingestion.
package image

import (
	"bytes"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
)

// MaxPixels caps decoded pixel count, preventing unexpectedly large allocations.
const MaxPixels = 16 * 1024 * 1024

var ErrSize = errors.New("image: invalid or excessive dimensions")

// Size validates dimensions of an already decoded image. Its owner retains the image.
func Size(src image.Image) (width, height int, err error) {
	if src == nil {
		return 0, 0, ErrSize
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 || w > MaxPixels/h {
		return 0, 0, ErrSize
	}
	return w, h, nil
}

// Decode accepts only PNG or JPEG bytes and checks dimensions before allocating pixels.
// The caller bounds the input byte slice before reading it from an untrusted stream.
func Decode(data []byte) (image.Image, error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if format != "png" && format != "jpeg" {
		return nil, errors.New("image: unsupported format")
	}
	if config.Width <= 0 || config.Height <= 0 || config.Width > MaxPixels/config.Height {
		return nil, ErrSize
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if _, _, err = Size(img); err != nil {
		return nil, err
	}
	return img, nil
}
