package nefergui

import (
	vi "github.com/bnema/nefergui/internal/image"
	"github.com/bnema/nefergui/internal/layout"
	"image"
	"math"
	"strings"
)

// Image borrows a decoded Go image. Treat its pixels as immutable while passed
// to Image; pass a new image value to change pixels. Renderer cache identity
// requires a comparable image.Image value (for example, *image.RGBA).
func (n Node) Image(img image.Image, options ...ContainerOption) {
	child := n.child("image", "img", options)
	if child.valid() && img != nil {
		child.element.image = img
		if w, h, err := vi.Size(img); err == nil {
			child.element.imageSize = layout.Size{W: float64(w), H: float64(h)}
		}
	}
}
func (n Node) Icon(name string, options ...ContainerOption) {
	child := n.child("icon", "img", options)
	if child.valid() {
		child.element.text = name
	}
}
func (n Node) Separator(options ...ContainerOption) { n.child("separator", "separator", options) }
func (n Node) Spacer(options ...ContainerOption)    { n.child("spacer", "", options) }
func (n Node) Radio(label, option string, selected *string, options ...ButtonOption) ChangeEvent {
	child := n.buttonLike("radio", "radio", label, options)
	if child.valid() && selected != nil {
		child.element.checked = *selected == option
		if activated(child) && *selected != option {
			*selected = option
			child.element.checked = true
			return ChangeEvent{true}
		}
	}
	return ChangeEvent{}
}
func (n Node) Slider(label string, value *float64, min, max, step float64, options ...ValueOption) ChangeEvent {
	var common []ContainerOption
	for _, o := range options {
		if c, ok := o.(ContainerOption); ok {
			common = append(common, c)
		}
	}
	child := n.child("slider", "slider", common)
	if !child.valid() {
		return ChangeEvent{}
	}
	child.element.text = label
	for _, o := range options {
		if _, ok := o.(ContainerOption); !ok {
			o.valueOption(child.element)
		}
	}
	child.frame.compute(child.element)
	child.element.fraction = -1
	if value != nil && finiteRange(min, max, step) {
		// Painted from the value after this call's changes.
		defer func() { child.element.fraction = sliderFraction(*value, min, max) }()
	}
	if value == nil || child.element.disabled || !finiteRange(min, max, step) {
		return ChangeEvent{}
	}
	original := *value
	next := original
	if math.IsNaN(next) || math.IsInf(next, 0) {
		next = min
	}
	next = sliderGrid(next, min, max, step)
	for _, event := range child.element.events {
		if event.Kind == "slider-pointer" {
			if event.Track.W > 0 && !math.IsNaN(event.X) && !math.IsInf(event.X, 0) {
				fraction := math.Max(0, math.Min(1, (event.X-event.Track.X)/event.Track.W))
				next = sliderGrid(min+fraction*(max-min), min, max, step)
			}
			continue
		}
		if event.Kind != "slider-key" {
			continue
		}
		switch strings.ToLower(event.Text) {
		case "left", "down":
			next = sliderGrid(next-step, min, max, step)
		case "right", "up":
			next = sliderGrid(next+step, min, max, step)
		case "page_up":
			next = sliderGrid(next+10*step, min, max, step)
		case "page_down":
			next = sliderGrid(next-10*step, min, max, step)
		case "home":
			next = min
		case "end":
			next = sliderGrid(max, min, max, step)
		}
	}
	if (math.IsNaN(original) || math.IsInf(original, 0)) || next != original {
		*value = next
		return ChangeEvent{true}
	}
	return ChangeEvent{}
}

// sliderGrid rounds to min+n*step then clamps; the final partial step is
// intentionally not a stop, so keyboard and pointer share the same grid.
// A relative epsilon keeps decimal steps such as 0.1 from losing their last
// stop. When the grid is finer than float64 precision (or the span overflows),
// the value is only clamped.
func sliderGrid(v, min, max, step float64) float64 {
	if v <= min {
		return min
	}
	if v >= max {
		v = max
	}
	q := (max - min) / step
	if math.IsInf(q, 0) || math.IsNaN(q) || q > 1<<53 {
		return v
	}
	steps := math.Round((v - min) / step)
	limit := math.Floor(q + q*1e-12 + 1e-12)
	steps = math.Max(0, math.Min(steps, limit))
	return math.Min(max, min+steps*step)
}

// sliderFraction is the painted thumb position; a non-finite value sits at
// the minimum and an empty range at the start.
func sliderFraction(v, min, max float64) float64 {
	if max <= min || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	f := (v - min) / (max - min)
	if span := max - min; math.IsInf(span, 0) {
		// Only overflowing spans are halved: halving loses subnormal ranges.
		f = (v/2 - min/2) / (max/2 - min/2)
	}
	if math.IsNaN(f) {
		return 0
	}
	return math.Max(0, math.Min(1, f))
}
func finiteRange(min, max, step float64) bool {
	return !math.IsNaN(min) && !math.IsNaN(max) && !math.IsNaN(step) && !math.IsInf(min, 0) && !math.IsInf(max, 0) && !math.IsInf(step, 0) && min <= max && step > 0
}
