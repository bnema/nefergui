//go:build linux

package wayland

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/bnema/wlturbo"
	"github.com/bnema/wlturbo/protocol/core"
	"github.com/bnema/wlturbo/wl"

	"github.com/bnema/nefergui/internal/platform/wayland/layershell"
)

// zwlr_layer_shell_v1 layers. The values are the wire values.
const (
	LayerBackground uint32 = 0
	LayerBottom     uint32 = 1
	LayerTop        uint32 = 2
	LayerOverlay    uint32 = 3
)

// zwlr_layer_surface_v1 anchor bits. They may be OR-ed together.
const (
	AnchorTop    uint32 = 1
	AnchorBottom uint32 = 2
	AnchorLeft   uint32 = 4
	AnchorRight  uint32 = 8

	anchorAll = AnchorTop | AnchorBottom | AnchorLeft | AnchorRight
)

// zwlr_layer_surface_v1 keyboard interactivity modes.
const (
	KeyboardNone      uint32 = 0
	KeyboardExclusive uint32 = 1
	// KeyboardOnDemand needs zwlr_layer_shell_v1 version 4.
	KeyboardOnDemand uint32 = 2
)

const (
	layerShellSupported = 4
	outputNameVersion   = 4 // wl_output.name was added in version 4
	// MaxLayerDimension bounds a compositor-assigned layer surface extent.
	MaxLayerDimension = 16384
	// DefaultLayerNamespace is used when LayerOptions.Namespace is empty. The
	// namespace is an opaque, generic role hint; nothing privileged depends on it.
	DefaultLayerNamespace = "nefergui"
)

// Rect is an integer rectangle in surface-local logical coordinates.
type Rect struct{ X, Y, W, H int32 }

// LayerOptions selects wlr-layer-shell instead of xdg-toplevel.
type LayerOptions struct {
	// Output is the exact wl_output name (for example "DP-1"). Empty lets the
	// compositor choose its preferred output. A named output that is absent
	// fails the connection; a named output removed later closes the window.
	Output string
	// OutputGlobal, when non-zero, selects an output lifetime returned by
	// Connection.Outputs instead of Output. The connection owner handles its
	// removal (OutputRemoved); the window is not closed for it.
	OutputGlobal uint32
	// Namespace is the layer surface namespace; empty selects DefaultLayerNamespace.
	Namespace string
	// Anchor is a bit set of Anchor* values.
	Anchor uint32
	// Layer is one of the Layer* values.
	Layer uint32
	// Keyboard is one of the Keyboard* values.
	Keyboard uint32
	// ExclusiveZone follows zwlr_layer_surface_v1.set_exclusive_zone
	// (positive reserves, 0 is neutral, -1 ignores other zones).
	ExclusiveZone int32
	// Margin is ordered top, right, bottom, left, like the protocol request.
	Margin [4]int32
}

// SurfaceOptions configures the role and input region of a new Window. The zero
// value selects the default xdg-toplevel with full-surface input.
type SurfaceOptions struct {
	// Layer, when non-nil, creates a layer surface and binds no xdg shell.
	Layer *LayerOptions
	// Lock selects a private output lifetime on this Connection's sole lock.
	// It is incompatible with Layer, transparency and custom input regions.
	Lock *LockOptions
	// InputRects is the initial input region in surface-local logical pixels.
	// nil means the whole surface (the protocol default); a non-nil empty slice
	// means no input (pointer and touch pass through).
	InputRects []Rect
}

// Validate reports the first invalid option.
func (o SurfaceOptions) Validate() error {
	if o.Lock != nil && (o.Layer != nil || o.InputRects != nil) {
		return errors.New("lock role cannot have layer or custom input")
	}
	if o.Layer != nil {
		if err := o.Layer.Validate(); err != nil {
			return err
		}
	}
	return validateRects(o.InputRects)
}

// Validate reports the first invalid layer option.
func (o LayerOptions) Validate() error {
	switch {
	case o.Layer > LayerOverlay:
		return fmt.Errorf("invalid layer %d", o.Layer)
	case o.Anchor&^anchorAll != 0:
		return fmt.Errorf("invalid anchor bits %#x", o.Anchor)
	case o.ExclusiveZone < -1:
		return fmt.Errorf("invalid exclusive zone %d", o.ExclusiveZone)
	case o.Keyboard > KeyboardOnDemand:
		return fmt.Errorf("invalid keyboard interactivity %d", o.Keyboard)
	case strings.ContainsRune(o.Namespace, 0):
		return errors.New("layer namespace contains NUL")
	case strings.ContainsRune(o.Output, 0):
		return errors.New("layer output contains NUL")
	case o.OutputGlobal != 0 && o.Output != "":
		return errors.New("layer output name and global are exclusive")
	}
	return nil
}

func validateRects(rects []Rect) error {
	for i, r := range rects {
		if r.W <= 0 || r.H <= 0 {
			return fmt.Errorf("input rect %d has non-positive size %dx%d", i, r.W, r.H)
		}
		if int64(r.X)+int64(r.W) > math.MaxInt32 || int64(r.Y)+int64(r.H) > math.MaxInt32 {
			return fmt.Errorf("input rect %d overflows", i)
		}
	}
	return nil
}

// layerConfigureSize validates a configure extent. Zero leaves that axis unchanged.
func layerConfigureSize(width, height uint32) (int32, int32, error) {
	if width > MaxLayerDimension || height > MaxLayerDimension {
		return 0, 0, fmt.Errorf("layer configure size %dx%d exceeds %d", width, height, MaxLayerDimension)
	}
	return int32(width), int32(height), nil
}

// SetTitle sets the toplevel title. Layer surfaces have no title, so it is a no-op.
func (w *Window) SetTitle(title string) error {
	if w == nil || w.Toplevel == nil {
		return nil
	}
	return w.Toplevel.SetTitle(title)
}

// IsLayer reports whether the window is a layer surface.
func (w *Window) IsLayer() bool { return w != nil && w.LayerSurface != nil }

// StageInputRects validates rects and stages them as the pending input region
// without committing; the next commit (normally Present) applies them. nil
// restores the full surface and a non-nil empty slice makes the surface pass
// all input through. A request identical to the current one sends nothing and
// allocates nothing. Call from the session owner loop only.
func (w *Window) StageInputRects(rects []Rect) error {
	_, err := w.stageInputRects(rects)
	return err
}

// SetInputRects stages rects like StageInputRects and, only if they changed and
// the window is configured, commits immediately. Before the first configure
// the initial commit carries the region.
func (w *Window) SetInputRects(rects []Rect) error {
	changed, err := w.stageInputRects(rects)
	if err != nil || !changed || !w.Configured {
		return err
	}
	return w.Surface.Commit()
}

func (w *Window) stageInputRects(rects []Rect) (changed bool, err error) {
	if w == nil || w.Surface == nil || w.Compositor == nil {
		return false, errors.New("window has no surface")
	}
	if err = validateRects(rects); err != nil {
		return false, err
	}
	if (rects != nil) == w.inputCustom && slices.Equal(rects, w.inputRects) {
		return false, nil
	}
	if err = w.stageInputRegion(rects); err != nil {
		return false, err
	}
	w.inputCustom = rects != nil
	w.inputRects = append(w.inputRects[:0], rects...)
	return true, nil
}

func (w *Window) stageInputRegion(rects []Rect) error {
	if rects == nil {
		return w.Surface.SetInputRegion(nil)
	}
	region, err := w.Compositor.CreateRegion()
	if err != nil {
		return err
	}
	for _, r := range rects {
		if err = region.Add(r.X, r.Y, r.W, r.H); err != nil {
			break
		}
	}
	if err == nil {
		err = w.Surface.SetInputRegion(region)
	}
	_ = region.Destroy()
	return err
}

// applyLayerSize applies a validated layer configure; zero axes are unchanged.
func (w *Window) applyLayerSize(width, height int32) {
	if width > 0 && w.Width != width {
		w.Width, w.Dirty = width, true
	}
	if height > 0 && w.Height != height {
		w.Height, w.Dirty = height, true
	}
}

// bindLayerShell binds zwlr_layer_shell_v1 (up to version 4).
func (w *Window) bindLayerShell(bind func(string, uint32, uint32, wl.Proxy) error) error {
	ls := layershell.NewLayerShell(w.Display.Context())
	if err := bind(layershell.LayerShellInterface, layerShellSupported, 1, ls); err != nil {
		w.Display.Context().Unregister(ls) // never bound: no destructor to send
		return err
	}
	w.LayerShell = ls
	return nil
}

type outputCandidate struct {
	out    *core.Output
	global uint32
	name   string
}

// releaseOutputs releases every candidate except keep.
func releaseOutputs(cands []*outputCandidate, keep *outputCandidate) {
	for _, c := range cands {
		if c != keep {
			_ = c.out.Release()
		}
	}
}

// selectOutput binds every wl_output that can report a name and returns the
// global name of the exact match. Unmatched outputs are released.
func (w *Window) selectOutput(name string) (*core.Output, uint32, error) {
	if w.connection != nil {
		for _, out := range w.connection.outputs {
			if out.Name == name {
				return out.proxy, out.Global, nil
			}
		}
		return nil, 0, &CapabilityError{Name: core.OutputInterface, Cause: fmt.Errorf("output %q not found in applied inventory", name)}
	}
	reg := w.Display.Registry()
	var globals []wl.Global
	legacy := 0 // outputs older than v4 cannot report a name
	for _, g := range reg.GetGlobals() {
		if g.Interface != core.OutputInterface {
			continue
		}
		if g.Version >= outputNameVersion {
			globals = append(globals, g)
		} else {
			legacy++
		}
	}
	sort.Slice(globals, func(i, j int) bool { return globals[i].Name < globals[j].Name })
	cands := make([]*outputCandidate, 0, len(globals))
	ctx := w.Display.Context()
	for _, g := range globals {
		c := &outputCandidate{out: core.NewOutput(ctx), global: g.Name}
		c.out.OnName(func(n string) { c.name = n })
		if err := reg.Bind(g.Name, core.OutputInterface, outputNameVersion, c.out); err != nil {
			ctx.Unregister(c.out)
			releaseOutputs(cands, nil)
			return nil, 0, &CapabilityError{Name: core.OutputInterface, Cause: err}
		}
		cands = append(cands, c)
	}
	if len(cands) > 0 {
		if err := w.Display.Roundtrip(); err != nil {
			releaseOutputs(cands, nil)
			return nil, 0, err
		}
	}
	var chosen *outputCandidate
	for _, c := range cands {
		if c.name == name {
			chosen = c
			break
		}
	}
	releaseOutputs(cands, chosen)
	if chosen == nil {
		cause := fmt.Errorf("output %q not found", name)
		if legacy > 0 {
			cause = fmt.Errorf("output %q not found; %d wl_output global(s) older than version %d cannot report names", name, legacy, outputNameVersion)
		}
		return nil, 0, &CapabilityError{Name: core.OutputInterface, Cause: cause}
	}
	return chosen.out, chosen.global, nil
}

// createLayerSurface creates the role object and stages all double-buffered
// state. The caller performs the initial empty commit.
func (w *Window) createLayerSurface(o LayerOptions) error {
	if o.Keyboard == KeyboardOnDemand && w.LayerShell.Version() < 4 {
		return &CapabilityError{Name: layershell.LayerShellInterface, Cause: fmt.Errorf("on-demand keyboard requires version 4, compositor negotiated %d", w.LayerShell.Version())}
	}
	var out *core.Output
	if o.OutputGlobal != 0 {
		if w.connection == nil {
			return errors.New("layer output global requires a connection")
		}
		sel, ok := w.connection.outputs[o.OutputGlobal]
		if !ok || sel.proxy == nil {
			return fmt.Errorf("layer output %d unavailable", o.OutputGlobal)
		}
		out = sel.proxy
	} else if o.Output != "" {
		var global uint32
		var err error
		if out, global, err = w.selectOutput(o.Output); err != nil {
			return err
		}
		w.layerOutputGlobal, w.layerOutputSelected = global, true
		w.Display.Registry().AddGlobalRemoveHandler(outputRemoved{w})
	}
	ns := o.Namespace
	if ns == "" {
		ns = DefaultLayerNamespace
	}
	ls, err := w.LayerShell.GetLayerSurface(w.Surface, out, o.Layer, ns)
	if out != nil && w.connection == nil {
		// The compositor keeps its own reference to the output. A shared
		// connection owns the proxy in its output inventory instead.
		_ = out.Release()
	}
	if err != nil {
		return err
	}
	w.LayerSurface = ls
	ls.OnClosed(func() { w.postClosed() })
	ls.OnConfigure(func(serial, width, height uint32) {
		cw, ch, e := layerConfigureSize(width, height)
		if e != nil {
			w.feedbackFailure(e)
			return
		}
		if w.readerStarted.Load() {
			w.post(Event{Kind: ConfigureLayer, Width: cw, Height: ch, Serial: serial})
			return
		}
		w.applyLayerSize(cw, ch)
		if e = ls.AckConfigure(serial); e != nil {
			w.feedbackFailure(e)
			return
		}
		w.Configured, w.FrameReady = true, true
	})
	// Opposite anchors let the compositor assign that axis (size 0).
	reqW, reqH := uint32(w.Width), uint32(w.Height)
	if o.Anchor&(AnchorLeft|AnchorRight) == AnchorLeft|AnchorRight {
		reqW = 0
	}
	if o.Anchor&(AnchorTop|AnchorBottom) == AnchorTop|AnchorBottom {
		reqH = 0
	}
	if err = ls.SetSize(reqW, reqH); err != nil {
		return err
	}
	if err = ls.SetAnchor(o.Anchor); err != nil {
		return err
	}
	if err = ls.SetExclusiveZone(o.ExclusiveZone); err != nil {
		return err
	}
	if err = ls.SetMargin(o.Margin[0], o.Margin[1], o.Margin[2], o.Margin[3]); err != nil {
		return err
	}
	return ls.SetKeyboardInteractivity(o.Keyboard)
}

// outputRemoved closes the window when its selected output disappears.
type outputRemoved struct{ w *Window }

func (h outputRemoved) HandleRegistryGlobalRemove(ev wlturbo.RegistryGlobalRemoveEvent) {
	if h.w.layerOutputSelected && ev.Name == h.w.layerOutputGlobal {
		h.w.postClosed()
	}
}

func (w *Window) postClosed() {
	if w.readerStarted.Load() {
		w.post(Event{Kind: CloseEvent})
		return
	}
	w.Closed = true
}

// closeLayer destroys the layer role and its factory; the wl_surface goes last.
func (w *Window) closeLayer() {
	if w.LayerSurface != nil {
		_ = w.LayerSurface.Destroy()
		w.LayerSurface = nil
	}
	if w.LayerShell != nil {
		// zwlr_layer_shell_v1.destroy needs version 3; otherwise forget it locally.
		if w.LayerShell.Destroy() != nil {
			if c := w.LayerShell.Context(); c != nil {
				c.Unregister(w.LayerShell)
			}
		}
		w.LayerShell = nil
	}
}
