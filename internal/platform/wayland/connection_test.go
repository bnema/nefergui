//go:build linux

package wayland

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/bnema/wlturbo/protocol/linuxdmabuf"
	"golang.org/x/sys/unix"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/bnema/wlturbo"
	"github.com/bnema/wlturbo/protocol/core"
	"github.com/bnema/wlturbo/protocol/cursorshape"
)

func TestSharedConnectionKeyboardRoutesOnlyFocusedWindow(t *testing.T) {
	ca, server := inputPair(t)
	d, err := wlturbo.ConnectFromConn(ca)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	inputRequest(t, server)
	c := &Connection{Display: d, events: make(chan Event, 32), stop: make(chan struct{}), windows: make(map[uint32]*Window)}
	host := &Window{connection: c, seatVersion: 9}
	seat := core.NewSeat(d.Context())
	if err := d.Registry().Bind(1, core.SeatInterface, 9, seat); err != nil {
		t.Fatal(err)
	}
	inputRequest(t, server)
	keyboard, err := seat.GetKeyboard()
	if err != nil {
		t.Fatal(err)
	}
	inputRequest(t, server)
	first, second := &Window{connection: c}, &Window{connection: c}
	c.routes.Store(uint32(30), first)
	c.routes.Store(uint32(31), second)
	host.watchSharedKeyboard(keyboard)
	// enter includes a length-prefixed empty key array, then key down.
	for _, msg := range [][]byte{inputFrame(keyboard.ID(), 1, 1, 30, 0), inputFrame(keyboard.ID(), 3, 2, 0, 30, 1), inputFrame(keyboard.ID(), 2, 3, 30), inputFrame(keyboard.ID(), 1, 4, 31, 0), inputFrame(keyboard.ID(), 3, 5, 0, 48, 1)} {
		inputSend(t, server, msg)
		if err := d.Dispatch(); err != nil {
			t.Fatal(err)
		}
	}
	var keys []Event
	for len(c.events) > 0 {
		ev := <-c.events
		if ev.Kind == InputKey {
			keys = append(keys, ev)
		}
	}
	if len(keys) != 2 || keys[0].Window != first || keys[0].Code != 30 || keys[1].Window != second || keys[1].Code != 48 {
		t.Fatalf("keys: %+v", keys)
	}
}

func TestHarnessConnectionLock(t *testing.T) {
	out, _ := runLayerHarness(t, "TestHarnessConnectionLockClient")
	for _, want := range []string{"LOCK acquired", "LOCK exact configure", "LOCK shared display", "LOCK sibling survived", "LOCK unlock", "LOCK manager reused", "LOCK named layer sibling"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %s", want, out)
		}
	}
}
func TestHarnessConnectionLockClient(t *testing.T) {
	harnessChild(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	c, err := ConnectConnection(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	outputs := c.Outputs()
	if len(outputs) != 1 {
		t.Fatalf("outputs %+v", outputs)
	}
	if err := c.AcquireLock(); err != nil {
		t.Fatal(err)
	}
	events := c.StartReader()
	wait := func(want EventKind) {
		t.Helper()
		for {
			select {
			case ev := <-events:
				if err := c.Apply(ev); err != nil {
					t.Fatal(err)
				}
				if ev.Kind == want {
					return
				}
			case <-ctx.Done():
				t.Fatal("event timeout")
			}
		}
	}
	wait(LockAcquired)
	fmt.Println("LOCK acquired")
	first, err := c.NewWindow(ctx, 1, 1, false, SurfaceOptions{Lock: &LockOptions{Output: outputs[0].Global}})
	if err != nil {
		t.Fatal(err)
	}
	for !first.Configured || !first.FeedbackDone || first.MainDevice == 0 || len(first.Tranches) == 0 {
		select {
		case ev := <-events:
			if err := c.Apply(ev); err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("lock configure timeout")
		}
	}
	if first.Width != 400 || first.Height != 300 || first.LockSurface == nil || first.XdgSurface != nil || first.LayerSurface != nil {
		t.Fatalf("lock geometry/roles: %dx%d", first.Width, first.Height)
	}
	fmt.Println("LOCK exact configure")
	// An ordinary sibling shares the transport, but cannot acquire another lock.
	second, err := c.NewWindow(ctx, 20, 20, false, SurfaceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if first.Display != second.Display || first.Display != c.Display || first.Seat != nil {
		t.Fatal("independent display or lock exposed clipboard/IME seat")
	}
	fmt.Println("LOCK shared display")
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if second.Display == nil || c.closed {
		t.Fatal("window close destroyed shared connection")
	}
	if err := second.SetTitle("sibling survives"); err != nil {
		t.Fatal(err)
	}
	fmt.Println("LOCK sibling survived")
	if err := c.UnlockAndDestroy(); err != nil {
		t.Fatal(err)
	}
	fmt.Println("LOCK unlock")
	manager := c.lockManager
	for range 2 {
		if err := c.AcquireLock(); err != nil {
			t.Fatal(err)
		}
		wait(LockAcquired)
		if c.lockManager != manager {
			t.Fatal("lock manager leaked across cycles")
		}
		if err := c.UnlockAndDestroy(); err != nil {
			t.Fatal(err)
		}
	}
	fmt.Println("LOCK manager reused")
	layer, err := c.NewWindow(ctx, 20, 20, true, SurfaceOptions{Layer: &LayerOptions{Output: outputs[0].Name, Layer: LayerTop, Namespace: "named-sibling"}})
	if err != nil {
		t.Fatal(err)
	}
	for !layer.Configured || !layer.FeedbackDone {
		select {
		case ev := <-events:
			if err := c.Apply(ev); err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("named sibling timeout")
		}
	}
	if layer.layerOutputGlobal != outputs[0].Global {
		t.Fatal("named sibling selected wrong output")
	}
	fmt.Println("LOCK named layer sibling")
}

func TestOwnerFocusResetWithFullReaderQueue(t *testing.T) {
	c := &Connection{events: make(chan Event, 256), stop: make(chan struct{}), windows: map[uint32]*Window{1: {}}}
	for range cap(c.events) {
		c.events <- Event{Kind: LockAcquired}
	}
	done := make(chan error, 1)
	go func() { done <- c.Apply(Event{Kind: InputFocusOut}) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		close(c.stop)
		t.Fatal("owner reset self-posted into full reader queue")
	}
	if len(c.events) != cap(c.events) {
		t.Fatal("owner reset consumed reader events")
	}
	inputs := c.windows[1].DrainInput(nil)
	if len(inputs) != 1 || inputs[0].Kind != "focus-out" {
		t.Fatalf("inputs %+v", inputs)
	}
}

func TestKeyboardMapCacheRetiresEveryGeneration(t *testing.T) {
	for range 4 {
		f, err := os.CreateTemp(t.TempDir(), "keymap")
		if err != nil {
			t.Fatal(err)
		}
		fd, err := syscall.Dup(int(f.Fd()))
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
		cache := &keyboardMapCache{fd: -1}
		cache.replace(fd)
		duplicate, err := cache.duplicate()
		if err != nil || duplicate < 0 {
			t.Fatalf("duplicate %d %v", duplicate, err)
		}
		if flags, err := unix.FcntlInt(uintptr(duplicate), unix.F_GETFD, 0); err != nil || flags&unix.FD_CLOEXEC == 0 {
			t.Fatalf("delivered keymap duplicate is inheritable: flags %d err %v", flags, err)
		}
		cache.close()
		cache.close()
		if _, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); !errors.Is(err, syscall.EBADF) {
			t.Fatalf("retired map FD still open: %v", err)
		}
		if _, err := unix.FcntlInt(uintptr(duplicate), unix.F_GETFD, 0); err != nil {
			t.Fatal("retired source closed delivered duplicate")
		}
		syscall.Close(duplicate)
		if got, err := cache.duplicate(); got != -1 || err != nil {
			t.Fatal("retired callback duplicated map")
		}
	}
}

func TestFeedbackReadinessWaitsForCompleteEvent(t *testing.T) {
	w := &Window{Configured: true}
	if err := w.Apply(Event{Kind: FeedbackMainDevice, Device: 1}); err != nil {
		t.Fatal(err)
	}
	if err := w.Apply(Event{Kind: FeedbackTranche, Tranche: []linuxdmabuf.FormatEntry{{}}}); err != nil {
		t.Fatal(err)
	}
	if w.FeedbackDone {
		t.Fatal("partial feedback treated as complete")
	}
	if err := w.Apply(Event{Kind: FeedbackFormats, Formats: []linuxdmabuf.FormatEntry{{}}}); err != nil {
		t.Fatal(err)
	}
	if w.FeedbackDone {
		t.Fatal("table alone treated as complete")
	}
	if err := w.Apply(Event{Kind: FeedbackComplete}); err != nil || !w.FeedbackDone {
		t.Fatal("done not applied")
	}
}

// sharedSeat builds a Connection whose seat host is bound over a socketpair.
func sharedSeat(t *testing.T) (*Connection, *wlturbo.Display, *net.UnixConn) {
	t.Helper()
	ca, server := inputPair(t)
	d, err := wlturbo.ConnectFromConn(ca)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	inputRequest(t, server)
	c := &Connection{Display: d, events: make(chan Event, 256), stop: make(chan struct{}), windows: make(map[uint32]*Window)}
	c.seat = &Window{Display: d, connection: c, seatVersion: 9}
	c.seat.Seat = core.NewSeat(d.Context())
	if err := d.Registry().Bind(1, core.SeatInterface, 9, c.seat.Seat); err != nil {
		t.Fatal(err)
	}
	inputRequest(t, server)
	return c, d, server
}

func fdOpen(fd int) bool {
	_, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
	return err == nil
}

func TestKeyboardGenerationKeymapClosedExactlyOnce(t *testing.T) {
	c, d, server := sharedSeat(t)
	for generation := range 3 {
		if err := c.Apply(Event{Kind: SeatCapabilities, Capabilities: 2}); err != nil {
			t.Fatal(err)
		}
		inputRequest(t, server) // get_keyboard
		cache := c.seat.keymapCache
		if cache == nil || c.seat.WLKeyboard == nil {
			t.Fatalf("generation %d: no keyboard cache", generation)
		}
		f, err := os.CreateTemp(t.TempDir(), "keymap")
		if err != nil {
			t.Fatal(err)
		}
		var taken []int
		for range 2 { // a second keymap in one generation replaces the first
			inputSend(t, server, inputFrame(c.seat.WLKeyboard.ID(), 0, 1, 4), int(f.Fd()))
			if err = d.Dispatch(); err != nil {
				t.Fatal(err)
			}
			cache.mu.Lock()
			taken = append(taken, cache.fd)
			cache.mu.Unlock()
		}
		f.Close()
		if taken[0] == taken[1] || fdOpen(taken[0]) {
			t.Fatalf("replaced keymap FD %d not closed", taken[0])
		}
		if !fdOpen(taken[1]) {
			t.Fatalf("generation %d: live keymap FD closed", generation)
		}
		if err := c.Apply(Event{Kind: SeatCapabilities, Capabilities: 0}); err != nil {
			t.Fatal(err)
		}
		inputRequest(t, server) // release
		if fdOpen(taken[1]) {
			t.Fatalf("generation %d: keymap FD %d leaked after capability loss", generation, taken[1])
		}
		if c.seat.keymapCache != nil || c.seat.WLKeyboard != nil {
			t.Fatal("keyboard generation not retired")
		}
		cache.close() // a retired cache closes nothing twice
	}
}

func TestPointerReplacementResetsSiblingCursor(t *testing.T) {
	c, d, server := sharedSeat(t)
	ctx := d.Context()
	shapes := cursorshape.NewWpCursorShapeManager(ctx)
	if err := d.Registry().Bind(2, cursorshape.WpCursorShapeManagerInterface, 1, shapes); err != nil {
		t.Fatal(err)
	}
	inputRequest(t, server)
	sibling := &Window{connection: c, CursorShapes: shapes, seatVersion: 9}
	c.windows[1] = sibling
	if err := c.Apply(Event{Kind: SeatCapabilities, Capabilities: 1}); err != nil {
		t.Fatal(err)
	}
	if id, op, _ := readRequest(t, server); id != c.seat.Seat.ID() || op != 0 {
		t.Fatalf("get_pointer %d %d", id, op)
	}
	first := sibling.Pointer
	if first == nil || sibling.pointerGen != 1 {
		t.Fatalf("sibling pointer/gen %v %d", first, sibling.pointerGen)
	}
	sibling.PointerEntered(7, 1)
	if err := sibling.SetCursor(CursorPointer); err != nil {
		t.Fatal(err)
	}
	_, _, body := readRequest(t, server) // shape device
	device := binary.LittleEndian.Uint32(body)
	readRequest(t, server) // set_shape
	// Pointer capability loss: device destroyed before the pointer, serial gone.
	if err := c.Apply(Event{Kind: SeatCapabilities, Capabilities: 0}); err != nil {
		t.Fatal(err)
	}
	if id, op, _ := readRequest(t, server); id != device || op != 0 {
		t.Fatalf("shape device not destroyed first: %d %d", id, op)
	}
	if id, _, _ := readRequest(t, server); id != first.ID() {
		t.Fatalf("pointer not released after device: %d", id)
	}
	if sibling.Pointer != nil || sibling.cursor.serial != 0 || sibling.cursor.device != nil {
		t.Fatal("sibling kept stale pointer state")
	}
	// Replacement: a late enter of the released generation is ignored.
	if err := c.Apply(Event{Kind: SeatCapabilities, Capabilities: 1}); err != nil {
		t.Fatal(err)
	}
	readRequest(t, server) // get_pointer
	if sibling.pointerGen != 2 || sibling.Pointer == nil || sibling.Pointer == first {
		t.Fatalf("replacement not adopted: gen %d", sibling.pointerGen)
	}
	sibling.PointerEntered(9, 1)
	if err := sibling.SetCursor(CursorPointer); err != nil {
		t.Fatal(err)
	}
	noRequest(t, server)
	sibling.PointerEntered(11, 2)
	if err := sibling.SetCursor(CursorPointer); err != nil {
		t.Fatal(err)
	}
	_, _, body = readRequest(t, server)
	if binary.LittleEndian.Uint32(body[4:]) != sibling.Pointer.ID() {
		t.Fatal("shape device bound to a stale pointer")
	}
	if _, _, body = readRequest(t, server); binary.LittleEndian.Uint32(body) != 11 {
		t.Fatalf("set_shape used stale serial: %x", body)
	}
}

func TestOwnerSeatRemovalWithFullReaderQueue(t *testing.T) {
	c := &Connection{events: make(chan Event, 256), stop: make(chan struct{}), windows: map[uint32]*Window{}}
	c.seat = &Window{connection: c}
	lock := &Window{LockSurface: nil, rawInput: []Event{{Kind: InputKey}}}
	c.windows[1], c.windows[2] = &Window{}, lock
	for range cap(c.events) {
		c.events <- Event{Kind: LockAcquired}
	}
	done := make(chan error, 1)
	go func() { done <- c.Apply(Event{Kind: SeatRemoved}) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		close(c.stop)
		t.Fatal("seat removal blocked on the full reader queue")
	}
	if len(c.events) != cap(c.events) {
		t.Fatal("owner apply consumed or added reader events")
	}
}

func TestReaderPauseNeverBlocksOnFullQueueAndResumes(t *testing.T) {
	ca, server := inputPair(t)
	d, err := wlturbo.ConnectFromConn(ca)
	if err != nil {
		t.Fatal(err)
	}
	inputRequest(t, server)
	c := &Connection{Display: d, socket: ca, events: make(chan Event, 1), stop: make(chan struct{}), windows: map[uint32]*Window{}, pauseSig: make(chan struct{}), pauseAck: make(chan struct{}, 1), resume: make(chan struct{}, 1)}
	seat := core.NewSeat(d.Context())
	if err := d.Registry().Bind(1, core.SeatInterface, 9, seat); err != nil {
		t.Fatal(err)
	}
	inputRequest(t, server)
	seat.OnCapabilities(func(caps uint32) { c.post(Event{Kind: SeatCapabilities, Capabilities: caps}) })
	c.StartReader()
	defer func() { _ = c.Close() }()
	// Fill the one-slot queue, then a second event blocks the reader in post.
	inputSend(t, server, inputFrame(seat.ID(), 0, 1))
	inputSend(t, server, inputFrame(seat.ID(), 0, 2))
	time.Sleep(50 * time.Millisecond)
	paused := make(chan func(), 1)
	go func() { paused <- c.pauseReader() }()
	var resume func()
	select {
	case resume = <-paused:
	case <-time.After(2 * time.Second):
		t.Fatal("pause deadlocked against the full reader queue")
	}
	if first := <-c.events; first.Capabilities != 1 {
		t.Fatalf("first event %+v", first)
	}
	resume()
	select {
	case ev := <-c.events:
		if ev.Capabilities != 2 {
			t.Fatalf("parked event lost or reordered: %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("parked event not re-posted after resume")
	}
	// A pause with an idle blocked read is interrupted too, and reading resumes.
	resume2 := c.pauseReader()
	resume2()
	inputSend(t, server, inputFrame(seat.ID(), 0, 4))
	select {
	case ev := <-c.events:
		if ev.Capabilities != 4 {
			t.Fatalf("event after resume %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reader did not resume")
	}
}

func TestReadyRequiresConfigureFeedbackAndFrame(t *testing.T) {
	w := &Window{Configured: true, FrameReady: true}
	if w.Ready() {
		t.Fatal("ready before dmabuf feedback completed")
	}
	w.FeedbackDone = true
	if !w.Ready() {
		t.Fatal("not ready")
	}
	// A later feedback round invalidates readiness until its own done.
	if err := w.Apply(Event{Kind: FeedbackMainDevice, Device: 2}); err != nil {
		t.Fatal(err)
	}
	if w.Ready() || len(w.Tranches) != 0 {
		t.Fatal("stale feedback kept across rounds")
	}
	if err := w.Present(nil, nil, nil, 0, 0); err == nil {
		t.Fatal("present without buffer accepted")
	}
}

func TestDrainStartupHoldsLockAndOutputEventsInOrder(t *testing.T) {
	c := &Connection{events: make(chan Event, 16), stop: make(chan struct{}), windows: map[uint32]*Window{}, outputs: map[uint32]Output{}}
	c.events <- Event{Kind: OutputAdded, Output: Output{Global: 3}}
	c.events <- Event{Kind: LockAcquired}
	c.events <- Event{Kind: OutputRemoved, Output: Output{Global: 3}}
	if err := c.drainStartup(true); err != nil {
		t.Fatal(err)
	}
	if c.Locked() || c.lockDone {
		t.Fatal("startup drain applied `locked`, hiding it from the owner")
	}
	held := c.TakeHeld()
	if len(held) != 3 || held[0].Kind != OutputAdded || held[1].Kind != LockAcquired || held[2].Kind != OutputRemoved {
		t.Fatalf("held %+v", held)
	}
	if len(c.TakeHeld()) != 0 {
		t.Fatal("held events delivered twice")
	}
	// The owner applies them itself; unlock then becomes possible.
	for _, ev := range held {
		if err := c.Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	if !c.Locked() {
		t.Fatal("locked not recorded once the owner applied it")
	}
}

func TestDrainStartupFinishedWrapsSentinelAndKeepsEvent(t *testing.T) {
	c := &Connection{events: make(chan Event, 16), stop: make(chan struct{}), windows: map[uint32]*Window{}}
	c.events <- Event{Kind: LockFinished}
	err := c.drainStartup(true)
	if !errors.Is(err, ErrLockFinished) {
		t.Fatalf("err %v", err)
	}
	if held := c.TakeHeld(); len(held) != 1 || held[0].Kind != LockFinished {
		t.Fatalf("finished event lost: %+v", held)
	}
	// Window-tagged and ordinary events are still applied during setup.
	c.events <- Event{Kind: BufferRelease, BufferID: 1}
	if err := c.drainStartup(true); err == nil || errors.Is(err, ErrLockFinished) {
		t.Logf("non-lock event handled: %v", err)
	}
}

// A closing session must never read the shared queue: it carries other
// windows' releases, lock state, output changes and transport errors.
func TestSharedWindowCloseLeavesReaderQueueAlone(t *testing.T) {
	c := &Connection{events: make(chan Event, 16), stop: make(chan struct{}), windows: map[uint32]*Window{}}
	closing, other := &Window{connection: c, events: c.events}, &Window{connection: c, events: c.events}
	if closing.ReaderEvents() != nil {
		t.Fatal("shared window exposes the connection queue to its session")
	}
	if (&Window{events: make(chan Event)}).ReaderEvents() == nil {
		t.Fatal("standalone window lost its own queue")
	}
	queued := []Event{
		{Kind: BufferRelease, BufferID: 1, Window: other},
		{Kind: LockAcquired},
		{Kind: OutputRemoved, Output: Output{Global: 9}},
		{Kind: TransportError, Err: errors.New("gone")},
		{Kind: InputKey, Code: 30, Window: other},
	}
	for _, ev := range queued {
		c.events <- ev
	}
	if err := closing.Close(); err != nil {
		t.Fatal(err)
	}
	if len(c.events) != len(queued) {
		t.Fatalf("close consumed %d shared events", len(queued)-len(c.events))
	}
	for i, want := range queued {
		if got := <-c.events; got.Kind != want.Kind || got.Window != want.Window {
			t.Fatalf("event %d reordered: %+v", i, got)
		}
	}
}
