package ui

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/bnema/wlturbo/protocol/core"
	"github.com/bnema/wlturbo/wl"

	"github.com/bnema/nefergui/internal/platform/wayland"
)

// InputKind classifies an InputEvent.
type InputKind uint8

const (
	InputPointerMotion InputKind = iota + 1
	InputPointerPress
	InputPointerRelease
	InputPointerAxis
	InputPointerLeave
	InputKey
	InputFocusIn
	InputFocusOut
	InputReset // platform dropped queued input; treat held state as released
)

// Modifiers is a bit set of keyboard modifiers held during a key event.
type Modifiers uint8

const (
	ModShift Modifiers = 1 << iota
	ModCtrl
)

// InputEvent is a raw platform event, delivered on the owner loop before the
// normal control routing. Coordinates are logical, surface-local pixels.
// Size changes are not input; use OnResize and Frame.Size. Which fields are set
// depends on Kind:
//
//	pointer motion/press/release/axis: X, Y; press/release: Button (evdev code)
//	and Pressed; axis: DX, DY
//	key: KeyName, Text, Modifiers, Pressed, Repeat
type InputEvent struct {
	Kind      InputKind
	X, Y      float64
	DX, DY    float64
	Button    uint32
	Pressed   bool
	Repeat    bool
	KeyName   string
	Text      string
	Modifiers Modifiers
}

// OnInput delivers every raw input event to fn on the session owner loop,
// before the event reaches NeferGUI controls; controls still receive it. fn may
// mutate the model the view reads. It returns true when it changed state the
// view shows, which schedules a redraw; a false return costs no frame, so
// pointer motion does not force a build. A nil fn is ignored. fn must not block.
func OnInput(fn func(InputEvent) bool) WindowOption {
	return windowOption(func(w *windowConfig) {
		if fn != nil {
			w.onInput = fn
		}
	})
}

// OnResize reports the logical size and scale once at start and again only
// when one of them changes; a change always schedules a redraw. It runs on the
// owner loop before the frame is built. A nil fn is ignored.
func OnResize(fn func(width, height int, scale float64)) WindowOption {
	return windowOption(func(w *windowConfig) {
		if fn != nil {
			w.onResize = fn
		}
	})
}

// Wake requests a redraw whenever a value arrives on ch, so a view can react
// to state changed by other goroutines. The caller synchronizes such state;
// the view still runs only on the owner loop. Closing ch stops forwarding. A nil ch is ignored.
func Wake(ch <-chan struct{}) WindowOption {
	return windowOption(func(w *windowConfig) {
		if ch != nil {
			w.wake = ch
		}
	})
}

// inputHooks holds the optional callbacks; all methods run on the owner loop.
type inputHooks struct {
	onInput       func(InputEvent) bool
	onResize      func(int, int, float64)
	haveSize      bool
	width, height int
	scale         float64
}

func toInputEvent(in wayland.Input) (InputEvent, bool) {
	ev := InputEvent{X: in.X, Y: in.Y, DX: in.DX, DY: in.DY, Button: in.Button}
	switch in.Kind {
	case "motion":
		ev.Kind = InputPointerMotion
	case "press":
		ev.Kind, ev.Pressed = InputPointerPress, true
	case "release":
		ev.Kind = InputPointerRelease
	case "axis":
		ev.Kind = InputPointerAxis
	case "leave":
		ev.Kind = InputPointerLeave
	case "key":
		ev.Kind = InputKey
		ev.KeyName, ev.Text = in.Key.Name, in.Key.Text
		ev.Pressed, ev.Repeat = in.Key.Pressed, in.Key.Repeat
		if in.Key.Shift {
			ev.Modifiers |= ModShift
		}
		if in.Key.Ctrl {
			ev.Modifiers |= ModCtrl
		}
	case "focus-in":
		ev.Kind = InputFocusIn
	case "focus-out":
		ev.Kind = InputFocusOut
	case wayland.InputReset:
		ev.Kind = InputReset
	default:
		return InputEvent{}, false
	}
	return ev, true
}

// input reports one platform input and whether the hook changed view state;
// it never allocates.
func (h *inputHooks) input(in wayland.Input) bool {
	if h.onInput == nil {
		return false
	}
	if ev, ok := toInputEvent(in); ok {
		return h.onInput(ev)
	}
	return false
}

// resize reports a size or scale only when it differs from the last report and
// reports whether it did.
func (h *inputHooks) resize(width, height int, scale float64) bool {
	if h.haveSize && h.width == width && h.height == height && h.scale == scale {
		return false
	}
	h.haveSize, h.width, h.height, h.scale = true, width, height, scale
	if h.onResize != nil {
		h.onResize(width, height, scale)
	}
	return true
}

// forwardWake turns external wake tokens into redraw requests. Redraw records
// the request before signalling the owner loop, so the next Build is not idle.
func (r *runtime) forwardWake(ctx context.Context, ch <-chan struct{}) {
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-ch:
			if !ok {
				return
			}
			r.Redraw()
		}
	}
}

// WaylandSurface exposes the window's Wayland connection and wl_surface so an
// integrator can register extra protocol objects on the same client, such as
// clipboard or surface-marking extensions. It is the escape hatch to the
// underlying wlturbo objects; it does not add any protocol itself.
//
// The values are owned by NeferGUI and valid until Run returns. Do not close
// the Display, destroy the Surface or attach buffers. Events for objects the
// callback creates are dispatched on the owner goroutine while requests such as
// roundtrips run before the event reader starts, and on NeferGUI's reader
// goroutine afterwards, so handlers must synchronize any state they share.
type WaylandSurface struct {
	Display *wl.Display
	Surface *core.Surface
}

// OnSurface calls fn once on the owner goroutine, after the surface has its
// role (xdg toplevel or layer surface) and its first configure, and before the
// first buffer is attached, so the window is not yet visible. ctx is the Run
// context: if it is cancelled while fn runs, NeferGUI closes the display so
// blocking protocol calls fail, and Run returns ctx.Err() (joined with any
// other error fn returned). A non-nil error aborts Run and closes the session.
// A nil fn is ignored.
func OnSurface(fn func(context.Context, WaylandSurface) error) WindowOption {
	return windowOption(func(w *windowConfig) {
		if fn != nil {
			w.onSurface = fn
		}
	})
}

// SetInputRects stages the input region for the surface: pointer input outside
// the rectangles passes through. It is applied after the view returns and
// and committed together with the frame's buffer, with no extra commit. nil restores the whole surface, and a non-nil
// empty slice makes the surface click-through. Rects need positive size. The
// slice is copied, so callers may reuse it; an unchanged region costs nothing.
// If the view never calls it, the Layer config's InputRects stay in effect.
func (f *Frame) SetInputRects(rects []Rect) error {
	if !f.active {
		panic("nefergui: SetInputRects outside frame")
	}
	for i, r := range rects {
		if r.Width <= 0 || r.Height <= 0 || int64(r.X)+int64(r.Width) > math.MaxInt32 || int64(r.Y)+int64(r.Height) > math.MaxInt32 {
			return fmt.Errorf("nefergui: input rect %d %+v is empty or overflows", i, r)
		}
	}
	o := f.owner
	o.inputNil = rects == nil
	o.inputRects = append(o.inputRects[:0], rects...)
	o.inputStaged = true
	return nil
}

// applyInputRects hands a staged region to the platform's stage function, which
// must not commit: the region rides the frame's own buffer commit. The region
// stays staged until stage succeeds, so a failure is retried on the next frame.
func (r *runtime) applyInputRects(stage func([]wayland.Rect) error) error {
	if !r.inputStaged {
		return nil
	}
	if r.inputNil {
		if err := stage(nil); err != nil {
			return err
		}
		r.inputStaged = false
		return nil
	}
	out := r.inputOut[:0]
	if out == nil {
		out = []wayland.Rect{}
	}
	for _, q := range r.inputRects {
		out = append(out, wayland.Rect{X: q.X, Y: q.Y, W: q.Width, H: q.Height})
	}
	r.inputOut = out
	if err := stage(out); err != nil {
		return err
	}
	r.inputStaged = false
	return nil
}

// startupHook runs the OnSurface callback. When ctx can be cancelled, the
// display is closed on cancellation so blocking calls in fn return; a
// cancelled ctx is reported joined with fn's own error.
func startupHook(ctx context.Context, fn func(context.Context, WaylandSurface) error, s WaylandSurface) error {
	if ctx.Done() != nil {
		closed := make(chan struct{})
		stop := context.AfterFunc(ctx, func() { _ = s.Display.Close(); close(closed) })
		defer func() {
			if !stop() {
				<-closed // the close callback already started; let it finish
			}
		}()
	}
	err := fn(ctx, s)
	if cerr := ctx.Err(); cerr != nil {
		return errors.Join(cerr, err)
	}
	return err
}
