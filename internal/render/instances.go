package render

import (
	"github.com/bnema/nefergui/internal/css"
	"github.com/bnema/nefergui/internal/presentation/vkdevice"
)

// ListStats reports every skipped prepared quad by operation.
type ListStats struct {
	Drawn   int
	Skipped map[string]int
}

// ListInstances converts prepared physical quads to straight-sRGB GPU inputs.
// Unknown operations are counted, not silently drawn as rectangles. Uploads
// remain the caller's responsibility.
func ListInstances(frame Frame) ([]vkdevice.Instance, ListStats) {
	instances, _, stats := ListBatches(frame)
	return instances, stats
}

// ListBatches groups consecutive atlas draws and isolates each image draw.
// First indexes the returned instances, retaining exact painter's order.
func ListBatches(frame Frame) ([]vkdevice.Instance, []vkdevice.Batch, ListStats) {
	out := make([]vkdevice.Instance, 0, len(frame.Quads))
	stats := ListStats{Skipped: make(map[string]int)}
	var batches []vkdevice.Batch
	for _, q := range frame.Quads {
		switch q.Op {
		case "rect", "selection", "caret", "preedit-underline", "preedit-caret", "glyph", "border", "outline", "shadow", "image":
			v := vkdevice.Instance{
				Bounds: [4]float32{float32(q.Rect.X), float32(q.Rect.Y), float32(q.Rect.W), float32(q.Rect.H)},
				Clip:   [4]float32{float32(q.Clip.X), float32(q.Clip.Y), float32(q.Clip.W), float32(q.Clip.H)},
				Color:  [4]float32{float32(q.Color.R), float32(q.Color.G), float32(q.Color.B), float32(q.Color.A)},
			}
			v.Shape = [4]float32{float32(q.Shape.X), float32(q.Shape.Y), float32(q.Shape.W), float32(q.Shape.H)}
			v.Widths = [4]float32{float32(q.Widths.Top), float32(q.Widths.Right), float32(q.Widths.Bottom), float32(q.Widths.Left)}
			v.Shadow = [4]float32{float32(q.Shadow[0]), float32(q.Shadow[1]), float32(q.Shadow[2]), float32(q.Shadow[3])}
			for i, c := range [4]css.Color{q.Colors.Top, q.Colors.Right, q.Colors.Bottom, q.Colors.Left} {
				v.Sides[i] = [4]float32{float32(c.R), float32(c.G), float32(c.B), float32(c.A * float64(v.Color[3]))}
			}
			switch q.Op {
			case "image":
				v.KindLayer[0] = 6
				v.UV = [4]float32{0, 0, 1, 1}
			case "border":
				v.KindLayer[0] = 2
			case "outline":
				v.KindLayer[0] = 3
			case "shadow":
				v.KindLayer[0] = 4
				if q.Inset {
					v.KindLayer[0] = 5
				}
			}
			for i, r := range q.Radii {
				v.Radii[i] = float32(r)
			}
			if q.Op == "glyph" {
				v.KindLayer = [4]float32{1, float32(q.Glyph.Page)}
				v.UV = [4]float32{float32(q.Glyph.Rect.Min.X) / vkdevice.AtlasSize, float32(q.Glyph.Rect.Min.Y) / vkdevice.AtlasSize, float32(q.Glyph.Rect.Max.X) / vkdevice.AtlasSize, float32(q.Glyph.Rect.Max.Y) / vkdevice.AtlasSize}
			}
			if q.Op == "image" {
				batches = append(batches, vkdevice.Batch{First: uint32(len(out)), Count: 1, Source: q.Image})
			} else if len(batches) == 0 || batches[len(batches)-1].Source != nil {
				batches = append(batches, vkdevice.Batch{First: uint32(len(out)), Count: 1})
			} else {
				batches[len(batches)-1].Count++
			}
			out = append(out, v)
		default:
			stats.Skipped[q.Op]++
		}
	}
	stats.Drawn = len(out)
	return out, batches, stats
}
