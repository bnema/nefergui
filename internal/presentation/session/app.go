//go:build linux

package session

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/bnema/nefergui/internal/platform/wayland"
	"github.com/bnema/nefergui/internal/render"
)

// AppHooks runs entirely on the session owner loop. Draw returns dirty=false
// when there is no new display list; the session retries a dirty frame until
// committed without waiting for GPU completion on the loop.
type AppHooks struct {
	Wake      <-chan struct{}
	Input     func([]wayland.Input) error
	Event     func(wayland.Event) error
	Draw      func() (render.Frame, bool, error)
	Committed func(uint64) error
}

// RunApp drives a UI until close or cancellation. Wayland input is batched after
// Apply, and wake tokens, repeat deadlines and release points share one select.
func (s *Session) RunApp(ctx context.Context, app AppHooks) error {
	if app.Draw == nil {
		return fmt.Errorf("session: nil app draw")
	}
	if s.waitCancel != nil {
		return fmt.Errorf("session already running")
	}
	s.waitCtx, s.waitCancel = context.WithCancel(ctx)
	s.releases = make(chan releaseEvent, 128)
	if s.debugDir != "" {
		fds, _ := os.ReadDir("/proc/self/fd")
		s.fdStart, s.fdPeak = len(fds), len(fds)
	}
	defer func() { s.waitCancel(); s.waiters.Wait() }()
	wlEvents := s.Window.StartReader()
	sched := newFrameSchedule()
	for !s.Window.Closed {
		if err := ctx.Err(); err != nil {
			return err
		}
		pending, err := s.finishPending()
		if err != nil {
			return err
		}
		// Resize or scale change (also set before RunApp, or by the event case
		// below): the prepared frame is stale until tick clears Window.Dirty.
		if s.Window.Dirty {
			sched.Invalidate()
		}
		if inputs := s.Window.DrainInput(nil); len(inputs) > 0 {
			sched.Invalidate()
			if app.Input != nil {
				if err := app.Input(inputs); err != nil {
					return err
				}
			}
		}
		committed, err := sched.Step(s.Window.Ready(), app.Draw, func(prepared render.Frame) (bool, error) {
			return s.tickFrame(prepared)
		})
		if err != nil {
			return err
		}
		if committed {
			if app.Committed != nil {
				if err := app.Committed(s.frame); err != nil {
					return err
				}
			}
			continue
		}
		var retry, repeat <-chan time.Time
		if sched.NeedsRetry(pending || s.framePending, s.Window.Ready()) {
			retry = time.After(retryInterval)
		}
		if adapter := s.Window.InputAdapter(); adapter != nil {
			if at, ok := adapter.NextRepeat(); ok {
				repeat = time.After(time.Until(at))
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-app.Wake:
			sched.Invalidate()
		case <-retry:
		case <-repeat:
			if adapter := s.Window.InputAdapter(); adapter != nil {
				if input, ok := adapter.Tick(time.Now()); ok {
					sched.Invalidate()
					if app.Input != nil {
						if err := app.Input([]wayland.Input{input}); err != nil {
							return err
						}
					}
				}
			}
		case ev := <-wlEvents:
			if ev.Kind == wayland.BufferRelease {
				if slot := s.Slots[ev.BufferID]; slot != nil {
					if err := s.Pool.Release(slot.Buffer); err != nil {
						return err
					}
					s.traceState(slot.Buffer, "wl_buffer.release")
				}
			} else if err := s.Window.Apply(ev); err != nil {
				return err
			}
			if app.Event != nil {
				if err := app.Event(ev); err != nil {
					return err
				}
			}
			// DrainInput is handled at the start of the next iteration.
		case ev := <-s.releases:
			if err := s.release(ev); err != nil {
				return err
			}
		}
	}
	if s.Device != nil && s.Device.Validation != nil && len(s.Device.Validation.Messages()) > 0 {
		return fmt.Errorf("%d Vulkan validation errors: %v", len(s.Device.Validation.Messages()), s.Device.Validation.Messages())
	}
	return nil
}
