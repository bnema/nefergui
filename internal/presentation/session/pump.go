//go:build linux

package session

import (
	"context"
	"errors"

	"github.com/bnema/nefergui/internal/render"
)

// Pump drives one Session from an owner loop that multiplexes several sessions
// on a shared Connection. It never reads Wayland events itself: the owner
// applies them and forwards BufferRelease. All methods run on the owner loop.
type Pump struct {
	s     *Session
	sched *frameSchedule
	draw  func() (render.Frame, bool, error)
}

// NewPump prepares s for a shared owner loop. wake is pinged (never blocked on)
// when a GPU release point is ready; the owner then calls Step. draw follows
// the AppHooks.Draw contract.
func (s *Session) NewPump(ctx context.Context, wake chan<- struct{}, draw func() (render.Frame, bool, error)) (*Pump, error) {
	if draw == nil {
		return nil, errors.New("session: nil draw")
	}
	if s.waitCancel != nil {
		return nil, errors.New("session already running")
	}
	s.waitCtx, s.waitCancel = context.WithCancel(ctx)
	s.releases = make(chan releaseEvent, 128)
	s.wake = wake
	return &Pump{s: s, sched: newFrameSchedule(), draw: draw}, nil
}

// Session returns the pumped session.
func (p *Pump) Session() *Session { return p.s }

// Invalidate requests a fresh draw at the next Step.
func (p *Pump) Invalidate() { p.sched.Invalidate() }

// BufferReleased returns a compositor-released wl_buffer to the pool.
func (p *Pump) BufferReleased(id uint64) error {
	if slot := p.s.Slots[id]; slot != nil {
		if err := p.s.Pool.Release(slot.Buffer); err != nil {
			return err
		}
		p.s.traceState(slot.Buffer, "wl_buffer.release")
	}
	return nil
}

// Step applies queued GPU releases and submits at most one frame. retry
// reports that the owner must call Step again after retryInterval.
func (p *Pump) Step() (retry bool, err error) {
	s := p.s
	for done := false; !done; {
		select {
		case ev := <-s.releases:
			if err = s.release(ev); err != nil {
				return false, err
			}
		default:
			done = true
		}
	}
	pending, err := s.finishPending()
	if err != nil {
		return false, err
	}
	if s.Window.Dirty {
		p.sched.Invalidate()
	}
	committed, err := p.sched.Step(s.Window.Ready(), p.draw, s.tickFrame)
	if err != nil {
		return false, err
	}
	if committed {
		return true, nil // the compositor will call back; step again soon for a follow-up
	}
	return p.sched.NeedsRetry(pending || s.framePending, s.Window.Ready()), nil
}

// Close stops release waiters. Call before Session.Close.
func (p *Pump) Close() {
	s := p.s
	if s.waitCancel != nil {
		s.waitCancel()
		s.waiters.Wait()
		s.waitCancel = nil
	}
}
