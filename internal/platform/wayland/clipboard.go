//go:build linux

package wayland

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/bnema/nefergui/internal/edit"
	"github.com/bnema/wlturbo"
	"github.com/bnema/wlturbo/protocol/core"
	"github.com/bnema/wlturbo/wl"
	"golang.org/x/sys/unix"
)

const (
	mimeUTF8        = "text/plain;charset=utf-8"
	mimeX11         = "UTF8_STRING"
	mimePlain       = "text/plain"
	transferTimeout = 3 * time.Second
)

// ClipboardResult is delivered through the session event queue, never from
// the reader into the editor. Data may contain invalid UTF-8; edit validates it.
type ClipboardResult struct {
	Data []byte
	Err  error
}
type ClipboardEvent struct {
	kind   string
	offer  *core.DataOffer
	mime   string
	source *core.DataSource
}

// Clipboard is the asynchronous Wayland clipboard adapter. The synchronous
// edit.Clipboard.ReadText contract cannot be used on the UI loop: callers must
// use ReadTextAsync and dispatch ClipboardResult on their next frame.
type Clipboard struct {
	manager *core.DataDeviceManager
	version uint32 // negotiated wl_data_device_manager version
	device  *core.DataDevice
	offer   *core.DataOffer
	dnd     *core.DataOffer
	pending map[*core.DataOffer]map[string]bool
	offers  map[uint32]*core.DataOffer
	mimes   map[string]bool
	source  *core.DataSource
	serial  func() uint32
	post    func(ClipboardEvent)
	closed  bool
	stop    context.CancelFunc
	workers sync.WaitGroup
	mu      sync.Mutex
}

var _ edit.AsyncClipboard = (*Clipboard)(nil)

// NewClipboard binds the optional manager at its announced version and creates
// one data device for seat. Call from the session loop after seat binding.
func NewClipboard(d *wl.Display, seat *core.Seat, serial func() uint32, post func(ClipboardEvent)) (*Clipboard, error) {
	if seat == nil || d == nil {
		return nil, nil
	}
	manager := core.NewDataDeviceManager(d.Context())
	version, err := d.Registry().BindNegotiated(core.DataDeviceManagerInterface, 3, manager)
	if err != nil {
		d.Context().Unregister(manager)
		if errors.Is(err, wlturbo.ErrGlobalNotFound) {
			return nil, nil
		}
		return nil, err
	}
	device, err := manager.GetDataDevice(seat)
	if err != nil {
		d.Context().Unregister(manager)
		return nil, err
	}
	c := &Clipboard{manager: manager, version: version, device: device, serial: serial, post: post, pending: make(map[*core.DataOffer]map[string]bool), offers: make(map[uint32]*core.DataOffer)}
	device.OnDataOffer(func(o *core.DataOffer) {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			_ = o.Destroy()
			return
		}
		c.offers[o.ID()] = o
		c.mu.Unlock()
		c.post(ClipboardEvent{kind: "offer", offer: o})
		o.OnOffer(func(m string) { c.post(ClipboardEvent{kind: "mime", offer: o, mime: m}) })
	})
	device.OnSelection(func(id uint32) { c.post(ClipboardEvent{kind: "selection", offer: c.findOffer(id)}) })
	// Drag-and-drop is unsupported in V1; retain its offer until leave/drop.
	device.OnEnter(func(_ uint32, _ uint32, _, _ wl.Fixed, id uint32) {
		c.post(ClipboardEvent{kind: "enter", offer: c.findOffer(id)})
	})
	device.OnLeave(func() { c.post(ClipboardEvent{kind: "leave"}) })
	device.OnDrop(func() { c.post(ClipboardEvent{kind: "drop"}) })
	return c, nil
}

// findOffer only resolves IDs from reader-owned notifications. It does not
// mutate clipboard state; the loop decides which offer is current.
func (c *Clipboard) findOffer(id uint32) *core.DataOffer {
	if id == 0 {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.offers[id]
}

// ApplyClipboard handles reader events on the session loop.
func (c *Clipboard) ApplyClipboard(ev ClipboardEvent) {
	switch ev.kind {
	case "offer":
		c.pending[ev.offer] = make(map[string]bool)
	case "mime":
		if m, ok := c.pending[ev.offer]; ok {
			m[ev.mime] = true
		} else if ev.offer == c.offer {
			c.mimes[ev.mime] = true
		}
	case "selection":
		if c.offer != nil && c.offer != ev.offer {
			c.destroyOffer(c.offer)
		}
		c.offer = ev.offer
		c.mimes = c.pending[ev.offer]
		if c.mimes == nil {
			c.mimes = make(map[string]bool)
		}
		delete(c.pending, ev.offer)
		for o := range c.pending {
			if o == c.dnd {
				continue
			}
			if o != ev.offer {
				c.destroyOffer(o)
				delete(c.pending, o)
			}
		}
	case "enter":
		c.clearDnD()
		c.dnd = ev.offer
		delete(c.pending, ev.offer)
	case "leave", "drop":
		c.clearDnD()
	case "cancelled":
		if c.source == ev.source {
			_ = c.source.Destroy()
			c.source = nil
		}
	}
}
func (c *Clipboard) clearDnD() {
	if c.dnd != nil && c.dnd != c.offer {
		c.destroyOffer(c.dnd)
	}
	c.dnd = nil
}
func (c *Clipboard) destroyOffer(o *core.DataOffer) {
	c.mu.Lock()
	delete(c.offers, o.ID())
	c.mu.Unlock()
	_ = o.Destroy()
}
func (c *Clipboard) bestMIME() string {
	for _, m := range []string{mimeUTF8, mimeX11, mimePlain} {
		if c.mimes[m] {
			return m
		}
	}
	return ""
}

// ReadTextAsync must be called on the loop. The compositor receives the write
// end; a worker reads at most limit+1 bytes. The caller posts the callback to
// its UI event queue. Concurrent reads cancel the previous transfer. Close
// cancels and joins reads, not callbacks: a callback racing Close may still run
// after Close returns. Callers must discard results when shutting down.
func (c *Clipboard) ReadTextAsync(ctx context.Context, limit int, deliver func([]byte, error)) error {
	if c == nil || c.offer == nil {
		return errors.New("clipboard unavailable")
	}
	mime := c.bestMIME()
	if mime == "" {
		return errors.New("no text MIME offered")
	}
	if limit < 0 || limit > edit.MaxClipboardBytes {
		limit = edit.MaxClipboardBytes
	}
	fd := make([]int, 2)
	if err := unix.Pipe2(fd, unix.O_CLOEXEC|unix.O_NONBLOCK); err != nil {
		return err
	}
	r := os.NewFile(uintptr(fd[0]), "clipboard-read")
	if err := c.offer.Receive(mime, fd[1]); err != nil {
		_ = syscall.Close(fd[1])
		_ = r.Close()
		return err
	}
	// WLTurbo closes the sent FD after a successful send; do not close it twice.
	ctx, cancel := context.WithTimeout(ctx, transferTimeout)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		cancel()
		_ = r.Close()
		return errors.New("clipboard closed")
	}
	if c.stop != nil {
		c.stop()
	}
	c.stop = cancel
	c.workers.Add(1)
	c.mu.Unlock()
	go func() {
		defer c.workers.Done()
		defer cancel()
		defer r.Close()
		// readClipboard polls ctx every 50ms; closing r from another goroutine
		// while it is read would race on the descriptor.
		data, e := readClipboard(ctx, r, limit)
		if ctx.Err() != nil {
			e = ctx.Err()
		}
		c.mu.Lock()
		open := !c.closed
		c.mu.Unlock()
		if !open || ctx.Err() != nil {
			return
		}
		// Arbitrary callbacks cannot be forcibly interrupted. They are not
		// joined by Close: a blocked UI enqueue must not stall shutdown.
		go deliver(data, e)
	}()
	return nil
}

// readClipboard polls a nonblocking pipe so cancellation and timeouts are
// effective even if the compositor never closes its write end.
func readClipboard(ctx context.Context, r *os.File, limit int) ([]byte, error) {
	data := make([]byte, 0, min(limit, 4096))
	buf := make([]byte, 4096)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, e := unix.Read(int(r.Fd()), buf[:min(len(buf), limit+1-len(data))])
		if n > 0 {
			data = append(data, buf[:n]...)
			if len(data) > limit {
				return nil, fmt.Errorf("clipboard exceeds %d bytes", limit)
			}
		}
		if e == nil && n == 0 {
			return data, nil
		}
		if e == nil {
			continue
		}
		if e != unix.EAGAIN && e != unix.EINTR {
			return nil, e
		}
		poll := []unix.PollFd{{Fd: int32(r.Fd()), Events: unix.POLLIN | unix.POLLHUP}}
		_, e = unix.Poll(poll, 50)
		if e != nil && e != unix.EINTR {
			return nil, e
		}
	}
}

// ReadText deliberately refuses to block the UI loop. Use ReadTextAsync.
func (c *Clipboard) ReadText(int) ([]byte, error) {
	return nil, errors.New("wayland clipboard requires ReadTextAsync")
}

func (c *Clipboard) WriteText(data []byte) error {
	if c == nil || c.device == nil {
		return errors.New("clipboard unavailable")
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return errors.New("clipboard closed")
	}
	if len(data) > edit.MaxClipboardBytes {
		return errors.New("clipboard too large")
	}
	serial := c.serial()
	if serial == 0 {
		return errors.New("no keyboard selection serial")
	}
	source, err := c.manager.CreateDataSource()
	if err != nil {
		return err
	}
	for _, m := range []string{mimeUTF8, mimeX11, mimePlain} {
		if err = source.Offer(m); err != nil {
			_ = source.Destroy()
			return err
		}
	}
	payload := append([]byte(nil), data...)
	source.OnSend(func(m string, fd *wl.OwnedFD) {
		n, e := fd.Take()
		if e != nil {
			return
		}
		if m != mimeUTF8 && m != mimeX11 && m != mimePlain {
			_ = os.NewFile(uintptr(n), "selection").Close()
			return
		}
		go writeSelection(n, payload)
	})
	source.OnCancelled(func() { c.post(ClipboardEvent{kind: "cancelled", source: source}) })
	if err = c.device.SetSelection(source, serial); err != nil {
		_ = source.Destroy()
		return err
	}
	if c.source != nil {
		_ = c.source.Destroy()
	}
	c.source = source
	return nil
}
func writeSelection(fd int, data []byte) {
	f := os.NewFile(uintptr(fd), "selection")
	defer f.Close()
	_ = unix.SetNonblock(fd, true)
	deadline := time.Now().Add(transferTimeout)
	for len(data) > 0 && time.Now().Before(deadline) {
		n, err := unix.Write(fd, data)
		if n > 0 {
			data = data[n:]
		}
		if err != nil && err != unix.EAGAIN && err != unix.EINTR {
			return
		}
		if n == 0 || err == unix.EAGAIN {
			_, _ = unix.Poll([]unix.PollFd{{Fd: int32(fd), Events: unix.POLLOUT}}, 50)
		}
	}
}
func (c *Clipboard) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.closed = true
	if c.stop != nil {
		c.stop()
	}
	c.mu.Unlock()
	c.workers.Wait() // reads poll cancellation every 50ms (transfer has a 3s deadline)
	c.clearDnD()
	if c.offer != nil {
		c.destroyOffer(c.offer)
	}
	for o := range c.pending {
		if o != c.offer {
			c.destroyOffer(o)
		}
	}
	if c.source != nil {
		_ = c.source.Destroy()
	}
	if c.device != nil {
		if c.version >= 2 {
			_ = c.device.Release()
		} else {
			if ctx := c.device.Context(); ctx != nil {
				ctx.Unregister(c.device)
			}
		}
	}
	// wl_data_device_manager has no destructor below version 4: sending
	// release is a protocol error, so only forget the proxy locally.
	if c.manager != nil {
		if ctx := c.manager.Context(); ctx != nil {
			ctx.Unregister(c.manager)
		}
	}
}
