package text

import (
	"math"
	"testing"

	"github.com/go-text/typesetting/di"
)

func TestParagraphSeparators(t *testing.T) {
	c := fixture(t)
	e := NewEngine(c)
	r := Request{Families: []string{"Noto Sans"}, Size: 20}
	cases := []struct {
		name, input string
		paragraphs  int
		starts      []int
		empty       []bool
		dirs        []di.Direction
	}{
		{"newline", "a\nb", 2, []int{0, 2}, []bool{false, false}, []di.Direction{di.DirectionLTR, di.DirectionLTR}},
		{"rtl auto", "abc\nسلام", 2, []int{0, 4}, []bool{false, false}, []di.Direction{di.DirectionLTR, di.DirectionRTL}},
		{"two empty", "\n\n", 3, nil, []bool{true, true, true}, []di.Direction{di.DirectionLTR, di.DirectionLTR, di.DirectionLTR}},
		{"trailing newline", "a\n", 2, []int{0}, []bool{false, true}, []di.Direction{di.DirectionLTR, di.DirectionLTR}},
		{"CRLF", "a\r\nسلام", 2, []int{0, 3}, []bool{false, false}, []di.Direction{di.DirectionLTR, di.DirectionRTL}},
		{"separators", "a\rb\u001cc\u001dd\u001ee\u0085f\u2029g", 7, []int{0, 2, 4, 6, 8, 10, 12}, []bool{false, false, false, false, false, false, false}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l, err := e.Measure(tc.input, r, 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(l.Lines) != tc.paragraphs {
				t.Fatalf("lines=%d want %d", len(l.Lines), tc.paragraphs)
			}
			if math.Abs(l.Height-float64(len(l.Lines))*l.LineHeight) > 0.01 {
				t.Errorf("height=%v lineHeight=%v", l.Height, l.LineHeight)
			}
			current := 0
			for i, line := range l.Lines {
				if i > 0 && line.Baseline <= l.Lines[i-1].Baseline {
					t.Errorf("baseline not stacked")
				}
				if len(tc.dirs) > i && line.Direction != tc.dirs[i] {
					t.Errorf("line %d direction %v want %v", i, line.Direction, tc.dirs[i])
				}
				if tc.empty[i] != (len(line.Runs) == 0) {
					t.Errorf("line %d empty=%v", i, len(line.Runs) == 0)
				}
				if len(line.Runs) > 0 {
					run := line.Runs[0]
					if run.Start != tc.starts[current] {
						t.Errorf("line %d start=%d want=%d", i, run.Start, tc.starts[current])
					}
					for _, rr := range line.Runs {
						if rr.Start < 0 || rr.End > len([]rune(tc.input)) {
							t.Errorf("global rune range %d:%d", rr.Start, rr.End)
						}
						for _, glyph := range rr.Glyphs {
							if glyph.Cluster < rr.Start || glyph.Cluster >= rr.End {
								t.Errorf("glyph cluster %d not global in %d:%d", glyph.Cluster, rr.Start, rr.End)
							}
						}
					}
					current++
				}
			}
		})
	}
}

// Offsets are rune indices into the original input, across multibyte text and
// a CRLF separator, and every cluster of the later paragraph is covered.
func TestParagraphClusterOffsets(t *testing.T) {
	e := NewEngine(fixture(t))
	in := "é日本\r\nxyz"
	l, err := e.Measure(in, Request{Families: []string{"Noto Sans"}, Size: 20}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Lines) != 2 {
		t.Fatalf("lines = %d, want 2", len(l.Lines))
	}
	runes := []rune(in)
	seen := map[int]bool{}
	for _, run := range l.Lines[1].Runs {
		if run.Start < 5 || run.End > len(runes) {
			t.Fatalf("run [%d,%d) outside second paragraph [5,%d)", run.Start, run.End, len(runes))
		}
		for _, g := range run.Glyphs {
			seen[g.Cluster] = true
		}
	}
	for i, want := range []rune("xyz") {
		if !seen[5+i] || runes[5+i] != want {
			t.Fatalf("cluster %d (%q) missing: %v", 5+i, want, seen)
		}
	}
}
