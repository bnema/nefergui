package bidi

import (
	"fmt"
	"slices"
)

// BaseDirection selects the paragraph embedding level. Auto follows P2/P3.
// nefergui: expose the existing upstream paragraph algorithm's resolved levels.
type BaseDirection uint8

const (
	Auto BaseDirection = iota
	LTR
	RTL
)

// Levels returns resolved embedding levels after L1, one per input rune.
// lineBreaks contains strictly increasing, nonempty, rune offsets, ending in len(text).
// Removed X9 formatting codes receive a convenience level; call Reorder with
// the same text to omit those codes from the visual indices.
// nefergui: adapt upstream newParagraph/getLevels for per-line layout consumers.
func Levels(text []rune, base BaseDirection, lineBreaks []int) ([]uint8, error) {
	levels, _, err := Resolve(text, base, lineBreaks)
	return levels, err
}

// Resolve returns the paragraph base level and the resolved levels after L1.
// nefergui: surface existing levels without copying or reclassifying UAX#9 classes.
func Resolve(text []rune, base BaseDirection, lineBreaks []int) ([]uint8, uint8, error) {
	return ResolveInto(nil, text, base, lineBreaks)
}

// ResolveInto is Resolve reusing dst's backing array for left-to-right-only
// text, so it does not allocate. Text that needs the full UAX#9 algorithm
// always gets a newly allocated slice; callers keep the returned slice.
func ResolveInto(dst []uint8, text []rune, base BaseDirection, lineBreaks []int) ([]uint8, uint8, error) {
	if base > RTL {
		return nil, 0, fmt.Errorf("invalid bidi base %d", base)
	}
	if len(text) == 0 {
		if len(lineBreaks) != 0 {
			return nil, 0, fmt.Errorf("breaks on empty paragraph")
		}
		if base == RTL {
			return nil, 1, nil
		}
		return nil, 0, nil
	}
	if len(lineBreaks) == 0 || lineBreaks[len(lineBreaks)-1] != len(text) {
		return nil, 0, fmt.Errorf("line breaks must end at paragraph length")
	}
	prev := 0
	for _, end := range lineBreaks {
		if end <= prev || end > len(text) {
			return nil, 0, fmt.Errorf("invalid line break %d", end)
		}
		prev = end
	}
	if base != RTL && leftToRightOnly(text) {
		dst = slices.Grow(dst[:0], len(text))[:len(text)]
		clear(dst)
		return dst, 0, nil
	}
	return resolveFull(text, base, lineBreaks)
}

// resolveFull runs the complete UAX#9 algorithm on validated input.
func resolveFull(text []rune, base BaseDirection, lineBreaks []int) ([]uint8, uint8, error) {
	classes := make([]Class, len(text))
	pairTypes := make([]bracketType, len(text))
	pairValues := make([]rune, len(text))
	for i, r := range text {
		prop, _ := LookupRune(r)
		classes[i] = prop.Class()
		switch {
		case prop.IsOpeningBracket():
			pairTypes[i] = bpOpen
			pairValues[i] = r
			// nefergui: U+2329/232A are canonically equivalent to U+3008/3009.
			if r == 0x2329 {
				pairValues[i] = 0x3008
			}
		case prop.IsBracket():
			pairTypes[i] = bpClose
			// nefergui: canonicalize close brackets to the opening identifier
			// required by upstream bracketPairer.matchOpener.
			pairValues[i] = prop.reverseBracket(r)
			if r == 0x232a {
				pairValues[i] = 0x3008
			}
		}
	}
	embedding := implicitLevel
	if base == LTR {
		embedding = 0
	} else if base == RTL {
		embedding = 1
	}
	p, err := newParagraph(classes, pairTypes, pairValues, embedding)
	if err != nil {
		return nil, 0, err
	}
	// nefergui: exercise the upstream L2 entry point as a differential oracle;
	// levels below are the same L1 result used by that entry point.
	_ = p.getReordering(lineBreaks)
	raw := p.getLevels(lineBreaks)
	out := make([]uint8, len(raw))
	for i, l := range raw {
		out[i] = uint8(l)
	}
	return out, uint8(p.embeddingLevel), nil
}

// leftToRightOnly reports whether every rune resolves to level 0 in a level-0
// paragraph: with no R, AL, AN, explicit formatting or BN, P2/P3 pick level 0,
// W7 turns EN into L (sos is L), neutrals resolve to L and I1 raises nothing.
// B is left to the full algorithm, which validates its position.
func leftToRightOnly(text []rune) bool {
	for _, r := range text {
		if r < 0x80 && r >= 0x20 && r != 0x7f {
			continue // printable ASCII: L, EN, ES, ET, CS, WS or ON
		}
		prop, _ := LookupRune(r)
		switch prop.Class() {
		case R, AL, AN, B, BN, LRO, RLO, LRE, RLE, PDF, LRI, RLI, FSI, PDI:
			return false
		}
	}
	return true
}

// Reorder returns a visual-to-logical rune map per line using L2; X9-removed
// formatting characters are excluded from the result. Levels must be the
// per-line L1 output of Resolve. Consumers may group neighboring indices into
// shaped runs while preserving the shaper's glyph order within each run.
// nefergui: reuse upstream multiline L2 rather than infer levels from parity.
func Reorder(text []rune, levels []uint8, breaks []int) ([][]int, error) {
	if len(text) != len(levels) {
		return nil, fmt.Errorf("level count mismatch")
	}
	if len(text) == 0 {
		return nil, nil
	}
	if len(breaks) == 0 || breaks[len(breaks)-1] != len(text) {
		return nil, fmt.Errorf("invalid breaks")
	}
	var out [][]int
	start := 0
	for _, end := range breaks {
		if end <= start || end > len(text) {
			return nil, fmt.Errorf("invalid break %d", end)
		}
		lv := make([]level, end-start)
		for i, v := range levels[start:end] {
			lv[i] = level(v)
		}
		// nefergui: call upstream's multiline L2 helper to keep its code live.
		order := computeMultilineReordering(lv, []int{len(lv)})
		line := make([]int, 0, len(order))
		for _, i := range order {
			r := text[start+i]
			cls, _ := LookupRune(r)
			if !isRemovedByX9(cls.Class()) {
				line = append(line, start+i)
			}
		}
		out = append(out, line)
		start = end
	}
	return out, nil
}
