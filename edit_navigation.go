package nefergui

import (
	"math"

	"github.com/bnema/nefergui/internal/edit"
	"github.com/bnema/nefergui/internal/layout"
)

// moveVisualLine uses the last committed wrap geometry; preferred X survives
// consecutive vertical moves, but is reset by horizontal editing and pointer input.
func moveVisualLine(s *edit.State, n *layout.Result, display []layout.Command, password, down, shift bool) {
	geometry := newEditorGeometry(s, n, display, password)
	lines := geometry.lines
	if len(lines) == 0 {
		return
	}
	current := 0
	for i, line := range lines {
		for _, b := range line {
			if b.byteOffset == s.Cursor {
				current = i
				break
			}
		}
	}
	// Navigation prefers the following line at a shared wrap boundary, unlike
	// the painted caret, which stays at the end of the preceding line.
	if !s.PreferredXValid {
		if b, ok := geometry.caretOnLine(current, s.Cursor); ok {
			s.PreferredX = b.x
		}
		s.PreferredXValid = true
	}
	next := current - 1
	if down {
		next = current + 1
	}
	if next < 0 || next >= len(lines) {
		return
	}
	best := s.Cursor
	distance := math.Inf(1)
	for _, b := range lines[next] {
		if down && b.byteOffset <= s.Cursor {
			continue
		}
		if !down && b.byteOffset >= s.Cursor {
			continue
		}
		if d := math.Abs(b.x - s.PreferredX); d < distance {
			distance = d
			best = b.byteOffset
		}
	}
	s.Move(best, shift)
}
