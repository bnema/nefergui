package ui

import (
	"context"
	"sync"

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
	buildMu               sync.Mutex
	mu                    sync.Mutex
	pending               []inputEvent
	wake                  chan struct{}
	generation            uint64
	committed             *element
	arenas                [2]elementArena // one holds the committed tree, the other builds the next
	committedArena        int
	styles                *css.Engine
	output                layout.Output
	state                 interactionState
	width, height, scale  float64
	textEngine            *text.Engine
	edits                 map[string]*edit.State
	clipboard             edit.Clipboard
	pasteCtx              context.Context
	pasteCancel           context.CancelFunc
	pasteID               uint64
	ime                   edit.IME
	redraw                bool
	inputRects            []Rect
	inputNil, inputStaged bool
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
	width, height := r.width, r.height
	r.mu.Unlock()
	// Build in the arena that does not hold the committed tree, which this
	// frame reads as previous. A frame that fails to commit leaves it as is.
	next := 1 - r.committedArena
	arena := &r.arenas[next]
	arena.reset()
	f := &Frame{arena: arena, width: width, height: height, generation: gen, active: true, events: events, previous: previous, styles: r.styles, state: state, edits: r.edits, clipboard: r.clipboard, ime: r.ime, layout: r.output, owner: r}
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
		f.refresh(f.root)
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
	r.committed, r.committedArena = f.root, next
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
