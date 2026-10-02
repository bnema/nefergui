package text

import (
	"fmt"
	"sort"

	"github.com/bnema/nefergui/internal/bidi"
	"github.com/go-text/typesetting/di"
	"github.com/go-text/typesetting/shaping"
)

// splitLevels prevents a single shaping run from crossing resolved paragraph
// levels (not merely direction parity). The wrapper can subsequently split
// further at UAX#14 break opportunities.
func splitLevels(out, items []shaping.Input, levels []uint8) []shaping.Input {
	out = out[:0]
	for _, item := range items {
		if item.RunStart >= item.RunEnd {
			continue
		}
		start := item.RunStart
		for i := start + 1; i < item.RunEnd; i++ {
			if levels[i] != levels[start] {
				part := item
				part.RunStart = start
				part.RunEnd = i
				part.Direction = directionFor(levels[start])
				out = append(out, part)
				start = i
			}
		}
		item.RunStart = start
		item.Direction = directionFor(levels[start])
		out = append(out, item)
	}
	return out
}
func directionFor(level uint8) di.Direction {
	if level&1 != 0 {
		return di.DirectionRTL
	}
	return di.DirectionLTR
}

type runItem struct {
	output shaping.Output
	level  uint8
}

// visualScratch holds order buffers reused across lines; the returned runs
// are valid until the next call.
type visualScratch struct {
	breaks []int
	levels []uint8
	runs   []runItem
}

// order resets trailing whitespace to the paragraph level (L1) by
// resolving levels against the *actual wrapped line breaks*, then applies L2
// to complete runs. The shaper already emits glyphs in visual order within an
// odd-level run: never reverse its glyph slice a second time.
func (v *visualScratch) order(text []rune, line shaping.Line, base bidi.BaseDirection) ([]runItem, error) {
	if len(line) == 0 {
		return nil, nil
	}
	start, end := line[0].Runes.Offset, line[len(line)-1].Runes.Offset+line[len(line)-1].Runes.Count
	if start < 0 || end > len(text) || end <= start {
		return nil, fmt.Errorf("invalid line range %d:%d", start, end)
	}
	// Include earlier lines so weak and neutral resolution use paragraph context.
	breaks := v.breaks[:0]
	if start > 0 {
		breaks = append(breaks, start)
	}
	breaks = append(breaks, end)
	if end < len(text) {
		breaks = append(breaks, len(text))
	}
	v.breaks = breaks
	levels, _, err := bidi.ResolveInto(v.levels, text, base, breaks)
	if err != nil {
		return nil, err
	}
	v.levels = levels
	// The wrapper's runs are in logical order. A level change within one output
	// requires reshaping at that boundary; splitLevels does this before wrapping.
	clear(v.runs) // drop glyph references from the previous line
	runs := v.runs[:0]
	for _, o := range line {
		s, e := o.Runes.Offset, o.Runes.Offset+o.Runes.Count
		if s < start || e > end || s >= e {
			return nil, fmt.Errorf("invalid shaped run %d:%d", s, e)
		}
		if sameLevel(levels, s, e) {
			runs = append(runs, runItem{output: o, level: levels[s]})
			continue
		}
		// L1 may reset a trailing space in a shaped RTL run. Split the output at
		// cluster boundaries, preserving its glyph metrics and HarfBuzz order.
		parts, err := splitWrappedLevel(o, levels)
		if err != nil {
			return nil, err
		}
		for _, part := range parts {
			runs = append(runs, runItem{output: part, level: levels[part.Runes.Offset]})
		}
	}
	v.runs = runs
	// L2 on run levels: from maximum down to the minimum odd level, reverse
	// contiguous sequences whose levels are >= the current level.
	highest, lowestOdd := uint8(0), uint8(255)
	for _, run := range runs {
		if run.level > highest {
			highest = run.level
		}
		if run.level&1 == 1 && run.level < lowestOdd {
			lowestOdd = run.level
		}
	}
	if lowestOdd != 255 {
		for current := int(highest); current >= int(lowestOdd); current-- {
			for i := 0; i < len(runs); {
				if int(runs[i].level) < current {
					i++
					continue
				}
				j := i + 1
				for j < len(runs) && int(runs[j].level) >= current {
					j++
				}
				for a, b := i, j-1; a < b; a, b = a+1, b-1 {
					runs[a], runs[b] = runs[b], runs[a]
				}
				i = j
			}
		}
	}
	return runs, nil
}

func sameLevel(levels []uint8, start, end int) bool {
	for i := start + 1; i < end; i++ {
		if levels[i] != levels[start] {
			return false
		}
	}
	return true
}

// splitWrappedLevel divides only at glyph cluster boundaries; UAX#14 wraps
// at these same boundaries. Empty/format glyphs stay attached to their cluster.
func splitWrappedLevel(o shaping.Output, levels []uint8) ([]shaping.Output, error) {
	start, end := o.Runes.Offset, o.Runes.Offset+o.Runes.Count
	if start < 0 || end > len(levels) || start >= end {
		return nil, fmt.Errorf("invalid output range")
	}
	if sameLevel(levels, start, end) {
		return []shaping.Output{o}, nil
	}
	// Group glyphs by lowest text cluster index, then restore HB visual order.
	clusters := map[int][]shaping.Glyph{}
	var keys []int
	for _, g := range o.Glyphs {
		idx := g.TextIndex()
		if idx < start || idx >= end {
			return nil, fmt.Errorf("glyph cluster outside run")
		}
		if _, ok := clusters[idx]; !ok {
			keys = append(keys, idx)
		}
		clusters[idx] = append(clusters[idx], g)
	}
	sort.Ints(keys)
	if len(keys) == 0 {
		return []shaping.Output{o}, nil
	}
	var parts []shaping.Output
	for i, key := range keys {
		limit := end
		if i+1 < len(keys) {
			limit = keys[i+1]
		}
		if levels[key] != levels[limit-1] {
			return nil, fmt.Errorf("bidi level changes within glyph cluster %d", key)
		}
		part := o
		part.Runes = shaping.Range{Offset: key, Count: limit - key}
		part.Glyphs = append([]shaping.Glyph(nil), clusters[key]...)
		part.Direction = directionFor(levels[key])
		part.RecomputeAdvance()
		parts = append(parts, part)
	}
	// Wrapper may have absorbed leading non-glyph format runes.
	if parts[0].Runes.Offset > start {
		parts[0].Runes.Count += parts[0].Runes.Offset - start
		parts[0].Runes.Offset = start
	}
	// Recombine adjacent same-level parts without changing glyph order.
	var merged []shaping.Output
	for _, part := range parts {
		if n := len(merged); n > 0 && levels[merged[n-1].Runes.Offset] == levels[part.Runes.Offset] {
			prev := &merged[n-1]
			prev.Runes.Count = part.Runes.Offset + part.Runes.Count - prev.Runes.Offset
			if o.Direction == di.DirectionRTL {
				prev.Glyphs = append(part.Glyphs, prev.Glyphs...)
			} else {
				prev.Glyphs = append(prev.Glyphs, part.Glyphs...)
			}
			prev.RecomputeAdvance()
			continue
		}
		merged = append(merged, part)
	}
	return merged, nil
}
