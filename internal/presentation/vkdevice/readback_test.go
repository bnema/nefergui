//go:build linux

package vkdevice

import (
	"image"
	"testing"
)

func TestSwizzleBGRA(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	copy(img.Pix, []byte{0x00, 0x80, 0xff, 0xff, 0x10, 0x20, 0x30, 0x40})
	SwizzleBGRA(img)
	want := []byte{0xff, 0x80, 0x00, 0xff, 0x30, 0x20, 0x10, 0x40}
	for i := range want {
		if img.Pix[i] != want[i] {
			t.Fatalf("pix = %x, want %x", img.Pix, want)
		}
	}
}
