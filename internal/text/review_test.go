package text

import (
	"image"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"
)

func TestVariableDefaultAndDistinctKeys(t *testing.T) {
	c := fixture(t)
	f := c.primary(Request{Families: []string{"Adwaita Sans"}})
	if !f.Variable {
		t.Fatal("fvar missing")
	}
	glyph, _ := f.Shape.NominalGlyph('H')
	def := f.WithVariations(nil)
	if len(def.Shape.Coords()) != 0 {
		t.Fatal("expected implicit default coordinates")
	}
	explicit := f.WithVariations([]font.Variation{{Tag: ot.MustNewTag("wght"), Value: 400}})
	heavy := f.WithVariations([]font.Variation{{Tag: ot.MustNewTag("wght"), Value: 700}})
	keys := map[[32]byte]bool{}
	for _, face := range []*Face{def, explicit, heavy} {
		k, err := GlyphKey(face, glyph, 32, 0, face.Variations)
		if err != nil {
			t.Fatal(err)
		}
		m, err := Rasterize(face, k)
		if err != nil || m.Alpha.Rect.Empty() {
			t.Fatalf("coords %v raster: %v", face.Shape.Coords(), err)
		}
		keys[k.Variations] = true
		if _, err := Rasterize(face, Key{FaceID: face.ID, Glyph: glyph, Size: k.Size, Variations: VariationHash([]font.Variation{{Tag: ot.MustNewTag("wght"), Value: 300}})}); err == nil {
			t.Error("accepted wrong key")
		}
	}
	if len(keys) != 3 {
		t.Fatalf("variation keys collapsed: %d", len(keys))
	}
}

func TestClusterFallback(t *testing.T) {
	c := fixture(t)
	choices := c.candidates(Request{Families: []string{"Noto Sans"}})
	cases := []string{"a\u1ab0", "क\u093f", "س\u064e", "♥\ufe0f", "👩\u200d💻"}
	for _, s := range cases {
		t.Run(s, func(t *testing.T) {
			r := []rune(s)
			faces := clusterFaces(r, choices)
			if len(faces) != len(r) {
				t.Fatalf("%d faces", len(faces))
			}
			for i := 1; i < len(faces); i++ {
				if faces[i] != faces[0] {
					t.Fatalf("cluster split at %d", i)
				}
			}
			l, err := NewEngine(c).Measure(s, Request{Families: []string{"Noto Sans"}, Size: 20}, 0)
			if err != nil || len(l.Lines) == 0 {
				t.Fatalf("measure: %v", err)
			}
		})
	}
}

func TestSourceDiagnostics(t *testing.T) {
	b, err := os.ReadFile("../../testdata/fonts/NotoSans-Regular.ttf")
	if err != nil {
		t.Fatal(err)
	}
	c, err := Load(sourceMap{"a.ttf": []byte("corrupt"), "b.ttf": b})
	if err != nil || len(c.Faces) != 1 || len(c.Diagnostics) != 1 {
		t.Fatalf("partial load: %v %+v", err, c)
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permissions")
	}
	root := t.TempDir()
	good, bad := filepath.Join(root, "good"), filepath.Join(root, "bad")
	if err := os.Mkdir(good, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(bad, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(good, "NotoSans-Regular.ttf"), b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(bad, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(bad, 0700) })
	// A failing directory still returns a diagnostic along with the readable files.
	files, e := DirectorySource(bad).Fonts()
	if e == nil || len(files) != 0 {
		t.Fatalf("unreadable dir: %v", e)
	}
	all, e := DirectorySource(root).Fonts()
	if e == nil {
		t.Fatal("missing diagnostic")
	}
	loaded, err := Load(sourceMapWithError{all, e})
	if err != nil || len(loaded.Faces) != 1 || len(loaded.Diagnostics) != 1 {
		t.Fatalf("partial scan: %v %+v", err, loaded)
	}
}

type sourceMapWithError struct {
	files map[string][]byte
	err   error
}

func (s sourceMapWithError) Fonts() (map[string][]byte, error) { return s.files, s.err }

func TestAtlasUndrainedBound(t *testing.T) {
	a, _ := NewAtlas(32, 2)
	m := Mask{Alpha: image.NewAlpha(image.Rect(0, 0, 11, 11))}
	m.Alpha.Pix[0] = 255
	for i := 0; i < 120; i++ {
		a.BeginFrame()
		for j := 0; j < 8; j++ {
			if _, err := a.Insert(Key{Glyph: font.GID(8*i + j + 1)}, m); err != nil {
				t.Fatal(err)
			}
		}
		if len(a.dirty) > a.MaxPages {
			t.Fatal("unbounded pending pages")
		}
	}
	up := a.Uploads()
	if len(up) > 2 {
		t.Fatalf("pending records %d", len(up))
	}
	total := 0
	for _, u := range up {
		total += len(u.Bytes)
		if len(u.Bytes) != u.Rect.Dx()*u.Rect.Dy() {
			t.Fatal("invalid upload stride")
		}
	}
	if total > 2*32*32 {
		t.Fatalf("pending bytes %d", total)
	}
	if len(a.Uploads()) != 0 {
		t.Fatal("not drained")
	}
}

func TestArabicJoiningAndBidi(t *testing.T) {
	c := fixture(t)
	e := NewEngine(c)
	r := Request{Families: []string{"Noto Sans Arabic"}, Size: 20}
	joined, err := e.Measure("سلام", r, 0)
	if err != nil {
		t.Fatal(err)
	}
	alone := map[font.GID]bool{}
	for _, letter := range "سلام" {
		l, err := e.Measure(string(letter), r, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, run := range l.Lines[0].Runs {
			for _, g := range run.Glyphs {
				alone[g.ID] = true
			}
		}
	}
	changed := false
	for _, run := range joined.Lines[0].Runs {
		for _, g := range run.Glyphs {
			if !alone[g.ID] {
				changed = true
			}
		}
	}
	if !changed {
		t.Fatal("no joined glyph differs from isolated letters")
	}
}

func TestDevanagariReordering(t *testing.T) {
	c := fixture(t)
	f := c.primary(Request{Families: []string{"Noto Sans Devanagari"}})
	ka, _ := f.Shape.NominalGlyph('क')
	// OpenType Indic substitution creates a pre-base matra form not found in cmap.
	matra, _ := f.Shape.NominalGlyph('ि')
	l, err := NewEngine(c).Measure("कि", Request{Families: []string{"Noto Sans Devanagari"}, Size: 24}, 0)
	if err != nil {
		t.Fatal(err)
	}
	var ids []font.GID
	for _, r := range l.Lines[0].Runs {
		for _, g := range r.Glyphs {
			ids = append(ids, g.ID)
		}
	}
	if len(ids) != 2 || ids[0] == ka || ids[1] != ka || ids[0] == 0 || ids[0] == matra {
		t.Fatalf("expected substituted pre-base i-matra before ka: got %v, nominal matra %d ka %d", ids, matra, ka)
	}
}
func BenchmarkAtlasWarmLookup(b *testing.B) {
	a, _ := NewAtlas(64, 1)
	k := Key{Glyph: 23}
	if _, err := a.Insert(k, Mask{Alpha: image.NewAlpha(image.Rect(0, 0, 8, 8))}); err != nil {
		b.Fatal(err)
	}
	a.Uploads()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, ok := a.Lookup(k); !ok {
			b.Fatal("cache miss")
		}
	}
}
func BenchmarkRasterDistinct1000(b *testing.B) {
	c := fixture(b)
	f := c.primary(Request{Families: []string{"Noto Sans"}})
	var keys []Key
	for r := rune(33); r < rune(5000) && len(keys) < 1000; r++ {
		gid, ok := f.Shape.NominalGlyph(r)
		if !ok || gid == 0 {
			continue
		}
		k, _ := GlyphKey(f, gid, 16, 0, nil)
		keys = append(keys, k)
	}
	if len(keys) < 1000 {
		b.Fatalf("only %d distinct glyphs", len(keys))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, k := range keys {
			if _, err := Rasterize(f, k); err != nil {
				b.Fatal(err)
			}
		}
	}
}
