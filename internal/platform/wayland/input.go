//go:build linux

package wayland

import (
	"fmt"
	"os"
	"time"

	"github.com/bnema/nefergui/internal/keyboard"
	"github.com/bnema/wlturbo/protocol/core"
	"github.com/bnema/wlturbo/wl"
)

// InputReset tells the runtime to synthesize pointer leave, release all buttons,
// keyboard focus-out and repeat cancellation before processing later inputs.
const InputReset = "reset"

// Input is the platform-to-UI port. wl_surface local fixed coordinates are
// already logical pixels and must not be divided by buffer scale.
type Input struct {
	Kind                             string // motion, press, release, axis, leave, key, focus-in, focus-out, reset
	X, Y, DX, DY                     float64
	Button, Serial, Axis, AxisSource uint32
	AxisStop                         bool
	Enter                            bool   // motion from wl_pointer.enter; Serial is the enter serial
	PointerGen                       uint64 // wl_pointer generation that sent Enter
	Value120                         int32
	Key                              keyboard.Key
}

// InputAdapter is owned by the session loop. Never call ApplyInput on the reader.
// The session loop also owns the repeat timer (Tick and NextRepeat).
type InputAdapter struct {
	Keyboard *keyboard.Keyboard
	serial   uint32
	focused  bool
}

// resetHeld drops held keys, repeat and compose state after InputReset while
// keeping keyboard focus, so later key events still apply without a new enter.
func (a *InputAdapter) resetHeld() {
	if a != nil && a.Keyboard != nil {
		a.Keyboard.Focus(a.focused)
	}
}

func NewInputAdapter(locale string) (*InputAdapter, error) {
	k, err := keyboard.New(locale)
	if err != nil {
		return nil, err
	}
	k.Focus(false)
	return &InputAdapter{Keyboard: k}, nil
}
func (a *InputAdapter) Close() {
	if a != nil && a.Keyboard != nil {
		a.Keyboard.Close()
	}
}
func (a *InputAdapter) SelectionSerial() uint32 { return a.serial }
func (a *InputAdapter) Tick(now time.Time) (Input, bool) {
	key, ok := a.Keyboard.Tick(now)
	return Input{Kind: "key", Key: key}, ok
}
func (a *InputAdapter) NextRepeat() (time.Time, bool) { return a.Keyboard.NextRepeat() }

// InputAdapter returns the loop-owned interpreter; the reader never calls it.
func (w *Window) InputAdapter() *InputAdapter { return w.inputAdapter }

// ApplyInput consumes the raw event including its keymap FD, on every path.
func (a *InputAdapter) ApplyInput(ev Event, now time.Time) ([]Input, error) {
	switch ev.Kind {
	case InputFrame:
		return ev.Inputs, nil
	case InputKeymap:
		if ev.FD < 0 {
			return nil, fmt.Errorf("missing keymap fd")
		}
		if ev.Format != 1 || ev.Size == 0 || ev.Size > 16<<20 {
			CloseEventFD(ev)
			return nil, fmt.Errorf("unsupported keymap format or size")
		}
		return nil, a.Keyboard.ReplaceFD(ev.FD, int(ev.Size))
	case InputFocusIn:
		a.Keyboard.Focus(true)
		a.focused = true
		a.serial = ev.Serial
		return []Input{{Kind: "focus-in", Serial: ev.Serial}}, nil
	case InputFocusOut:
		a.Keyboard.Focus(false)
		a.focused = false
		a.serial = 0
		return []Input{{Kind: "focus-out"}}, nil
	case InputKey:
		if ev.Pressed && a.Keyboard != nil {
			a.serial = ev.Serial
		}
		key, err := a.Keyboard.Event(ev.Code, ev.Pressed, now)
		if err != nil || key.Physical == 0 {
			return nil, err
		}
		return []Input{{Kind: "key", Key: key, Serial: ev.Serial}}, nil
	case InputModifiers:
		return nil, a.Keyboard.Mask(ev.Mods[0], ev.Mods[1], ev.Mods[2], 0, 0, ev.Mods[3])
	case InputRepeatInfo:
		a.Keyboard.RepeatInfo(int(ev.Rate), time.Duration(ev.Delay)*time.Millisecond)
		return nil, nil
	}
	return nil, fmt.Errorf("not an input event: %d", ev.Kind)
}

// CloseEventFD releases a keymap event not delivered to the loop, including FD 0.
func CloseEventFD(ev Event) {
	if ev.Kind == InputKeymap && ev.FD >= 0 {
		_ = os.NewFile(uintptr(ev.FD), "keymap").Close()
	}
}

// BindSeat binds the optional initial seat. The reader posts capability changes
// and global removal; ApplySeat runs exclusively on the session loop.
func (w *Window) BindSeat() error {
	seat := core.NewSeat(w.Display.Context())
	g, ok := w.Display.Registry().FindGlobal(core.SeatInterface)
	if !ok {
		w.Display.Context().Unregister(seat)
		return nil
	}
	version, err := w.Display.Registry().BindNegotiated(core.SeatInterface, 9, seat)
	if err != nil {
		w.Display.Context().Unregister(seat)
		return err
	}
	w.Seat, w.seatName, w.seatVersion = seat, g.Name, version
	seat.OnCapabilities(func(c uint32) { w.post(Event{Kind: SeatCapabilities, Capabilities: c}) })
	w.Display.Registry().AddGlobalRemoveHandler(seatRemoved{w: w, name: g.Name})
	return nil
}

type seatRemoved struct {
	w    *Window
	name uint32
}

func (r seatRemoved) HandleRegistryGlobalRemove(e wl.RegistryGlobalRemoveEvent) {
	if e.Name == r.name {
		r.w.post(Event{Kind: SeatRemoved})
	}
}
func (w *Window) ApplySeat(ev Event) error {
	switch ev.Kind {
	case SeatRemoved:
		if w.WLKeyboard != nil {
			w.post(Event{Kind: InputFocusOut})
		}
		return w.releaseSeat()
	case SeatCapabilities:
		if w.Seat == nil {
			return nil
		}
		if ev.Capabilities&1 == 0 && w.Pointer != nil {
			w.dropCursorDevice()
			w.releaseDevice(w.Pointer)
			w.Pointer = nil
		}
		if ev.Capabilities&2 == 0 && w.WLKeyboard != nil {
			w.releaseDevice(w.WLKeyboard)
			w.WLKeyboard = nil
			w.post(Event{Kind: InputFocusOut})
		}
		if ev.Capabilities&1 != 0 && w.Pointer == nil {
			p, err := w.Seat.GetPointer()
			if err != nil {
				return err
			}
			w.Pointer = p
			w.watchPointer(p)
		}
		if ev.Capabilities&2 != 0 && w.WLKeyboard == nil {
			k, err := w.Seat.GetKeyboard()
			if err != nil {
				return err
			}
			w.WLKeyboard = k
			w.watchKeyboard(k)
		}
		return nil
	}
	return fmt.Errorf("not a seat event: %d", ev.Kind)
}

// releaseDevice destroys a wl_pointer or wl_keyboard. Their release request
// exists since seat version 3; older seats only forget the proxy locally.
func (w *Window) releaseDevice(p interface {
	wl.Proxy
	Release() error
}) {
	if w.seatVersion >= 3 {
		_ = p.Release()
	} else if c := p.Context(); c != nil {
		c.Unregister(p)
	}
}
func (w *Window) releaseSeat() error {
	if w.Pointer != nil {
		w.dropCursorDevice()
		w.releaseDevice(w.Pointer)
		w.Pointer = nil
	}
	if w.WLKeyboard != nil {
		w.releaseDevice(w.WLKeyboard)
		w.WLKeyboard = nil
	}
	if w.Seat != nil {
		if w.seatVersion >= 5 {
			_ = w.Seat.Release()
		} else {
			w.Display.Context().Unregister(w.Seat)
		}
		w.Seat = nil
	}
	return nil
}
func (w *Window) watchPointer(p *core.Pointer) {
	var frame []Input // only accessed by the reader
	var x, y float64
	var inside bool
	var source uint32
	// The generation tells a late enter from a released pointer apart from
	// the current one; object IDs can be reused.
	w.pointerGen++
	surfaceID, version, gen := w.Surface.ID(), w.seatVersion, w.pointerGen
	flush := func() {
		if len(frame) > 0 {
			w.post(Event{Kind: InputFrame, Inputs: frame})
			frame = nil
		}
		source = 0
	}
	add := func(i Input) {
		i.AxisSource = source
		frame = append(frame, i)
		if version < 5 {
			flush()
		}
	}
	p.OnEnter(func(serial uint32, id uint32, px, py wl.Fixed) {
		if id != surfaceID {
			return
		}
		inside = true
		x, y = px.Float64(), py.Float64()
		add(Input{Kind: "motion", X: x, Y: y, Enter: true, Serial: serial, PointerGen: gen})
	})
	p.OnLeave(func(_ uint32, id uint32) {
		if id != surfaceID {
			return
		}
		inside = false
		add(Input{Kind: "leave"})
	})
	p.OnMotion(func(_ uint32, px, py wl.Fixed) {
		if inside {
			x, y = px.Float64(), py.Float64()
			add(Input{Kind: "motion", X: x, Y: y})
		}
	})
	p.OnButton(func(serial, _, button, state uint32) {
		if !inside && state == 1 {
			return
		}
		kind := "release"
		if state == 1 {
			kind = "press"
		}
		add(Input{Kind: kind, X: x, Y: y, Button: button, Serial: serial})
	})
	p.OnAxisSource(func(s uint32) { source = s })
	p.OnAxis(func(_, axis uint32, v wl.Fixed) {
		if inside {
			i := Input{Kind: "axis", X: x, Y: y, Axis: axis}
			if axis == 0 {
				i.DY = v.Float64()
			} else {
				i.DX = v.Float64()
			}
			add(i)
		}
	})
	p.OnAxisValue120(func(axis uint32, v int32) {
		if inside {
			add(Input{Kind: "axis", X: x, Y: y, Axis: axis, Value120: v})
		}
	})
	p.OnAxisStop(func(_, axis uint32) {
		if inside {
			add(Input{Kind: "axis", X: x, Y: y, Axis: axis, AxisStop: true})
		}
	})
	p.OnFrame(flush)
}
func (w *Window) watchKeyboard(k *core.Keyboard) {
	surfaceID := w.Surface.ID()
	k.OnKeymap(func(format uint32, fd *wl.OwnedFD, size uint32) {
		n, err := fd.Take()
		if err != nil {
			w.post(Event{Kind: TransportError, Err: err})
			return
		}
		w.postFD(Event{Kind: InputKeymap, FD: n, Format: format, Size: size})
	})
	k.OnEnter(func(serial, id uint32, _ []byte) {
		if id == surfaceID {
			w.post(Event{Kind: InputFocusIn, Serial: serial})
		}
	})
	k.OnLeave(func(_, id uint32) {
		if id == surfaceID {
			w.post(Event{Kind: InputFocusOut})
		}
	})
	k.OnKey(func(serial, _, key, state uint32) {
		w.post(Event{Kind: InputKey, Serial: serial, Code: key, Pressed: state == 1})
	})
	k.OnModifiers(func(_, depressed, latched, locked, group uint32) {
		w.post(Event{Kind: InputModifiers, Mods: [4]uint32{depressed, latched, locked, group}})
	})
	k.OnRepeatInfo(func(rate, delay int32) { w.post(Event{Kind: InputRepeatInfo, Rate: rate, Delay: delay}) })
}

// InputEvent is a loop-owned input value. Runtime integration drains these later.
type InputEvent = Input

// DrainInput appends pending input in order to dst and clears the queue.
// The queue holds at most 4096 events. Motion is evicted first, compatible
// adjacent axes are merged; if neither is possible it emits InputReset and
// starts a new queue. InputOverflow counts discarded events. Call on the Apply loop.
func (w *Window) DrainInput(dst []InputEvent) []InputEvent {
	dst = append(dst, w.inputQueue...)
	clear(w.inputQueue)
	w.inputQueue = w.inputQueue[:0]
	return dst
}

func mergeAxis(dst *Input, src Input) bool {
	if dst.Kind != "axis" || src.Kind != "axis" || dst.Axis != src.Axis ||
		dst.AxisSource != src.AxisSource || dst.AxisStop || src.AxisStop {
		return false
	}
	dst.DX += src.DX
	dst.DY += src.DY
	dst.Value120 += src.Value120
	dst.X, dst.Y = src.X, src.Y
	return true
}

func (w *Window) queueInput(inputs []Input) {
	for _, input := range inputs {
		if len(w.inputQueue) == 4096 {
			index := -1
			for i, v := range w.inputQueue {
				// An enter motion carries the serial cursor shapes need.
				if v.Kind == "motion" && !v.Enter {
					index = i
					break
				}
			}
			if index < 0 {
				// Adjacent axes on the same axis/source can be combined without
				// crossing a button or key event or losing scroll distance.
				for i := 0; i+1 < len(w.inputQueue); i++ {
					if mergeAxis(&w.inputQueue[i], w.inputQueue[i+1]) {
						index = i + 1
						break
					}
				}
			}
			if index >= 0 {
				if w.inputQueue[index].Kind == "motion" {
					w.InputOverflow++
				}
				copy(w.inputQueue[index:], w.inputQueue[index+1:])
				w.inputQueue = w.inputQueue[:len(w.inputQueue)-1]
			} else {
				// A previous reset is still pending if the consumer has not
				// drained yet; keep one marker across successive overflows.
				dropped := len(w.inputQueue)
				if w.inputQueue[0].Kind == InputReset {
					dropped--
				}
				w.InputOverflow += uint64(dropped)
				// Keep the latest discarded enter or leave: it still decides
				// whether the pointer is inside, and an enter carries the serial
				// cursor shapes need.
				var focus *Input
				for i := range w.inputQueue {
					if v := w.inputQueue[i]; v.Enter || v.Kind == "leave" {
						focus = &v
					}
				}
				clear(w.inputQueue)
				w.inputQueue = w.inputQueue[:0]
				w.inputQueue = append(w.inputQueue, Input{Kind: InputReset})
				if focus != nil {
					w.inputQueue = append(w.inputQueue, *focus)
				}
				// Discarded presses must not keep repeating on the loop.
				w.inputAdapter.resetHeld()
			}
		}
		w.inputQueue = append(w.inputQueue, input)
	}
}
