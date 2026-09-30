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
	dirty := true
	var prepared render.Frame
	havePrepared := false
	for !s.Window.Closed {
		if err := ctx.Err(); err != nil {
			return err
		}
		pending, err := s.finishPending()
		if err != nil {
			return err
		}
		if s.Window.Dirty {
			dirty = true
		}
		if inputs := s.Window.DrainInput(nil); len(inputs) > 0 {
			dirty = true
			havePrepared = false
			if app.Input != nil {
				if err := app.Input(inputs); err != nil {
					return err
				}
			}
		}
		if dirty && s.Window.FrameReady {
			if !havePrepared {
				frame, changed, err := app.Draw()
				if err != nil {
					return err
				}
				if !changed {
					dirty = false
				} else {
					prepared, havePrepared = frame, true
				}
			}
			if havePrepared {
				instances, batches, stats := render.ListBatches(prepared)
				if len(stats.Skipped) != 0 {
					s.Preparer.Atlas.MarkAllDirty()
					return fmt.Errorf("unsupported list operations: %v", stats.Skipped)
				}
				ok, err := s.tick(nil, instances, prepared.Uploads, batches)
				if err != nil {
					return err
				}
				if ok {
					dirty = false
					havePrepared = false
					if app.Committed != nil {
						if err := app.Committed(s.frame); err != nil {
							return err
						}
					}
					continue
				}
			}
		}
		var retry, repeat <-chan time.Time
		if pending || s.framePending || (dirty && havePrepared && s.Window.FrameReady) {
			retry = time.After(5 * time.Millisecond)
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
			dirty = true
			havePrepared = false
		case <-retry:
		case <-repeat:
			if adapter := s.Window.InputAdapter(); adapter != nil {
				if input, ok := adapter.Tick(time.Now()); ok {
					dirty = true
					havePrepared = false
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
			if s.Window.Dirty {
				dirty = true
				havePrepared = false
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
