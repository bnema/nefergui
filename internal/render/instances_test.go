package render

import (
	"github.com/bnema/nefergui/internal/css"
	"github.com/bnema/nefergui/internal/layout"
	"github.com/bnema/nefergui/internal/text"
	"image"
	"testing"
)

func TestImageBatchOrder(t *testing.T) {
	a, b := image.NewRGBA(image.Rect(0, 0, 2, 2)), image.NewNRGBA(image.Rect(0, 0, 2, 2))
	frame := Frame{Quads: []Quad{{Op: "rect"}, {Op: "glyph"}, {Op: "image", Image: a, Color: css.Color{A: .5}}, {Op: "image", Image: b}, {Op: "rect"}, {Op: "border"}, {Op: "image", Image: a}}}
	instances, batches, stats := ListBatches(frame)
	if len(instances) != 7 || stats.Drawn != 7 || len(batches) != 5 {
		t.Fatalf("instances=%d batches=%+v stats=%+v", len(instances), batches, stats)
	}
	for i, want := range []struct {
		first, count uint32
		source       image.Image
	}{{0, 2, nil}, {2, 1, a}, {3, 1, b}, {4, 2, nil}, {6, 1, a}} {
		if batches[i].First != want.first || batches[i].Count != want.count || batches[i].Source != want.source {
			t.Fatalf("batch %d: %+v", i, batches[i])
		}
	}
	if instances[2].KindLayer[0] != 6 || instances[2].UV != [4]float32{0, 0, 1, 1} || instances[2].Color[3] != .5 {
		t.Fatalf("image instance: %+v", instances[2])
	}
}

func TestListInstances(t *testing.T) {
	frame := Frame{Quads: []Quad{
		{Op: "rect", Rect: layout.Rect{X: 1, Y: 2, W: 3, H: 4}, Clip: layout.Rect{W: 10, H: 10}, Color: css.Color{R: 1, G: .5, A: .5}, Radii: [4]float64{5, 6, 7, 8}},
		{Op: "border"}, {Op: "glyph", Glyph: text.Placement{Page: 2, Rect: image.Rect(10, 20, 14, 26)}},
	}}
	got, stats := ListInstances(frame)
	if stats.Drawn != 3 || len(stats.Skipped) != 0 || len(got) != 3 {
		t.Fatalf("stats=%+v got=%+v", stats, got)
	}
	if got[0].Bounds != [4]float32{1, 2, 3, 4} || got[0].Clip != [4]float32{0, 0, 10, 10} || got[0].Color != [4]float32{1, .5, 0, .5} || got[0].Radii != [4]float32{5, 6, 7, 8} {
		t.Fatalf("instance=%+v", got[0])
	}
	if got[2].KindLayer != [4]float32{1, 2} || got[2].UV != [4]float32{10.0 / 1024, 20.0 / 1024, 14.0 / 1024, 26.0 / 1024} {
		t.Fatalf("glyph=%+v", got[2])
	}
}
