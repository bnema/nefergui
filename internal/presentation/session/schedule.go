//go:build linux

package session

import (
	"time"

	"github.com/bnema/nefergui/internal/render"
)

// retryInterval bounds how long the owner loop sleeps while a frame is blocked
// on GPU or buffer readiness that has no event source of its own.
const retryInterval = 5 * time.Millisecond

// frameSchedule is RunApp's frame state, touched only from the owner loop.
// A frame is wanted (dirty) until the compositor accepted a commit or the app
// reported no change. A prepared display list is kept across blocked submits so
// a retry does not call Draw again, and it is dropped whenever input, a wake, a
// resize or a scale change makes it stale.
type frameSchedule struct {
	dirty    bool
	prepared render.Frame
	have     bool
}

// newFrameSchedule starts dirty so the first configured frame is drawn.
func newFrameSchedule() *frameSchedule { return &frameSchedule{dirty: true} }

// Invalidate records that the UI or surface changed: draw again, and never
// submit a previously prepared frame.
func (f *frameSchedule) Invalidate() {
	f.dirty = true
	f.have = false
	f.prepared = render.Frame{}
}

// Step attempts one frame. Nothing is drawn unless a frame is wanted and the
// compositor allows one (ready). Draw runs only when no prepared frame is held;
// an unchanged draw clears dirty. submit reports whether the frame was
// committed: on false the prepared frame is kept for the next attempt, on true
// the schedule is clean until the next Invalidate.
func (f *frameSchedule) Step(
	ready bool,
	draw func() (render.Frame, bool, error),
	submit func(render.Frame) (bool, error),
) (committed bool, err error) {
	if !f.dirty || !ready {
		return false, nil
	}
	if !f.have {
		frame, changed, err := draw()
		if err != nil {
			return false, err
		}
		if !changed {
			f.dirty = false
			return false, nil
		}
		f.prepared, f.have = frame, true
	}
	committed, err = submit(f.prepared)
	if err != nil || !committed {
		return false, err
	}
	f.dirty, f.have, f.prepared = false, false, render.Frame{}
	return true, nil
}

// NeedsRetry reports whether the loop must wake itself after retryInterval.
// gpuPending covers work whose completion only polling can observe. A frame
// still to be drawn, or one waiting for a frame callback, is woken by events
// instead, so an idle or compositor-throttled loop never spins.
func (f *frameSchedule) NeedsRetry(gpuPending, ready bool) bool {
	return gpuPending || (f.dirty && f.have && ready)
}
