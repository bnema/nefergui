//go:build linux

package wayland

import (
	"encoding/binary"
	"net"
	"os"
	"testing"
	"time"

	"github.com/bnema/wlturbo"
	"github.com/bnema/wlturbo/protocol/core"
	"golang.org/x/sys/unix"
)

func inputPair(t *testing.T) (*net.UnixConn, *net.UnixConn) {
	t.Helper()
	f, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	a := os.NewFile(uintptr(f[0]), "client")
	b := os.NewFile(uintptr(f[1]), "server")
	ca, e := net.FileConn(a)
	_ = a.Close()
	if e != nil {
		t.Fatal(e)
	}
	cb, e := net.FileConn(b)
	_ = b.Close()
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = ca.Close(); _ = cb.Close() })
	return ca.(*net.UnixConn), cb.(*net.UnixConn)
}
func inputFrame(id uint32, op uint16, body ...uint32) []byte {
	b := make([]byte, 8+len(body)*4)
	binary.LittleEndian.PutUint32(b, id)
	binary.LittleEndian.PutUint32(b[4:], uint32(len(b))<<16|uint32(op))
	for i, v := range body {
		binary.LittleEndian.PutUint32(b[8+i*4:], v)
	}
	return b
}
func inputSend(t *testing.T, p *net.UnixConn, msg []byte, fd ...int) {
	t.Helper()
	var oob []byte
	if len(fd) > 0 {
		oob = unix.UnixRights(fd...)
	}
	if _, _, err := p.WriteMsgUnix(msg, oob, nil); err != nil {
		t.Fatal(err)
	}
}
func inputRequest(t *testing.T, p *net.UnixConn) {
	t.Helper()
	_ = p.SetReadDeadline(time.Now().Add(time.Second))
	b := make([]byte, 256)
	if _, err := p.Read(b); err != nil {
		t.Fatal(err)
	}
}
func TestPointerFrameOverSocketpair(t *testing.T) {
	ca, server := inputPair(t)
	d, err := wlturbo.ConnectFromConn(ca)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	inputRequest(t, server)
	ctx := d.Context()
	seat := core.NewSeat(ctx)
	if err = d.Registry().Bind(1, core.SeatInterface, 9, seat); err != nil {
		t.Fatal(err)
	}
	inputRequest(t, server)
	p, err := seat.GetPointer()
	if err != nil {
		t.Fatal(err)
	}
	inputRequest(t, server)
	surface := core.NewSurface(ctx)
	surface.SetID(30)
	ctx.Register(surface)
	w := &Window{Surface: surface, seatVersion: 9, events: make(chan Event, 10), readerStop: make(chan struct{})}
	w.watchPointer(p)
	for _, msg := range [][]byte{
		inputFrame(p.ID(), 0, 1, 30, 256*12, 256*7), // enter: surface-local logical pixels
		inputFrame(p.ID(), 2, 2, 256*14, 256*8),
		inputFrame(p.ID(), 6, 1), // wheel source
		inputFrame(p.ID(), 4, 3, 0, 256*5),
		inputFrame(p.ID(), 9, 0, 120),
		inputFrame(p.ID(), 3, 4, 4, 0x110, 1),
		inputFrame(p.ID(), 5),
	} {
		inputSend(t, server, msg)
		if err = d.Dispatch(); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case ev := <-w.events:
		if ev.Kind != InputFrame || len(ev.Inputs) != 5 {
			t.Fatalf("frame: %+v", ev)
		}
		if !ev.Inputs[0].Enter || ev.Inputs[0].Serial != 1 || ev.Inputs[0].PointerGen != w.pointerGen || ev.Inputs[0].X != 12 || ev.Inputs[1].X != 14 || ev.Inputs[2].DY != 5 || ev.Inputs[2].AxisSource != 1 || ev.Inputs[3].Value120 != 120 || ev.Inputs[4].Kind != "press" {
			t.Fatalf("inputs: %+v", ev.Inputs)
		}
	default:
		t.Fatal("missing pointer frame")
	}
}
func TestKeyboardFDAndRepeatOverSocketpair(t *testing.T) {
	ca, server := inputPair(t)
	d, err := wlturbo.ConnectFromConn(ca)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	inputRequest(t, server)
	ctx := d.Context()
	seat := core.NewSeat(ctx)
	if err = d.Registry().Bind(1, core.SeatInterface, 9, seat); err != nil {
		t.Fatal(err)
	}
	inputRequest(t, server)
	k, err := seat.GetKeyboard()
	if err != nil {
		t.Fatal(err)
	}
	inputRequest(t, server)
	surface := core.NewSurface(ctx)
	surface.SetID(30)
	ctx.Register(surface)
	w := &Window{Surface: surface, events: make(chan Event, 10), readerStop: make(chan struct{})}
	w.watchKeyboard(k)
	f, err := os.CreateTemp(t.TempDir(), "keymap")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	inputSend(t, server, inputFrame(k.ID(), 0, 0, 5), int(f.Fd()))
	if err = d.Dispatch(); err != nil {
		t.Fatal(err)
	}
	ev := <-w.events
	if ev.Kind != InputKeymap || ev.FD < 0 {
		t.Fatalf("keymap: %+v", ev)
	}
	adapter, err := NewInputAdapter("")
	if err != nil {
		t.Skipf("xkb unavailable: %v", err)
	}
	defer adapter.Close()
	if _, err = adapter.ApplyInput(ev, time.Now()); err == nil {
		t.Fatal("unsupported keymap accepted")
	}
	if err = unix.Fstat(ev.FD, &unix.Stat_t{}); err != unix.EBADF {
		t.Fatalf("fd not closed: %v", err)
	}
	inputSend(t, server, inputFrame(k.ID(), 5, 0, 200))
	if err = d.Dispatch(); err != nil {
		t.Fatal(err)
	}
	if _, err = adapter.ApplyInput(<-w.events, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, ok := adapter.NextRepeat(); ok {
		t.Fatal("repeat should be disabled")
	}
}

// Each keymap path transfers ownership to the adapter or the discarded-event
// cleanup, including a valid XKB keymap and malformed compositor payloads.
func TestKeymapOwnershipAndFocusRepeat(t *testing.T) {
	adapter, err := NewInputAdapter("")
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	data, err := os.ReadFile("../../keyboard/testdata/fr.xkb")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		data    []byte
		format  uint32
		discard bool
		good    bool
	}{
		{"valid", append(data, 0), 1, false, true},
		{"invalid", []byte("bad keymap\x00"), 1, false, false},
		{"unsupported", append(data, 0), 2, false, false},
		{"disconnect", append(data, 0), 1, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := os.CreateTemp(t.TempDir(), "keymap")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.Write(tc.data); err != nil {
				t.Fatal(err)
			}
			// ApplyInput owns a duplicate, not the os.File descriptor.
			fd, err := unix.Dup(int(f.Fd()))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			ev := Event{Kind: InputKeymap, FD: fd, Format: tc.format, Size: uint32(len(tc.data))}
			if tc.discard {
				CloseEventFD(ev)
			} else {
				_, err = adapter.ApplyInput(ev, time.Now())
				if (err == nil) != tc.good {
					t.Fatalf("apply: %v", err)
				}
			}
			if err = unix.Fstat(ev.FD, &unix.Stat_t{}); err != unix.EBADF {
				t.Fatalf("keymap FD still open: %v", err)
			}
		})
	}
	now := time.Now()
	for _, ev := range []Event{{Kind: InputFocusIn, Serial: 42}, {Kind: InputRepeatInfo, Rate: 20, Delay: 100}, {Kind: InputKey, Code: 16, Pressed: true, Serial: 43}} {
		if _, err := adapter.ApplyInput(ev, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := adapter.NextRepeat(); !ok {
		t.Fatal("missing repeat")
	}
	if _, err := adapter.ApplyInput(Event{Kind: InputRepeatInfo, Rate: 0}, now); err != nil {
		t.Fatal(err)
	}
	if _, ok := adapter.NextRepeat(); ok {
		t.Fatal("rate zero retained repeat")
	}
	if _, err := adapter.ApplyInput(Event{Kind: InputRepeatInfo, Rate: 20, Delay: 100}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.ApplyInput(Event{Kind: InputKey, Code: 16, Pressed: true}, now); err != nil {
		t.Fatal(err)
	}
	if _, ok := adapter.NextRepeat(); !ok {
		t.Fatal("repeat not rearmed")
	}
	if _, err := adapter.ApplyInput(Event{Kind: InputFocusOut}, now); err != nil {
		t.Fatal(err)
	}
	if _, ok := adapter.NextRepeat(); ok {
		t.Fatal("keyboard leave retained repeat")
	}

	// Overflow reset cancels repeat from a discarded press but keeps focus.
	w := &Window{inputAdapter: adapter}
	for _, ev := range []Event{{Kind: InputFocusIn, Serial: 44}, {Kind: InputKey, Code: 16, Pressed: true, Serial: 45}} {
		if _, err := adapter.ApplyInput(ev, now); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i <= 4096; i++ {
		w.queueInput([]Input{{Kind: "press"}})
	}
	if _, ok := adapter.NextRepeat(); ok {
		t.Fatal("overflow reset retained repeat")
	}
	out, err := adapter.ApplyInput(Event{Kind: InputKey, Code: 16, Pressed: true, Serial: 46}, now)
	if err != nil || len(out) != 1 {
		t.Fatalf("key after reset: %v %+v", err, out)
	}
	if _, ok := adapter.NextRepeat(); !ok {
		t.Fatal("focus lost by overflow reset")
	}
}

func TestInputQueueOverflowResetAndCoalescing(t *testing.T) {
	w := &Window{}
	for i := 0; i < 4096; i++ {
		w.queueInput([]Input{{Kind: "press", Button: uint32(i)}})
	}
	w.queueInput([]Input{{Kind: "release", Button: 7}})
	got := w.DrainInput(nil)
	if len(got) != 2 || got[0].Kind != InputReset || got[1].Kind != "release" || w.InputOverflow != 4096 {
		t.Fatalf("reset: len=%d events=%+v overflows=%d", len(got), got, w.InputOverflow)
	}
	w.queueInput([]Input{{Kind: "motion", X: 1}})
	for i := 1; i < 4096; i++ {
		w.queueInput([]Input{{Kind: "press"}})
	}
	w.queueInput([]Input{{Kind: "focus-out"}})
	got = w.DrainInput(nil)
	if len(got) != 4096 || got[0].Kind != "press" || got[4095].Kind != "focus-out" || w.InputOverflow != 4097 {
		t.Fatalf("eviction: len=%d first=%+v last=%+v overflows=%d", len(got), got[0], got[len(got)-1], w.InputOverflow)
	}
	w.queueInput([]Input{{Kind: "axis", Axis: 1, DY: 2}})
	w.queueInput([]Input{{Kind: "axis", Axis: 1, DY: 3}})
	for i := 2; i < 4096; i++ {
		w.queueInput([]Input{{Kind: "press"}})
	}
	w.queueInput([]Input{{Kind: "release"}})
	got = w.DrainInput(nil)
	if len(got) != 4096 || got[0].Kind != "axis" || got[0].DY != 5 || got[4095].Kind != "release" || w.InputOverflow != 4097 {
		t.Fatalf("axis coalescing: len=%d first=%+v last=%+v overflows=%d", len(got), got[0], got[len(got)-1], w.InputOverflow)
	}
	// Eviction skips enter motions; a reset keeps the latest one.
	w.queueInput([]Input{{Kind: "motion", Enter: true, Serial: 5}})
	for i := 1; i < 4096; i++ {
		w.queueInput([]Input{{Kind: "press"}})
	}
	w.queueInput([]Input{{Kind: "release"}})
	got = w.DrainInput(nil)
	if len(got) != 3 || got[0].Kind != InputReset || !got[1].Enter || got[1].Serial != 5 || got[2].Kind != "release" {
		t.Fatalf("enter across reset: len=%d first=%+v", len(got), got[:min(3, len(got))])
	}
}

func TestApplySeatPointerKeyAndDrain(t *testing.T) {
	w := &Window{}
	data, err := os.ReadFile("../../keyboard/testdata/fr.xkb")
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.CreateTemp(t.TempDir(), "keymap")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write(append(data, 0)); err != nil {
		t.Fatal(err)
	}
	// ApplyInput consumes the event FD; keep the os.File descriptor separate.
	fd, err := unix.Dup(int(f.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, ev := range []Event{
		{Kind: InputKeymap, FD: fd, Format: 1, Size: uint32(len(data) + 1)},
		{Kind: InputFocusIn, Serial: 3},
		{Kind: SeatCapabilities, Capabilities: 3},
		{Kind: InputFrame, Inputs: []Input{{Kind: "motion", X: 4}, {Kind: "press", Button: 1}}},
		{Kind: InputKey, Code: 16, Pressed: true, Serial: 4},
	} {
		if err := w.Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	defer w.inputAdapter.Close()
	got := w.DrainInput(nil)
	if len(got) != 4 || got[0].Kind != "focus-in" || got[1].Kind != "motion" || got[2].Kind != "press" || got[3].Kind != "key" {
		t.Fatalf("drained: %+v", got)
	}
	if len(w.DrainInput(nil)) != 0 {
		t.Fatal("queue not drained")
	}
}
