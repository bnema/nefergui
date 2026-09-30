package nefergui

import (
	"math"

	"github.com/bnema/nefergui/internal/layout"
)

func (r *runtime) scrollCaret(root *element, out *layout.Output) bool {
	if root == nil || r.state.focus == nil {
		return false
	}
	id := idKey(r.state.focus)
	var find func(*element) *element
	find = func(e *element) *element {
		if e == nil {
			return nil
		}
		if idKey(e.identity) == id {
			return e
		}
		for _, child := range e.children {
			if v := find(child); v != nil {
				return v
			}
		}
		return nil
	}
	e := find(root)
	if e == nil || e.disabled || (e.typ != "input" && e.typ != "textarea") {
		return false
	}
	s := r.edits[id]
	n := resultByID(out.Tree, id)
	if s == nil || n == nil {
		return false
	}
	rendered := e.text
	cursor := s.Cursor
	if e.password {
		cursor = len([]rune(s.Value[:s.Cursor])) * len("•")
	}
	lines := textBoundaries(rendered, n, out.Display)
	var caret *textBoundary
	for i := range lines {
		for j := range lines[i] {
			if lines[i][j].byteOffset == cursor {
				caret = &lines[i][j]
			}
		}
	}
	if caret == nil {
		return false
	}
	old := r.state.scroll[id]
	next := old
	if e.typ == "input" {
		if caret.x < n.Content.X {
			next.W += caret.x - n.Content.X
		} else if caret.x > n.Content.X+n.Content.W-1 {
			next.W += caret.x - (n.Content.X + n.Content.W - 1)
		}
		next.H = 0
	} else {
		height := 0.0
		if len(n.Lines) > 0 {
			height = n.Lines[0].Height
		}
		if caret.y < n.Content.Y {
			next.H += caret.y - n.Content.Y
		} else if caret.y+height > n.Content.Y+n.Content.H {
			next.H += caret.y + height - (n.Content.Y + n.Content.H)
		}
		next.W = 0
	}
	next.W = math.Max(0, math.Min(next.W, math.Max(0, n.ContentSize.W-n.Content.W)))
	next.H = math.Max(0, math.Min(next.H, math.Max(0, n.ContentSize.H-n.Content.H)))
	if next == old {
		return false
	}
	if r.state.scroll == nil {
		r.state.scroll = make(map[string]layout.Size)
	}
	r.state.scroll[id] = next
	return true
}
