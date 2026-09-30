//go:build linux

package wayland

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bnema/wlturbo"
	"github.com/bnema/wlturbo/protocol/core"
	"github.com/bnema/wlturbo/protocol/extsessionlock"
)

// Connection owns one socket, one reader and one seat. All NewWindow, Apply,
// Outputs, AcquireLock and Close calls belong to the same UI owner goroutine.
// Reader callbacks post immutable events; they never mutate owner window state.
type Connection struct {
	Display     *wlturbo.Display
	events      chan Event
	stop        chan struct{}
	done        chan struct{}
	once        sync.Once
	started     atomic.Bool
	windows     map[uint32]*Window
	routes      sync.Map // immutable surface->window identities published by owner
	outputs     map[uint32]Output
	seat        *Window // transport-only seat host, never a rendering window
	lockManager *extsessionlock.ExtSessionLockManager
	lock        *extsessionlock.ExtSessionLock
	lockFinish  bool    // finished event applied
	lockDone    bool    // locked event applied
	held        []Event // lock/output events kept by drainStartup(true); owner only
	closed      bool

	// Reader pause gate. The owner pauses the reader to register handlers on
	// freshly created objects (whose first events may arrive immediately) or to
	// run its own roundtrip. While paused, post parks events instead of
	// blocking, so neither side can wait on the bounded queue.
	socket     net.Conn
	gate       sync.Mutex
	pausing    bool
	pauseSig   chan struct{} // closed when a pause is requested
	parked     []Event
	pauseDepth int // owner goroutine only
	pauseAck   chan struct{}
	resume     chan struct{}
}

// Output identifies the registry lifetime, not merely a reusable monitor name.
type Output struct {
	Global uint32
	Name   string
	Scale  int32
	proxy  *core.Output
}

func ConnectConnection(ctx context.Context, name string) (res *Connection, err error) {
	if ctx == nil {
		return nil, errors.New("nil context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := waylandSocketPath(name)
	if err != nil {
		return nil, err
	}
	socket, err := (&net.Dialer{Timeout: dialTimeout}).DialContext(ctx, "unix", path)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Wayland: %w", err)
	}
	display, err := wlturbo.ConnectFromConn(socket)
	if err != nil {
		return nil, err
	}
	c := &Connection{Display: display, socket: socket, events: make(chan Event, 256), stop: make(chan struct{}), windows: make(map[uint32]*Window), outputs: make(map[uint32]Output), pauseSig: make(chan struct{}), pauseAck: make(chan struct{}, 1), resume: make(chan struct{}, 1)}
	watch := context.AfterFunc(ctx, func() { _ = display.Close() })
	defer func() {
		if !watch() && err == nil {
			err = ctx.Err()
		}
		if err != nil {
			_ = c.Close()
			res = nil
			if ctx.Err() != nil {
				err = errors.Join(ctx.Err(), err)
			}
		}
	}()
	if err = display.Roundtrip(); err != nil {
		return nil, err
	}
	c.seat = &Window{Display: display, connection: c, events: c.events, readerStop: c.stop}
	if err = c.seat.BindSeat(); err != nil {
		return nil, err
	}
	display.Registry().AddGlobalHandler(c)
	display.Registry().AddGlobalRemoveHandler(c)
	for name, g := range display.Registry().GetGlobals() {
		if g.Interface == core.OutputInterface {
			if err = c.bindOutput(name, g.Version); err != nil {
				return nil, err
			}
		}
	}
	if err = display.Roundtrip(); err != nil {
		return nil, err
	}
	if err = c.drainStartup(false); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Connection) bindOutput(global, version uint32) error {
	if version < 4 {
		return &CapabilityError{Name: core.OutputInterface, Cause: fmt.Errorf("output names require version 4, announced %d", version)}
	}
	out := core.NewOutput(c.Display.Context())
	if err := c.Display.Registry().Bind(global, core.OutputInterface, 4, out); err != nil {
		c.Display.Context().Unregister(out)
		return err
	}
	c.outputs[global] = Output{Global: global, Scale: 1, proxy: out}
	out.OnName(func(name string) { c.post(Event{Kind: OutputNamed, Output: Output{Global: global, Name: name}}) })
	out.OnScale(func(scale int32) { c.post(Event{Kind: OutputScale, Output: Output{Global: global, Scale: scale}}) })
	out.OnDone(func() { c.post(Event{Kind: OutputAdded, Output: Output{Global: global}}) })
	return nil
}

func (c *Connection) HandleRegistryGlobal(e wlturbo.RegistryGlobalEvent) {
	if e.Interface == core.OutputInterface {
		c.post(Event{Kind: OutputGlobal, Output: Output{Global: e.Name}, Version: e.Version})
	}
}
func (c *Connection) HandleRegistryGlobalRemove(e wlturbo.RegistryGlobalRemoveEvent) {
	c.post(Event{Kind: OutputRemoved, Output: Output{Global: e.Name}})
}
func (c *Connection) post(ev Event) {
	c.gate.Lock()
	if c.pausing {
		c.parked = append(c.parked, ev)
		c.gate.Unlock()
		return
	}
	sig := c.pauseSig
	c.gate.Unlock()
	select {
	case c.events <- ev:
	case <-c.stop:
		CloseEventFD(ev)
	case <-sig:
		c.gate.Lock()
		c.parked = append(c.parked, ev)
		c.gate.Unlock()
	}
}

// pauseReader stops the reader between two Dispatch calls and returns once it
// is parked. The nested calls of one owner goroutine share the first pause;
// the returned func ends it. Without a running reader it does nothing.
func (c *Connection) pauseReader() (resume func()) {
	if !c.started.Load() || c.done == nil {
		return func() {}
	}
	c.pauseDepth++
	if c.pauseDepth > 1 {
		return func() { c.pauseDepth-- }
	}
	c.gate.Lock()
	c.pausing = true
	close(c.pauseSig)
	c.gate.Unlock()
	if c.socket != nil {
		_ = c.socket.SetReadDeadline(time.Unix(1, 0)) // wakes a blocked read
	}
	select {
	case <-c.pauseAck:
	case <-c.done:
	}
	if c.socket != nil {
		_ = c.socket.SetReadDeadline(time.Time{})
	}
	return func() {
		c.pauseDepth--
		if c.pauseDepth > 0 {
			return
		}
		c.gate.Lock()
		c.pausing = false
		c.pauseSig = make(chan struct{})
		c.gate.Unlock()
		select {
		case c.resume <- struct{}{}:
		default:
		}
	}
}

// flushParked re-posts events held while paused; reader goroutine only.
func (c *Connection) flushParked() {
	c.gate.Lock()
	held := c.parked
	c.parked = nil
	c.gate.Unlock()
	for _, ev := range held {
		c.post(ev)
	}
}

// ErrLockFinished is wrapped by platform errors when the compositor refused or
// ended the session lock (ext_session_lock_v1.finished).
var ErrLockFinished = errors.New("wayland: session lock finished")

// holdDuringSetup reports events the lock owner must see through its own event
// handling: lock state and output lifetimes. They are never applied by the
// startup drain, which would hide them from the owner.
func holdDuringSetup(k EventKind) bool {
	switch k {
	case LockAcquired, LockFinished, OutputGlobal, OutputNamed, OutputScale, OutputAdded, OutputRemoved:
		return true
	}
	return false
}

// drainStartup applies queued events before the reader starts. With hold set
// (window creation after the lock exists) lock and output events are kept, in
// order, for TakeHeld instead of being applied; a held `finished` fails setup.
func (c *Connection) drainStartup(hold bool) error {
	for {
		select {
		case ev := <-c.events:
			if hold && ev.Window == nil && holdDuringSetup(ev.Kind) {
				c.held = append(c.held, ev)
				if ev.Kind == LockFinished {
					c.lockFinish = true // Close then sends the plain destroy
					return ErrLockFinished
				}
				continue
			}
			if err := c.Apply(ev); err != nil {
				return err
			}
		default:
			return nil
		}
	}
}

// TakeHeld returns the lock and output events kept during window setup, oldest
// first. The owner feeds them to its normal event handling right after it
// starts the reader, so they precede everything the reader posts later.
func (c *Connection) TakeHeld() []Event {
	held := c.held
	c.held = nil
	return held
}

// Outputs returns a copy of owner-applied output lifetimes. Call after applying
// OutputAdded/Removed events; names and scales are never read from the reader.
func (c *Connection) Outputs() []Output {
	result := make([]Output, 0, len(c.outputs))
	for _, out := range c.outputs {
		if out.Name != "" {
			result = append(result, out)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Global < result[j].Global })
	return result
}

// AcquireLock creates the sole lock object for this connection. Locked and
// Finished are transport events, not an authentication or unlock policy.
func (c *Connection) AcquireLock() error {
	if c.closed || c.lock != nil {
		return errors.New("connection closed or lock already acquired")
	}
	defer c.pauseReader()() // locked/finished may follow the request immediately
	if c.lockManager == nil {
		manager := extsessionlock.NewExtSessionLockManager(c.Display.Context())
		if _, err := c.Display.Registry().BindNegotiated(extsessionlock.ExtSessionLockManagerInterface, 1, manager); err != nil {
			c.Display.Context().Unregister(manager)
			return &CapabilityError{Name: extsessionlock.ExtSessionLockManagerInterface, Cause: err}
		}
		c.lockManager = manager
	}
	lock, err := c.lockManager.Lock()
	if err != nil {
		return err
	}
	c.lock = lock
	lock.OnLocked(func() { c.post(Event{Kind: LockAcquired}) })
	lock.OnFinished(func() { c.post(Event{Kind: LockFinished}) })
	if !c.started.Load() {
		// The compositor answers with locked or finished immediately; queue it
		// now so window setup can hold it for the owner (see drainStartup).
		return c.Display.Roundtrip()
	}
	return nil
}

// Locked and Finished report the owner-applied lock state.
func (c *Connection) Locked() bool   { return c.lockDone && !c.lockFinish }
func (c *Connection) Finished() bool { return c.lockFinish }

// UnlockAndDestroy sends unlock_and_destroy and roundtrips so the compositor
// has processed it before the caller closes anything. It is only valid after
// the locked event; the platform supplies no authorization policy. The reader
// is paused for the roundtrip, so events it posts are parked, never lost.
func (c *Connection) UnlockAndDestroy() error {
	if c.lock == nil {
		return errors.New("no session lock")
	}
	if !c.Locked() {
		return errors.New("unlock before the locked event")
	}
	defer c.pauseReader()()
	if err := c.lock.UnlockAndDestroy(); err != nil {
		return err
	}
	c.lock, c.lockDone = nil, false
	return c.Display.Roundtrip()
}

func (c *Connection) StartReader() <-chan Event {
	c.once.Do(func() {
		c.started.Store(true)
		for _, w := range c.windows {
			w.readerStarted.Store(true)
		}
		c.done = make(chan struct{})
		go func() {
			defer close(c.done)
			for {
				select {
				case <-c.stop:
					return
				default:
				}
				c.gate.Lock()
				paused := c.pausing
				c.gate.Unlock()
				if paused {
					c.pauseAck <- struct{}{}
					select {
					case <-c.resume:
						c.flushParked()
						continue
					case <-c.stop:
						return
					}
				}
				err := c.Display.Dispatch()
				if errors.Is(err, os.ErrDeadlineExceeded) {
					continue // a pause request interrupted the read
				}
				if err != nil {
					c.post(Event{Kind: TransportError, Err: err})
					return
				}
			}
		}()
	})
	return c.events
}
func (c *Connection) Events() <-chan Event { return c.events }

// Apply handles connection events or routes a tagged event to its window.
func (c *Connection) Apply(ev Event) error {
	if ev.Window != nil {
		return ev.Window.Apply(ev)
	}
	switch ev.Kind {
	case OutputGlobal:
		if _, exists := c.outputs[ev.Output.Global]; !exists {
			defer c.pauseReader()() // name/scale/done follow the bind
			return c.bindOutput(ev.Output.Global, ev.Version)
		}
	case OutputNamed, OutputScale:
		out, ok := c.outputs[ev.Output.Global]
		if !ok {
			return nil
		}
		if ev.Kind == OutputNamed {
			out.Name = ev.Output.Name
		} else if ev.Output.Scale > 0 {
			out.Scale = ev.Output.Scale
		}
		c.outputs[out.Global] = out
	case OutputAdded:
	case OutputRemoved:
		if out, ok := c.outputs[ev.Output.Global]; ok {
			_ = out.proxy.Release()
			delete(c.outputs, ev.Output.Global)
		}
	case SeatCapabilities, SeatRemoved:
		oldPointer := c.seat.Pointer
		defer c.pauseReader()() // keymap/capability events follow get_keyboard
		if ev.Kind == SeatRemoved || (ev.Capabilities&1 == 0 && oldPointer != nil) {
			// Shape devices die before the wl_pointer they were created from
			// is released; their enter serials belonged to that pointer.
			for _, w := range c.windows {
				w.dropCursorDevice()
			}
		}
		if err := c.seat.ApplySeat(ev); err != nil {
			return err
		}
		for _, w := range c.windows {
			if oldPointer != c.seat.Pointer {
				w.dropCursorDevice()
				w.queueInput([]Input{{Kind: "leave"}})
			}
			w.Seat, w.Pointer, w.WLKeyboard = c.seat.Seat, c.seat.Pointer, c.seat.WLKeyboard
			if w.LockSurface != nil {
				w.Seat = nil
			}
			w.pointerGen = c.seat.pointerGen
			if ev.Kind == SeatRemoved || ev.Capabilities&2 == 0 {
				w.ownerFocusOut()
			}
		}
	case InputFocusOut:
		for _, w := range c.windows {
			w.ownerFocusOut()
		}
	case TransportError:
		return ev.Err
	case LockAcquired:
		c.lockDone = true
	case LockFinished:
		c.lockFinish = true
	default:
		return fmt.Errorf("unrouted connection event %d", ev.Kind)
	}
	return nil
}

func (c *Connection) Close() error {
	if c == nil || c.closed {
		return nil
	}
	c.closed = true
	close(c.stop)
	for _, w := range c.windows {
		_ = w.Close()
	}
	if c.lock != nil && c.lockFinish {
		// Refused, or ended after locked: the compositor ended the lock, so a
		// plain destroy is valid (unlock_and_destroy already cleared c.lock).
		_ = c.lock.Destroy()
	}
	if c.lockManager != nil {
		_ = c.lockManager.Destroy()
	}
	err := c.Display.Close() // closing an acquired owner never requests unlock
	if c.done != nil {
		<-c.done
	}
	if c.seat != nil {
		_ = c.seat.releaseSeat()
	}
	for len(c.events) > 0 {
		CloseEventFD(<-c.events)
	}
	for _, ev := range c.held {
		CloseEventFD(ev)
	}
	c.held = nil
	c.gate.Lock()
	for _, ev := range c.parked {
		CloseEventFD(ev)
	}
	c.parked = nil
	c.gate.Unlock()
	return err
}
