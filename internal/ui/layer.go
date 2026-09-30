package ui

import (
	"fmt"
	"slices"

	"github.com/bnema/nefergui/internal/platform/wayland"
)

// LayerLevel is the wlr-layer-shell stacking layer. The zero value is invalid,
// so a surface never silently lands below every window.
type LayerLevel uint8

const (
	LayerBackground LayerLevel = iota + 1
	LayerBottom
	LayerTop
	LayerOverlay
)

// Anchor is a bit set of output edges a layer surface attaches to.
type Anchor uint8

const (
	AnchorTop Anchor = 1 << iota
	AnchorBottom
	AnchorLeft
	AnchorRight
)

const anchorAll = AnchorTop | AnchorBottom | AnchorLeft | AnchorRight

// KeyboardMode is the layer surface keyboard interactivity. The zero value is
// KeyboardNone: the surface never takes keyboard focus. KeyboardOnDemand needs
// layer-shell version 4.
type KeyboardMode uint8

const (
	KeyboardNone KeyboardMode = iota
	KeyboardExclusive
	KeyboardOnDemand
)

// Rect is an integer logical-pixel rectangle in surface coordinates.
type Rect struct{ X, Y, Width, Height int32 }

// LayerConfig requests a wlr-layer-shell surface instead of an xdg toplevel.
// Size still gives the initial logical size; an axis anchored to both opposite
// edges is sized by the compositor and reported through OnResize and Frame.Size.
// ExclusiveZone > 0 asks the compositor to reserve that many logical pixels
// from an anchored edge (only for one edge, or one edge plus both perpendicular
// edges); 0 reserves nothing and asks to avoid other surfaces' positive zones;
// -1 ignores other exclusive zones and extends to the anchored output edges.
type LayerConfig struct {
	Output        string // output name; empty lets the compositor choose
	Namespace     string // defaults to "nefergui"
	Level         LayerLevel
	Anchors       Anchor
	Keyboard      KeyboardMode
	ExclusiveZone int32
	Margin        [4]int32 // top, right, bottom, left
	// InputRects, when non-nil, restricts pointer input to these rectangles.
	// An empty non-nil slice makes the surface fully click-through. Rects need
	// positive width and height.
	InputRects []Rect
}

// surfaceOptions lowers the config to platform options and validates it.
func (c LayerConfig) surfaceOptions() (wayland.SurfaceOptions, error) {
	l := wayland.LayerOptions{Output: c.Output, Namespace: c.Namespace, ExclusiveZone: c.ExclusiveZone, Margin: c.Margin}
	switch c.Level {
	case LayerBackground:
		l.Layer = wayland.LayerBackground
	case LayerBottom:
		l.Layer = wayland.LayerBottom
	case LayerTop:
		l.Layer = wayland.LayerTop
	case LayerOverlay:
		l.Layer = wayland.LayerOverlay
	default:
		return wayland.SurfaceOptions{}, fmt.Errorf("nefergui: invalid layer level %d", c.Level)
	}
	if c.Anchors&^anchorAll != 0 {
		return wayland.SurfaceOptions{}, fmt.Errorf("nefergui: invalid layer anchors %#x", uint8(c.Anchors))
	}
	if c.Anchors&AnchorTop != 0 {
		l.Anchor |= wayland.AnchorTop
	}
	if c.Anchors&AnchorBottom != 0 {
		l.Anchor |= wayland.AnchorBottom
	}
	if c.Anchors&AnchorLeft != 0 {
		l.Anchor |= wayland.AnchorLeft
	}
	if c.Anchors&AnchorRight != 0 {
		l.Anchor |= wayland.AnchorRight
	}
	switch c.Keyboard {
	case KeyboardNone:
		l.Keyboard = wayland.KeyboardNone
	case KeyboardExclusive:
		l.Keyboard = wayland.KeyboardExclusive
	case KeyboardOnDemand:
		l.Keyboard = wayland.KeyboardOnDemand
	default:
		return wayland.SurfaceOptions{}, fmt.Errorf("nefergui: invalid keyboard mode %d", c.Keyboard)
	}
	o := wayland.SurfaceOptions{Layer: &l}
	if c.InputRects != nil {
		o.InputRects = make([]wayland.Rect, len(c.InputRects))
		for i, r := range c.InputRects {
			o.InputRects[i] = wayland.Rect{X: r.X, Y: r.Y, W: r.Width, H: r.Height}
		}
	}
	if err := o.Validate(); err != nil {
		return wayland.SurfaceOptions{}, fmt.Errorf("nefergui: layer: %w", err)
	}
	return o, nil
}

// Layer selects a wlr-layer-shell surface for the window.
func Layer(c LayerConfig) WindowOption {
	c.InputRects = slices.Clone(c.InputRects)
	return windowOption(func(w *windowConfig) { w.layer = &c })
}
