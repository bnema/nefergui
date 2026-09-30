//go:build linux

// Package wayland owns the minimal xdg-shell window and presentation protocol
// objects. Vulkan allocations and DMA-BUF image creation remain the renderer's
// responsibility; a window may not attach until the first configure is acked.
package wayland

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bnema/wlturbo"
	"github.com/bnema/wlturbo/protocol/core"
	"github.com/bnema/wlturbo/protocol/cursorshape"
	"github.com/bnema/wlturbo/protocol/drmsyncobj"
	"github.com/bnema/wlturbo/protocol/extsessionlock"
	"github.com/bnema/wlturbo/protocol/fractionalscale"
	"github.com/bnema/wlturbo/protocol/linuxdmabuf"
	"github.com/bnema/wlturbo/protocol/viewporter"
	"github.com/bnema/wlturbo/protocol/xdgshell"
	"github.com/bnema/wlturbo/wl"

	"github.com/bnema/nefergui/internal/platform/wayland/layershell"
)

// CapabilityError reports a missing or insufficient required protocol.
type CapabilityError struct {
	Name  string
	Cause error
}

func (e *CapabilityError) Error() string {
	return fmt.Sprintf("Wayland capability %s: %v", e.Name, e.Cause)
}
func (e *CapabilityError) Unwrap() error { return e.Cause }

const (
	DRMXRGB8888 = uint32(0x34325258)
	DRMARGB8888 = uint32(0x34325241)
)

type Window struct {
	connection                *Connection
	ownsConnection            bool
	LockSurface               *extsessionlock.ExtSessionLockSurface
	Display                   *wl.Display
	Compositor                *core.Compositor
	Shell                     *xdgshell.XdgWmBase
	Dmabuf                    *linuxdmabuf.LinuxDmabuf
	SyncManager               *drmsyncobj.WpLinuxDrmSyncobjManager
	LayerShell                *layershell.LayerShell   // nil for xdg windows
	LayerSurface              *layershell.LayerSurface // nil for xdg windows
	Surface                   *core.Surface
	XdgSurface                *xdgshell.XdgSurface
	Toplevel                  *xdgshell.XdgToplevel
	SyncSurface               *drmsyncobj.WpLinuxDrmSyncobjSurface
	Viewporter                *viewporter.WpViewporter
	Viewport                  *viewporter.WpViewport
	ScaleManager              *fractionalscale.WpFractionalScaleManager
	Fractional                *fractionalscale.WpFractionalScale
	Feedback                  *linuxdmabuf.LinuxDmabufFeedback
	Seat                      *core.Seat
	Pointer                   *core.Pointer
	CursorShapes              *cursorshape.WpCursorShapeManager // optional
	cursor                    cursorState
	pointerGen                uint64 // bumped per wl_pointer; session loop only
	WLKeyboard                *core.Keyboard
	keymapCache               *keyboardMapCache // owner retires each keyboard generation
	seatName, seatVersion     uint32
	Width, Height             int32
	Scale                     float64
	Configured, Closed, Dirty bool
	Transparent               bool
	FrameReady                bool
	// Feedback fields are populated by the default feedback event stream.
	FeedbackDone  bool // owner has applied the complete feedback round
	MainDevice    uint64
	Formats       []linuxdmabuf.FormatEntry
	Tranches      [][]linuxdmabuf.FormatEntry
	feedbackErr   error
	events        chan Event
	readerDone    chan struct{}
	readerStop    chan struct{}
	readerOnce    sync.Once
	readerStarted atomic.Bool
	rawInput      []Event // lock-only numeric protocol events, never ordinary text
	inputAdapter  *InputAdapter
	inputQueue    []InputEvent
	// inputRects/inputCustom record the current input region; session loop only.
	inputRects          []Rect
	inputCustom         bool
	layerOutputGlobal   uint32
	lockOutput          uint32
	layerOutputSelected bool
	// InputOverflow counts events discarded when the 4096-event queue fills.
	InputOverflow uint64
}

// Connect opens a default xdg-toplevel window with full-surface input. It
// cannot be cancelled; use ConnectWithOptions for a cancellable startup.
func Connect(name string, width, height int32, transparent bool) (*Window, error) {
	return ConnectWithOptions(context.Background(), name, width, height, transparent, SurfaceOptions{})
}

// dialTimeout bounds the unix-socket dial; a local dial is normally instant.
const dialTimeout = 5 * time.Second

// waylandSocketPath resolves name like wlturbo.Connect does.
func waylandSocketPath(name string) (string, error) {
	if name == "" {
		name = os.Getenv("WAYLAND_DISPLAY")
		if name == "" {
			name = "wayland-0"
		}
	}
	if filepath.IsAbs(name) {
		return name, nil
	}
	runDir := os.Getenv("XDG_RUNTIME_DIR")
	if runDir == "" {
		return "", errors.New("XDG_RUNTIME_DIR not set")
	}
	return filepath.Join(runDir, name), nil
}

// ConnectWithOptions opens a window whose role and initial input region are
// selected by opts. The zero SurfaceOptions is identical to Connect. Startup
// (dial, registry, configure, dmabuf feedback) is abandoned with ctx: the
// connection is closed and the returned error wraps ctx.Err(). Once it
// returns successfully, ctx no longer affects the window.
func ConnectWithOptions(ctx context.Context, name string, width, height int32, transparent bool, opts SurfaceOptions) (*Window, error) {
	if ctx == nil {
		return nil, errors.New("nil context")
	}
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid logical size %dx%d", width, height)
	}
	if err := opts.Validate(); err != nil {
		return nil, err
	}
	c, err := ConnectConnection(ctx, name)
	if err != nil {
		return nil, err
	}
	w, err := c.NewWindow(ctx, width, height, transparent, opts)
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	w.ownsConnection = true
	return w, nil
}

// NewWindow creates a sibling on this connection. Before StartReader it waits
// for initial configure/feedback; afterwards it returns unconfigured and the
// owner must Apply tagged events before allocating/presenting buffers.
func (c *Connection) NewWindow(ctx context.Context, width, height int32, transparent bool, opts SurfaceOptions) (res *Window, err error) {
	if ctx == nil {
		return nil, errors.New("nil context")
	}
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid logical size %dx%d", width, height)
	}
	if err = opts.Validate(); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if c.closed {
		return nil, errors.New("connection closed")
	}
	if opts.Lock != nil && (transparent || opts.Layer != nil || opts.InputRects != nil) {
		return nil, errors.New("lock role requires opaque full-input surface")
	}
	if opts.Lock != nil && !c.started.Load() {
		// A lock that was already refused or ended must not get surfaces; its
		// lock and output events are held for the owner, not applied here.
		if err = c.drainStartup(true); err != nil {
			return nil, err
		}
	}
	// Objects created below get their handlers after their request is sent;
	// a running reader must not dispatch their first events in between.
	defer c.pauseReader()()
	d := c.Display
	// Closing the display unblocks any pending read; Display.Close is idempotent.
	stopWatch := context.AfterFunc(ctx, func() { _ = d.Close() })
	w := &Window{connection: c, events: c.events, readerStop: c.stop, Display: d, Width: width, Height: height, Scale: 1, Transparent: transparent}
	w.readerStarted.Store(c.started.Load())
	defer func() {
		if !stopWatch() && err == nil {
			// Cancelled after the last step but before return: the watcher owns a
			// closed display, so the window is unusable.
			err = ctx.Err()
		}
		if err != nil {
			if cerr := ctx.Err(); cerr != nil && !errors.Is(err, cerr) {
				err = errors.Join(cerr, err)
			}
			_ = w.Close()
			res = nil
		}
	}()
	wctx := d.Context()
	bind := func(name string, supported, minimum uint32, proxy wl.Proxy) error {
		advertised, ok := d.Registry().FindGlobal(name)
		if !ok || advertised.Version < minimum {
			return &CapabilityError{Name: name, Cause: fmt.Errorf("requires version %d, announced %d", minimum, advertised.Version)}
		}
		got, e := d.Registry().BindNegotiated(name, supported, proxy)
		if e != nil {
			return &CapabilityError{Name: name, Cause: e}
		}
		if got < minimum {
			return &CapabilityError{Name: name, Cause: fmt.Errorf("requires version %d, compositor negotiated %d", minimum, got)}
		}
		return nil
	}
	w.Compositor = core.NewCompositor(wctx)
	if err = bind(core.CompositorInterface, 6, 4, w.Compositor); err != nil {
		return nil, err
	}
	if opts.Layer != nil {
		if err = w.bindLayerShell(bind); err != nil {
			return nil, err
		}
	} else if opts.Lock == nil {
		w.Shell = xdgshell.NewXdgWmBase(wctx)
		if err = bind(xdgshell.XdgWmBaseInterface, 6, 1, w.Shell); err != nil {
			return nil, err
		}
	}
	w.Dmabuf = linuxdmabuf.NewLinuxDmabuf(wctx)
	if err = bind(linuxdmabuf.LinuxDmabufInterface, 4, 4, w.Dmabuf); err != nil {
		return nil, err
	}
	w.SyncManager = drmsyncobj.NewWpLinuxDrmSyncobjManager(wctx)
	if err = bind(drmsyncobj.WpLinuxDrmSyncobjManagerInterface, 1, 1, w.SyncManager); err != nil {
		return nil, err
	}
	w.Viewporter = viewporter.NewWpViewporter(wctx)
	if _, e := d.Registry().BindNegotiated(viewporter.WpViewporterInterface, 1, w.Viewporter); e != nil {
		if !errors.Is(e, wlturbo.ErrGlobalNotFound) {
			return nil, e
		}
		wctx.Unregister(w.Viewporter)
		w.Viewporter = nil
	}
	w.ScaleManager = fractionalscale.NewWpFractionalScaleManager(wctx)
	if _, e := d.Registry().BindNegotiated(fractionalscale.WpFractionalScaleManagerInterface, 1, w.ScaleManager); e != nil {
		if !errors.Is(e, wlturbo.ErrGlobalNotFound) {
			return nil, e
		}
		wctx.Unregister(w.ScaleManager)
		w.ScaleManager = nil
	}
	w.CursorShapes = cursorshape.NewWpCursorShapeManager(wctx)
	if _, e := d.Registry().BindNegotiated(cursorshape.WpCursorShapeManagerInterface, 1, w.CursorShapes); e != nil {
		if !errors.Is(e, wlturbo.ErrGlobalNotFound) {
			return nil, e
		}
		wctx.Unregister(w.CursorShapes)
		w.CursorShapes = nil
	}
	w.Seat, w.Pointer, w.WLKeyboard = c.seat.Seat, c.seat.Pointer, c.seat.WLKeyboard
	if opts.Lock != nil {
		w.Seat = nil
	} // clipboard/IME constructors require Seat
	w.pointerGen = c.seat.pointerGen
	if w.Shell != nil {
		w.Shell.OnPing(func(serial uint32) {
			if e := w.Shell.Pong(serial); e != nil {
				w.feedbackFailure(e)
			}
		})
	}
	w.Surface, err = w.Compositor.CreateSurface()
	if err != nil {
		return nil, err
	}
	if w.Viewporter != nil {
		w.Viewport, err = w.Viewporter.GetViewport(w.Surface)
		if err != nil {
			return nil, err
		}
	}
	if w.ScaleManager != nil {
		w.Fractional, err = w.ScaleManager.GetFractionalScale(w.Surface)
		if err != nil {
			return nil, err
		}
		w.Fractional.OnPreferredScale(func(scale uint32) {
			if w.readerStarted.Load() {
				w.post(Event{Kind: PreferredScale, Scale: scale})
				return
			}
			if scale == 0 {
				w.feedbackFailure(fmt.Errorf("invalid fractional scale zero"))
				return
			}
			if scale%120 != 0 && w.Viewport == nil {
				w.feedbackFailure(&CapabilityError{Name: viewporter.WpViewporterInterface, Cause: fmt.Errorf("fractional scale %d/120 requires viewporter", scale)})
				return
			}
			if w.Scale != float64(scale)/120 {
				w.Scale = float64(scale) / 120
				w.Dirty = true
			}
		})
	}
	w.Surface.OnPreferredBufferScale(func(factor int32) {
		if w.readerStarted.Load() {
			w.post(Event{Kind: BufferScale, Factor: factor})
			return
		}
		if w.Fractional == nil && factor > 0 && w.Scale != float64(factor) {
			w.Scale = float64(factor)
			w.Dirty = true
		}
	})
	c.windows[w.Surface.ID()] = w
	c.routes.Store(w.Surface.ID(), w)
	if opts.Lock != nil {
		if err = w.createLockSurface(*opts.Lock); err != nil {
			return nil, err
		}
	} else if opts.Layer != nil {
		if err = w.createLayerSurface(*opts.Layer); err != nil {
			return nil, err
		}
	} else if err = w.createToplevel(); err != nil {
		return nil, err
	}
	if opts.InputRects != nil {
		if err = w.SetInputRects(opts.InputRects); err != nil {
			return nil, err
		}
	}
	w.SyncSurface, err = w.SyncManager.GetSurface(w.Surface)
	if err != nil {
		return nil, err
	}
	w.Feedback, err = w.Dmabuf.GetDefaultFeedback()
	if err != nil {
		return nil, err
	}
	w.watchFeedback()
	// ext-session-lock configure is sent by get_lock_surface; unlike xdg and
	// layer-shell it forbids the initial empty commit before ACK+buffer.
	if opts.Lock == nil {
		if err = w.Surface.Commit(); err != nil {
			return nil, err
		}
	}
	if c.started.Load() {
		return w, nil
	}
	for !w.Configured || !w.FeedbackDone || w.MainDevice == 0 || len(w.Tranches) == 0 {
		if w.Closed {
			return nil, fmt.Errorf("window closed before initial configure and feedback")
		}
		if err = c.drainStartup(true); err != nil {
			return nil, err // a finished already queued: do not wait for a configure
		}
		if err = w.Dispatch(); err != nil {
			return nil, err
		}
		if err = c.drainStartup(true); err != nil {
			return nil, err
		}
	}
	return w, nil
}

func (w *Window) createToplevel() error {
	var err error
	w.XdgSurface, err = w.Shell.GetXdgSurface(w.Surface)
	if err != nil {
		return err
	}
	w.Toplevel, err = w.XdgSurface.GetToplevel()
	if err != nil {
		return err
	}
	w.Toplevel.OnClose(func() {
		if w.readerStarted.Load() {
			w.post(Event{Kind: CloseEvent})
			return
		}
		w.Closed = true
	})
	w.Toplevel.OnConfigure(func(width, height int32, _ []byte) {
		if w.readerStarted.Load() {
			w.post(Event{Kind: ConfigureSize, Width: width, Height: height})
			return
		}
		if width > 0 && height > 0 && (w.Width != width || w.Height != height) {
			w.Width, w.Height, w.Dirty = width, height, true
		}
	})
	w.XdgSurface.OnConfigure(func(serial uint32) {
		if w.readerStarted.Load() {
			w.post(Event{Kind: ConfigureSerial, Serial: serial})
			return
		}
		if e := w.XdgSurface.AckConfigure(serial); e != nil {
			w.feedbackFailure(e)
			return
		}
		w.Configured, w.FrameReady = true, true
	})
	return nil
}

func (w *Window) Dispatch() error {
	if w.connection != nil && w.connection.started.Load() {
		return errors.New("connection reader already started")
	}
	if w.feedbackErr != nil {
		return w.feedbackErr
	}
	if err := w.Display.Dispatch(); err != nil {
		return err
	}
	return w.feedbackErr
}

// Ready reports that a buffer may be allocated and attached: the role's
// configure was acknowledged, the dmabuf feedback round is complete and the
// previous frame callback has fired.
func (w *Window) Ready() bool { return w.Configured && w.FeedbackDone && w.FrameReady }

// IsLock reports whether the window is an ext-session-lock surface.
func (w *Window) IsLock() bool { return w != nil && w.LockSurface != nil }

func (w *Window) PhysicalSize() (int32, int32, error) {
	if w.Scale <= 0 || w.Width <= 0 || w.Height <= 0 {
		return 0, 0, fmt.Errorf("invalid window geometry")
	}
	if w.Scale != math.Trunc(w.Scale) && w.Viewport == nil {
		return 0, 0, &CapabilityError{Name: viewporter.WpViewporterInterface, Cause: fmt.Errorf("fractional scale %g requires viewporter", w.Scale)}
	}
	// Ignore sub-nanopixel floating noise at exact integer boundaries.
	x, y := math.Ceil(float64(w.Width)*w.Scale-1e-9), math.Ceil(float64(w.Height)*w.Scale-1e-9)
	if x > math.MaxInt32 || y > math.MaxInt32 {
		return 0, 0, fmt.Errorf("buffer dimensions overflow")
	}
	return int32(x), int32(y), nil
}

// Present sets both syncobj points on every buffer commit. The caller supplies
// an already-imported wl_buffer and two persistent Wayland timeline objects.
func (w *Window) Present(buffer *core.Buffer, acquire, release *drmsyncobj.WpLinuxDrmSyncobjTimeline, acquirePoint, releasePoint uint64) error {
	if !w.Configured || buffer == nil || acquire == nil || release == nil {
		return fmt.Errorf("unconfigured surface or missing buffer/timeline")
	}
	if !w.FeedbackDone {
		return fmt.Errorf("dmabuf feedback incomplete")
	}
	if !w.FrameReady {
		return fmt.Errorf("frame callback pending")
	}
	pw, ph, err := w.PhysicalSize()
	if err != nil {
		return err
	}
	if w.Viewport != nil {
		if err = w.Viewport.SetDestination(w.Width, w.Height); err != nil {
			return err
		}
	} else if w.Scale > 1 {
		if err = w.Surface.SetBufferScale(int32(w.Scale)); err != nil {
			return err
		}
	}
	if w.Transparent {
		if err = w.Surface.SetOpaqueRegion(nil); err != nil {
			return err
		}
	} else {
		region, e := w.Compositor.CreateRegion()
		if e != nil {
			return e
		}
		if err = region.Add(0, 0, w.Width, w.Height); err == nil {
			err = w.Surface.SetOpaqueRegion(region)
		}
		_ = region.Destroy()
		if err != nil {
			return err
		}
	}
	if err = w.SyncSurface.SetAcquirePoint(acquire, uint32(acquirePoint>>32), uint32(acquirePoint)); err != nil {
		return err
	}
	if err = w.SyncSurface.SetReleasePoint(release, uint32(releasePoint>>32), uint32(releasePoint)); err != nil {
		return err
	}
	if err = w.Surface.Attach(buffer, 0, 0); err != nil {
		return err
	}
	if err = w.Surface.DamageBuffer(0, 0, pw, ph); err != nil {
		return err
	}
	cb, e := w.Surface.Frame()
	if e != nil {
		return e
	}
	cb.OnDone(func(_ uint32) {
		if w.readerStarted.Load() {
			w.post(Event{Kind: FrameEvent})
			return
		}
		w.FrameReady = true
	})
	if err = w.Surface.Commit(); err != nil {
		return err
	}
	w.FrameReady, w.Dirty = false, false
	return nil
}

// DestroySyncSurface stops associating new explicit-sync points with the surface.
// The session calls it after committing a null attachment at shutdown.
func (w *Window) DestroySyncSurface() error {
	if w == nil || w.SyncSurface == nil {
		return nil
	}
	err := w.SyncSurface.Destroy()
	if err == nil {
		w.SyncSurface = nil
	}
	return err
}

// Close destroys children before their parents. The caller first detaches and
// waits for its own GPU work before destroying its Vulkan allocations.
func (w *Window) Close() error {
	if w == nil {
		return nil
	}
	if w.ownsConnection && w.connection != nil && !w.connection.closed {
		return w.connection.Close()
	}
	for _, ev := range w.rawInput {
		CloseEventFD(ev)
	}
	clear(w.rawInput)
	w.rawInput = nil
	// The loop sends protocol destructors before Close; the reader is
	// unblocked by the connection close at the end of this method.
	if w.inputAdapter != nil {
		w.inputAdapter.Close()
		w.inputAdapter = nil
	}
	if w.Seat != nil && w.connection == nil {
		_ = w.releaseSeat()
	}
	w.dropCursorDevice()
	if w.CursorShapes != nil {
		_ = w.CursorShapes.Destroy()
		w.CursorShapes = nil
	}
	if w.Feedback != nil {
		_ = w.Feedback.Destroy()
		w.Feedback = nil
	}
	if w.SyncSurface != nil {
		_ = w.SyncSurface.Destroy()
		w.SyncSurface = nil
	}
	if w.connection != nil && w.Surface != nil {
		delete(w.connection.windows, w.Surface.ID())
		w.connection.routes.Delete(w.Surface.ID())
	}
	if w.LockSurface != nil {
		_ = w.LockSurface.Destroy()
		w.LockSurface = nil
	}
	w.closeLayer() // before wl_surface
	if w.Toplevel != nil {
		_ = w.Toplevel.Destroy()
		w.Toplevel = nil
	}
	if w.XdgSurface != nil {
		_ = w.XdgSurface.Destroy()
		w.XdgSurface = nil
	}
	if w.Fractional != nil {
		_ = w.Fractional.Destroy()
		w.Fractional = nil
	}
	if w.Viewport != nil {
		_ = w.Viewport.Destroy()
		w.Viewport = nil
	}
	if w.Surface != nil {
		_ = w.Surface.Destroy()
		w.Surface = nil
	}
	if w.SyncManager != nil {
		_ = w.SyncManager.Destroy()
		w.SyncManager = nil
	}
	if w.Dmabuf != nil {
		_ = w.Dmabuf.Destroy()
		w.Dmabuf = nil
	}
	if w.ScaleManager != nil {
		_ = w.ScaleManager.Destroy()
		w.ScaleManager = nil
	}
	if w.Viewporter != nil {
		_ = w.Viewporter.Destroy()
		w.Viewporter = nil
	}
	if w.Shell != nil {
		_ = w.Shell.Destroy()
		w.Shell = nil
	}
	// wl_compositor is bound at most at version 6; its release destructor
	// needs version 7, so the proxy is only forgotten locally.
	if w.Compositor != nil {
		if c := w.Compositor.Context(); c != nil {
			c.Unregister(w.Compositor)
		}
		w.Compositor = nil
	}
	if w.connection != nil {
		w.Display = nil
		w.Closed = true
		if w.ownsConnection {
			return w.connection.Close()
		}
		return nil
	}
	if w.Display != nil {
		if w.readerDone != nil {
			close(w.readerStop)
		}
		err := w.Display.Close()
		if w.readerDone != nil {
			<-w.readerDone
			w.readerDone = nil
		}
		for len(w.events) > 0 {
			CloseEventFD(<-w.events)
		}
		w.Display = nil
		return err
	}
	return nil
}

func (w *Window) watchFeedback() {
	var table []linuxdmabuf.FormatEntry
	var indices []uint16
	w.Feedback.OnFormatTable(func(fd *wl.OwnedFD, size uint32) {
		var err error
		table, err = linuxdmabuf.ReadFormatTable(fd, size)
		if err != nil {
			w.feedbackFailure(err)
		}
	})
	w.Feedback.OnMainDevice(func(device []byte) {
		if len(device) != 8 {
			w.feedbackFailure(fmt.Errorf("invalid dmabuf main_device length %d", len(device)))
			return
		}
		if w.readerStarted.Load() {
			w.post(Event{Kind: FeedbackMainDevice, Device: binary.NativeEndian.Uint64(device)})
		} else {
			w.MainDevice = binary.NativeEndian.Uint64(device)
		}
	})
	w.Feedback.OnTrancheFormats(func(data []byte) {
		var err error
		indices, err = linuxdmabuf.TrancheIndices(data, len(table))
		if err != nil {
			w.feedbackFailure(err)
		}
	})
	w.Feedback.OnTrancheDone(func() {
		var tranche []linuxdmabuf.FormatEntry
		for _, i := range indices {
			tranche = append(tranche, table[i])
		}
		if w.readerStarted.Load() {
			w.post(Event{Kind: FeedbackTranche, Tranche: tranche})
		} else {
			w.Tranches = append(w.Tranches, tranche)
		}
		indices = nil
	})
	w.Feedback.OnDone(func() {
		if w.readerStarted.Load() {
			w.post(Event{Kind: FeedbackFormats, Formats: table})
			w.post(Event{Kind: FeedbackComplete})
		} else {
			w.Formats = table
			w.FeedbackDone = true
		}
	})
}

func (w *Window) feedbackFailure(err error) {
	if w.readerStarted.Load() {
		w.post(Event{Kind: FeedbackError, Err: err})
	} else {
		w.feedbackErr = err
	}
}
