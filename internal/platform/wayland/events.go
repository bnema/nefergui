//go:build linux

package wayland

import (
	"fmt"
	"github.com/bnema/wlturbo/protocol/linuxdmabuf"
	"net"
	"time"
)

type EventKind uint8

const (
	ConfigureSize EventKind = iota + 1
	ConfigureSerial
	PreferredScale
	BufferScale
	CloseEvent
	FrameEvent
	BufferRelease
	TransportError
	SeatCapabilities
	SeatRemoved
	InputFrame
	InputKeymap
	InputFocusIn
	InputFocusOut
	InputKey
	InputModifiers
	InputRepeatInfo
	FeedbackError
	FeedbackMainDevice
	FeedbackTranche
	FeedbackFormats
	ClipboardInput
	IMEInput
	// ConfigureLayer carries a layer-surface extent and its configure serial;
	// Apply resizes and acks together. Zero axes are unchanged.
	ConfigureLayer
)

type Event struct {
	Kind                             EventKind
	Width, Height                    int32
	Serial, Scale                    uint32
	Factor                           int32
	BufferID                         uint64
	Err                              error
	Device                           uint64
	Tranche, Formats                 []linuxdmabuf.FormatEntry
	Inputs                           []Input
	Clipboard                        ClipboardEvent
	IME                              IMEEvent
	Capabilities, Format, Size, Code uint32
	FD                               int // owned by ApplyInput or CloseEventFD when Kind == InputKeymap
	Pressed                          bool
	Mods                             [4]uint32
	Rate, Delay                      int32
}

// StartReader launches exactly one blocking Wayland reader. Registered callbacks
// only send typed values to Events; Apply is called by the session loop alone.
func (w *Window) StartReader() <-chan Event {
	w.readerOnce.Do(func() {
		w.readerStarted.Store(true)
		w.readerDone = make(chan struct{})
		go func() {
			defer close(w.readerDone)
			for {
				err := w.Display.Dispatch()
				if err != nil {
					select {
					case <-w.readerStop:
						return
					case w.events <- Event{Kind: TransportError, Err: err}:
						return
					}
				}
			}
		}()
	})
	return w.events
}

// Events exposes the reader's typed event queue to the session owner.
func (w *Window) Events() <-chan Event { return w.events }

// PostClipboard and PostIME enqueue transport callbacks for the owner loop.
func (w *Window) PostClipboard(ev ClipboardEvent) { w.post(Event{Kind: ClipboardInput, Clipboard: ev}) }
func (w *Window) PostIME(ev IMEEvent)             { w.post(Event{Kind: IMEInput, IME: ev}) }

func (w *Window) post(ev Event) {
	select {
	case <-w.readerStop:
		return
	case w.events <- ev:
	}
}

// postFD closes an undeliverable keymap descriptor rather than leaking it.
func (w *Window) postFD(ev Event) {
	select {
	case <-w.readerStop:
		CloseEventFD(ev)
	case w.events <- ev:
	}
}

// BufferReleased is safe to call from the reader callback: it never touches
// Pool, Session, or Vulkan. The serial loop records the optional event.
func (w *Window) BufferReleased(id uint64) {
	w.post(Event{Kind: BufferRelease, BufferID: id})
}
func (w *Window) Apply(ev Event) error {
	switch ev.Kind {
	case ConfigureSize:
		if ev.Width > 0 && ev.Height > 0 && (w.Width != ev.Width || w.Height != ev.Height) {
			w.Width, w.Height, w.Dirty = ev.Width, ev.Height, true
		}
	case ConfigureSerial:
		var err error
		if w.XdgSurface != nil {
			err = w.XdgSurface.AckConfigure(ev.Serial)
		} else {
			err = fmt.Errorf("configure without a shell surface")
		}
		if err != nil {
			return err
		}
		w.Configured, w.FrameReady = true, true
	case ConfigureLayer:
		w.applyLayerSize(ev.Width, ev.Height)
		if w.LayerSurface == nil {
			return fmt.Errorf("configure without a layer surface")
		}
		if err := w.LayerSurface.AckConfigure(ev.Serial); err != nil {
			return err
		}
		w.Configured, w.FrameReady = true, true
	case PreferredScale:
		if ev.Scale == 0 {
			return fmt.Errorf("invalid preferred scale zero")
		}
		if ev.Scale%120 != 0 && w.Viewport == nil {
			return &CapabilityError{Name: "wp_viewporter", Cause: fmt.Errorf("fractional scale %d/120 requires viewporter", ev.Scale)}
		}
		if scale := float64(ev.Scale) / 120; w.Scale != scale {
			w.Scale = scale
			w.Dirty = true
		}
	case BufferScale:
		if w.Fractional == nil && ev.Factor > 0 && w.Scale != float64(ev.Factor) {
			w.Scale = float64(ev.Factor)
			w.Dirty = true
		}
	case CloseEvent:
		w.Closed = true
	case FrameEvent:
		w.FrameReady = true
	case FeedbackError:
		if w.feedbackErr == nil {
			w.feedbackErr = ev.Err
		}
		return ev.Err
	case FeedbackMainDevice:
		w.MainDevice = ev.Device
	case FeedbackTranche:
		w.Tranches = append(w.Tranches, ev.Tranche)
	case FeedbackFormats:
		w.Formats = ev.Formats
	case SeatCapabilities, SeatRemoved:
		return w.ApplySeat(ev)
	case ClipboardInput, IMEInput:
		// These are applied by the session owner after Window.Apply.
	case InputFrame, InputKeymap, InputFocusIn, InputFocusOut, InputKey, InputModifiers, InputRepeatInfo:
		if w.inputAdapter == nil {
			var err error
			w.inputAdapter, err = NewInputAdapter("")
			if err != nil {
				CloseEventFD(ev)
				return err
			}
		}
		inputs, err := w.inputAdapter.ApplyInput(ev, time.Now())
		w.queueInput(inputs)
		return err
	case BufferRelease: // consumed by the session owner; no pixel ownership implication
	case TransportError:
		if ev.Err != nil {
			return ev.Err
		}
		return net.ErrClosed
	default:
		return fmt.Errorf("unknown Wayland event %d", ev.Kind)
	}
	return nil
}
