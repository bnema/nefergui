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
	FeedbackComplete
	ClipboardInput
	IMEInput
	// ConfigureLayer carries a layer-surface extent and its configure serial;
	// Apply resizes and acks together. Zero axes are unchanged.
	ConfigureLayer
	ConfigureLock
	OutputGlobal
	OutputNamed
	OutputScale
	OutputAdded
	OutputRemoved
	LockAcquired
	LockFinished
)

type Event struct {
	Window                           *Window // tagged by transport, applied only by the shared UI owner
	Output                           Output
	Version                          uint32
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
	if w.connection != nil {
		return w.connection.StartReader()
	}
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

// ReaderEvents is the queue a closing session may drain. It is nil for a window
// on a Connection it does not own: that queue carries other windows' events,
// the lock state and transport errors, and belongs to the connection owner.
func (w *Window) ReaderEvents() <-chan Event {
	if w.connection != nil && !w.ownsConnection {
		return nil
	}
	return w.events
}

// PostClipboard and PostIME enqueue transport callbacks for the owner loop.
func (w *Window) PostClipboard(ev ClipboardEvent) { w.post(Event{Kind: ClipboardInput, Clipboard: ev}) }
func (w *Window) PostIME(ev IMEEvent)             { w.post(Event{Kind: IMEInput, IME: ev}) }

func (w *Window) post(ev Event) {
	if w.connection != nil {
		if w != w.connection.seat {
			ev.Window = w
		}
		w.connection.post(ev)
		return
	}
	select {
	case <-w.readerStop:
		return
	case w.events <- ev:
	}
}

// postFD closes an undeliverable keymap descriptor rather than leaking it.
func (w *Window) postFD(ev Event) {
	if w.connection != nil {
		w.post(ev)
		return
	}
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
	if w.connection != nil && (ev.Window == nil || ev.Window != w) {
		return w.connection.Apply(ev)
	}
	if w.Closed {
		CloseEventFD(ev)
		return nil
	}
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
	case ConfigureLock:
		if w.LockSurface == nil || ev.Width <= 0 || ev.Height <= 0 || ev.Width > 16384 || ev.Height > 16384 {
			return fmt.Errorf("invalid lock configure %dx%d", ev.Width, ev.Height)
		}
		if err := w.LockSurface.AckConfigure(ev.Serial); err != nil {
			return err
		}
		w.Width, w.Height = ev.Width, ev.Height
		w.Configured, w.FrameReady, w.Dirty = true, true, true
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
		if w.FeedbackDone { // a new feedback round replaces the previous one
			w.FeedbackDone, w.Tranches, w.Formats = false, nil, nil
		}
		w.MainDevice = ev.Device
	case FeedbackTranche:
		w.Tranches = append(w.Tranches, ev.Tranche)
	case FeedbackFormats:
		w.Formats = ev.Formats
	case FeedbackComplete:
		if w.feedbackErr != nil {
			return w.feedbackErr
		}
		w.FeedbackDone = true
	case SeatCapabilities, SeatRemoved:
		return w.ApplySeat(ev)
	case ClipboardInput, IMEInput:
		// These are applied by the session owner after Window.Apply.
	case InputFrame, InputKeymap, InputFocusIn, InputFocusOut, InputKey, InputModifiers, InputRepeatInfo:
		if w.LockSurface != nil {
			if ev.Kind == InputFrame {
				return nil // pointer input has no consumer on a lock surface
			}
			// Lock owners consume numeric/raw protocol events through their
			// secret keyboard path. Never instantiate the ordinary translator.
			if len(w.rawInput) >= 4096 {
				CloseEventFD(ev)
				return fmt.Errorf("lock raw input queue full")
			}
			w.rawInput = append(w.rawInput, ev)
			return nil
		}
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
