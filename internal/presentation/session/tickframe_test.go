//go:build linux

package session

import (
	"image"
	"strings"
	"testing"

	"github.com/bnema/nefergui/internal/render"
)

// An unsupported quad is rejected before any GPU work, and the borrowed list
// buffer is released so it never pins the frame's image.
func TestTickFrameRejectsUnsupportedAndReleasesImages(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	s := &Session{}
	_, err := s.tickFrame(render.Frame{Quads: []render.Quad{{Op: "image", Image: img}, {Op: "bogus"}}})
	if err == nil || !strings.Contains(err.Error(), "unsupported list operations") {
		t.Fatalf("err=%v", err)
	}
	instances, batches, _ := s.list.List(render.Frame{})
	if len(instances) != 0 || len(batches) != 0 {
		t.Fatal("stale list state")
	}
	s.list.Release()
}
