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
// The returned slices and map are freshly allocated and owned by the caller.
func ListBatches(frame Frame) ([]vkdevice.Instance, []vkdevice.Batch, ListStats) {
	return listBatches(frame, nil, nil, make(map[string]int))
}

// ListBuffer is a single-owner scratch area for repeated ListBatches calls.
// The slices it returns alias its storage: they are valid only until the next
// List or Release call, and must not be retained, shared across goroutines, or
// used while another List may run. This is safe for consumers that copy the
// data synchronously (Target.submit uploads instances into GPU memory and
// resolve copies the batches before recording returns); anything that keeps the
// slices past that point must use ListBatches instead. Retained frames hold
// only render.Frame, never these slices, so a blocked frame is re-listed on retry.
// Stats.Skipped shares the same lifetime. The zero value is ready to use.
type ListBuffer struct {
	instances []vkdevice.Instance
	batches   []vkdevice.Batch
	skipped   map[string]int
}

// bufferSlack is how much larger than needed retained storage may be before
// List drops it, so one huge frame does not pin memory forever. It matches
// layout's retainSlack.
const bufferSlack = 4

// List is ListBatches reusing this buffer's storage where capacity allows.
func (b *ListBuffer) List(frame Frame) ([]vkdevice.Instance, []vkdevice.Batch, ListStats) {
	b.Release()
	if b.skipped == nil {
		b.skipped = make(map[string]int)
	}
	clear(b.skipped)
	instances, batches, stats := listBatches(frame, b.instances[:0], b.batches[:0], b.skipped)
	b.instances, b.batches = instances[:0], batches[:0]
	return instances, batches, stats
}

// Release drops borrowed image references from the previous result. Call it
// once the result has been consumed so an idle buffer does not pin images.
func (b *ListBuffer) Release() {
	clear(b.batches[:cap(b.batches)])
}

// drawn reports whether the operation draws an instance.
func drawn(op string) bool {
	switch op {
	case "rect", "selection", "caret", "preedit-underline", "preedit-caret", "glyph", "border", "outline", "shadow", "image":
		return true
	}
	return false
}

// listBatches fills out and batches (both length zero), growing them only when
// their capacity is short of the exact counts or absurdly larger than needed.
func listBatches(frame Frame, out []vkdevice.Instance, batches []vkdevice.Batch, skipped map[string]int) ([]vkdevice.Instance, []vkdevice.Batch, ListStats) {
	// Count first so both slices are sized exactly: no append growth, and no
	// over-allocation for frames that skip quads.
	drawCount, batchCount, open := 0, 0, false
	for i := range frame.Quads {
		op := frame.Quads[i].Op
		if !drawn(op) {
			continue
		}
		drawCount++
		if op == "image" {
			batchCount++
			open = false
		} else if !open {
			batchCount++
			open = true
		}
	}
	if out == nil || cap(out) < drawCount || cap(out) > bufferSlack*drawCount+64 {
		out = make([]vkdevice.Instance, 0, drawCount)
	}
	if batchCount == 0 {
		batches = nil // as before: no draws, no batches
	} else if cap(batches) < batchCount || cap(batches) > bufferSlack*batchCount+16 {
		batches = make([]vkdevice.Batch, 0, batchCount)
	}
	stats := ListStats{Skipped: skipped}
	for i := range frame.Quads {
		q := &frame.Quads[i]
		if !drawn(q.Op) {
			stats.Skipped[q.Op]++
			continue
		}
		if q.Op == "image" {
			batches = append(batches, vkdevice.Batch{First: uint32(len(out)), Count: 1, Source: q.Image})
		} else if len(batches) == 0 || batches[len(batches)-1].Source != nil {
			batches = append(batches, vkdevice.Batch{First: uint32(len(out)), Count: 1})
		} else {
			batches[len(batches)-1].Count++
		}
		out = append(out, instanceOf(q))
	}
	stats.Drawn = len(out)
	return out, batches, stats
}

// instanceOf converts one drawable quad to its GPU instance.
func instanceOf(q *Quad) vkdevice.Instance {
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
	return v
}
