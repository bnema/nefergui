package render

import (
	"github.com/bnema/nefergui/internal/css"
	"github.com/bnema/nefergui/internal/layout"
	"testing"
)

func TestStyleInstances(t *testing.T) {
	p, err := NewPreparer(128, 1)
	if err != nil {
		t.Fatal(err)
	}
	box := layout.Rect{X: 20, Y: 20, W: 80, H: 60}
	sides := css.ColorSides{Top: css.Color{R: 1, A: 1}, Right: css.Color{G: 1, A: 1}, Bottom: css.Color{B: 1, A: 1}, Left: css.Color{R: 1, B: 1, A: 1}}
	sh := css.Shadow{X: css.Length{Value: 2, Unit: "px"}, Y: css.Length{Value: 3, Unit: "px"}, Blur: css.Length{Value: 4, Unit: "px"}, Spread: css.Length{Value: 1, Unit: "px"}, Color: css.Color{A: .5}}
	cmds := []layout.Command{
		{Op: "rect", Rect: box, Radii: [4]float64{10, 10, 10, 10}, Color: css.Color{G: 1, A: 1}, Opacity: 1},
		{Op: "border", Rect: box, Radii: [4]float64{10, 10, 10, 10}, Widths: layout.Edges{Top: 2, Right: 3, Bottom: 4, Left: 5}, Colors: sides, Color: css.Color{A: 1}, Opacity: 1},
		{Op: "outline", Rect: box, Widths: layout.Edges{Top: 2, Right: 2, Bottom: 2, Left: 2}, Color: css.Color{B: 1, A: 1}, Opacity: 1},
		{Op: "shadow", Rect: box, Shadow: &sh, Opacity: 1},
		{Op: "preedit-caret", Rect: layout.Rect{X: 4, Y: 5, W: 1, H: 12}, Color: css.Color{R: 1, A: 1}, Opacity: 1},
	}
	frame, err := p.Prepare(cmds, 2, 300, 250)
	if err != nil {
		t.Fatal(err)
	}
	got, stats := ListInstances(frame)
	if stats.Drawn != len(cmds) || len(stats.Skipped) != 0 {
		t.Fatalf("stats=%+v", stats)
	}
	if got[0].Radii != [4]float32{20, 20, 20, 20} || got[0].Color != [4]float32{0, 1, 0, 1} {
		t.Fatalf("fill=%+v", got[0])
	}
	if got[1].KindLayer[0] != 2 || got[1].Widths != [4]float32{4, 6, 8, 10} || got[1].Sides[0] != [4]float32{1, 0, 0, 1} || got[1].Sides[1] != [4]float32{0, 1, 0, 1} || got[1].Sides[2] != [4]float32{0, 0, 1, 1} || got[1].Sides[3] != [4]float32{1, 0, 1, 1} {
		t.Fatalf("border=%+v", got[1])
	}
	if got[2].KindLayer[0] != 3 || got[2].Bounds != [4]float32{36, 36, 168, 128} || got[2].Shape != [4]float32{40, 40, 160, 120} || got[2].Color != [4]float32{0, 0, 1, 1} {
		t.Fatalf("outline=%+v", got[2])
	}
	if got[3].KindLayer[0] != 4 || got[3].Bounds != [4]float32{30, 32, 188, 148} || got[3].Shadow != [4]float32{4, 6, 8, 2} || got[3].Color != [4]float32{0, 0, 0, .5} {
		t.Fatalf("shadow=%+v", got[3])
	}
	if got[4].KindLayer[0] != 0 || got[4].Bounds != [4]float32{8, 10, 2, 24} || got[4].Color != [4]float32{1, 0, 0, 1} {
		t.Fatalf("caret=%+v", got[4])
	}
	sh.Inset = true
	frame, err = p.Prepare([]layout.Command{{Op: "shadow", Rect: box, Shadow: &sh, Opacity: 1}}, 2, 300, 250)
	if err != nil {
		t.Fatal(err)
	}
	got, stats = ListInstances(frame)
	if len(stats.Skipped) != 0 || len(got) != 1 || got[0].KindLayer[0] != 5 || got[0].Bounds != [4]float32{40, 40, 160, 120} {
		t.Fatalf("inset=%+v %+v", got, stats)
	}
	if _, err = p.Prepare([]layout.Command{{Op: "shadow", Rect: box}}, 1, 300, 250); err == nil {
		t.Fatal("accepted missing shadow")
	}
}
