package nefergui

import (
	"math"

	"github.com/bnema/nefergui/internal/edit"
	"github.com/bnema/nefergui/internal/layout"
)

// moveVisualLine uses the last committed wrap geometry; preferred X survives
// consecutive vertical moves, but is reset by horizontal editing and pointer input.
func moveVisualLine(s *edit.State, n *layout.Result, display []layout.Command, down, shift bool) {
	lines := textBoundaries(s.Value, n, display)
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
		// Prefer the following line at a shared wrap boundary.
	}
	if !s.PreferredXValid {
		for _, b := range lines[current] {
			if b.byteOffset == s.Cursor {
				s.PreferredX = b.x
				break
			}
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
