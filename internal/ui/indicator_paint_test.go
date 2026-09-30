package ui

import (
	"math"
	"testing"

	"github.com/bnema/nefergui/internal/css"
	"github.com/bnema/nefergui/internal/layout"
)

func ownCommands(r *runtime, id string) []layout.Command {
	var out []layout.Command
	for _, cmd := range r.output.Display {
		if cmd.ID == id {
			out = append(out, cmd)
		}
	}
	return out
}

func accentRect(cmds []layout.Command, accent css.Color) (layout.Rect, bool) {
	for _, cmd := range cmds {
		if cmd.Op == "rect" && cmd.Color == accent {
			return cmd.Rect, true
		}
	}
	return layout.Rect{}, false
}

// Checkbox and radio paint an outlined indicator when unchecked and an
// accent-filled one when checked, inside the reserved left padding; author
// accent-color and :checked drive them.
func TestCheckIndicatorsFollowStateAndCSS(t *testing.T) {
	r := newRuntime()
	r.styles = css.Compile(css.UA(), css.Parse(`checkbox, radio {width:200px;height:24px;font-size:16px} checkbox:checked {accent-color:#00ff00} radio {accent-color:#ff0000}`))
	checked, choice := false, ""
	view := func(f *Frame) {
		root := f.Root()
		root.Checkbox("Box", &checked, Key("box"))
		root.Radio("A", "a", &choice, Key("a"))
	}
	green, red := css.Color{G: 1, A: 1}, css.Color{R: 1, A: 1}
	for _, on := range []bool{false, true} {
		checked = on
		choice = map[bool]string{true: "a"}[on]
		r.Redraw() // the model changed outside NeferGUI's events
		r.Build(view)
		for id, accent := range map[string]css.Color{"root/k3:box": green, "root/k1:a": red} {
			cmds := ownCommands(r, id)
			n := resultByID(r.output.Tree, id)
			rect, filled := accentRect(cmds, accent)
			if filled != on {
				t.Fatalf("%s checked=%v: accent fill %v in %+v", id, on, filled, cmds)
			}
			if !on {
				found := false
				for _, cmd := range cmds {
					if cmd.Op == "border" && cmd.Rect.W == 16 {
						found, rect = true, cmd.Rect
					}
				}
				if !found {
					t.Fatalf("%s unchecked: no outlined indicator in %+v", id, cmds)
				}
			}
			if rect.W != 16 || rect.H != 16 || rect.X+rect.W > n.Content.X || rect.X < n.Border.X {
				t.Fatalf("%s indicator %+v outside the reserved padding (content %+v)", id, rect, n.Content)
			}
		}
	}
}

// The slider paints a rail across the pointer track, then fill and thumb at
// the value fraction; the thumb follows the value after a key change.
func TestSliderTrackFollowsValue(t *testing.T) {
	r := newRuntime()
	r.styles = css.Compile(css.UA(), css.Parse(`slider {width:200px;font-size:16px;accent-color:#0000ff}`))
	v := 2.5
	view := func(f *Frame) { f.Root().Slider("Volume", &v, 0, 10, 0.5, Key("s")) }
	r.Build(view)
	id := "root/k1:s"
	n := resultByID(r.output.Tree, id)
	blue := css.Color{B: 1, A: 1}
	thumbX := func() float64 {
		t.Helper()
		var rects []layout.Rect
		for _, cmd := range ownCommands(r, id) {
			if cmd.Op == "rect" && cmd.Color == blue {
				rects = append(rects, cmd.Rect)
			}
		}
		if len(rects) != 2 {
			t.Fatalf("want fill and thumb, got %+v", rects)
		}
		thumb := rects[1]
		if thumb.Y < n.Content.Y+n.Content.H || thumb.Y+thumb.H > n.Border.Y+n.Border.H {
			t.Fatalf("thumb %+v outside the reserved bottom band of %+v", thumb, n.Border)
		}
		return thumb.X + thumb.W/2
	}
	if got, want := thumbX(), n.Content.X+0.25*n.Content.W; got != want {
		t.Fatalf("thumb center %v, want %v", got, want)
	}
	r.route(platformInput{Kind: "press", X: n.Content.X + 0.75*n.Content.W, Y: n.Content.Y + 1, Button: pointerPrimary})
	r.Build(view)
	r.Build(view)
	if v != 7.5 {
		t.Fatalf("pointer value %v", v)
	}
	if got, want := thumbX(), n.Content.X+0.75*n.Content.W; got != want {
		t.Fatalf("thumb center after press %v, want %v", got, want)
	}
}

// Edge cases: a clipping control keeps its padding paint outside the content
// clip, a slider without bottom padding paints no track, the unchecked
// outline carries its muted alpha once, disabled checkboxes are dimmed, and
// extreme finite ranges still give a finite thumb position.
func TestIndicatorEdgeCases(t *testing.T) {
	r := newRuntime()
	r.styles = css.Compile(css.UA(), css.Parse(`checkbox {width:200px;height:24px;font-size:16px;overflow:hidden} slider {width:200px;padding-bottom:0}`))
	checked, v, off := false, 1.0, false
	r.Build(func(f *Frame) {
		root := f.Root()
		root.Checkbox("Box", &checked, Key("box"))
		root.Checkbox("Off", &off, Key("off"), Disabled(true))
		root.Slider("S", &v, 0, 10, 1, Key("s"))
	})
	clipped, outlined := false, false
	for _, cmd := range r.output.Display {
		if cmd.ID != "root/k3:box" {
			continue
		}
		if cmd.Op == "clip-push" {
			clipped = true
		}
		if cmd.Op == "border" && cmd.Rect.W == 16 {
			outlined = true
			if clipped {
				t.Error("indicator painted inside the content clip")
			}
			if cmd.Color.A != 1 || cmd.Colors.Top.A != 0.45 {
				t.Errorf("outline alpha color=%v side=%v, want opaque base and 0.45 sides", cmd.Color.A, cmd.Colors.Top.A)
			}
		}
	}
	if !clipped || !outlined {
		t.Fatalf("clipped checkbox: clip=%v indicator=%v", clipped, outlined)
	}
	dimmed := false
	for _, cmd := range ownCommands(r, "root/k3:off") {
		if cmd.Op == "border" && cmd.Rect.W == 16 {
			dimmed = true
			if cmd.Opacity != 0.5 {
				t.Errorf("disabled checkbox indicator opacity %v", cmd.Opacity)
			}
		}
	}
	if !dimmed {
		t.Error("disabled checkbox indicator missing")
	}
	for _, cmd := range ownCommands(r, "root/k1:s") {
		if cmd.Op == "rect" {
			t.Errorf("slider without bottom padding painted %+v", cmd)
			break
		}
	}
	for _, c := range []struct{ v, min, max, want float64 }{
		{0, -math.MaxFloat64, math.MaxFloat64, 0.5},
		{math.MaxFloat64, -math.MaxFloat64, math.MaxFloat64, 1},
		{5, 5, 5, 0},
		{2.5, 0, 10, 0.25},
		{math.SmallestNonzeroFloat64, 0, 2 * math.SmallestNonzeroFloat64, 0.5},
		{math.NaN(), 0, 1, 0},
	} {
		if got := sliderFraction(c.v, c.min, c.max); got != c.want {
			t.Errorf("sliderFraction(%v,%v,%v)=%v want %v", c.v, c.min, c.max, got, c.want)
		}
	}
}
