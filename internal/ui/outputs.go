package ui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/bnema/nefergui/internal/css"
	"github.com/bnema/nefergui/internal/platform/wayland"
	"github.com/bnema/nefergui/internal/presentation/session"
	"github.com/bnema/nefergui/internal/render"
	"github.com/bnema/nefergui/internal/text"
)

// outputSurface is one layer surface and its own layout runtime: outputs
// differ in size and scale, so each lays the shared view out separately.
type outputSurface struct {
	w      *wayland.Window
	output uint32
	r      *runtime
	s      *session.Session
	p      *session.Pump
}

// outputSet runs one layer surface per output on a single connection and
// owner loop, following the session-lock structure. Outputs added later gain a
// surface; removed outputs, or surfaces the compositor closes, lose theirs.
type outputSet struct {
	ctx         context.Context
	conn        *wayland.Connection
	opts        wayland.SurfaceOptions
	width       int32
	height      int32
	transparent bool
	sheet       css.Sheet
	catalog     *text.Catalog
	view        func(*Frame)
	hooks       *inputHooks
	surfaces    map[*wayland.Window]*outputSurface
	pending     map[*wayland.Window]uint32 // created, waiting for configure and feedback
	covered     map[uint32]bool            // outputs with a surface or a pending one
	gpuWake     chan struct{}
}

// runAllOutputs is Run with LayerConfig.AllOutputs. It returns on cancellation
// or the first failure; it never ends because outputs come and go.
func runAllOutputs(ctx context.Context, cfg windowConfig, view func(*Frame)) (err error) {
	if cfg.onSurface != nil || cfg.onResize != nil {
		return errors.New("nefergui: AllOutputs excludes OnSurface and OnResize")
	}
	opts, err := cfg.layer.surfaceOptions()
	if err != nil {
		return err
	}
	a := &outputSet{ctx: ctx, opts: opts, width: int32(cfg.width), height: int32(cfg.height), transparent: cfg.transparent, view: view,
		hooks: &inputHooks{onInput: cfg.onInput}, surfaces: map[*wayland.Window]*outputSurface{}, pending: map[*wayland.Window]uint32{},
		covered: map[uint32]bool{}, gpuWake: make(chan struct{}, 1)}
	if cfg.styles != "" {
		data, e := os.ReadFile(cfg.styles)
		if e != nil {
			return fmt.Errorf("nefergui: styles: %w", e)
		}
		a.sheet = css.Parse(string(data))
	}
	if a.catalog, err = text.Load(text.SystemSource{}); err != nil {
		return fmt.Errorf("nefergui: fonts: %w", err)
	}
	if len(a.catalog.Faces) == 0 {
		return errors.New("nefergui: no usable fonts")
	}
	if a.conn, err = wayland.ConnectConnection(ctx, ""); err != nil {
		return fmt.Errorf("nefergui: open platform: %w", err)
	}
	defer func() {
		a.closeAll()
		err = errors.Join(err, a.conn.Close())
	}()
	// Surfaces for the outputs present now are configured before the reader
	// starts; output events seen meanwhile are held and handled right after.
	if err = a.reconcile(); err != nil {
		return err
	}
	events := a.conn.StartReader()
	for _, ev := range a.conn.TakeHeld() {
		if err = a.handle(ev); err != nil {
			return err
		}
	}
	ext := make(chan struct{}, 1)
	if cfg.wake != nil {
		loopCtx, stop := context.WithCancel(ctx)
		defer stop()
		go forward(loopCtx, cfg.wake, ext)
	}
	return a.loop(events, ext)
}

// forward relays external wake tokens to the owner loop until ctx ends or ch
// closes; tokens coalesce.
func forward(ctx context.Context, ch <-chan struct{}, out chan<- struct{}) {
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-ch:
			if !ok {
				return
			}
			select {
			case out <- struct{}{}:
			default:
			}
		}
	}
}

func (a *outputSet) loop(events <-chan wayland.Event, ext <-chan struct{}) error {
	for {
		if err := a.promote(); err != nil {
			return err
		}
		// Every Redraw runs on this goroutine, so draining each runtime's own
		// wake here sees every request, including Build follow-ups.
		a.drainWakes()
		retry := false
		for _, o := range a.surfaces {
			again, err := o.p.Step()
			if err != nil {
				return err
			}
			retry = retry || again
		}
		if a.drainWakes() {
			continue // a Build asked for a follow-up frame
		}
		var retryC <-chan time.Time
		if retry {
			retryC = time.After(5 * time.Millisecond)
		}
		select {
		case <-a.ctx.Done():
			return a.ctx.Err()
		case <-ext:
			for _, o := range a.surfaces {
				o.r.Redraw()
			}
		case <-a.gpuWake:
		case <-retryC:
		case ev := <-events:
			if err := a.handle(ev); err != nil {
				return err
			}
		}
	}
}

// drainWakes invalidates every surface whose runtime requested a frame and
// reports whether any did.
func (a *outputSet) drainWakes() bool {
	woke := false
	for _, o := range a.surfaces {
		select {
		case <-o.r.wake:
			o.p.Invalidate()
			woke = true
		default:
		}
	}
	return woke
}

func (a *outputSet) handle(ev wayland.Event) error {
	switch ev.Kind {
	case wayland.BufferRelease:
		if o := a.surfaces[ev.Window]; o != nil {
			return o.p.BufferReleased(ev.BufferID)
		}
		return nil
	case wayland.OutputRemoved:
		a.removeOutput(ev.Output.Global)
	}
	if err := a.conn.Apply(ev); err != nil {
		return err
	}
	if w := ev.Window; w != nil && w.Closed {
		// The compositor closed this surface (layer_surface.closed); the output
		// stays covered so it is not recreated in a loop.
		a.closeWindow(w)
		return nil
	}
	switch ev.Kind {
	case wayland.OutputGlobal, wayland.OutputNamed, wayland.OutputAdded:
		return a.reconcile()
	}
	// Seat changes queue input (leave, focus-out) on every window.
	for _, o := range a.surfaces {
		if err := a.input(o); err != nil {
			return err
		}
	}
	return nil
}

// reconcile creates a layer surface for every named output without one.
func (a *outputSet) reconcile() error {
	for _, out := range a.conn.Outputs() {
		if a.covered[out.Global] {
			continue
		}
		a.covered[out.Global] = true
		l := *a.opts.Layer
		l.OutputGlobal = out.Global
		opts := a.opts
		opts.Layer = &l
		w, err := a.conn.NewWindow(a.ctx, a.width, a.height, a.transparent, opts)
		if err != nil {
			return fmt.Errorf("nefergui: layer surface for output %s: %w", out.Name, err)
		}
		a.pending[w] = out.Global
	}
	return nil
}

// promote opens the render path of every configured window with complete
// dmabuf feedback, in ascending output order.
func (a *outputSet) promote() error {
	ready := make([]*wayland.Window, 0, len(a.pending))
	for w := range a.pending {
		if w.Configured && w.FeedbackDone {
			ready = append(ready, w)
		}
	}
	slices.SortFunc(ready, func(x, y *wayland.Window) int { return cmp.Compare(a.pending[x], a.pending[y]) })
	for _, w := range ready {
		output := a.pending[w]
		delete(a.pending, w)
		if w.Closed { // closed by the compositor before the reader started
			_ = w.Close()
			continue
		}
		o, err := a.open(w, output) // closes w on failure
		if err != nil {
			return fmt.Errorf("nefergui: layer render path: %w", err)
		}
		a.surfaces[w] = o
	}
	return nil
}

func (a *outputSet) open(w *wayland.Window, output uint32) (*outputSurface, error) {
	s, err := session.OpenWindow(w, a.transparent)
	if err != nil {
		return nil, err
	}
	r := newRuntime()
	r.styles = css.Compile(css.UA(), a.sheet)
	r.setTextEngine(text.NewEngine(a.catalog))
	o := &outputSurface{w: w, output: output, r: r, s: s}
	if o.p, err = s.NewPump(a.ctx, a.gpuWake, func() (render.Frame, bool, error) { return a.draw(o) }); err != nil {
		_ = s.Close()
		return nil, err
	}
	return o, nil
}

func (a *outputSet) draw(o *outputSurface) (render.Frame, bool, error) {
	w := o.w
	o.r.route(platformInput{Kind: "resize", Width: float64(w.Width), Height: float64(w.Height), Scale: w.Scale})
	if !o.r.Build(a.view) {
		return render.Frame{}, false, nil
	}
	if o.r.consumedInput() {
		// Control events may have changed the shared model: the other outputs
		// rebuild without events, so this cannot bounce back.
		for _, other := range a.surfaces {
			if other != o {
				other.r.Redraw()
			}
		}
	}
	if err := o.r.applyInputRects(w.StageInputRects); err != nil {
		return render.Frame{}, false, err
	}
	if err := w.SetCursor(cursorShape(o.r.cursor())); err != nil {
		return render.Frame{}, false, err
	}
	pw, ph, err := w.PhysicalSize()
	if err != nil {
		return render.Frame{}, false, err
	}
	frame, err := o.s.Preparer.Prepare(o.r.output.Display, w.Scale, int(pw), int(ph))
	return frame, true, err
}

// input routes pointer input to the surface's runtime. A raw hook that
// changed the model redraws every output, since they share it.
func (a *outputSet) input(o *outputSurface) error {
	inputs := o.w.DrainInput(nil)
	if len(inputs) == 0 {
		return nil
	}
	for _, in := range inputs {
		switch {
		case in.Enter:
			o.w.PointerEntered(in.Serial, in.PointerGen)
		case in.Kind == "leave":
			o.w.PointerLeft()
		}
		if a.hooks.input(in) {
			for _, other := range a.surfaces {
				other.r.Redraw()
			}
		}
		o.r.routeInput(in)
	}
	o.p.Invalidate()
	return o.w.SetCursor(cursorShape(o.r.cursor()))
}

func (a *outputSet) removeOutput(global uint32) {
	delete(a.covered, global)
	for w, output := range a.pending {
		if output == global {
			delete(a.pending, w)
			_ = w.Close()
		}
	}
	for w, o := range a.surfaces {
		if o.output == global {
			delete(a.surfaces, w)
			o.close()
		}
	}
}

func (a *outputSet) closeWindow(w *wayland.Window) {
	if _, ok := a.pending[w]; ok {
		delete(a.pending, w)
		_ = w.Close()
	}
	if o := a.surfaces[w]; o != nil {
		delete(a.surfaces, w)
		o.close()
	}
}

func (o *outputSurface) close() {
	o.p.Close()
	_ = o.s.Close()
}

func (a *outputSet) closeAll() {
	for w, o := range a.surfaces {
		delete(a.surfaces, w)
		o.close()
	}
	for w := range a.pending {
		delete(a.pending, w)
		_ = w.Close()
	}
}
