package nefergui

import (
	"math"

	"github.com/bnema/nefergui/internal/css"
	"github.com/bnema/nefergui/internal/edit"
	"github.com/bnema/nefergui/internal/layout"
)

var selectionColor = css.Color{R: 0.22, G: 0.43, B: 0.85, A: 0.4}
var caretColor = css.Color{R: 0, G: 0, B: 0, A: 1}

func (r *runtime) paintEditors(root *element, out *layout.Output) {
	var visit func(*element)
	visit = func(e *element) {
		if e == nil {
			return
		}
		if e.typ == "input" || e.typ == "textarea" {
			id := idKey(e.identity)
			s := r.edits[id]
			n := resultByID(out.Tree, id)
			if s != nil && n != nil {
				var commands []layout.Command
				if s.Value == "" && s.Preedit == "" && e.placeholder != "" && r.textEngine != nil {
					commands = placeholderCommand(e, n, r.textEngine)
				}
				if !e.disabled {
					commands = append(commands, editorDecoration(s, n, out.Display, e.password, e.identity.same(r.state.focus))...)
					if s.Preedit != "" && e.identity.same(r.state.focus) && r.textEngine != nil {
						commands = append(commands, preeditCommands(s, n, e, r.textEngine, out.Display)...)
					}
				}
				insertClippedCommands(out, id, commands)
			}
		}
		for _, child := range e.children {
			visit(child)
		}
	}
	visit(root)
}

// editorDecoration maps logical grapheme selection to visual cluster spans.
// A bidi boundary uses the run containing the character before the cursor:
// later duplicate offsets must not override the first span's trailing edge.
func editorDecoration(s *edit.State, n *layout.Result, display []layout.Command, password, focused bool) []layout.Command {
	if n == nil {
		return nil
	}
	value := s.Value
	start, end := s.Range()
	cursor := s.Cursor
	if password {
		boundaries := edit.Boundaries(value)
		value = s.Mask()
		render := edit.Boundaries(value)
		for i, b := range boundaries {
			if b == start {
				start = render[i]
			}
			if b == end {
				end = render[i]
			}
			if b == cursor {
				cursor = render[i]
			}
		}
	}
	lines := textBoundaries(value, n, display)
	var result []layout.Command
	for i, line := range lines {
		h := 0.0
		if i < len(n.Lines) {
			h = n.Lines[i].Height
		}
		for j := 0; j+1 < len(line); j += 2 {
			a, b := line[j], line[j+1]
			if a.byteOffset == b.byteOffset {
				continue
			}
			low, high := a.byteOffset, b.byteOffset
			if low > high {
				low, high = high, low
			}
			if start < high && end > low && start != end {
				x := math.Min(a.x, b.x)
				w := math.Abs(a.x - b.x)
				if w > 0 {
					result = append(result, layout.Command{Op: "selection", ID: n.ID, Rect: layout.Rect{X: x, Y: a.y, W: w, H: h}, Color: selectionColor, Opacity: 1})
				}
			}
		}
	}
	if focused {
		var point *textBoundary
		// The trailing edge of the preceding character takes priority over the
		// leading edge of the following run when both map to the same offset.
		for i := range lines {
			for j := range lines[i] {
				b := &lines[i][j]
				if b.byteOffset == cursor && point == nil {
					point = b
				}
			}
		}
		if point == nil && len(lines) == 0 {
			result = append(result, layout.Command{Op: "caret", ID: n.ID, Rect: layout.Rect{X: n.Content.X, Y: n.Content.Y, W: 1, H: n.Content.H}, Color: caretColor, Opacity: 1})
		}
		if point != nil {
			h := n.Content.H
			for i, line := range lines {
				if len(line) > 0 && line[0].y == point.y && i < len(n.Lines) {
					h = n.Lines[i].Height
					break
				}
			}
			result = append(result, layout.Command{Op: "caret", ID: n.ID, Rect: layout.Rect{X: point.x, Y: point.y, W: 1, H: h}, Color: caretColor, Opacity: 1})
		}
	}
	return result
}
