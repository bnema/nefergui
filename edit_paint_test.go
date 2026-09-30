package nefergui

import (
	"github.com/bnema/nefergui/internal/css"
	"github.com/bnema/nefergui/internal/text"
	"math"
	"testing"
)

func TestEditorVisualSelectionAndCaret(t *testing.T) {
	catalog, err := text.Load(text.DirectorySource("testdata/fonts"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, value string
		start, end  int
	}{{"latin", "abc def", 1, 5}, {"arabic", "مرحبا", 0, len("مرحبا")}} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRuntime()
			r.setTextEngine(text.NewEngine(catalog))
			r.styles = css.Compile(css.UA(), css.Parse(`input {width:240px;height:24px;font-size:16px}`))
			value := tc.value
			view := func(f *Frame) { f.Root().Input("label", &value) }
			r.Build(view)
			r.route(key("Tab"))
			r.Build(view)
			s := r.edits[idKey(r.Target(0))]
			s.Select(tc.start, tc.end)
			r.Redraw()
			r.Build(view)
			count := 0
			minX, maxX := math.Inf(1), math.Inf(-1)
			caret := 0
			for _, c := range r.output.Display {
				if c.ID != idKey(r.Target(0)) {
					continue
				}
				if c.Op == "selection" {
					count++
					minX = math.Min(minX, c.Rect.X)
					maxX = math.Max(maxX, c.Rect.X+c.Rect.W)
					if c.Rect.W <= 0 {
						t.Fatal("zero highlight")
					}
				}
				if c.Op == "caret" {
					caret++
					if c.Rect.W != 1 {
						t.Fatalf("caret width: %+v", c.Rect)
					}
				}
			}
			if count == 0 || caret != 1 || !(maxX > minX) {
				t.Fatalf("selection=%d caret=%d extent=%g:%g", count, caret, minX, maxX)
			}
			if tc.name == "arabic" {
				var x float64
				for _, c := range r.output.Display {
					if c.Op == "caret" {
						x = c.Rect.X
					}
				}
				if math.Abs(x-minX) > 2 {
					t.Fatalf("RTL trailing caret at %g, highlight starts %g", x, minX)
				}
			}
		})
	}
}
