package render

import (
	"github.com/bnema/nefergui/internal/css"
	"github.com/bnema/nefergui/internal/layout"
	"github.com/bnema/nefergui/internal/text"
	"image"
	"reflect"
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

func TestListBufferMatchesListBatches(t *testing.T) {
	a := image.NewRGBA(image.Rect(0, 0, 2, 2))
	frames := []Frame{
		listFixture(200),
		{Quads: []Quad{{Op: "rect"}, {Op: "mystery"}, {Op: "image", Image: a}, {Op: "mystery"}, {Op: "glyph"}, {Op: "glyph"}}},
		{},
		listFixture(7),
	}
	var buf ListBuffer
	for round := 0; round < 2; round++ {
		for i, f := range frames {
			wi, wb, ws := ListBatches(f)
			gi, gb, gs := buf.List(f)
			if !reflect.DeepEqual(wi, gi) || !reflect.DeepEqual(wb, gb) || !reflect.DeepEqual(ws, gs) {
				t.Fatalf("round %d frame %d differs:\nwant %v %v %v\ngot  %v %v %v", round, i, len(wi), wb, ws, len(gi), gb, gs)
			}
		}
	}
}

// A result stays valid until the next List; a later smaller frame must not
// leave stale instances, batches, image references or skip counts visible.
func TestListBufferReuseLifetime(t *testing.T) {
	a := image.NewRGBA(image.Rect(0, 0, 2, 2))
	var buf ListBuffer
	i1, b1, s1 := buf.List(Frame{Quads: []Quad{{Op: "rect"}, {Op: "image", Image: a}, {Op: "nope"}}})
	if len(i1) != 2 || len(b1) != 2 || s1.Skipped["nope"] != 1 {
		t.Fatalf("first: %v %v %v", i1, b1, s1)
	}
	i2, b2, s2 := buf.List(Frame{Quads: []Quad{{Op: "glyph"}}})
	if len(i2) != 1 || len(b2) != 1 || b2[0].Source != nil || len(s2.Skipped) != 0 || i2[0].KindLayer[0] != 1 {
		t.Fatalf("second: %v %v %v", i2, b2, s2)
	}
	buf.Release()
	for _, batch := range b2[:cap(b2)] {
		if batch.Source != nil {
			t.Fatal("Release left an image reference")
		}
	}
	// Results from the non-buffer API never alias the buffer.
	fi, fb, _ := ListBatches(Frame{Quads: []Quad{{Op: "rect"}}})
	buf.List(listFixture(50))
	if fi[0].KindLayer != [4]float32{} || len(fb) != 1 || fb[0].Count != 1 {
		t.Fatalf("ListBatches result aliased: %v %v", fi, fb)
	}
}

func TestListBufferDropsOversizedStorage(t *testing.T) {
	var buf ListBuffer
	buf.List(listFixture(20000))
	if _, _, _ = buf.List(listFixture(10)); cap(buf.instances) > 4*10+64 {
		t.Fatalf("retained %d instances for 10 quads", cap(buf.instances))
	}
}

func TestListBatchesAllocations(t *testing.T) {
	frame := listFixture(2000)
	if n := testing.AllocsPerRun(20, func() { ListBatches(frame) }); n > 4 {
		t.Fatalf("ListBatches allocs=%v, want <= 4 (instances, batches, skipped map)", n)
	}
	var buf ListBuffer
	buf.List(frame)
	if n := testing.AllocsPerRun(20, func() { buf.List(frame) }); n != 0 {
		t.Fatalf("ListBuffer.List allocs=%v, want 0 after warmup", n)
	}
}
