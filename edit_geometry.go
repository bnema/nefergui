package nefergui

import (
	"math"
	"sort"
	"unicode/utf8"

	"github.com/bnema/nefergui/internal/edit"
	"github.com/bnema/nefergui/internal/layout"
	"github.com/go-text/typesetting/di"
)

// textBoundary is one painted edge of a grapheme cluster. trailing marks the
// far edge of the character that precedes byteOffset; otherwise it is the
// leading edge of the character that follows. line indexes layout.Result.Lines.
type textBoundary struct {
	byteOffset int
	x, y       float64
	line       int
	trailing   bool
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
					current = append(current, textBoundary{byteOffset: offset, x: a, y: top, line: len(lines)})
				}
				if offset, ok := runeAt[end]; ok {
					current = append(current, textBoundary{byteOffset: offset, x: b, y: top, line: len(lines), trailing: true})
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
			current = append(current, textBoundary{byteOffset: offset, x: n.Content.X, y: top, line: len(lines)})
		}
		lines = append(lines, current)
		top += line.Height
	}
	return lines
}

// displayText is the text an editor shows for a logical value, with the
// mapping between their byte offsets. A password shows one bullet per grapheme
// cluster, so logical and shown offsets differ; otherwise they are identical.
// It is the only place that maps password offsets.
type displayText struct {
	shown            string
	logical, display []int // grapheme boundaries of the value and of shown; nil when identical
}

func newDisplayText(value string, password bool) displayText {
	if !password {
		return displayText{shown: value}
	}
	shown := (&edit.State{Value: value}).Mask()
	return displayText{shown: shown, logical: edit.Boundaries(value), display: edit.Boundaries(shown)}
}

// toShown maps a logical byte offset to the shown string. An offset inside a
// grapheme cluster maps to the start of that cluster.
func (d displayText) toShown(offset int) int {
	if d.logical == nil {
		return offset
	}
	i := sort.SearchInts(d.logical, offset+1) - 1
	if i < 0 {
		i = 0
	}
	return d.display[i]
}

// toLogical maps a byte offset of the shown string back to the value.
func (d displayText) toLogical(offset int) int {
	if d.logical == nil {
		return offset
	}
	i := sort.SearchInts(d.display, offset+1) - 1
	if i < 0 {
		i = 0
	}
	return d.logical[i]
}

// editorGeometry is the single owner of where editor byte offsets sit on
// screen. Every caller (pointer placement, vertical movement, selection and
// caret painting, caret scrolling, preedit origin) reads it, so they agree.
// Boundary offsets are logical value offsets, password or not.
//
// Caret policy: a boundary can have several visual positions (bidi run
// changes, soft wraps). The caret sits at the trailing edge of the character
// before the offset; the leading edge of the following character is used only
// when no character precedes the offset.
type editorGeometry struct {
	node  *layout.Result
	lines [][]textBoundary
}

func newEditorGeometry(s *edit.State, n *layout.Result, display []layout.Command, password bool) editorGeometry {
	text := newDisplayText(s.Value, password)
	lines := textBoundaries(text.shown, n, display)
	if text.logical != nil {
		for _, line := range lines {
			for i := range line {
				line[i].byteOffset = text.toLogical(line[i].byteOffset)
			}
		}
	}
	return editorGeometry{node: n, lines: lines}
}

// caret returns the visual caret position for a logical offset.
func (g editorGeometry) caret(offset int) (textBoundary, bool) {
	var leading *textBoundary
	for i := range g.lines {
		b, trailing := preferredBoundary(g.lines[i], offset)
		if b == nil {
			continue
		}
		if trailing {
			return *b, true
		}
		if leading == nil {
			leading = b
		}
	}
	if leading == nil {
		return textBoundary{}, false
	}
	return *leading, true
}

// caretOnLine applies the caret policy within one visual line.
func (g editorGeometry) caretOnLine(line, offset int) (textBoundary, bool) {
	b, _ := preferredBoundary(g.lines[line], offset)
	if b == nil {
		return textBoundary{}, false
	}
	return *b, true
}

// preferredBoundary picks the trailing edge of the preceding character over
// the leading edge of the following one, else the first leading edge.
func preferredBoundary(line []textBoundary, offset int) (*textBoundary, bool) {
	var leading *textBoundary
	for j := range line {
		b := &line[j]
		if b.byteOffset != offset {
			continue
		}
		if b.trailing {
			return b, true
		}
		if leading == nil {
			leading = b
		}
	}
	return leading, false
}

// lineHeight is the height of a visual line, falling back to the content box.
func (g editorGeometry) lineHeight(line int) float64 {
	if line >= 0 && line < len(g.node.Lines) {
		return g.node.Lines[line].Height
	}
	return g.node.Content.H
}

// nearest returns the logical offset whose boundary is horizontally closest
// to (x, y) on the visual line under y.
func (g editorGeometry) nearest(x, y float64) int {
	if len(g.lines) == 0 {
		return 0
	}
	line := 0
	top := g.node.Content.Y
	for i, shaped := range g.node.Lines {
		if y >= top+shaped.Height/2 {
			line = i
		}
		top += shaped.Height
	}
	if line >= len(g.lines) {
		line = len(g.lines) - 1
	}
	best := 0
	distance := math.Inf(1)
	for _, b := range g.lines[line] {
		if d := math.Abs(b.x - x); d < distance {
			distance = d
			best = b.byteOffset
		}
	}
	return best
}
