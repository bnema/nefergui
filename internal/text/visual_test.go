package text

import (
	"bytes"
	"image"
	"math"
	"reflect"
	"testing"

	"github.com/bnema/nefergui/internal/bidi"
	"github.com/go-text/typesetting/di"
	"github.com/go-text/typesetting/font"
)

func runTexts(t *testing.T, s string, l Line) []string {
	t.Helper()
	source := []rune(s)
	out := make([]string, 0, len(l.Runs))
	lastX := -1.0
	for _, r := range l.Runs {
		if r.Start < 0 || r.End > len(source) || r.Start >= r.End {
			t.Fatalf("invalid range %d:%d", r.Start, r.End)
		}
		if r.X <= lastX {
			t.Fatalf("x not increasing at %q: %v <= %v", string(source[r.Start:r.End]), r.X, lastX)
		}
		if math.Abs(r.X+r.Advance-l.Width) > 0.02 && r.X+r.Advance > l.Width+0.02 {
			t.Fatalf("run beyond line: %+v", r)
		}
		lastX = r.X
		out = append(out, string(source[r.Start:r.End]))
	}
	return out
}
func TestVisualOrder(t *testing.T) {
	c := fixture(t)
	e := NewEngine(c)
	cases := []struct {
		name, text string
		base       bidi.BaseDirection
		direction  di.Direction
		want       []string
	}{
		{"ltr", "abc سلام 123 عالم def", bidi.LTR, di.DirectionLTR, []string{"abc ", "عالم", " ", "123", " ", "سلام", " def"}},
		{"rtl", "سلام abc def عالم", bidi.RTL, di.DirectionRTL, []string{"عالم", " ", "abc def", " ", "سلام"}},
		{"numbers", "سعر 123 دولار", bidi.Auto, di.DirectionRTL, []string{"دولار", " ", "123", " ", "سعر"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l, err := e.Measure(tc.text, Request{Families: []string{"Noto Sans"}, Size: 20, Direction: tc.base}, 0)
			if err != nil {
				t.Fatal(err)
			}
			if l.Direction != tc.direction || len(l.Lines) != 1 {
				t.Fatalf("direction %v lines %d", l.Direction, len(l.Lines))
			}
			got := runTexts(t, tc.text, l.Lines[0])
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("visual order %q; want %q", got, tc.want)
			}
			for _, r := range l.Lines[0].Runs {
				if r.Direction != di.Direction(r.Level&1) {
					t.Fatalf("run %q direction %v level %d", string([]rune(tc.text)[r.Start:r.End]), r.Direction, r.Level)
				}
			}
		})
	}
}
func TestWrapVisualPerLine(t *testing.T) {
	c := fixture(t)
	s := "abc سلام 123 عالم def abc سلام 123 عالم def"
	l, err := NewEngine(c).Measure(s, Request{Families: []string{"Noto Sans"}, Size: 20, Direction: bidi.LTR}, 145)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"abc ", "123", " ", "سلام", " "}, {"عالم", " def abc "}, {"عالم", " ", "123", " ", "سلام", " "}, {"def"}}
	if len(l.Lines) != len(want) {
		t.Fatalf("got %d lines; want %d", len(l.Lines), len(want))
	}
	for i, line := range l.Lines {
		got := runTexts(t, s, line)
		if !reflect.DeepEqual(got, want[i]) {
			t.Errorf("line %d got %q want %q", i, got, want[i])
		}
	}
}

// A shadow renderer applies only the published tightly-packed uploads. An
// eviction after an undrained dirty rect must replay the *current* whole page,
// including zeros, not stale glyph pixels.
func TestAtlasUploadReplay(t *testing.T) {
	a, err := NewAtlas(16, 1)
	if err != nil {
		t.Fatal(err)
	}
	shadow := make([]byte, 16*16)
	replay := func() {
		for _, u := range a.Uploads() {
			if u.Page != 0 || len(u.Bytes) != u.Rect.Dx()*u.Rect.Dy() {
				t.Fatalf("invalid upload %+v", u)
			}
			w := u.Rect.Dx()
			for y := 0; y < u.Rect.Dy(); y++ {
				copy(shadow[(u.Rect.Min.Y+y)*16+u.Rect.Min.X:], u.Bytes[y*w:(y+1)*w])
			}
		}
		if !bytes.Equal(shadow, a.pages[0].pixels) {
			t.Fatal("shadow differs from atlas pixels")
		}
	}
	m := Mask{Alpha: image.NewAlpha(image.Rect(0, 0, 5, 5))}
	for i := range m.Alpha.Pix {
		m.Alpha.Pix[i] = 0x75
	}
	if _, err := a.Insert(Key{Glyph: 1}, m); err != nil {
		t.Fatal(err)
	}
	replay()
	for i := 2; i <= 4; i++ {
		if _, err := a.Insert(Key{Glyph: font.GID(i)}, m); err != nil {
			t.Fatal(err)
		}
	} // pending partial rects
	a.BeginFrame()
	for i := 5; i <= 8; i++ {
		if _, err := a.Insert(Key{Glyph: font.GID(i)}, m); err != nil {
			t.Fatal(err)
		}
	} // one page eviction + clear, not yet drained
	if len(a.dirty) != 1 {
		t.Fatalf("pending pages %d", len(a.dirty))
	}
	replay()
	a.BeginFrame()
	m.Alpha.Pix[0] = 0xff
	if _, err := a.Insert(Key{Glyph: 9}, m); err != nil {
		t.Fatal(err)
	}
	replay()
}
