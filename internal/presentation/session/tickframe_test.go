//go:build linux

package session

import (
	"image"
	"runtime"
	"strings"
	"testing"
	"weak"

	"github.com/bnema/nefergui/internal/render"
)

// An unsupported quad is rejected before any GPU work, and the borrowed list
// buffer is released so it never pins the frame's image.
func TestTickFrameRejectsUnsupportedAndReleasesImages(t *testing.T) {
	s := &Session{}
	ref := func() weak.Pointer[image.RGBA] {
		img := image.NewRGBA(image.Rect(0, 0, 1, 1))
		ref := weak.Make(img)
		_, err := s.tickFrame(render.Frame{Quads: []render.Quad{{Op: "image", Image: img}, {Op: "bogus"}}})
		if err == nil || !strings.Contains(err.Error(), "unsupported list operations") {
			t.Fatalf("err=%v", err)
		}
		return ref
	}()
	runtime.GC()
	if ref.Value() != nil {
		t.Fatal("idle list buffer retains the frame image")
	}
	runtime.KeepAlive(s)
}
