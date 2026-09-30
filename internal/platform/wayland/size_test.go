//go:build linux

package wayland

import (
	"fmt"
	"testing"

	"github.com/bnema/wlturbo/protocol/viewporter"
)

func TestPhysicalSizeCeilsFractionalScale(t *testing.T) {
	for _, tc := range []struct {
		width, height int32
		scale         float64
		wantW, wantH  int32
	}{
		{3, 5, 1.25, 4, 7}, {3, 5, 1.5, 5, 8}, {4, 6, 1.5, 6, 9}, {3, 5, 1.25 + 1e-12, 4, 7},
	} {
		w := &Window{Width: tc.width, Height: tc.height, Scale: tc.scale, Viewport: &viewporter.WpViewport{}}
		x, y, err := w.PhysicalSize()
		if err != nil || x != tc.wantW || y != tc.wantH {
			t.Fatalf("%dx%d scale %g: %dx%d err=%v; want %dx%d", tc.width, tc.height, tc.scale, x, y, err, tc.wantW, tc.wantH)
		}
	}
}

func TestFeedbackAppliedOnLoop(t *testing.T) {
	w := &Window{events: make(chan Event, 2), readerStop: make(chan struct{})}
	w.readerStarted.Store(true)
	go w.feedbackFailure(fmt.Errorf("feedback failed"))
	ev := <-w.events
	if w.feedbackErr != nil {
		t.Fatal("reader mutated loop-owned error")
	}
	if err := w.Apply(ev); err == nil || w.feedbackErr == nil {
		t.Fatalf("apply: %v feedback: %v", err, w.feedbackErr)
	}
}
