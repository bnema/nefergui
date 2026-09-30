package nefergui

import (
	"math"
	"sort"
	"unicode/utf8"

	"github.com/bnema/nefergui/internal/edit"
	"github.com/bnema/nefergui/internal/layout"
	"github.com/go-text/typesetting/di"
)

type textBoundary struct {
	byteOffset int
	x, y       float64
}

// textBoundaries projects editor byte boundaries onto laid-out glyph clusters.
// Runs are in visual order; glyph cluster indices are source rune offsets.
func textBoundaries(value string, n *layout.Result, display []layout.Command) [][]textBoundary {
	if n == nil {
		return nil
	}
	offsets := edit.Boundaries(value)
	runeAt := make(map[int]int, len(offsets))
	for _, b := range offsets {
		runeAt[utf8.RuneCountInString(value[:b])] = b
	}
	var runs []layout.Run
	for _, cmd := range display {
		if cmd.ID == n.ID && cmd.Op == "text" {
			runs = cmd.Runs
			break
		}
	}
	var lines [][]textBoundary
	ri := 0
	top := n.Content.Y
	for _, line := range n.Lines {
		current := []textBoundary{}
		for _, shaped := range line.Runs {
			if ri >= len(runs) {
				break
			}
			run := runs[ri]
			ri++
			// Cluster start and end positions are derived from the glyph ink advances,
			// not rune counts (ligatures and combining graphemes have one visual span).
			type span struct{ low, high float64 }
			spans := make(map[int]span)
			for _, g := range run.Glyphs {
				p, ok := spans[g.Cluster]
				if !ok {
					p = span{g.X, g.X + g.Advance}
				} else {
					p.low = math.Min(p.low, g.X)
					p.high = math.Max(p.high, g.X+g.Advance)
				}
				spans[g.Cluster] = p
			}
			starts := make([]int, 0, len(spans))
			for k := range spans {
				starts = append(starts, k)
			}
			sort.Ints(starts)
			for i, k := range starts {
				end := shaped.End
				if i+1 < len(starts) {
					end = starts[i+1]
				}
				p := spans[k]
				a, b := p.low, p.high
				if shaped.Direction == di.DirectionRTL {
					a, b = b, a
				}
				if offset, ok := runeAt[k]; ok {
					current = append(current, textBoundary{offset, a, top})
				}
				if offset, ok := runeAt[end]; ok {
					current = append(current, textBoundary{offset, b, top})
				}
			}
		}
		if len(current) == 0 { // empty paragraph, including a trailing newline
			offset := 0
			if len(lines) > 0 && len(lines[len(lines)-1]) > 0 {
				for _, boundary := range lines[len(lines)-1] {
					if boundary.byteOffset > offset {
						offset = boundary.byteOffset
					}
				}
				for offset < len(value) && value[offset] == '\n' {
					offset++
				}
			}
			current = append(current, textBoundary{offset, n.Content.X, top})
		}
		lines = append(lines, current)
		top += line.Height
	}
	return lines
}

func nearestTextBoundary(value string, n *layout.Result, display []layout.Command, x, y float64) int {
	lines := textBoundaries(value, n, display)
	if len(lines) == 0 {
		return 0
	}
	line := 0
	top := n.Content.Y
	for i, shaped := range n.Lines {
		if y >= top+shaped.Height/2 {
			line = i
		}
		top += shaped.Height
	}
	if line >= len(lines) {
		line = len(lines) - 1
	}
	best := 0
	distance := math.Inf(1)
	for _, b := range lines[line] {
		if d := math.Abs(b.x - x); d < distance {
			distance = d
			best = b.byteOffset
		}
	}
	return best
}
