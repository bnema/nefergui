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
// a retry does not call Draw again. Input, a wake, resize or scale changes ask
// Draw for a replacement; an unchanged draw retains the unsubmitted frame.
type frameSchedule struct {
	dirty    bool
	prepared render.Frame
	have     bool
	stale    bool
}

// newFrameSchedule starts dirty so the first configured frame is drawn.
func newFrameSchedule() *frameSchedule { return &frameSchedule{dirty: true} }

// Invalidate asks Draw to refresh the UI before submitting. Keep any held
// frame until Draw actually replaces it: no-op input must not lose a frame.
func (f *frameSchedule) Invalidate() {
	f.dirty = true
	f.stale = true
}

// Step attempts one frame. Nothing is drawn unless a frame is wanted and the
// compositor allows one (ready). Draw runs when no frame is held or it is
// stale; an unchanged draw keeps a held frame, or goes idle without one.
// submit reports whether the frame was
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
	if !f.have || f.stale {
		frame, changed, err := draw()
		if err != nil {
			return false, err
		}
		f.stale = false
		if changed {
			f.prepared, f.have = frame, true
		} else if !f.have {
			f.dirty = false
			return false, nil
		}
	}
	committed, err = submit(f.prepared)
	if err != nil || !committed {
		return false, err
	}
	f.dirty, f.have, f.stale, f.prepared = false, false, false, render.Frame{}
	return true, nil
}

// NeedsRetry reports whether the loop must wake itself after retryInterval.
// gpuPending covers work whose completion only polling can observe. A frame
// still to be drawn, or one waiting for a frame callback, is woken by events
// instead, so an idle or compositor-throttled loop never spins.
func (f *frameSchedule) NeedsRetry(gpuPending, ready bool) bool {
	return gpuPending || (f.dirty && f.have && ready)
}
