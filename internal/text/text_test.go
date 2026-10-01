package text

import (
	"crypto/sha256"
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/go-text/typesetting/di"
	"github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"
)

func fixture(t testing.TB) *Catalog {
	t.Helper()
	c, e := Load(DirectorySource("../../testdata/fonts"))
	if e != nil {
		t.Fatal(e)
	}
	return c
}
func TestScriptsAndMetrics(t *testing.T) {
	c := fixture(t)
	e := NewEngine(c)
	r := Request{Families: []string{"Noto Sans"}, Size: 20}
	cases := []struct {
		label, text, family string
		rtl                 bool
	}{
		{"Latin", "office fi", "Noto Sans", false}, {"Arabic", "سلام", "Noto Sans Arabic", true}, {"Devanagari", "किरण", "Noto Sans Devanagari", false}, {"CJK", "日本語", "IBM Plex Sans JP", false}, {"combining", "a\u0301", "Noto Sans", false}, {"bidi", "abc سلام 123", "", true}, {"tofu", "\U0010ffff", "Noto Sans", false},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			l, err := e.Measure(tc.text, r, 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(l.Lines) == 0 || l.Width <= 0 || l.Height <= 0 {
				t.Fatalf("bad metrics: %+v", l)
			}
			found, rtl, missing := false, false, false
			for _, line := range l.Lines {
				for _, run := range line.Runs {
					if run.Face == nil {
						t.Fatal("nil face")
					}
					if tc.family == run.Face.Family {
						found = true
					}
					if run.Direction == di.DirectionRTL {
						rtl = true
					}
					sum := 0.0
					for _, g := range run.Glyphs {
						sum += g.Advance
						missing = missing || g.Missing
					}
					if math.Abs(sum-run.Advance) > 0.02 {
						t.Fatalf("advance mismatch %f %f", sum, run.Advance)
					}
				}
			}
			if tc.family != "" && !found {
				t.Errorf("missing fallback %s; got %+v", tc.family, l.Lines[0].Runs)
			}
			if tc.rtl && !rtl {
				t.Error("missing RTL run")
			}
			if tc.label == "tofu" && !missing {
				t.Error("missing tofu flag")
			}
			again, err := e.Measure(tc.text, r, 0)
			if err != nil || !reflect.DeepEqual(l, again) {
				t.Error("nondeterministic output")
			}
		})
	}
	a, _ := e.Measure("office fi", r, 0)
	glyphs := 0
	for _, run := range a.Lines[0].Runs {
		glyphs += len(run.Glyphs)
	}
	if glyphs >= len([]rune("office fi")) {
		t.Error("expected fi ligature")
	}
	wide, _ := e.Measure("one two three four five", r, 220)
	narrow, _ := e.Measure("one two three four five", r, 60)
	if narrow.Height < wide.Height || len(narrow.Lines) <= len(wide.Lines) {
		t.Errorf("wrap monotonicity: %+v %+v", narrow, wide)
	}
}

func TestCorruptAndMatching(t *testing.T) {
	if _, err := Load(sourceMap{"broken.ttf": []byte("not a font")}); err == nil {
		t.Fatal("accepted corrupt font")
	}
	c := fixture(t)
	if f := c.primary(Request{Families: []string{"missing", "sans-serif"}}); f.Family != "Noto Sans" {
		t.Fatalf("generic mismatch %s", f.Family)
	}
	if f := c.primary(Request{Families: []string{"IBM Plex Sans JP"}}); f.Family != "IBM Plex Sans JP" {
		t.Fatalf("family mismatch %s", f.Family)
	}
	if _, err := Load(sourceMap{}); err == nil {
		t.Fatal("accepted empty catalog")
	}
}

type sourceMap map[string][]byte

func (s sourceMap) Fonts() (map[string][]byte, error) { return s, nil }
func TestRasterCoverage(t *testing.T) {
	c := fixture(t)
	f := c.primary(Request{Families: []string{"Noto Sans"}})
	gid, ok := f.Shape.NominalGlyph('H')
	if !ok {
		t.Fatal("no H")
	}
	key, err := GlyphKey(f, gid, 32, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	mask, err := Rasterize(f, key)
	if err != nil {
		t.Fatal(err)
	}
	if mask.Alpha.Rect.Dx() == 0 {
		t.Fatal("empty mask")
	}
	coverage := 0.0
	for _, v := range mask.Alpha.Pix {
		coverage += float64(v) / 255
	}
	// Independent outline area oracle: flatten Beziers, sum signed contour areas.
	// Uses go-text font-unit outlines; rasterizer uses sfnt 26.6 outlines.
	outline, ok := f.Shape.GlyphData(gid).(font.GlyphOutline)
	if !ok {
		t.Fatal("outline")
	}
	area := 0.0
	var start, prev [2]float64
	contour := [][2]float64{}
	flush := func() {
		if len(contour) < 2 {
			return
		}
		for i, p := range contour {
			q := contour[(i+1)%len(contour)]
			area += p[0]*q[1] - q[0]*p[1]
		}
		contour = nil
	}
	for _, s := range outline.Segments {
		p := s.ArgsSlice()
		conv := func(pt ot.SegmentPoint) [2]float64 {
			return [2]float64{float64(pt.X) * 32 / float64(f.Shape.Upem()), float64(pt.Y) * 32 / float64(f.Shape.Upem())}
		}
		switch s.Op {
		case ot.SegmentOpMoveTo:
			flush()
			start = conv(p[0])
			prev = start
			contour = append(contour, start)
		case ot.SegmentOpLineTo:
			prev = conv(p[0])
			contour = append(contour, prev)
		case ot.SegmentOpQuadTo:
			ctrl, end := conv(p[0]), conv(p[1])
			from := prev
			for i := 1; i <= 24; i++ {
				v := float64(i) / 24
				point := [2]float64{(1-v)*(1-v)*from[0] + 2*(1-v)*v*ctrl[0] + v*v*end[0], (1-v)*(1-v)*from[1] + 2*(1-v)*v*ctrl[1] + v*v*end[1]}
				contour = append(contour, point)
			}
			prev = end
		case ot.SegmentOpCubeTo:
			a, b, end := conv(p[0]), conv(p[1]), conv(p[2])
			from := prev
			for i := 1; i <= 24; i++ {
				v := float64(i) / 24
				u := 1 - v
				contour = append(contour, [2]float64{u*u*u*from[0] + 3*u*u*v*a[0] + 3*u*v*v*b[0] + v*v*v*end[0], u*u*u*from[1] + 3*u*u*v*a[1] + 3*u*v*v*b[1] + v*v*v*end[1]})
			}
			prev = end
		}
		_ = start
	}
	flush()
	area = math.Abs(area) / 2
	if math.Abs(coverage-area) > area*0.14+2 {
		t.Fatalf("coverage %.2f outline area %.2f", coverage, area)
	}
	for _, phase := range []float64{0, 0.26, 0.51, 0.76} {
		k, _ := GlyphKey(f, gid, 32, phase, nil)
		m, err := Rasterize(f, k)
		if err != nil || m.Alpha.Rect.Dx() == 0 {
			t.Fatalf("phase %v: %v", phase, err)
		}
	}
	if _, err := Rasterize(f, Key{}); err == nil {
		t.Fatal("invalid key accepted")
	}
}
func TestAtlas(t *testing.T) {
	a, _ := NewAtlas(32, 2)
	m := Mask{Alpha: image.NewAlpha(image.Rect(0, 0, 11, 11))}
	seen := map[int][]image.Rectangle{}
	for i := 0; i < 8; i++ {
		p, err := a.Insert(Key{FaceID: string(rune('a' + i))}, m)
		if err != nil {
			t.Fatal(err)
		}
		for _, rect := range seen[p.Page] {
			if p.Rect.Overlaps(rect) {
				t.Fatalf("overlap %v %v", p.Rect, rect)
			}
		}
		seen[p.Page] = append(seen[p.Page], p.Rect)
	}
	if _, err := a.Insert(Key{FaceID: "overflow"}, m); err == nil {
		t.Error("evicted in active frame")
	}
	if up := a.Uploads(); len(up) != 2 {
		t.Fatalf("coalesced uploads %d", len(up))
	}
	a.BeginFrame()
	p, err := a.Insert(Key{FaceID: "new"}, m)
	if err != nil || p.Page < 0 {
		t.Fatalf("eviction: %v", err)
	}
	up := a.Uploads()
	if len(up) != 1 || up[0].Rect != image.Rect(0, 0, 32, 32) {
		t.Fatalf("missing coalesced clear: %+v", up)
	}
	if len(up[0].Bytes) > 32*32 {
		t.Fatal("upload exceeded page size")
	}
}
func TestVariableFixture(t *testing.T) {
	c := fixture(t)
	f := c.primary(Request{Families: []string{"Adwaita Sans"}})
	r := Request{Families: []string{"Adwaita Sans"}, Size: 20, Variations: []font.Variation{{Tag: ot.MustNewTag("wght"), Value: 700}}}
	l, err := NewEngine(c).Measure("Hello", r, 0)
	if err != nil || len(l.Lines) == 0 {
		t.Fatalf("variable shaping: %v", err)
	}
	if len(l.Lines[0].Runs) == 0 || len(l.Lines[0].Runs[0].Face.Shape.Coords()) == 0 {
		t.Fatal("variation axis not active")
	}
	glyph := l.Lines[0].Runs[0].Glyphs[0]
	variable := l.Lines[0].Runs[0].Face
	k, _ := GlyphKey(variable, glyph.ID, 20, 0, r.Variations)
	if k.Variations == VariationHash(nil) {
		t.Fatal("lost variation identity")
	}
	m, err := Rasterize(variable, k)
	if err != nil || m.Alpha.Rect.Dx() == 0 {
		t.Fatalf("variable raster: %v", err)
	}
	if _, err := Rasterize(f, k); err == nil {
		t.Fatal("default face accepted non-default key")
	}
}

// ShapeParagraph measures uncached shaping; ShapeParagraphCached measures a
// Measure call that hits the per-frame cache.
func BenchmarkShapeParagraph(b *testing.B) {
	c := fixture(b)
	e := NewEngine(c)
	r := Request{Families: []string{"Noto Sans"}, Size: 16}
	s := strings.Repeat("Hello سلام कक्षा 日本語! ", 8)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.measure(s, r, 240); err != nil {
			b.Fatal(err)
		}
	}
}
func BenchmarkShapeParagraphCached(b *testing.B) {
	e := NewEngine(fixture(b))
	r := Request{Families: []string{"Noto Sans"}, Size: 16}
	s := strings.Repeat("Hello سلام कक्षा 日本語! ", 8)
	if _, err := e.Measure(s, r, 240); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := e.Measure(s, r, 240); err != nil {
			b.Fatal(err)
		}
	}
}
func BenchmarkAtlasInsert(b *testing.B) {
	a, _ := NewAtlas(1024, 4)
	m := Mask{Alpha: image.NewAlpha(image.Rect(0, 0, 12, 16))}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.BeginFrame()
		k := Key{Glyph: font.GID(i)}
		if _, err := a.Insert(k, m); err != nil {
			b.Fatal(err)
		}
		a.Uploads()
	}
}
func TestFixtureHashes(t *testing.T) {
	expected := map[string]string{"NotoSans-Regular.ttf": "478c558ea716033cd60c03438f628dfa75694dcf6b5f6d505a2f05fd2b4f3823", "NotoSansArabic-Regular.ttf": "bdff3e5659d67e67def05b33f749683b9376ae819d65d3dd62ac4640b3aaef48", "NotoSansDevanagari-Regular.ttf": "306b53ecfb182a504dd8a7446093c316387d2fd8dc350d0792ed1753fe0996cd", "NotoSansSymbols2-Regular.ttf": "c4a0a80f0041ce4be81e2478faad22776d23edb98ae3f0d19bd37044820ecf9d", "IBMPlexSansJP-Regular.ttf": "25d96fe620f12fba6cf09158807117ec13d1e7c9debff488117cf21dc8844688", "AdwaitaSans-Regular.ttf": "8381c33b9a44f066f2b99dba3d416a2342891e28c956a35dfd8d16ee2987e6d4"}
	for name, hash := range expected {
		b, err := os.ReadFile(filepath.Join("../../testdata/fonts", name))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(b)
		if fmt.Sprintf("%x", sum) != hash {
			t.Errorf("fixture %s hash mismatch", name)
		}
	}
}

func TestLetterSpacingWrap(t *testing.T) {
	e := NewEngine(fixture(t))
	r := Request{Families: []string{"Noto Sans"}, Size: 16}
	plain, err := e.Measure("hello world", r, 1000)
	if err != nil {
		t.Fatal(err)
	}
	r.LetterSpacing = 4
	spaced, err := e.Measure("hello world", r, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if spaced.Width <= plain.Width+10 {
		t.Fatalf("spacing width: %v -> %v", plain.Width, spaced.Width)
	}
	width := (plain.Width + spaced.Width) / 2
	r.LetterSpacing = 0
	unwrapped, err := e.Measure("hello world", r, width)
	if err != nil {
		t.Fatal(err)
	}
	r.LetterSpacing = 4
	wrapped, err := e.Measure("hello world", r, width)
	if err != nil {
		t.Fatal(err)
	}
	if len(unwrapped.Lines) != 1 || len(wrapped.Lines) < 2 {
		t.Fatalf("spacing wrap: %d -> %d at %v", len(unwrapped.Lines), len(wrapped.Lines), width)
	}
	r.LetterSpacing = math.NaN()
	if _, err = e.Measure("x", r, width); err == nil {
		t.Fatal("accepted NaN spacing")
	}
}

func TestLetterSpacingClusterGeometry(t *testing.T) {
	e := NewEngine(fixture(t))
	r := Request{Families: []string{"Noto Sans"}, Size: 20}
	for _, tc := range []struct {
		name, input   string
		first, second int
	}{{"rtl", "سلام", 0, 1}, {"combining", "a\u0301b", 0, 2}, {"ligature", "fib", 0, 2}} {
		t.Run(tc.name, func(t *testing.T) {
			r.LetterSpacing = 0
			plain, err := e.Measure(tc.input, r, 0)
			if err != nil {
				t.Fatal(err)
			}
			r.LetterSpacing = 6
			spaced, err := e.Measure(tc.input, r, 0)
			if err != nil {
				t.Fatal(err)
			}
			if len(plain.Lines) != 1 || len(spaced.Lines) != 1 {
				t.Fatalf("lines: %d %d", len(plain.Lines), len(spaced.Lines))
			}
			if tc.name == "rtl" && math.Abs(spaced.Width-plain.Width-6*float64(len([]rune(tc.input))-1)) > 0.1 {
				t.Fatalf("RTL spacing width: %v -> %v", plain.Width, spaced.Width)
			}
			// Compare visually adjacent cluster origins rather than rune order: RTL is
			// painted in visual order. Glyphs in the same cluster must not gain space.
			clusters := func(l Layout) map[int][]Glyph {
				m := map[int][]Glyph{}
				for _, run := range l.Lines[0].Runs {
					for _, g := range run.Glyphs {
						m[g.Cluster] = append(m[g.Cluster], g)
					}
				}
				return m
			}
			a, b := clusters(plain), clusters(spaced)
			if len(b) <= 1 || len(a) != len(b) || spaced.Width <= plain.Width+2 {
				t.Fatalf("clusters/width: %v -> %v", a, b)
			}
			if tc.name == "combining" || tc.name == "ligature" {
				if _, ok := b[tc.second]; !ok {
					t.Fatalf("missing next cluster %d: %v", tc.second, b)
				}
				if _, ok := b[tc.first]; !ok {
					t.Fatalf("missing first cluster %d: %v", tc.first, b)
				}
				if math.Abs((b[tc.second][0].X-b[tc.first][0].X)-(a[tc.second][0].X-a[tc.first][0].X)-6) > 0.1 {
					t.Fatalf("spacing not between clusters: %v -> %v", a, b)
				}
			}
			if tc.name == "ligature" && len(b[0]) != 1 {
				t.Fatalf("ligature split by spacing: %v", b[0])
			}
			if tc.name == "rtl" {
				moved := false
				for cluster, old := range a {
					if math.Abs(b[cluster][0].X-old[0].X) > 1 {
						moved = true
					}
				}
				if !moved {
					t.Fatalf("RTL glyph positions unchanged: %v -> %v", a, b)
				}
			}
			for _, run := range spaced.Lines[0].Runs {
				sum := 0.0
				for _, g := range run.Glyphs {
					sum += g.Advance
				}
				if math.Abs(sum-run.Advance) > .02 {
					t.Fatalf("advance mismatch %v %v", sum, run.Advance)
				}
			}
		})
	}
}

// BenchmarkMeasureMissShort measures an uncached short label, the common
// per-frame miss when a counter or clock changes.
func BenchmarkMeasureMissShort(b *testing.B) {
	e := NewEngine(fixture(b))
	r := Request{Families: []string{"Noto Sans"}, Size: 16}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := e.measure("Value: 42", r, 0); err != nil {
			b.Fatal(err)
		}
	}
}

// allocBaselineMeasureMiss is the measured allocation count of an uncached
// short label; most of the remainder is inside go-text segmentation and
// wrapping. Lower it with each optimization.
const allocBaselineMeasureMiss = 20

func TestAllocMeasureMissShort(t *testing.T) {
	if raceEnabled {
		t.Skip("race instrumentation changes allocation counts")
	}
	e := NewEngine(fixture(t))
	r := Request{Families: []string{"Noto Sans"}, Size: 16}
	measure := func() {
		if _, err := e.measure("Value: 42", r, 0); err != nil {
			t.Fatal(err)
		}
	}
	measure() // load fonts and fill scratch buffers
	if got := testing.AllocsPerRun(50, measure); got > allocBaselineMeasureMiss {
		t.Fatalf("allocs per uncached measure = %v, baseline %v", got, allocBaselineMeasureMiss)
	}
}
