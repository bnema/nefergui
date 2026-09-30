//go:build linux

package wayland

import (
	"sync"
	"syscall"

	"github.com/bnema/wlturbo/protocol/core"
	"github.com/bnema/wlturbo/wl"
	"golang.org/x/sys/unix"
)

// A cache belongs to exactly one keyboard generation. Dup/replace/close share
// its FD mutex, not a window-state mutex, so capability loss cannot close an FD
// while a reader callback duplicates it. Retired callbacks are inert.
type keyboardMapCache struct {
	mu     sync.Mutex
	fd     int
	closed bool
}

func (m *keyboardMapCache) replace(fd int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		_ = syscall.Close(fd)
		return
	}
	if m.fd >= 0 {
		_ = syscall.Close(m.fd)
	}
	m.fd = fd
}
func (m *keyboardMapCache) duplicate() (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.fd < 0 {
		return -1, nil
	}
	// Close-on-exec: the map must not leak into any child process.
	return unix.FcntlInt(uintptr(m.fd), unix.F_DUPFD_CLOEXEC, 0)
}
func (m *keyboardMapCache) close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.closed && m.fd >= 0 {
		_ = syscall.Close(m.fd)
	}
	m.fd = -1
	m.closed = true
}

func (c *Connection) route(id uint32, ev Event) {
	if value, ok := c.routes.Load(id); ok {
		ev.Window = value.(*Window)
		c.post(ev)
	} else {
		CloseEventFD(ev)
	}
}

// Focus and frame accumulators below are exclusively reader-owned. They carry
// surface IDs, never pointers to mutable UI/window state.
func (w *Window) watchSharedPointer(p *core.Pointer) {
	c := w.connection
	w.pointerGen++
	generation, version := w.pointerGen, w.seatVersion
	var focus uint32
	var x, y float64
	var frame []Input
	var source uint32
	flush := func() {
		if len(frame) > 0 {
			c.route(focus, Event{Kind: InputFrame, Inputs: frame})
			frame = nil
		}
		source = 0
	}
	add := func(in Input) {
		in.AxisSource = source
		frame = append(frame, in)
		if version < 5 {
			flush()
		}
	}
	p.OnEnter(func(serial, id uint32, px, py wl.Fixed) {
		flush()
		focus = id
		x, y = px.Float64(), py.Float64()
		add(Input{Kind: "motion", X: x, Y: y, Enter: true, Serial: serial, PointerGen: generation})
	})
	p.OnLeave(func(_ uint32, id uint32) {
		if focus == id {
			add(Input{Kind: "leave"})
			flush()
			focus = 0
		}
	})
	p.OnMotion(func(_ uint32, px, py wl.Fixed) {
		if focus != 0 {
			x, y = px.Float64(), py.Float64()
			add(Input{Kind: "motion", X: x, Y: y})
		}
	})
	p.OnButton(func(serial, _, button, state uint32) {
		if focus != 0 {
			kind := "release"
			if state == 1 {
				kind = "press"
			}
			add(Input{Kind: kind, X: x, Y: y, Button: button, Serial: serial})
		}
	})
	p.OnAxisSource(func(value uint32) { source = value })
	p.OnAxis(func(_, axis uint32, value wl.Fixed) {
		if focus != 0 {
			in := Input{Kind: "axis", X: x, Y: y, Axis: axis}
			if axis == 0 {
				in.DY = value.Float64()
			} else {
				in.DX = value.Float64()
			}
			add(in)
		}
	})
	p.OnAxisValue120(func(axis uint32, value int32) {
		if focus != 0 {
			add(Input{Kind: "axis", X: x, Y: y, Axis: axis, Value120: value})
		}
	})
	p.OnAxisStop(func(_, axis uint32) {
		if focus != 0 {
			add(Input{Kind: "axis", X: x, Y: y, Axis: axis, AxisStop: true})
		}
	})
	p.OnFrame(flush)
}

func (w *Window) watchSharedKeyboard(k *core.Keyboard) {
	c := w.connection
	var focus uint32
	cache := &keyboardMapCache{fd: -1}
	if w.keymapCache != nil {
		w.keymapCache.close() // one live cache per keyboard generation
	}
	w.keymapCache = cache
	var format, size uint32
	var mods [4]uint32
	var rate, delay int32
	// Keep one reader-owned duplicate to initialize windows created on hotplug.
	// Its lifetime ends when this keyboard proxy or the connection is released.
	sendMap := func(id uint32) {
		fd, err := cache.duplicate()
		if err != nil {
			c.post(Event{Kind: TransportError, Err: err})
			return
		}
		if fd >= 0 {
			c.route(id, Event{Kind: InputKeymap, FD: fd, Format: format, Size: size})
		}
	}
	k.OnKeymap(func(f uint32, fd *wl.OwnedFD, n uint32) {
		next, err := fd.Take()
		if err != nil {
			c.post(Event{Kind: TransportError, Err: err})
			return
		}
		cache.replace(next)
		format, size = f, n
		if focus != 0 {
			sendMap(focus)
		}
	})
	k.OnEnter(func(serial, id uint32, _ []byte) {
		focus = id
		sendMap(id)
		c.route(id, Event{Kind: InputRepeatInfo, Rate: rate, Delay: delay})
		c.route(id, Event{Kind: InputModifiers, Mods: mods})
		c.route(id, Event{Kind: InputFocusIn, Serial: serial})
	})
	k.OnLeave(func(_, id uint32) {
		c.route(id, Event{Kind: InputFocusOut})
		if focus == id {
			focus = 0
		}
	})
	k.OnKey(func(serial, _, key, state uint32) {
		if focus != 0 {
			c.route(focus, Event{Kind: InputKey, Serial: serial, Code: key, Pressed: state == 1})
		}
	})
	k.OnModifiers(func(_, depressed, latched, locked, group uint32) {
		mods = [4]uint32{depressed, latched, locked, group}
		if focus != 0 {
			c.route(focus, Event{Kind: InputModifiers, Mods: mods})
		}
	})
	k.OnRepeatInfo(func(r, d int32) {
		rate, delay = r, d
		if focus != 0 {
			c.route(focus, Event{Kind: InputRepeatInfo, Rate: r, Delay: d})
		}
	})
}
