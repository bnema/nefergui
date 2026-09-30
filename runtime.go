package nefergui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/bnema/nefergui/internal/platform/wayland"
	"github.com/bnema/nefergui/internal/presentation/session"
	"github.com/bnema/nefergui/internal/render"

	"github.com/bnema/nefergui/internal/css"
	"github.com/bnema/nefergui/internal/edit"
	"github.com/bnema/nefergui/internal/layout"
	"github.com/bnema/nefergui/internal/text"
)

// inputEvent is routed against the last committed layout. Target is opaque.
type inputEvent struct {
	Target      *identity
	Kind        string
	Text        string
	Err         error
	Shift, Ctrl bool
	IME         edit.IMEBatch
	X, Y        float64     // pointer position in logical pixels
	Track       layout.Rect // committed slider content box
}

// runtime serializes frame construction; Queue and Redraw may be called concurrently.
// Re-entrant or concurrent Build requests a follow-up redraw instead of waiting.
type runtime struct {
	buildMu              sync.Mutex
	mu                   sync.Mutex
	pending              []inputEvent
	wake                 chan struct{}
	generation           uint64
	committed            *element
	styles               *css.Engine
	output               layout.Output
	state                interactionState
	width, height, scale float64
	textEngine           *text.Engine
	edits                map[string]*edit.State
	clipboard            edit.Clipboard
	pasteCtx             context.Context
	pasteCancel          context.CancelFunc
	pasteID              uint64
	ime                  edit.IME
	redraw               bool
}

func newRuntime() *runtime {
	return &runtime{wake: make(chan struct{}, 1), styles: css.Compile(css.UA(), css.Sheet{}), width: 800, height: 600, scale: 1, edits: make(map[string]*edit.State)}
}
func (r *runtime) Queue(ev inputEvent) {
	if ev.Target == nil {
		return
	}
	r.mu.Lock()
	r.pending = append(r.pending, ev)
	r.mu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Redraw requests a frame even without input; duplicate requests coalesce.
func (r *runtime) Redraw() {
	r.mu.Lock()
	r.redraw = true
	r.mu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Target is a test-only committed-child-path helper; platform input uses route and Hit.
func (r *runtime) Target(path ...int) *identity {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := r.committed
	for _, i := range path {
		if e == nil || i < 0 || i >= len(e.children) {
			return nil
		}
		e = e.children[i]
	}
	if e == nil {
		return nil
	}
	return e.identity
}

// Build drains all already queued input in one synchronous frame. A concurrent
// or re-entrant call never waits: it requests another frame and returns false.
// The owner completes its commit before the loop can build that next frame.
func (r *runtime) Build(view func(*Frame)) bool {
	if !r.buildMu.TryLock() {
		r.Redraw()
		return false
	}
	defer r.buildMu.Unlock()
	r.mu.Lock()
	// Discard the wake token for the batch we are about to process. New input
	// arriving during construction leaves its own wake token for the next build.
	select {
	case <-r.wake:
	default:
	}
	if len(r.pending) == 0 && !r.redraw && r.generation != 0 {
		r.mu.Unlock()
		return false
	}
	events := r.pending
	r.pending = nil
	r.redraw = false
	r.generation++
	gen := r.generation
	previous := r.committed
	state := r.state
	r.mu.Unlock()
	f := &Frame{generation: gen, active: true, events: events, previous: previous, styles: r.styles, state: state, edits: r.edits, clipboard: r.clipboard, ime: r.ime, layout: r.output, owner: r}
	defer func() {
		f.active = false
		r.styles.EndFrame()
		if r.textEngine != nil {
			r.textEngine.EndFrame()
		}
		// A panicking view is not committed; the next build may retry.
		if recovered := recover(); recovered != nil {
			r.Redraw()
			panic(recovered)
		}
	}()
	view(f)
	r.mu.Lock()
	// State can change while the view runs. Schedule another frame instead of
	// overwriting that newer input with this frame's snapshot.
	if f.root != nil {
		var refresh func(*element)
		refresh = func(e *element) {
			f.compute(e)
			for _, ch := range e.children {
				refresh(ch)
			}
		}
		refresh(f.root)
		input := r.layoutTree(f.root)
		out, err := layout.Layout(input, layout.Options{Width: r.width, Height: r.height, TextEngine: r.textEngine})
		if err != nil {
			r.mu.Unlock()
			r.Redraw()
			return false
		}
		// Re-layout at most once after moving a focused caret into its editor
		// viewport; the offset is per frame identity and bounded by measured extent.
		if r.scrollCaret(f.root, &out) {
			input = r.layoutTree(f.root)
			out, err = layout.Layout(input, layout.Options{Width: r.width, Height: r.height, TextEngine: r.textEngine})
			if err != nil {
				r.mu.Unlock()
				r.Redraw()
				return false
			}
		}
		r.paintEditors(f.root, &out)
		r.paintIndicators(f.root, &out)
		r.output = out
	}
	r.committed = f.root
	// Events mutate the model while the view runs, so elements declared before
	// the handling control painted the previous value. One follow-up frame,
	// without events, shows the settled state; it cannot schedule another.
	if len(events) > 0 {
		r.redraw = true
	}
	// Release every losing editor before enabling the winner: the IME port
	// represents one seat, not one port per editor. Never depend on map order.
	for id, editor := range r.edits {
		e := r.lookup(id)
		if e == nil || (e.typ != "input" && e.typ != "textarea") || !e.identity.same(r.state.focus) || e.disabled {
			editor.Blur(r.ime)
			if e == nil || (e.typ != "input" && e.typ != "textarea") {
				delete(r.edits, id)
			}
		}
	}
	if focused := r.lookup(idKey(r.state.focus)); focused != nil && (focused.typ == "input" || focused.typ == "textarea") && !focused.disabled {
		if editor := r.edits[idKey(focused.identity)]; editor != nil && !editor.IMEActive() && r.ime != nil {
			editor.Focus(r.ime, focused.password, focused.typ == "textarea")
		}
	}
	if r.state.capture != nil && resultByID(r.output.Tree, idKey(r.state.capture)) == nil {
		r.state.capture = nil
		r.state.down = false
		r.redraw = true
	}
	if r.state.space != nil && (resultByID(r.output.Tree, idKey(r.state.space)) == nil || !focusable(r.lookup(idKey(r.state.space)))) {
		r.state.space = nil
		r.redraw = true
	}
	if r.state.focus != nil && (resultByID(r.output.Tree, idKey(r.state.focus)) == nil || !focusable(r.lookup(idKey(r.state.focus)))) {
		r.setFocus(nil, false)
		r.redraw = true
	}
	if r.state.inside {
		hit := r.hit(r.state.x, r.state.y)
		var id *identity
		if hit != nil {
			id = hit.identity
		}
		if !r.state.hover.same(id) {
			r.state.hover = id
			r.redraw = true
		}
	}
	for key := range r.state.scroll {
		n := resultByID(r.output.Tree, key)
		e := r.lookup(key)
		if n == nil || (!scrollable(e, n) && (e == nil || (e.typ != "input" && e.typ != "textarea"))) {
			delete(r.state.scroll, key)
		} else {
			r.state.scroll[key] = layout.Size{W: n.ScrollX, H: n.ScrollY}
		}
	}
	follow := r.redraw
	r.mu.Unlock()
	if follow {
		select {
		case r.wake <- struct{}{}:
		default:
		}
	}
	return true
}

// Wait blocks until input/redraw is available or the context is cancelled.
func (r *runtime) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.wake:
		return nil
	}
}

// Run builds and presents an immediate view on the Wayland session owner loop.
func Run[T any](ctx context.Context, model *T, view func(*Frame, *T), options ...WindowOption) error {
	return runWithCommit(ctx, model, view, nil, options...)
}

// RunFrames is a deterministic harness entry: each commit requests a redraw.
func RunFrames[T any](ctx context.Context, frames int, model *T, view func(*Frame, *T), committed func(uint64) error, options ...WindowOption) error {
	if frames < 1 {
		return errors.New("nefergui: frames must be positive")
	}
	return runWithCommit(ctx, model, view, func(frame uint64) (bool, error) {
		if committed != nil {
			if err := committed(frame); err != nil {
				return false, err
			}
		}
		return frame >= uint64(frames), nil
	}, options...)
}

func runWithCommit[T any](ctx context.Context, model *T, view func(*Frame, *T), committed func(uint64) (bool, error), options ...WindowOption) (err error) {
	if model == nil || view == nil {
		return errors.New("nefergui: nil model or view")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	cfg := windowConfig{title: "NeferGUI", width: 800, height: 600}
	for _, o := range options {
		if o == nil {
			return errors.New("nefergui: nil window option")
		}
		o.window(&cfg)
	}
	if cfg.width <= 0 || cfg.height <= 0 || int64(cfg.width) > 1<<31-1 || int64(cfg.height) > 1<<31-1 {
		return fmt.Errorf("nefergui: invalid size %dx%d", cfg.width, cfg.height)
	}
	r := newRuntime()
	if cfg.styles != "" {
		data, err := os.ReadFile(cfg.styles)
		if err != nil {
			return fmt.Errorf("nefergui: styles: %w", err)
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
	s, err := session.Open("", int32(cfg.width), int32(cfg.height), cfg.transparent)
	if err != nil {
		return fmt.Errorf("nefergui: open platform: %w", err)
	}
	// Close errors (debug artifact writes) are reported after any earlier error.
	defer func() { err = errors.Join(err, s.Close()) }()
	if err := s.Window.Toplevel.SetTitle(cfg.title); err != nil {
		return fmt.Errorf("nefergui: title: %w", err)
	}
	r.pasteCtx = ctx
	defer func() {
		if r.pasteCancel != nil {
			r.pasteCancel()
		}
	}()
	var clipboard *wayland.Clipboard
	var ime *wayland.IME
	if s.Window.Seat != nil {
		clipboard, err = wayland.NewClipboard(s.Window.Display, s.Window.Seat, func() uint32 {
			if a := s.Window.InputAdapter(); a != nil {
				return a.SelectionSerial()
			}
			return 0
		}, s.Window.PostClipboard)
		if err != nil {
			return fmt.Errorf("nefergui: clipboard: %w", err)
		}
		defer clipboard.Close()
		ime, err = wayland.NewIME(s.Window.Display, s.Window.Seat, s.Window.Surface, s.Window.PostIME)
		if err != nil {
			return fmt.Errorf("nefergui: IME: %w", err)
		}
		defer ime.Close()
	}
	if clipboard != nil {
		r.clipboard = clipboard
	}
	if ime != nil {
		r.ime = ime
	}
	loopCtx, stop := context.WithCancel(ctx)
	defer stop()
	finished := false
	err = s.RunApp(loopCtx, session.AppHooks{
		Wake: r.wake,
		Input: func(inputs []wayland.Input) error {
			for _, input := range inputs {
				// An overflow reset keeps the latest enter or leave in the
				// queue, so the serial stays valid across it.
				switch {
				case input.Enter:
					s.Window.PointerEntered(input.Serial, input.PointerGen)
				case input.Kind == "leave":
					s.Window.PointerLeft()
				}
				r.routeInput(input)
			}
			// Hover moves without a rebuild; update the cursor from the last
			// committed styles now, and again after the next build.
			return s.Window.SetCursor(cursorShape(r.cursor()))
		},
		Event: func(ev wayland.Event) error {
			switch ev.Kind {
			case wayland.ClipboardInput:
				if clipboard != nil {
					clipboard.ApplyClipboard(ev.Clipboard)
				}
			case wayland.IMEInput:
				if ime != nil {
					if batch, ok := ime.ApplyIME(ev.IME); ok {
						r.route(platformInput{Kind: "ime-done", IME: batch})
					}
				}
			}
			return nil
		},
		Committed: func(frame uint64) error {
			if committed == nil {
				return nil
			}
			done, err := committed(frame)
			if err != nil {
				return err
			}
			if done {
				finished = true
				stop()
			} else {
				r.Redraw()
			}
			return nil
		},
		Draw: func() (render.Frame, bool, error) {
			r.route(platformInput{Kind: "resize", Width: float64(s.Window.Width), Height: float64(s.Window.Height), Scale: s.Window.Scale})
			if !r.Build(func(f *Frame) { view(f, model) }) {
				return render.Frame{}, false, nil
			}
			if dir := os.Getenv("NEFERGUI_DEBUG_DIR"); dir != "" {
				data, e := json.Marshal(r.output.Tree)
				if e != nil {
					return render.Frame{}, false, e
				}
				if e = os.WriteFile(filepath.Join(dir, "layout.json"), data, 0600); e != nil {
					return render.Frame{}, false, e
				}
			}
			if ime != nil && ime.Err != nil {
				return render.Frame{}, false, ime.Err
			}
			if err := s.Window.SetCursor(cursorShape(r.cursor())); err != nil {
				return render.Frame{}, false, err
			}
			w, h, err := s.Window.PhysicalSize()
			if err != nil {
				return render.Frame{}, false, err
			}
			frame, err := s.Preparer.Prepare(r.output.Display, s.Window.Scale, int(w), int(h))
			return frame, true, err
		},
	})
	if finished && errors.Is(err, context.Canceled) && ctx.Err() == nil {
		return nil
	}
	return err
}

type WindowOption interface{ window(*windowConfig) }
type windowConfig struct {
	title, styles string
	width, height int
	transparent   bool
}
type windowOption func(*windowConfig)

func (o windowOption) window(c *windowConfig) { o(c) }
func Title(s string) WindowOption             { return windowOption(func(c *windowConfig) { c.title = s }) }
func Size(w, h int) WindowOption {
	return windowOption(func(c *windowConfig) { c.width = w; c.height = h })
}
func Styles(s string) WindowOption { return windowOption(func(c *windowConfig) { c.styles = s }) }

// Transparent requests an alpha-capable Wayland surface (opaque by default).
func Transparent() WindowOption { return windowOption(func(c *windowConfig) { c.transparent = true }) }
