package ui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bnema/nefergui/internal/css"
	"github.com/bnema/nefergui/internal/keyboard"
	"github.com/bnema/nefergui/internal/platform/wayland"
	"github.com/bnema/nefergui/internal/presentation/session"
	"github.com/bnema/nefergui/internal/render"
	"github.com/bnema/nefergui/internal/text"
)

// ErrLockFinished reports that the compositor refused the session lock or
// ended it (ext_session_lock_v1.finished). NeferGUI never unlocks in response.
var ErrLockFinished = errors.New("nefergui: session lock finished by the compositor")

// SecretBuffer is a caller-owned fixed-capacity byte buffer for typed secret
// input. Typed bytes never become a Go string and are never stored in the
// model, tree or render text; the view only sees Len. Bytes beyond the
// capacity are dropped. It is used from the lock owner loop only.
type SecretBuffer struct {
	buf   []byte
	n     int
	runes int
}

// NewSecretBuffer allocates a buffer holding at most capacity bytes (1..4096).
func NewSecretBuffer(capacity int) *SecretBuffer {
	if capacity < 1 || capacity > 4096 {
		panic("nefergui: secret buffer capacity must be between 1 and 4096")
	}
	return &SecretBuffer{buf: make([]byte, capacity)}
}

// Len is the number of typed code points (the mask count).
func (b *SecretBuffer) Len() int { return b.runes }

// Bytes returns the typed bytes. The slice aliases the buffer: do not retain
// it past the next edit or Wipe, and never convert it to a string.
func (b *SecretBuffer) Bytes() []byte { return b.buf[:b.n:b.n] }

// Wipe zeroes the buffer. The caller may call it at any time on the owner loop.
func (b *SecretBuffer) Wipe() {
	clear(b.buf)
	b.n, b.runes = 0, 0
}

func (b *SecretBuffer) append(p []byte) {
	if len(p) == 0 || len(p) > len(b.buf)-b.n || !utf8.Valid(p) {
		return
	}
	b.n += copy(b.buf[b.n:], p)
	b.runes += utf8.RuneCount(p)
}

func (b *SecretBuffer) backspace() {
	if b.n == 0 {
		return
	}
	_, size := utf8.DecodeLastRune(b.buf[:b.n])
	clear(b.buf[b.n-size : b.n])
	b.n -= size
	b.runes--
}

// LockStatus is an opaque caller-driven state shown by the view, for example
// while an authentication attempt runs. NeferGUI attaches no meaning to it.
type LockStatus uint8

const (
	LockIdle   LockStatus = iota // ready for input
	LockBusy                     // an attempt is being verified
	LockFailed                   // the last attempt was rejected
)

// LockState is what the lock view may know: never the secret itself.
type LockState struct {
	Mask          int        // typed code points, for a mask of that many bullets
	Locked        bool       // the compositor confirmed the lock (locked event)
	Status        LockStatus // latest value received on LockConfig.Status
	Width, Height int        // logical size of the surface hosting the view
}

// Bullets returns Mask bullet characters.
func (s LockState) Bullets() string { return strings.Repeat("•", s.Mask) }

// LockConfig configures RunLock.
type LockConfig struct {
	// Display is the Wayland socket name; empty uses the environment.
	Display string
	// Styles is an optional author stylesheet path, as for the Styles option.
	Styles string
	// Secret receives typed input. Required.
	Secret *SecretBuffer
	// View builds the content of the one output that hosts it (the output whose
	// surface has keyboard focus, initially the first). Required. Other outputs
	// show plain opaque black.
	View func(*Frame, LockState)
	// OnLocked runs once on the owner loop when the compositor sends locked.
	OnLocked func()
	// OnSubmit runs on the owner loop when Enter is pressed after locked. It
	// must not block: verification is asynchronous. secret aliases Secret's
	// memory and is valid only during the call; it is wiped right after it
	// returns, so the callee copies or consumes it before returning.
	OnSubmit func(secret []byte)
	// Unlock, when it receives a value, asks RunLock to send
	// unlock_and_destroy, roundtrip and return nil. This is the only unlock
	// path: it is the caller's explicit request after its own verification. A
	// request received before the locked event is held until locked; closing
	// the channel is not a request. A nil channel never unlocks.
	Unlock <-chan struct{}
	// Status delivers the state shown as LockState.Status; each received value
	// schedules a redraw. Optional.
	Status <-chan LockStatus
	// Wake requests a redraw for each received value.
	Wake <-chan struct{}
	// OnOutputError reports an output added after acquisition that could not
	// get a lock surface; the compositor keeps it black. Optional, owner loop.
	OnOutputError func(output string, err error)
}

type lockSurface struct {
	w      *wayland.Window
	output uint32
	s      *session.Session
	p      *session.Pump
}

// RunLock acquires ext-session-lock on one connection and shows opaque black on
// every output through the regular Vulkan/DMA-BUF/explicit-sync path. Outputs
// added or removed later gain or lose a lock surface. Keyboard input feeds
// cfg.Secret only through the secret keyboard path. RunLock returns nil after
// an unlock requested through cfg.Unlock, ErrLockFinished (wrapped) when the
// compositor ends or refuses the lock, or the first failure; it never unlocks
// on failure or cancellation (closing the connection keeps the session locked).
func RunLock(ctx context.Context, cfg LockConfig) (err error) {
	if cfg.Secret == nil || cfg.View == nil {
		return errors.New("nefergui: lock needs Secret and View")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	defer cfg.Secret.Wipe() // also on panic and every early return
	ctx, stop := context.WithCancel(ctx)
	defer stop() // ends the wake forwarder with RunLock
	r := newRuntime()
	if cfg.Styles != "" {
		data, e := os.ReadFile(cfg.Styles)
		if e != nil {
			return fmt.Errorf("nefergui: styles: %w", e)
		}
		r.styles = css.Compile(css.UA(), css.Parse(string(data)))
	}
	catalog, err := text.Load(text.SystemSource{})
	if err != nil {
		return fmt.Errorf("nefergui: fonts: %w", err)
	}
	if len(catalog.Faces) == 0 {
		return errors.New("nefergui: no usable fonts")
	}
	r.setTextEngine(text.NewEngine(catalog))
	conn, err := wayland.ConnectConnection(ctx, cfg.Display)
	if err != nil {
		return fmt.Errorf("nefergui: open platform: %w", err)
	}
	l := &locker{cfg: cfg, r: r, conn: conn, ctx: ctx, surfaces: map[*wayland.Window]*lockSurface{}, pending: map[*wayland.Window]pendingLock{}, covered: map[uint32]bool{}, initial: map[uint32]bool{}, gpuWake: make(chan struct{}, 1), scratch: make([]byte, 128)}
	defer func() {
		l.closeAll()
		clear(l.scratch)
		if l.kb != nil {
			l.kb.Close()
		}
		err = errors.Join(err, conn.Close())
	}()
	if l.kb, err = keyboard.New(""); err != nil {
		return err
	}
	l.kb.Focus(false)
	if err = conn.AcquireLock(); err != nil {
		return err
	}
	// Surfaces for the outputs present now are created before the reader starts
	// so the compositor can see them while it decides to send locked.
	if err = l.reconcile(true); err != nil {
		return err
	}
	events := conn.StartReader()
	// Lock and output events that arrived while the first surfaces were set up
	// were kept for us: locked must enable unlock, finished must fail the run.
	for _, ev := range conn.TakeHeld() {
		if err = l.handle(ev); err != nil {
			return err
		}
	}
	if cfg.Wake != nil {
		go r.forwardWake(ctx, cfg.Wake)
	}
	return l.loop(events)
}

// pendingLock is a created lock surface and the output it covers.
type pendingLock struct {
	output uint32
	name   string // connector name, for error reports
}

type locker struct {
	cfg      LockConfig
	r        *runtime
	conn     *wayland.Connection
	ctx      context.Context
	kb       *keyboard.Keyboard
	scratch  []byte
	surfaces map[*wayland.Window]*lockSurface
	pending  map[*wayland.Window]pendingLock // created, not yet configured with feedback
	covered  map[uint32]bool                 // output globals with a surface or a pending one
	initial  map[uint32]bool                 // outputs present at acquisition: their failures abort
	host     *lockSurface
	locked   bool
	status   LockStatus
	unlock   bool // the caller asked for unlock; honoured once locked
	gpuWake  chan struct{}
}

func (l *locker) loop(events <-chan wayland.Event) error {
	for {
		if err := l.promote(); err != nil {
			return err
		}
		retry := false
		for _, ls := range l.surfaces {
			again, err := ls.p.Step()
			if err != nil {
				return err
			}
			retry = retry || again
		}
		var retryC, repeatC <-chan time.Time
		if retry {
			retryC = time.After(5 * time.Millisecond)
		}
		if at, ok := l.kb.NextRepeat(); ok {
			repeatC = time.After(time.Until(at))
		}
		select {
		case <-l.ctx.Done():
			return l.ctx.Err()
		case <-l.r.wake:
			l.invalidateHost()
		case _, ok := <-l.cfg.Unlock:
			if ok {
				l.unlock = true
			} else {
				l.cfg.Unlock = nil // closing is not a request
			}
		case st, ok := <-l.cfg.Status:
			if ok {
				l.status = st
				l.secretChanged()
			} else {
				l.cfg.Status = nil
			}
		case <-l.gpuWake:
		case <-retryC:
		case <-repeatC:
			if _, err := l.kb.TickSecret(time.Now(), l.scratch, l.consume); err != nil {
				return err
			}
			l.secretChanged()
		case ev := <-events:
			if err := l.handle(ev); err != nil {
				return err
			}
		}
		if l.shouldUnlock() {
			return l.finishUnlock()
		}
	}
}

func (l *locker) handle(ev wayland.Event) error {
	switch ev.Kind {
	case wayland.BufferRelease:
		if ls := l.surfaces[ev.Window]; ls != nil {
			return ls.p.BufferReleased(ev.BufferID)
		}
		return nil
	case wayland.OutputRemoved:
		l.removeOutput(ev.Output.Global)
	}
	if err := l.conn.Apply(ev); err != nil {
		return err
	}
	switch ev.Kind {
	case wayland.LockAcquired:
		l.locked = true
		l.invalidateHost()
		if l.cfg.OnLocked != nil {
			l.cfg.OnLocked()
		}
	case wayland.LockFinished:
		return ErrLockFinished
	case wayland.OutputGlobal, wayland.OutputNamed, wayland.OutputAdded:
		if err := l.reconcile(false); err != nil {
			return err
		}
	}
	return l.drainInput()
}

// reconcile gives every named output without one a lock surface. A failure on
// an initial output aborts the lock. A hotplugged output that cannot get a
// surface is logged and left alone: the compositor keeps it black regardless.
func (l *locker) reconcile(initial bool) error {
	for _, o := range l.conn.Outputs() {
		if l.covered[o.Global] {
			continue
		}
		l.covered[o.Global] = true // also on failure: never retried for this lifetime
		w, err := l.conn.NewWindow(l.ctx, 1, 1, false, wayland.SurfaceOptions{Lock: &wayland.LockOptions{Output: o.Global}})
		if err != nil {
			if err = l.outputFailure(initial, "lock surface", o.Name, err); err != nil {
				return err
			}
			continue
		}
		if initial {
			l.initial[o.Global] = true
		}
		l.pending[w] = pendingLock{output: o.Global, name: o.Name}
	}
	return nil
}

// promote opens the Vulkan session of every window whose configure was
// acknowledged and whose dmabuf feedback round is complete, in ascending
// output global order so the first host is deterministic.
func (l *locker) promote() error {
	ready := make([]*wayland.Window, 0, len(l.pending))
	for w := range l.pending {
		if w.Configured && w.FeedbackDone {
			ready = append(ready, w)
		}
	}
	slices.SortFunc(ready, func(a, b *wayland.Window) int { return cmp.Compare(l.pending[a].output, l.pending[b].output) })
	for _, w := range ready {
		pl := l.pending[w]
		output := pl.output
		delete(l.pending, w)
		ls, err := l.open(w, output) // closes w on failure
		if err != nil {
			if err = l.outputFailure(l.initial[output], "lock render path", pl.name, err); err != nil {
				return err
			}
			continue
		}
		l.surfaces[w] = ls
		if l.host == nil {
			l.setHost(ls)
		}
	}
	return nil
}

// outputFailure applies the per-output failure policy. A refused or ended lock
// always aborts (as ErrLockFinished). Otherwise only outputs present at
// acquisition abort; a hotplugged output is skipped, since the
// compositor keeps it black without our surface; OnOutputError reports it.
func (l *locker) outputFailure(initial bool, what, output string, err error) error {
	if abort := outputFailure(initial, what, output, err); abort != nil {
		return abort
	}
	if l.cfg.OnOutputError != nil {
		l.cfg.OnOutputError(output, fmt.Errorf("%s: %w", what, err))
	}
	return nil
}

func outputFailure(initial bool, what, output string, err error) error {
	if errors.Is(err, wayland.ErrLockFinished) {
		return fmt.Errorf("%w: %w", ErrLockFinished, err)
	}
	if initial {
		return fmt.Errorf("nefergui: %s for output %s: %w", what, output, err)
	}
	return nil
}

// open builds the Vulkan session and frame pump of a configured lock window.
func (l *locker) open(w *wayland.Window, output uint32) (*lockSurface, error) {
	s, err := session.OpenWindow(w, false)
	if err != nil {
		return nil, err
	}
	ls := &lockSurface{w: w, output: output, s: s}
	if ls.p, err = s.NewPump(l.ctx, l.gpuWake, func() (render.Frame, bool, error) { return l.draw(ls) }); err != nil {
		_ = s.Close()
		return nil, err
	}
	return ls, nil
}

func (l *locker) removeOutput(global uint32) {
	delete(l.covered, global)
	for w, pl := range l.pending {
		if pl.output == global {
			delete(l.pending, w)
			_ = w.Close()
		}
	}
	for w, ls := range l.surfaces {
		if ls.output != global {
			continue
		}
		delete(l.surfaces, w)
		ls.close()
		if l.host == ls {
			l.host = nil
			var next *lockSurface
			for _, o := range l.surfaces {
				if next == nil || o.output < next.output {
					next = o
				}
			}
			if next != nil {
				l.setHost(next)
			}
		}
	}
}

func (ls *lockSurface) close() {
	ls.p.Close()
	_ = ls.s.Close()
}

func (l *locker) closeAll() {
	for w, ls := range l.surfaces {
		delete(l.surfaces, w)
		ls.close()
	}
	for w := range l.pending {
		delete(l.pending, w)
		_ = w.Close()
	}
}

func (l *locker) setHost(ls *lockSurface) {
	if l.host == ls {
		return
	}
	old := l.host
	l.host = ls
	if old != nil {
		old.p.Invalidate()
	}
	l.r.Redraw()
	ls.p.Invalidate()
}

func (l *locker) invalidateHost() {
	if l.host != nil {
		l.host.p.Invalidate()
	}
}

// draw shows the view on the host output and plain black elsewhere. An empty
// frame is the opaque clear color; alpha is not part of the XRGB buffer.
func (l *locker) draw(ls *lockSurface) (render.Frame, bool, error) {
	if l.host != ls {
		return render.Frame{}, true, nil
	}
	w := ls.w
	l.r.route(platformInput{Kind: "resize", Width: float64(w.Width), Height: float64(w.Height), Scale: w.Scale})
	state := LockState{Mask: l.cfg.Secret.Len(), Locked: l.locked, Status: l.status, Width: int(w.Width), Height: int(w.Height)}
	if !l.r.Build(func(f *Frame) { l.cfg.View(f, state) }) {
		return render.Frame{}, false, nil
	}
	pw, ph, err := w.PhysicalSize()
	if err != nil {
		return render.Frame{}, false, err
	}
	frame, err := ls.s.Preparer.Prepare(l.r.output.Display, w.Scale, int(pw), int(ph))
	return frame, true, err
}

// drainInput feeds raw keyboard protocol events from every lock window to the
// secret keyboard. Nothing here creates a string or reaches the UI runtime.
func (l *locker) drainInput() error {
	take := func(w *wayland.Window) error {
		events := w.TakeInputEvents()
		for i, e := range events {
			if err := l.input(w, e); err != nil {
				for _, rest := range events[i+1:] {
					wayland.CloseEventFD(rest) // never leak keymap FDs
				}
				return err
			}
		}
		return nil
	}
	for w := range l.surfaces {
		if err := take(w); err != nil {
			return err
		}
	}
	for w := range l.pending {
		if err := take(w); err != nil {
			return err
		}
	}
	return nil
}

func (l *locker) input(w *wayland.Window, e wayland.Event) error {
	switch e.Kind {
	case wayland.InputKeymap:
		if e.FD < 0 {
			return errors.New("nefergui: missing keymap fd")
		}
		if e.Format != 1 || e.Size == 0 || e.Size > 16<<20 {
			wayland.CloseEventFD(e)
			return nil
		}
		return l.kb.ReplaceFD(e.FD, int(e.Size))
	case wayland.InputModifiers:
		_ = l.kb.Mask(e.Mods[0], e.Mods[1], e.Mods[2], 0, 0, e.Mods[3]) // no keymap yet: ignore
	case wayland.InputRepeatInfo:
		l.kb.RepeatInfo(int(e.Rate), time.Duration(e.Delay)*time.Millisecond)
	case wayland.InputFocusIn:
		l.kb.Focus(true)
		if ls := l.surfaces[w]; ls != nil {
			l.setHost(ls)
		}
	case wayland.InputFocusOut:
		l.kb.ClearSecret()
		l.kb.Focus(false)
	case wayland.InputKey:
		if err := l.kb.EventSecret(e.Code, e.Pressed, time.Now(), l.scratch, l.consume); err != nil {
			return err
		}
		l.secretChanged()
	}
	return nil
}

func (l *locker) secretChanged() {
	l.r.Redraw()
	l.invalidateHost()
}

// consume applies one secret key: bytes append, Backspace removes a code
// point, Escape and Ctrl+U clear, Enter submits.
func (l *locker) consume(k keyboard.SecretKey, chunk []byte) {
	if !k.Pressed {
		return
	}
	b := l.cfg.Secret
	switch k.Kind {
	case keyboard.SecretBytes:
		b.append(chunk)
	case keyboard.SecretBackspace:
		b.backspace()
	case keyboard.SecretEscape, keyboard.SecretClear:
		b.Wipe()
	case keyboard.SecretReturn:
		// Before the locked event Enter only clears. Submitting never unlocks.
		defer b.Wipe() // even if OnSubmit panics
		if l.locked && l.cfg.OnSubmit != nil && b.Len() > 0 {
			l.cfg.OnSubmit(b.Bytes())
		}
	}
}

// shouldUnlock reports that the caller asked for unlock and the lock is held.
func (l *locker) shouldUnlock() bool { return l.unlock && l.locked }

// finishUnlock is reached only after the caller sent on cfg.Unlock.
func (l *locker) finishUnlock() error {
	if err := l.conn.UnlockAndDestroy(); err != nil { // includes the display roundtrip
		return err
	}
	l.closeAll()
	return nil
}
