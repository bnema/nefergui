package text

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"reflect"
	"testing"

	"github.com/go-text/typesetting/font"
)

type countingSource struct {
	entries []FontFile
	data    map[string][]byte
	reads   map[string]int
}

func (s *countingSource) Fonts() (map[string][]byte, error) { panic("Load must use metadata index") }
func (s *countingSource) Index() ([]FontFile, error)        { return s.entries, nil }
func (s *countingSource) Open(p string) (io.ReadCloser, error) {
	s.reads[p]++
	return io.NopCloser(bytes.NewReader(s.data[p])), nil
}
func TestLazyFontSelection(t *testing.T) {
	b, err := os.ReadFile("../../testdata/fonts/NotoSans-Regular.ttf")
	if err != nil {
		t.Fatal(err)
	}
	s := &countingSource{data: map[string][]byte{"good": b, "bad": []byte("broken")}, reads: map[string]int{}}
	s.entries = append(s.entries, FontFile{Path: "bad", Family: "Noto Sans", Aspect: font.Aspect{Weight: 400, Stretch: 1}})
	s.entries = append(s.entries, FontFile{Path: "good", Family: "Noto Sans", Aspect: font.Aspect{Weight: 400, Stretch: 1}})
	for i := 0; i < 2000; i++ {
		s.entries = append(s.entries, FontFile{Path: fmt.Sprint(i), Family: fmt.Sprintf("unused%d", i)})
	}
	c, err := Load(s)
	if err != nil || len(s.reads) != 0 {
		t.Fatalf("index read font bytes: %v %v", err, s.reads)
	}
	e := NewEngine(c)
	for i := 0; i < 2; i++ {
		l, err := e.Measure("Hello", Request{Families: []string{"Noto Sans"}, Size: 16}, 0)
		if err != nil || l.Width == 0 {
			t.Fatalf("measure: %v diagnostics %v", err, c.Diagnostics)
		}
		e.EndFrame()
	}
	if len(s.reads) != 2 || s.reads["bad"] != 1 || s.reads["good"] != 1 || len(c.Diagnostics) != 1 {
		t.Fatalf("reads: %v diagnostics: %v", s.reads, c.Diagnostics)
	}
}

func TestUnknownFamilyFallback(t *testing.T) {
	b, err := os.ReadFile("../../testdata/fonts/NotoSans-Regular.ttf")
	if err != nil {
		t.Fatal(err)
	}
	s := &countingSource{entries: []FontFile{{Path: "first", Family: "Unlisted Family", Aspect: font.Aspect{Weight: 400, Stretch: 1}}, {Path: "second", Family: "Other Family", Aspect: font.Aspect{Weight: 400, Stretch: 1}}}, data: map[string][]byte{"first": b, "second": b}, reads: map[string]int{}}
	c, err := Load(s)
	if err != nil {
		t.Fatal(err)
	}
	l, err := NewEngine(c).Measure("Hello", Request{Families: []string{"sans-serif"}, Size: 16}, 0)
	if err != nil || l.Width == 0 || len(s.reads) != 1 || s.reads["first"] != 1 {
		t.Fatalf("fallback %v reads %v", err, s.reads)
	}
}

func TestCollectionDiscovery(t *testing.T) {
	if !isFontPath("font.TTC") || !isFontPath("font.otf") || isFontPath("font.txt") {
		t.Fatal("font extension filter")
	}
	// A collection entry retains its individual face index in the catalog.
	s := &countingSource{entries: []FontFile{{Path: "faces.ttc", Family: "Other Family", Index: 1}}, reads: map[string]int{}}
	c, err := Load(s)
	if err != nil || len(c.Faces) != 1 || c.Faces[0].index != 1 {
		t.Fatalf("collection entry: %v %+v", err, c)
	}
}

func TestOversizedSelectedFont(t *testing.T) {
	b, err := os.ReadFile("../../testdata/fonts/NotoSans-Regular.ttf")
	if err != nil {
		t.Fatal(err)
	}
	s := &countingSource{entries: []FontFile{{Path: "large", Family: "Noto Sans"}, {Path: "good", Family: "Noto Sans"}}, data: map[string][]byte{"large": make([]byte, maxFontBytes+1), "good": b}, reads: map[string]int{}}
	c, err := Load(s)
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine(c)
	for i := 0; i < 2; i++ {
		l, err := e.Measure("Hello", Request{Families: []string{"Noto Sans"}, Size: 16}, 0)
		if err != nil || l.Width == 0 {
			t.Fatalf("measure: %v", err)
		}
		e.EndFrame()
	}
	if s.reads["large"] != 1 || s.reads["good"] != 1 || len(c.Diagnostics) != 1 {
		t.Fatalf("reads %v diagnostics %v", s.reads, c.Diagnostics)
	}
}

func TestIndexedGlyphFallback(t *testing.T) {
	latin, err := os.ReadFile("../../testdata/fonts/NotoSans-Regular.ttf")
	if err != nil {
		t.Fatal(err)
	}
	japanese, err := os.ReadFile("../../testdata/fonts/IBMPlexSansJP-Regular.ttf")
	if err != nil {
		t.Fatal(err)
	}
	s := &countingSource{entries: []FontFile{{Path: "latin", Family: "Noto Sans"}, {Path: "japanese", Family: "Unlisted Japanese", UnicodeRanges: [4]uint32{0, 1 << (59 - 32)}}}, data: map[string][]byte{"latin": latin, "japanese": japanese}, reads: map[string]int{}}
	c, err := Load(s)
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine(c)
	var first *Face
	for i := 0; i < 2; i++ {
		l, err := e.Measure("日本語", Request{Families: []string{"Noto Sans"}, Size: 20}, 0)
		if err != nil {
			t.Fatal(err)
		}
		f := l.Lines[0].Runs[0].Face
		if f.Family != "Unlisted Japanese" || len(l.Lines[0].Runs[0].Glyphs) == 0 || l.Lines[0].Runs[0].Glyphs[0].Missing {
			t.Fatalf("wrong fallback: %+v", l.Lines[0].Runs)
		}
		if first != nil && f != first {
			t.Fatal("selection changed")
		}
		first = f
		e.EndFrame()
	}
	if s.reads["latin"] != 1 || s.reads["japanese"] != 1 {
		t.Fatalf("reads %v", s.reads)
	}
}

func TestIndexedFallbackCap(t *testing.T) {
	b, err := os.ReadFile("../../testdata/fonts/NotoSans-Regular.ttf")
	if err != nil {
		t.Fatal(err)
	}
	s := &countingSource{data: map[string][]byte{}, reads: map[string]int{}}
	for i := 0; i < 2000; i++ {
		p := fmt.Sprint(i)
		s.entries = append(s.entries, FontFile{Path: p, Family: fmt.Sprintf("Other %d", i)})
		s.data[p] = b
	}
	c, err := Load(s)
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine(c)
	// The unknown codepoint has no OS/2 mapping; every face is eligible.
	for i := 0; i < 2; i++ {
		l, err := e.Measure("\U0010ffff", Request{Families: []string{"sans-serif"}, Size: 20}, 0)
		if err != nil {
			t.Fatal(err)
		}
		if !l.Lines[0].Runs[0].Glyphs[0].Missing {
			t.Fatal("expected tofu")
		}
		if len(s.reads) != maxFallbackFaces {
			t.Fatalf("measure %d: %d reads, want %d", i, len(s.reads), maxFallbackFaces)
		}
		e.EndFrame()
	}
}

func TestUnicodeRangeFilter(t *testing.T) {
	f := &Face{unicodeRanges: [4]uint32{1, 0, 0, 0}}
	if !f.mayCover('A') || f.mayCover('日') || !f.mayCover('\U0010ffff') {
		t.Fatal("range filter")
	}
}

func TestPriorityIndependentOfClusterOrder(t *testing.T) {
	latin, err := os.ReadFile("../../testdata/fonts/NotoSans-Regular.ttf")
	if err != nil {
		t.Fatal(err)
	}
	jp, err := os.ReadFile("../../testdata/fonts/IBMPlexSansJP-Regular.ttf")
	if err != nil {
		t.Fatal(err)
	}
	s := &countingSource{entries: []FontFile{{Path: "named", Family: "Noto Sans"}, {Path: "other", Family: "Other"}}, data: map[string][]byte{"named": latin, "other": jp}, reads: map[string]int{}}
	c, err := Load(s)
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine(c)
	r := Request{Families: []string{"Noto Sans"}, Size: 20}
	for i := 0; i < 2; i++ {
		joined, err := e.Measure("日A", r, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(joined.Lines[0].Runs) < 2 {
			t.Fatalf("expected separate runs: %+v", joined.Lines[0].Runs)
		}
		left, err := e.Measure("日", r, 0)
		if err != nil {
			t.Fatal(err)
		}
		right, err := e.Measure("A", r, 0)
		if err != nil {
			t.Fatal(err)
		}
		runs := joined.Lines[0].Runs
		if runs[0].Face != left.Lines[0].Runs[0].Face || runs[len(runs)-1].Face != right.Lines[0].Runs[0].Face || runs[len(runs)-1].Face.Family != "Noto Sans" {
			t.Fatalf("priority changed: %+v", runs)
		}
		e.EndFrame()
	}
}

func TestFallbackWindowAcrossManyBlocks(t *testing.T) {
	b, err := os.ReadFile("../../testdata/fonts/NotoSans-Regular.ttf")
	if err != nil {
		t.Fatal(err)
	}
	s := &countingSource{data: map[string][]byte{}, reads: map[string]int{}}
	for i := 0; i < 2000; i++ {
		p := fmt.Sprint(i)
		s.entries = append(s.entries, FontFile{Path: p, Family: fmt.Sprintf("Other %d", i)})
		s.data[p] = b
	}
	c, err := Load(s)
	if err != nil {
		t.Fatal(err)
	}
	var sample []rune
	for i := 0; i < 50; i++ {
		sample = append(sample, rune(0x10000+i*0x500))
	}
	e := NewEngine(c)
	r := Request{Families: []string{"sans-serif"}, Size: 16}
	first, err := e.Measure(string(sample), r, 0)
	if err != nil {
		t.Fatal(err)
	}
	reads := len(s.reads)
	if reads > maxFallbackFaces || reads == 0 {
		t.Fatalf("first measure loaded %d", reads)
	}
	e.EndFrame()
	second, err := e.Measure(string(sample), r, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.reads) != reads || !reflect.DeepEqual(first, second) {
		t.Fatalf("unstable fallback: %d -> %d", reads, len(s.reads))
	}
}

// A failed first fallback face must not stall primary (metrics) selection.
func TestPrimarySkipsFailedFallbackFace(t *testing.T) {
	b, err := os.ReadFile("../../testdata/fonts/NotoSans-Regular.ttf")
	if err != nil {
		t.Fatal(err)
	}
	s := &countingSource{entries: []FontFile{{Path: "broken", Family: "Unlisted A"}, {Path: "good", Family: "Unlisted B"}}, data: map[string][]byte{"broken": []byte("broken"), "good": b}, reads: map[string]int{}}
	c, err := Load(s)
	if err != nil {
		t.Fatal(err)
	}
	r := Request{Families: []string{"sans-serif"}, Size: 16}
	for i := 0; i < 2; i++ {
		if f := c.primary(r); f == nil || f.path != "good" {
			t.Fatalf("primary %+v", f)
		}
	}
	e := NewEngine(c)
	if l, err := e.Measure("Hello", r, 0); err != nil || l.Width == 0 {
		t.Fatalf("measure: %v", err)
	}
	// Empty text has no missing runes but must still find a face for metrics.
	if l, err := e.Measure("", r, 0); err != nil || l.LineHeight == 0 {
		t.Fatalf("empty measure: %+v %v", l, err)
	}
	if s.reads["broken"] != 1 || s.reads["good"] != 1 {
		t.Fatalf("reads %v", s.reads)
	}
}

// A named face that fails to load must not let a new face into the window.
func TestFallbackWindowIgnoresFailedNamedFaces(t *testing.T) {
	s := &countingSource{data: map[string][]byte{"named": []byte("broken")}, reads: map[string]int{}}
	s.entries = append(s.entries, FontFile{Path: "named", Family: "Noto Sans"})
	for i := 0; i < maxFallbackFaces+4; i++ {
		s.entries = append(s.entries, FontFile{Path: fmt.Sprint(i), Family: fmt.Sprintf("Other %d", i)})
	}
	c, err := Load(s)
	if err != nil {
		t.Fatal(err)
	}
	r := Request{Families: []string{"Noto Sans"}, Size: 16}
	before := c.fallbackWindow([]rune{'A'}, r)
	if c.primary(r) != nil && c.Faces[0].bad {
		t.Fatal("broken named face selected")
	}
	if !c.Faces[0].bad {
		t.Fatal("named face should have failed")
	}
	after := c.fallbackWindow([]rune{'A'}, r)
	if !reflect.DeepEqual(before, after) || len(after) != maxFallbackFaces {
		t.Fatalf("window moved after failure: %d -> %d", len(before), len(after))
	}
	for _, f := range after {
		if f.path == "named" {
			t.Fatal("named face entered the fallback window")
		}
	}
}
