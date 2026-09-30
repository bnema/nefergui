package render

import (
	"image"
	"testing"

	"github.com/bnema/nefergui/internal/css"
	"github.com/bnema/nefergui/internal/layout"
	"github.com/bnema/nefergui/internal/text"
)

// listFixture is a product-like mix: atlas draws with an image every 50 quads.
func listFixture(n int) Frame {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	quads := make([]Quad, n)
	for i := range quads {
		switch {
		case i%50 == 49:
			quads[i] = Quad{Op: "image", Image: img, Rect: layout.Rect{W: 4, H: 4}}
		case i%3 == 0:
			quads[i] = Quad{Op: "glyph", Glyph: text.Placement{Page: 1, Rect: image.Rect(1, 2, 9, 12)}, Color: css.Color{A: 1}}
		case i%7 == 0:
			quads[i] = Quad{Op: "border", Color: css.Color{A: 1}}
		default:
			quads[i] = Quad{Op: "rect", Rect: layout.Rect{X: float64(i), W: 3, H: 3}, Color: css.Color{A: 1}}
		}
	}
	return Frame{Quads: quads}
}

func BenchmarkListBatches(b *testing.B) {
	frame := listFixture(2000)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		instances, batches, stats := ListBatches(frame)
		if len(instances) != 2000 || len(batches) == 0 || len(stats.Skipped) != 0 {
			b.Fatal("list")
		}
	}
}

func BenchmarkListBufferReuse(b *testing.B) {
	frame := listFixture(2000)
	var buf ListBuffer
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		instances, batches, stats := buf.List(frame)
		if len(instances) != 2000 || len(batches) == 0 || len(stats.Skipped) != 0 {
			b.Fatal("list")
		}
	}
}
