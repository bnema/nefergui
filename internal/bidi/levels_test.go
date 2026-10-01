package bidi

import (
	"slices"
	"testing"
	"unicode"
)

// The left-to-right fast path must match the full algorithm on every rune
// it accepts, alone and next to other accepted runes.
func TestLeftToRightOnlyMatchesFullAlgorithm(t *testing.T) {
	var accepted []rune
	for r := rune(0); r <= unicode.MaxRune; r++ {
		if (r >= 0xd800 && r <= 0xdfff) || !leftToRightOnly([]rune{r}) {
			continue
		}
		accepted = append(accepted, r)
	}
	if len(accepted) < 1000 {
		t.Fatalf("only %d runes accepted", len(accepted))
	}
	check := func(text []rune) {
		t.Helper()
		breaks := []int{len(text)}
		if len(text) > 1 {
			breaks = []int{1, len(text)} // L1 on a line end inside the paragraph
		}
		for _, base := range []BaseDirection{Auto, LTR} {
			fast, fastBase, err := Resolve(text, base, breaks)
			if err != nil {
				t.Fatal(err)
			}
			full, fullBase, err := resolveFull(text, base, breaks)
			if err != nil {
				t.Fatal(err)
			}
			if fastBase != fullBase || !slices.Equal(fast, full) {
				t.Fatalf("%U base %d: fast %v/%d, full %v/%d", text, base, fast, fastBase, full, fullBase)
			}
		}
	}
	for _, r := range accepted {
		check([]rune{r})
		check([]rune{'1', r, '2'})
		check([]rune{'(', r, ' ', '1', ')'})
	}
	check([]rune("Value: 42, (x+y) = -3.5% \t#1"))
}

func TestAllocResolveLeftToRight(t *testing.T) {
	text := []rune("Value: 42")
	breaks := []int{len(text)}
	if got := testing.AllocsPerRun(100, func() { _, _, _ = Resolve(text, Auto, breaks) }); got > 1 {
		t.Fatalf("allocs = %v, want at most 1 (the result)", got)
	}
}
