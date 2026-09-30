package nefergui

import (
	"math"
	"testing"

	"github.com/bnema/nefergui/internal/css"
	"github.com/bnema/nefergui/internal/layout"
	"github.com/bnema/nefergui/internal/text"
	"github.com/go-text/typesetting/di"
)

func TestMixedDirectionSelectionVisualRects(t *testing.T) {
	catalog, err := text.Load(text.DirectorySource("testdata/fonts"))
	if err != nil {
		t.Fatal(err)
	}
	r := newRuntime()
	r.setTextEngine(text.NewEngine(catalog))
	r.styles = css.Compile(css.UA(), css.Parse(`input {width:300px;height:28px;font-size:16px}`))
	value := "abc مرحبا def"
	view := func(f *Frame) { f.Root().Input("mixed", &value) }
	r.Build(view)
	r.route(key("Tab"))
	r.Build(view)
	n := r.output.Tree.Children[0]
	if len(n.Lines) != 1 {
		t.Fatalf("expected one line, got %d", len(n.Lines))
	}
	rtl := false
	ltr := false
	for _, run := range n.Lines[0].Runs {
		rtl = rtl || run.Direction == di.DirectionRTL
		ltr = ltr || run.Direction == di.DirectionLTR
	}
	if !rtl || !ltr {
		t.Fatalf("not mixed bidi: %+v", n.Lines[0].Runs)
	}
	s := r.edits[n.ID]
	s.Select(len("abc "), len("abc مرحبا "))
	r.Redraw()
	r.Build(view)
	n = r.output.Tree.Children[0]
	boundaries := textBoundaries(value, n, r.output.Display)
	if len(boundaries) != 1 {
		t.Fatalf("expected one visual line: %+v", boundaries)
	}
	// Selected logical clusters must paint at the corresponding visual span,
	// including the reversed RTL run and the following LTR separator.
	var rects []layout.Rect
	for _, c := range r.output.Display {
		if c.ID == n.ID && c.Op == "selection" {
			rects = append(rects, c.Rect)
		}
	}
	if len(rects) < 6 {
		t.Fatalf("missing per-cluster bidi highlights: %+v", rects)
	}
	for _, b := range []int{len("abc "), len("abc م"), len("abc مر"), len("abc مرح"), len("abc مرحب")} {
		has := false
		for _, edge := range boundaries[0] {
			if edge.byteOffset != b {
				continue
			}
			for _, rect := range rects {
				if math.Abs(rect.X-edge.x) < 0.01 || math.Abs(rect.X+rect.W-edge.x) < 0.01 {
					has = true
				}
			}
		}
		if !has {
			t.Fatalf("byte edge %d not painted at visual cluster: %+v rects=%+v", b, boundaries[0], rects)
		}
	}
}
