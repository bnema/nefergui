package ui

import (
	"math"

	"github.com/bnema/nefergui/internal/css"
	"github.com/bnema/nefergui/internal/layout"
)

// paintIndicators adds checkbox and radio indicators and slider tracks after
// layout. They sit in the room the UA sheet reserves (checkbox and radio
// padding-left, slider padding-bottom), are sized from font-size and colored
// from accent-color and color, so author CSS drives them like any other paint.
func (r *runtime) paintIndicators(root *element, out *layout.Output) {
	var visit func(*element)
	visit = func(e *element) {
		if e == nil {
			return
		}
		if (e.typ == "checkbox" || e.typ == "radio" || e.typ == "slider") && e.computed != nil {
			id := idKey(e.identity)
			if n := resultByID(out.Tree, id); n != nil {
				insertOwnCommands(out, id, indicatorCommands(e, n, e.computed.Style))
			}
		}
		for _, child := range e.children {
			visit(child)
		}
	}
	visit(root)
}

// indicatorCommands returns the paint for one control. The indicator is one
// em square (16px when font-size is unset).
func indicatorCommands(e *element, n *layout.Result, s *css.Style) []layout.Command {
	size := s.FontSize.Value
	if size <= 0 {
		size = 16
	}
	base := layout.Command{ID: n.ID, Opacity: math.Min(1, math.Max(0, s.Opacity))}
	muted := s.Color
	muted.A *= 0.45
	if e.typ == "slider" {
		return sliderCommands(e, n, base, size, s.AccentColor, muted)
	}
	// Center the box in the reserved left padding, and vertically on the
	// content box so it lines up with the label.
	left := n.Border.X + n.BorderWidths.Left
	x := left + math.Max(0, (n.Padding.Left-size)/2)
	y := n.Content.Y + (n.Content.H-size)/2
	box := layout.Rect{X: x, Y: y, W: size, H: size}
	radius := size * 0.2
	if e.typ == "radio" {
		radius = size / 2
	}
	radii := [4]float64{radius, radius, radius, radius}
	if !e.checked {
		w := math.Max(1, math.Round(size/10))
		cmd := base
		// The renderer multiplies side alpha by Color alpha: keep Color opaque
		// so the sides carry the muted alpha once.
		opaque := muted
		opaque.A = 1
		cmd.Op, cmd.Rect, cmd.Radii, cmd.Color = "border", box, radii, opaque
		cmd.Widths = layout.Edges{Top: w, Right: w, Bottom: w, Left: w}
		cmd.Colors = css.ColorSides{Top: muted, Right: muted, Bottom: muted, Left: muted}
		return []layout.Command{cmd}
	}
	fill := base
	fill.Op, fill.Rect, fill.Radii, fill.Color = "rect", box, radii, s.AccentColor
	// A contrasting inner mark: a dot for radios, a smaller square for
	// checkboxes (the renderer paints rounded boxes, not glyph paths).
	inner := size * 0.4
	if e.typ == "checkbox" {
		inner = size * 0.5
	}
	mark := base
	markRadius := inner / 2
	if e.typ == "checkbox" {
		markRadius = inner * 0.2
	}
	mark.Op, mark.Color = "rect", css.Color{R: 1, G: 1, B: 1, A: 1}
	mark.Rect = layout.Rect{X: x + (size-inner)/2, Y: y + (size-inner)/2, W: inner, H: inner}
	mark.Radii = [4]float64{markRadius, markRadius, markRadius, markRadius}
	return []layout.Command{fill, mark}
}

// sliderCommands paints the track under the label, in the reserved bottom
// padding, across the content width the pointer maps to. The filled part and
// the thumb follow the value fraction.
func sliderCommands(e *element, n *layout.Result, base layout.Command, size float64, accent, muted css.Color) []layout.Command {
	// Without reserved bottom padding there is no room: paint nothing rather
	// than outside the control's box.
	band := n.Padding.Bottom
	if band <= 0 {
		return nil
	}
	centerY := n.Content.Y + n.Content.H + band/2
	trackH := math.Max(2, math.Round(size/4))
	track := layout.Rect{X: n.Content.X, Y: centerY - trackH/2, W: n.Content.W, H: trackH}
	radii := [4]float64{trackH / 2, trackH / 2, trackH / 2, trackH / 2}
	rail := base
	rail.Op, rail.Rect, rail.Radii, rail.Color = "rect", track, radii, muted
	commands := []layout.Command{rail}
	if e.fraction < 0 {
		return commands
	}
	x := track.X + e.fraction*track.W
	if filled := x - track.X; filled > 0 {
		fill := base
		fill.Op, fill.Radii, fill.Color = "rect", radii, accent
		fill.Rect = layout.Rect{X: track.X, Y: track.Y, W: filled, H: track.H}
		commands = append(commands, fill)
	}
	d := math.Min(size, band)
	thumb := base
	thumb.Op, thumb.Color = "rect", accent
	thumb.Rect = layout.Rect{X: x - d/2, Y: centerY - d/2, W: d, H: d}
	thumb.Radii = [4]float64{d / 2, d / 2, d / 2, d / 2}
	return append(commands, thumb)
}

// insertOwnCommands places padding paint after the element's box paint and
// before its content: at its clip-push when it clips (the clip is the content
// box, which would hide the padding), otherwise after its last command.
func insertOwnCommands(out *layout.Output, id string, commands []layout.Command) {
	at := -1
	for i := range out.Display {
		if out.Display[i].ID != id {
			continue
		}
		if out.Display[i].Op == "clip-push" {
			at = i
			break
		}
		at = i + 1
	}
	insertAt(out, at, commands)
}

// insertClippedCommands places content decorations inside the element's clip,
// after its text and before its clip-pop. Editors always clip.
func insertClippedCommands(out *layout.Output, id string, commands []layout.Command) {
	for i := range out.Display {
		if out.Display[i].ID == id && out.Display[i].Op == "clip-pop" {
			insertAt(out, i, commands)
			return
		}
	}
}
func insertAt(out *layout.Output, at int, commands []layout.Command) {
	if at < 0 || len(commands) == 0 {
		return
	}
	out.Display = append(out.Display[:at], append(commands, out.Display[at:]...)...)
}
