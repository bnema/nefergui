package ui

import (
	"github.com/bnema/nefergui/internal/css"
	"testing"
)

func TestSliderPointerCapture(t *testing.T) {
	r := newRuntime()
	r.styles = css.Compile(css.UA(), css.Parse(`slider {width:100px;height:20px}`))
	v := 0.0
	disabled := false
	view := func(f *Frame) { f.Root().Slider("volume", &v, 0, 10, 2, Key("volume"), Disabled(disabled)) }
	r.Build(view)
	box := r.output.Tree.Children[0].Content
	y := r.output.Tree.Children[0].Border.Y + 1
	r.route(platformInput{Kind: "press", Button: pointerPrimary, X: box.X + box.W*.29, Y: y})
	r.Build(view)
	if v != 2 {
		t.Fatalf("rounded: %v", v)
	}
	r.route(platformInput{Kind: "motion", X: box.X + box.W + 500, Y: y})
	r.Build(view)
	if v != 10 {
		t.Fatalf("clamped captured drag: %v", v)
	}
	r.route(platformInput{Kind: "release", Button: pointerPrimary, X: box.X, Y: y})
	r.Build(view)
	if v != 0 || r.state.capture != nil {
		t.Fatalf("release: %v", v)
	}
	disabled = true
	r.Redraw()
	r.Build(view)
	r.route(platformInput{Kind: "press", Button: pointerPrimary, X: box.X + box.W, Y: y})
	r.Build(view)
	if v != 0 {
		t.Fatal("disabled mutation", v)
	}
}
