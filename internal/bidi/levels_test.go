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
	dst := make([]uint8, 0, len(text))
	if got := testing.AllocsPerRun(100, func() { dst, _, _ = ResolveInto(dst, text, Auto, breaks) }); got != 0 {
		t.Fatalf("ResolveInto allocs = %v, want 0", got)
	}
	dst[0] = 7 // stale levels must be cleared on reuse
	if levels, _, _ := ResolveInto(dst, text, Auto, breaks); levels[0] != 0 {
		t.Fatalf("reused levels not cleared: %v", levels)
	}
}

func TestResolveIntoFullPathIgnoresDst(t *testing.T) {
	text := []rune("abc \u05d0\u05d1\u05d2 12")
	breaks := []int{len(text)}
	for _, base := range []BaseDirection{Auto, RTL} {
		want, wantPara, err := Resolve(text, base, breaks)
		if err != nil {
			t.Fatal(err)
		}
		dst := make([]uint8, len(text))
		for i := range dst {
			dst[i] = 7
		}
		got, para, err := ResolveInto(dst, text, base, breaks)
		if err != nil || para != wantPara || !slices.Equal(got, want) {
			t.Fatalf("base %d: got %v %d %v, want %v %d", base, got, para, err, want, wantPara)
		}
		if &got[0] == &dst[0] || dst[0] != 7 {
			t.Fatalf("base %d: full path wrote into dst", base)
		}
	}
}
