package ui

import (
	"fmt"
	"math"
)

// Rect is an integer logical-pixel rectangle in surface coordinates.
type Rect struct{ X, Y, Width, Height int32 }

// SetInputRects stages the input region for the surface: pointer input outside
// the rectangles passes through. Renderer reports it in Output.InputRects when
// it changes. nil restores the whole surface, and a non-nil empty slice makes
// the surface click-through. Rects need positive size. The slice is copied, so
// callers may reuse it; an unchanged region costs nothing.
func (f *Frame) SetInputRects(rects []Rect) error {
	if !f.active {
		panic("nefergui: SetInputRects outside frame")
	}
	for i, r := range rects {
		if r.Width <= 0 || r.Height <= 0 || int64(r.X)+int64(r.Width) > math.MaxInt32 || int64(r.Y)+int64(r.Height) > math.MaxInt32 {
			return fmt.Errorf("nefergui: input rect %d %+v is empty or overflows", i, r)
		}
	}
	o := f.owner
	o.inputNil = rects == nil
	o.inputRects = append(o.inputRects[:0], rects...)
	o.inputStaged = true
	return nil
}
