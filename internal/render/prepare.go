// Package render prepares ordered physical-pixel primitives for Vulkan submission.
// It does not own the display list or its borrowed font faces.
package render

import (
	"fmt"
	"image"
	"math"

	"github.com/bnema/nefergui/internal/css"
	"github.com/bnema/nefergui/internal/layout"
	"github.com/bnema/nefergui/internal/text"
	"github.com/go-text/typesetting/font"
)

// Quad is a physical-pixel rectangle with a physical clip. Color is straight
// sRGB; conversion to premultiplied linear color belongs at the GPU boundary.
// Glyphs refer to an atlas placement valid only for this prepared frame.
type Quad struct {
	Op     string
	Rect   layout.Rect
	Clip   layout.Rect
	Color  css.Color
	Radii  [4]float64
	Widths layout.Edges
	Colors css.ColorSides
	Glyph  text.Placement
	Image  image.Image
	// Shape is the original box when the draw bounds extend past it.
	Shape layout.Rect
	// Shadow holds physical offset, blur and spread. Inset is carried separately.
	Shadow [4]float64
	Inset  bool
}

type Frame struct {
	Quads   []Quad
	Uploads []text.Upload
}

type Preparer struct{ Atlas *text.Atlas }

// NewPreparer bounds the grayscale glyph atlas. Call Prepare synchronously with
// layout; the atlas and shaped faces are not safe for concurrent use.
func NewPreparer(size, pages int) (*Preparer, error) {
	a, err := text.NewAtlas(size, pages)
	if err != nil {
		return nil, err
	}
	return &Preparer{Atlas: a}, nil
}

func intersect(a, b layout.Rect) layout.Rect {
	x, y := math.Max(a.X, b.X), math.Max(a.Y, b.Y)
	return layout.Rect{X: x, Y: y, W: math.Max(0, math.Min(a.X+a.W, b.X+b.W)-x), H: math.Max(0, math.Min(a.Y+a.H, b.Y+b.H)-y)}
}

func physical(r layout.Rect, scale float64) layout.Rect {
	x, y, w, h := layout.Physical(r, scale)
	return layout.Rect{X: float64(x), Y: float64(y), W: float64(w), H: float64(h)}
}

func scaled(e layout.Edges, scale float64) layout.Edges {
	return layout.Edges{Top: e.Top * scale, Right: e.Right * scale, Bottom: e.Bottom * scale, Left: e.Left * scale}
}

// Prepare converts logical rectangles once, using layout.Physical as the sole
// logical-to-physical edge-rounding boundary. A clip-push intersects its parent
// clip; rounded clips intentionally use only their axis-aligned bounds in V1.
// Clip-pop without a push and unknown operations are rejected, not dropped.
// Image commands without a source are rejected. Failed frames do not produce
// partial output.
func (p *Preparer) Prepare(commands []layout.Command, scale float64, width, height int) (Frame, error) {
	if p == nil || p.Atlas == nil || scale <= 0 || math.IsNaN(scale) || math.IsInf(scale, 0) || width <= 0 || height <= 0 {
		return Frame{}, fmt.Errorf("render: invalid frame dimensions, scale, or atlas")
	}
	p.Atlas.BeginFrame()
	frame := Frame{}
	clip := layout.Rect{W: float64(width), H: float64(height)}
	stack := []layout.Rect{}
	for _, cmd := range commands {
		switch cmd.Op {
		case "clip-push":
			stack = append(stack, clip)
			clip = intersect(clip, physical(cmd.Rect, scale))
			continue
		case "clip-pop":
			if len(stack) == 0 {
				return Frame{}, fmt.Errorf("render: unbalanced clip-pop")
			}
			clip = stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			continue
		case "text", "preedit-text":
			for _, run := range cmd.Runs {
				if run.Face == nil || run.FaceID != run.Face.ID || run.Size <= 0 {
					return Frame{}, fmt.Errorf("render: missing or mismatched face for %q", cmd.ID)
				}
				for _, glyph := range run.Glyphs {
					x, y := glyph.X*scale, glyph.Y*scale
					key, err := text.GlyphKey(run.Face, font.GID(glyph.ID), run.Size*scale, x, run.Face.Variations)
					if err != nil {
						return Frame{}, err
					}
					place, ok := p.Atlas.Lookup(key)
					if !ok {
						mask, err := text.Rasterize(run.Face, key)
						if err != nil {
							return Frame{}, err
						}
						place, err = p.Atlas.Insert(key, mask)
						if err != nil {
							return Frame{}, err
						}
					}
					if place.Page < 0 || place.Rect.Empty() {
						continue
					}
					r := layout.Rect{X: float64(int(math.Floor(x)) + place.Origin.X), Y: float64(int(math.Floor(y)) + place.Origin.Y), W: float64(place.Rect.Dx()), H: float64(place.Rect.Dy())}
					if intersect(clip, r).W > 0 && intersect(clip, r).H > 0 {
						frame.Quads = append(frame.Quads, Quad{Op: "glyph", Rect: r, Clip: clip, Color: opacity(cmd.Color, cmd.Opacity), Glyph: place})
					}
				}
			}
			continue
		case "image":
			if cmd.Image == nil {
				return Frame{}, fmt.Errorf("render: image %q has no pixel source", cmd.ID)
			}
		case "rect", "border", "outline", "shadow", "selection", "caret", "preedit-underline", "preedit-caret":
		default:
			return Frame{}, fmt.Errorf("render: unknown display operation %q", cmd.Op)
		}
		r := physical(cmd.Rect, scale)
		shape := r
		var shadow [4]float64
		inset := false
		if cmd.Op == "outline" {
			// Layout already applies outline-offset to Rect; width grows outward.
			w := cmd.Widths.Top * scale
			r = layout.Rect{X: r.X - w, Y: r.Y - w, W: r.W + 2*w, H: r.H + 2*w}
		}
		if cmd.Op == "shadow" {
			if cmd.Shadow == nil {
				return Frame{}, fmt.Errorf("render: shadow %q has no parameters", cmd.ID)
			}
			sh := cmd.Shadow
			shadow = [4]float64{sh.X.Value * scale, sh.Y.Value * scale, sh.Blur.Value * scale, sh.Spread.Value * scale}
			inset = sh.Inset
			if !inset {
				// Three standard deviations cover the visible Gaussian tail.
				pad := shadow[2]*1.5 + math.Max(0, shadow[3])
				r = layout.Rect{X: r.X + shadow[0] - pad, Y: r.Y + shadow[1] - pad, W: r.W + 2*pad, H: r.H + 2*pad}
			}
		}
		if intersect(clip, r).W == 0 || intersect(clip, r).H == 0 {
			continue
		}
		color := cmd.Color
		if cmd.Op == "shadow" {
			color = cmd.Shadow.Color
		}
		q := Quad{Op: cmd.Op, Rect: r, Clip: clip, Color: opacity(color, cmd.Opacity), Widths: scaled(cmd.Widths, scale), Colors: cmd.Colors, Shape: shape, Shadow: shadow, Inset: inset, Image: cmd.Image}
		if cmd.Op == "image" {
			q.Color = css.Color{R: 1, G: 1, B: 1, A: opacity(css.Color{A: 1}, cmd.Opacity).A}
		}
		for i, v := range cmd.Radii {
			q.Radii[i] = v * scale
		}
		frame.Quads = append(frame.Quads, q)
	}
	if len(stack) > 0 {
		return Frame{}, fmt.Errorf("render: unbalanced clip-push")
	}
	frame.Uploads = p.Atlas.Uploads()
	return frame, nil
}

func opacity(c css.Color, a float64) css.Color {
	c.A *= math.Max(0, math.Min(1, a))
	return c
}
