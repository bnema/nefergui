//go:build linux

package session

import (
	"errors"
	"testing"

	"github.com/bnema/nefergui/internal/render"
)

// scheduleHarness plays the app and session sides of frameSchedule.
type scheduleHarness struct {
	t         *testing.T
	s         *frameSchedule
	draws     int
	submits   []int // frame id (Quads length) of each submit attempt
	changed   bool
	blocked   bool
	ready     bool
	drawErr   error
	submitErr error
}

func newHarness(t *testing.T) *scheduleHarness {
	return &scheduleHarness{t: t, s: newFrameSchedule(), changed: true, ready: true}
}

func (h *scheduleHarness) step() bool {
	h.t.Helper()
	ok, err := h.s.Step(h.ready, func() (render.Frame, bool, error) {
		h.draws++
		return render.Frame{Quads: make([]render.Quad, h.draws)}, h.changed, h.drawErr
	}, func(f render.Frame) (bool, error) {
		h.submits = append(h.submits, len(f.Quads))
		return !h.blocked, h.submitErr
	})
	if err != nil && h.drawErr == nil && h.submitErr == nil {
		h.t.Fatal(err)
	}
	return ok
}

func TestScheduleBlockedFrameKeepsPreparedAndRetries(t *testing.T) {
	h := newHarness(t)
	h.blocked = true
	if h.step() || h.draws != 1 || !h.s.NeedsRetry(false, true) {
		t.Fatalf("blocked: draws=%d retry=%v", h.draws, h.s.NeedsRetry(false, true))
	}
	// Retries resubmit the same prepared frame without redrawing.
	if h.step() || h.step() || h.draws != 1 || len(h.submits) != 3 || h.submits[2] != 1 {
		t.Fatalf("retry redrew: draws=%d submits=%v", h.draws, h.submits)
	}
	h.blocked = false
	if !h.step() || h.draws != 1 || h.s.NeedsRetry(false, true) {
		t.Fatalf("release did not commit cleanly: draws=%d submits=%v", h.draws, h.submits)
	}
	if h.step() || len(h.submits) != 4 {
		t.Fatalf("clean schedule submitted again: %v", h.submits)
	}
}

func TestScheduleInvalidateReplacesChangedFrame(t *testing.T) {
	h := newHarness(t)
	h.blocked = true
	h.step()
	h.s.Invalidate()
	h.blocked = false
	if !h.step() || h.draws != 2 || h.submits[len(h.submits)-1] != 2 {
		t.Fatalf("stale frame submitted: draws=%d submits=%v", h.draws, h.submits)
	}
}

func TestScheduleNoOpInputKeepsUnsubmittedFrame(t *testing.T) {
	h := newHarness(t)
	h.blocked = true
	h.step()
	h.s.Invalidate()
	h.changed = false
	if h.step() || h.draws != 2 || h.submits[len(h.submits)-1] != 1 {
		t.Fatalf("no-op lost held frame: draws=%d submits=%v", h.draws, h.submits)
	}
	h.blocked = false
	if !h.step() || h.draws != 2 || h.submits[len(h.submits)-1] != 1 {
		t.Fatalf("release lost held frame: draws=%d submits=%v", h.draws, h.submits)
	}
	if h.s.NeedsRetry(false, true) {
		t.Fatal("committed schedule did not go idle")
	}
}

func TestScheduleDrawUnchangedGoesIdle(t *testing.T) {
	h := newHarness(t)
	h.changed = false
	if h.step() || h.draws != 1 || len(h.submits) != 0 {
		t.Fatalf("unchanged draw: draws=%d submits=%v", h.draws, h.submits)
	}
	if h.s.NeedsRetry(false, true) {
		t.Fatal("idle schedule asks for retry (busy loop)")
	}
	for i := 0; i < 3; i++ {
		h.step()
	}
	if h.draws != 1 {
		t.Fatalf("idle loop redrew %d times", h.draws)
	}
	h.s.Invalidate()
	h.changed = true
	if !h.step() || h.draws != 2 {
		t.Fatalf("wake after idle: draws=%d", h.draws)
	}
}

func TestScheduleWaitsForFrameCallback(t *testing.T) {
	h := newHarness(t)
	h.ready = false
	if h.step() || h.draws != 0 || len(h.submits) != 0 || h.s.NeedsRetry(false, false) {
		t.Fatalf("drew before frame callback: draws=%d", h.draws)
	}
	h.ready = true
	if !h.step() || h.draws != 1 {
		t.Fatalf("frame callback did not release draw: draws=%d", h.draws)
	}
	// A prepared frame waiting for a callback is woken by the event, not polled.
	h.s.Invalidate()
	h.blocked = true
	h.step()
	if h.s.NeedsRetry(false, false) || !h.s.NeedsRetry(false, true) {
		t.Fatal("retry must follow frame readiness")
	}
}

func TestSchedulePendingGPUForcesRetry(t *testing.T) {
	h := newHarness(t)
	h.changed = false
	h.step()
	if !h.s.NeedsRetry(true, false) {
		t.Fatal("pending GPU work must schedule a bounded retry")
	}
}

func TestScheduleErrorsPropagate(t *testing.T) {
	h := newHarness(t)
	h.drawErr = errors.New("draw")
	if _, err := h.s.Step(true, func() (render.Frame, bool, error) { return render.Frame{}, false, h.drawErr }, nil); !errors.Is(err, h.drawErr) {
		t.Fatalf("draw err = %v", err)
	}
	h.drawErr = nil
	h.submitErr = errors.New("submit")
	if ok, err := h.s.Step(true, func() (render.Frame, bool, error) { return render.Frame{}, true, nil }, func(render.Frame) (bool, error) { return true, h.submitErr }); ok || !errors.Is(err, h.submitErr) {
		t.Fatalf("submit error: ok=%v err=%v", ok, err)
	}
}
