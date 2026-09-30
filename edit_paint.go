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
// The caret follows editorGeometry's policy: at an ambiguous boundary, such as
// a bidi run change, the preceding character's trailing edge is used.
func editorDecoration(s *edit.State, n *layout.Result, display []layout.Command, password, focused bool) []layout.Command {
	if n == nil {
		return nil
	}
	start, end := s.Range()
	geometry := newEditorGeometry(s, n, display, password)
	lines := geometry.lines
	var result []layout.Command
	for i, line := range lines {
		h := 0.0
		if i < len(n.Lines) {
			h = geometry.lineHeight(i)
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
		// Selection and caret share the geometry's caret policy: the trailing
		// edge of the preceding character wins over a later duplicate offset.
		point, found := geometry.caret(s.Cursor)
		if !found && len(lines) == 0 {
			result = append(result, layout.Command{Op: "caret", ID: n.ID, Rect: layout.Rect{X: n.Content.X, Y: n.Content.Y, W: 1, H: n.Content.H}, Color: caretColor, Opacity: 1})
		}
		if found {
			result = append(result, layout.Command{Op: "caret", ID: n.ID, Rect: layout.Rect{X: point.x, Y: point.y, W: 1, H: geometry.lineHeight(point.line)}, Color: caretColor, Opacity: 1})
		}
	}
	return result
}
