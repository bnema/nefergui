//go:build linux

package ui

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"slices"

	"github.com/bnema/nefergui/internal/css"
	"github.com/bnema/nefergui/internal/edit"
	"github.com/bnema/nefergui/internal/layout"
	"github.com/bnema/nefergui/internal/presentation/session"
	"github.com/bnema/nefergui/internal/presentation/vkdevice"
	"github.com/bnema/nefergui/internal/text"
)

// Format is a DRM format and modifier the compositor accepts.
type Format struct {
	FourCC   uint32
	Modifier uint64
}

// Clipboard is the optional asynchronous clipboard; IME the optional input method.
type (
	Clipboard = edit.AsyncClipboard
	IME       = edit.IME
)

// Plane, Timeline and Retired are the transport-facing parts of Output.
type (
	Plane    = session.Plane
	Timeline = session.Timeline
	Retired  = session.Retired
)

// Cursor is the pointer shape the surface wants.
type Cursor uint8

const (
	CursorDefault Cursor = iota
	CursorPointer
	CursorText
	CursorNotAllowed
)

// RendererConfig configures a Wayland-free Renderer.
type RendererConfig struct {
	MainDevice  uint64   // dev_t from linux-dmabuf feedback main_device
	Formats     []Format // compositor preference order, tranches flattened
	Transparent bool     // ARGB8888 instead of XRGB8888
	Styles      string   // optional CSS file path
	Clipboard   Clipboard
	IME         IME
}

// Input is one platform input event. Text is read only during the call.
type Input struct {
	Kind         InputKind
	X, Y, DX, DY float64 // logical pixels; DX, DY are scroll deltas
	Button       uint32  // evdev code
	Pressed      bool
	Repeat       bool
	Keysym       uint32 // xkb keysym
	Text         []byte
	Modifiers    Modifiers
}

// Output describes one frame to present, in physical pixels. Every file
// descriptor stays owned by the Renderer. Slices are reused and valid until the
// next Render.
type Output struct {
	Buffer            uint64
	NewBuffer         bool
	Retired           []Retired
	Width, Height     int32
	FourCC            uint32
	Modifier          uint64
	Planes            [4]Plane
	PlaneCount        int
	Acquire, Release  Timeline
	NewTimelines      bool
	AcquirePoint      uint64
	ReleasePoint      uint64
	ReleaseFD         int
	Damage            []Rect
	Cursor            Cursor
	InputRects        []Rect
	InputRectsChanged bool
}

// target is the GPU seam of Renderer. *session.Target implements it.
type target interface {
	Resize(width, height int32)
	Draw(display []layout.Command, scale float64, out *session.Output) (bool, error)
	Released(buffer uint64) error
	Waiting() bool
	Close() error
}

// Renderer renders views into DMA-BUF buffers for a transport it does not
// know. It owns no Wayland object and starts no goroutine. All methods except
// Wake run on one owner goroutine.
type Renderer struct {
	rt     *runtime
	target target
	tout   session.Output
	sized  bool
	unsent bool // a built frame still waits for a free buffer
	w, h   float64
	scale  float64
	pw, ph int32
	closed bool

	// Last input region handed out, to report only changes.
	region     []Rect
	regionNil  bool
	haveRegion bool
}

// asyncClipboard lets an AsyncClipboard satisfy the editor's Clipboard port;
// synchronous reads are not supported, paste uses ReadTextAsync.
type asyncClipboard struct{ edit.AsyncClipboard }

func (asyncClipboard) ReadText(int) ([]byte, error) {
	return nil, errors.New("nefergui: synchronous clipboard read not supported")
}

// NewRenderer opens the GPU for cfg.MainDevice and loads fonts and styles. It
// allocates no images.
func NewRenderer(cfg RendererConfig) (*Renderer, error) {
	formats := make([]vkdevice.Format, len(cfg.Formats))
	for i, f := range cfg.Formats {
		formats[i] = vkdevice.Format{FourCC: f.FourCC, Modifier: f.Modifier}
	}
	t, err := session.NewTarget(session.TargetConfig{MainDevice: cfg.MainDevice, Formats: formats, Transparent: cfg.Transparent})
	if err != nil {
		return nil, fmt.Errorf("nefergui: open renderer: %w", err)
	}
	r, err := newRenderer(cfg, t, text.SystemSource{})
	if err != nil {
		_ = t.Close()
		return nil, err
	}
	return r, nil
}

func newRenderer(cfg RendererConfig, t target, fonts text.FontSource) (*Renderer, error) {
	rt := newRuntime()
	if cfg.Styles != "" {
		data, err := os.ReadFile(cfg.Styles)
		if err != nil {
			return nil, fmt.Errorf("nefergui: styles: %w", err)
		}
		rt.styles = css.Compile(css.UA(), css.Parse(string(data)))
	}
	catalog, err := text.Load(fonts)
	if err != nil {
		return nil, fmt.Errorf("nefergui: fonts: %w", err)
	}
	if len(catalog.Faces) == 0 {
		return nil, errors.New("nefergui: no usable fonts")
	}
	rt.setTextEngine(text.NewEngine(catalog))
	ctx, cancel := context.WithCancel(context.Background())
	rt.pasteCtx, rt.pasteCancel = ctx, cancel
	if cfg.Clipboard != nil {
		rt.clipboard = asyncClipboard{cfg.Clipboard}
	}
	if cfg.IME != nil {
		rt.ime = cfg.IME
	}
	return &Renderer{rt: rt, target: t, scale: 1}, nil
}

// Wake returns a channel signaled when work arrives from another goroutine
// (for example a finished paste); call Render after receiving. Safe from any goroutine.
func (r *Renderer) Wake() <-chan struct{} { return r.rt.wake }

// Pending reports that a built frame is waiting for the GPU (a buffer still
// being drawn, or a release wait not yet installed) rather than for the
// compositor. No event announces this: arm a short timer (for example 2 ms)
// and call Render again. It is false while every buffer belongs to the
// compositor; Released is the wake-up for that case.
func (r *Renderer) Pending() bool { return r.unsent && r.target.Waiting() }

// Resize sets the logical surface size and scale. Invalid values are ignored.
func (r *Renderer) Resize(width, height int, scale float64) {
	if r.closed {
		return
	}
	w, h := float64(width), float64(height)
	if width <= 0 || height <= 0 || !(scale > 0) || math.IsInf(scale, 0) {
		return
	}
	pw, ph := math.Ceil(w*scale-1e-9), math.Ceil(h*scale-1e-9)
	if pw > math.MaxInt32 || ph > math.MaxInt32 {
		return
	}
	if r.sized && r.w == w && r.h == h && r.scale == scale {
		return
	}
	r.sized, r.w, r.h, r.scale = true, w, h, scale
	r.pw, r.ph = int32(pw), int32(ph)
	r.target.Resize(r.pw, r.ph)
	r.rt.route(platformInput{Kind: "resize", Width: w, Height: h, Scale: scale})
}

// Invalidate requests a rebuild at the next Render.
func (r *Renderer) Invalidate() { r.rt.Redraw() }

// Input routes one event and reports whether a redraw is needed.
func (r *Renderer) Input(ev *Input) bool {
	if ev == nil || r.closed {
		return false
	}
	switch ev.Kind {
	case InputReset:
		r.rt.route(platformInput{Kind: "leave"})
		r.rt.route(platformInput{Kind: "focus-out"})
	case InputPointerMotion:
		r.rt.route(platformInput{Kind: "motion", X: ev.X, Y: ev.Y})
	case InputPointerPress:
		r.rt.route(platformInput{Kind: "press", X: ev.X, Y: ev.Y, Button: ev.Button, Shift: ev.Modifiers&ModShift != 0})
	case InputPointerRelease:
		r.rt.route(platformInput{Kind: "release", X: ev.X, Y: ev.Y, Button: ev.Button})
	case InputPointerAxis:
		if ev.DX == 0 && ev.DY == 0 {
			return false
		}
		r.rt.route(platformInput{Kind: "axis", X: ev.X, Y: ev.Y, DX: ev.DX, DY: ev.DY})
	case InputPointerLeave:
		r.rt.route(platformInput{Kind: "leave"})
	case InputFocusIn:
		r.rt.route(platformInput{Kind: "focus-in"})
	case InputFocusOut:
		r.rt.route(platformInput{Kind: "focus-out"})
	case InputKey:
		shift, ctrl := ev.Modifiers&ModShift != 0, ev.Modifiers&ModCtrl != 0
		key := keyEvent{Name: keysymName(ev.Keysym), Pressed: ev.Pressed, Repeat: ev.Repeat, Shift: shift, Ctrl: ctrl}
		if len(ev.Text) > 0 {
			key.Text = string(ev.Text)
		}
		r.rt.route(platformInput{Kind: "key", Key: key, Shift: shift, Ctrl: ctrl})
	default:
		return false
	}
	r.rt.mu.Lock()
	need := r.rt.redraw || len(r.rt.pending) > 0
	r.rt.mu.Unlock()
	return need
}

// Render builds the view and, when something changed and a buffer is free,
// draws it. It returns false without touching the GPU when nothing changed, no
// buffer is available yet (call it again after Released) or before the first
// Resize. When it returns true, out describes the frame to present.
func (r *Renderer) Render[T any](out *Output, model *T, view func(*Frame, *T)) (bool, error) {
	if r.closed {
		return false, errClosed
	}
	if out == nil || model == nil || view == nil {
		return false, errors.New("nefergui: nil output, model or view")
	}
	if !r.sized {
		return false, nil
	}
	if r.rt.Build(func(f *Frame) { view(f, model) }) {
		r.unsent = true
	}
	if !r.unsent {
		return false, nil
	}
	ok, err := r.target.Draw(r.rt.output.Display, r.scale, &r.tout)
	if err != nil || !ok {
		return false, err
	}
	r.unsent = false
	r.export(out)
	return true, nil
}

// Measure returns the logical size, in pixels rounded up, that view needs for
// model: its natural width, at most maxWidth, and the height the content takes
// at that width (text wraps at maxWidth). Use it to size a surface before
// creating it. The size includes the root's margins.
//
// The view is built in a temporary runtime that shares only this Renderer's
// styles and text engine. Those caches only grow during Measure: it does not
// age their entries, so a later Render finds its own entries intact. Hover,
// focus, scroll offsets, editor state, queued input, the Wake channel and the
// target are neither read nor changed, nothing is drawn, and the view sees no
// events.
//
// There is no surface yet, so the height is indefinite: inside the view
// Frame.Size reports an unbounded height (1<<20), and percentage height,
// min-height and max-height of the root are treated as auto, as for nested
// boxes, instead of resolving against that value. Lengths in pixels apply.
//
// Call it from the owner goroutine, before or after Resize, never from inside
// a view callback. It allocates and shapes text: use it when opening a
// surface, not on every frame. It returns an error when the Renderer is
// closed, model or view is nil, maxWidth is not a finite number above zero, or
// the view cannot be laid out (the layout error is wrapped). A view that
// declares no root measures 0 by 0.
func (r *Renderer) Measure[T any](model *T, view func(*Frame, *T), maxWidth float64) (width, height float64, err error) {
	if r.closed {
		return 0, 0, errClosed
	}
	if model == nil || view == nil {
		return 0, 0, errors.New("nefergui: nil model or view")
	}
	if math.IsNaN(maxWidth) || math.IsInf(maxWidth, 0) || maxWidth <= 0 {
		return 0, 0, errors.New("nefergui: Measure needs a finite maxWidth above zero")
	}
	const tall = 1 << 20
	tmp := &runtime{
		wake: make(chan struct{}, 1), styles: r.rt.styles, textEngine: r.rt.textEngine,
		width: maxWidth, height: tall, scale: 1, redraw: true,
		edits: make(map[string]*edit.State), measuring: true,
	}
	if !tmp.Build(func(f *Frame) { view(f, model) }) {
		if tmp.layoutErr != nil {
			return 0, 0, fmt.Errorf("nefergui: measure: %w", tmp.layoutErr)
		}
		return 0, 0, errors.New("nefergui: measure: could not build the view")
	}
	if tmp.committed == nil {
		return 0, 0, nil
	}
	root := tmp.layoutTree(tmp.committed)
	if root.Style != nil {
		st := layout.IndefiniteHeights(*root.Style)
		root.Style = &st
	}
	opts := layout.Options{Width: maxWidth, Height: tall, TextEngine: tmp.textEngine}
	natural, err := layout.Natural(root, opts)
	if err != nil {
		return 0, 0, fmt.Errorf("nefergui: measure: %w", err)
	}
	// Natural is a border box without margins, while the surface holds the
	// margin box: Layout subtracts the margins from the width it is given.
	var m layout.Edges
	if tmp.output.Tree != nil {
		m = tmp.output.Tree.Margin
	}
	w := math.Min(natural.W+math.Max(0, m.Left)+math.Max(0, m.Right), maxWidth)
	opts.Width = w
	out, err := layout.Layout(root, opts)
	if err != nil {
		return 0, 0, fmt.Errorf("nefergui: measure: %w", err)
	}
	if out.Tree == nil {
		return 0, 0, nil
	}
	tree := out.Tree
	h := tree.Border.Y + tree.Border.H + math.Max(0, tree.Margin.Bottom)
	return math.Ceil(w), math.Ceil(h), nil
}

// export copies the target's frame description into out and adds the parts
// derived from the UI: damage, cursor and the staged input region.
func (r *Renderer) export(out *Output) {
	t := &r.tout
	retired := append(out.Retired[:0], t.Retired...)
	damage := append(out.Damage[:0], Rect{Width: t.Width, Height: t.Height})
	rects := out.InputRects
	*out = Output{
		Buffer: t.Buffer, NewBuffer: t.NewBuffer, Retired: retired,
		Width: t.Width, Height: t.Height, FourCC: t.FourCC, Modifier: t.Modifier,
		Planes: t.Planes, PlaneCount: t.PlaneCount, Acquire: t.Acquire, Release: t.Release,
		NewTimelines: t.NewTimelines, AcquirePoint: t.AcquirePoint, ReleasePoint: t.ReleasePoint, ReleaseFD: t.ReleaseFD,
		Damage: damage, Cursor: cursorOf(r.rt.cursor()), InputRects: rects[:0],
	}
	rt := r.rt
	if !rt.inputStaged {
		return
	}
	rt.inputStaged = false
	// A region equal to the last reported one costs nothing.
	if r.haveRegion && r.regionNil == rt.inputNil && slices.Equal(r.region, rt.inputRects) {
		return
	}
	r.haveRegion, r.regionNil = true, rt.inputNil
	r.region = append(r.region[:0], rt.inputRects...)
	out.InputRectsChanged = true
	if rt.inputNil {
		out.InputRects = nil
		return
	}
	if out.InputRects == nil {
		out.InputRects = []Rect{}
	}
	out.InputRects = append(out.InputRects, rt.inputRects...)
}

func cursorOf(k css.Keyword) Cursor {
	switch k {
	case css.KeywordPointer:
		return CursorPointer
	case css.KeywordText:
		return CursorText
	case css.KeywordNotAllowed:
		return CursorNotAllowed
	}
	return CursorDefault
}

// Released must be called when the release eventfd of an Output buffer is
// readable. It lets the buffer be reused.
func (r *Renderer) Released(buffer uint64) error {
	if r.closed {
		return errClosed
	}
	return r.target.Released(buffer)
}

var errClosed = errors.New("nefergui: renderer closed")

// Close cancels pending pastes and frees the GPU resources. The Renderer is
// unusable afterwards.
func (r *Renderer) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	if r.rt.pasteCancel != nil {
		r.rt.pasteCancel()
	}
	return r.target.Close()
}

// xkb keysym values; internal/ui never imports an xkb library.
const (
	keysymSpace     = 0x0020
	keysymBackspace = 0xff08
	keysymTab       = 0xff09
	keysymReturn    = 0xff0d
	keysymEscape    = 0xff1b
	keysymHome      = 0xff50
	keysymLeft      = 0xff51
	keysymUp        = 0xff52
	keysymRight     = 0xff53
	keysymDown      = 0xff54
	keysymPageUp    = 0xff55
	keysymPageDown  = 0xff56
	keysymEnd       = 0xff57
	keysymKPEnter   = 0xff8d
	keysymISOLeft   = 0xfe20
	keysymDelete    = 0xffff
)

// keysymName maps a keysym to the name interaction.go dispatches on; other
// keysyms have no name because only their text matters.
func keysymName(sym uint32) string {
	switch sym {
	case keysymSpace:
		return "space"
	case keysymTab:
		return "tab"
	case keysymISOLeft:
		return "iso_left_tab"
	case keysymLeft:
		return "left"
	case keysymRight:
		return "right"
	case keysymUp:
		return "up"
	case keysymDown:
		return "down"
	case keysymHome:
		return "home"
	case keysymEnd:
		return "end"
	case keysymPageUp:
		return "page_up"
	case keysymPageDown:
		return "page_down"
	case keysymBackspace:
		return "backspace"
	case keysymDelete:
		return "delete"
	case 'a', 'A':
		return "a"
	case 'c', 'C':
		return "c"
	case 'x', 'X':
		return "x"
	case 'v', 'V':
		return "v"
	case keysymReturn:
		return "return"
	case keysymKPEnter:
		return "kp_enter"
	case keysymEscape:
		return "escape"
	}
	return ""
}
