package text

import (
	"errors"
	"github.com/bnema/nefergui/internal/bidi"
)

type paragraphRange struct{ start, end int }

// paragraphRanges excludes paragraph separators from shaping. CRLF is a
// single separator but consumes two source runes, so Run offsets stay global.
// The terminal empty range is a caret line only when the input ends in a
// separator; the empty input retains Measure's existing zero-height behavior.
func paragraphRanges(text []rune) ([]paragraphRange, bool) {
	var out []paragraphRange
	start := 0
	split := false
	for i := 0; i < len(text); i++ {
		r := text[i]
		if !paragraphSeparator(r) {
			continue
		}
		out = append(out, paragraphRange{start, i})
		split = true
		if r == '\r' && i+1 < len(text) && text[i+1] == '\n' {
			i++
		}
		start = i + 1
	}
	if split {
		out = append(out, paragraphRange{start, len(text)})
	}
	return out, split
}
func paragraphSeparator(r rune) bool {
	switch r {
	case '\n', '\r', 0x001c, 0x001d, 0x001e, 0x0085, 0x2029:
		return true
	}
	return false
}

func (e *Engine) measureParagraphs(text []rune, paragraphs []paragraphRange, r Request, width float64) (Layout, error) {
	var result Layout
	if r.Direction > bidi.RTL {
		return result, errors.New("invalid paragraph direction")
	}
	for index, p := range paragraphs {
		// Uncached: part is modified in place below.
		part, err := e.measure(string(text[p.start:p.end]), r, width)
		if err != nil {
			return Layout{}, err
		}
		if index == 0 {
			result.Direction = part.Direction
			result.Baseline = part.Baseline
			result.LineHeight = part.LineHeight
		}
		if len(part.Lines) == 0 {
			part.Lines = []Line{{Direction: part.Direction, Height: part.LineHeight, Baseline: part.Baseline}}
		}
		for _, line := range part.Lines {
			line.Baseline += result.Height
			for i := range line.Runs {
				run := &line.Runs[i]
				run.Start += p.start
				run.End += p.start
				run.Y += result.Height
				for j := range run.Glyphs {
					run.Glyphs[j].Y += result.Height
					run.Glyphs[j].Cluster += p.start
				}
			}
			result.Lines = append(result.Lines, line)
			if line.Width > result.Width {
				result.Width = line.Width
			}
		}
		result.Height += float64(len(part.Lines)) * part.LineHeight
		if part.MinContent > result.MinContent {
			result.MinContent = part.MinContent
		}
		if part.MaxContent > result.MaxContent {
			result.MaxContent = part.MaxContent
		}
	}
	return result, nil
}

// A layout's Direction refers to the first paragraph. Each Line has its own
// Direction so subsequent auto-detected RTL paragraphs retain their basis.
