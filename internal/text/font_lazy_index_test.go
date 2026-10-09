package text

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/mock"
)

// lazyFixture is a LazySource over the test fonts, with extra paths listed
// before them that index to nothing. indexed records every path handed to
// IndexFiles.
func lazyFixture(t *testing.T, extra ...string) (*MockLazySource, *[]string) {
	t.Helper()
	dir := "../../testdata/fonts"
	real, err := filepath.Glob(filepath.Join(dir, "*.ttf"))
	if err != nil || len(real) == 0 {
		t.Fatalf("fixture fonts: %v", err)
	}
	paths := append(slices.Clone(extra), real...)
	var indexed []string
	src := NewMockLazySource(t)
	src.EXPECT().Paths().Return(paths, nil)
	src.EXPECT().IndexFiles(mock.Anything).RunAndReturn(func(ps []string) ([]FontFile, error) {
		indexed = append(indexed, ps...)
		var real []string
		for _, p := range ps {
			if !slices.Contains(extra, p) {
				real = append(real, p)
			}
		}
		return SystemSource{}.IndexFiles(real)
	}).Maybe()
	src.EXPECT().Open(mock.Anything).RunAndReturn(func(p string) (io.ReadCloser, error) {
		b, err := os.ReadFile(p)
		return io.NopCloser(bytes.NewReader(b)), err
	}).Maybe()
	return src, &indexed
}

func base(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = filepath.Base(p)
	}
	slices.Sort(out)
	return out
}

func TestLazyIndexReadsOnlyNamedFamilies(t *testing.T) {
	src, indexed := lazyFixture(t, "/fonts/Unrelated-Regular.ttf")
	c, err := Load(src)
	if err != nil {
		t.Fatal(err)
	}
	l, err := NewEngine(c).Measure("Hello", Request{Families: []string{"monospace"}, Size: 16}, 0)
	if err != nil || l.Width == 0 {
		t.Fatalf("measure: %v %v", err, c.Diagnostics)
	}
	// monospace names no fixture family; the sans-serif and script fallbacks do.
	want := []string{"AdwaitaSans-Regular.ttf", "IBMPlexSansJP-Regular.ttf", "NotoSans-Regular.ttf", "NotoSansArabic-Regular.ttf", "NotoSansDevanagari-Regular.ttf", "NotoSansSymbols2-Regular.ttf"}
	if got := base(*indexed); !slices.Equal(got, want) {
		t.Fatalf("indexed %v, want %v", got, want)
	}
	if c.complete {
		t.Fatal("catalog fully indexed for a covered request")
	}
}

func TestLazyIndexScansAllForUnknownFamily(t *testing.T) {
	src, indexed := lazyFixture(t, "/fonts/Unrelated-Regular.ttf")
	c, err := Load(src)
	if err != nil {
		t.Fatal(err)
	}
	// The file name does not reveal the family: only a full scan can find it.
	if _, err := NewEngine(c).Measure("Hello", Request{Families: []string{"Some Family"}, Size: 16}, 0); err != nil {
		t.Fatal(err)
	}
	if !c.complete || !slices.Contains(*indexed, "/fonts/Unrelated-Regular.ttf") {
		t.Fatalf("unknown family did not index every file: %v", *indexed)
	}
}

func TestLazyIndexFallbackGlyph(t *testing.T) {
	src, _ := lazyFixture(t)
	c, err := Load(src)
	if err != nil {
		t.Fatal(err)
	}
	l, err := NewEngine(c).Measure("日本語", Request{Families: []string{"Adwaita Sans"}, Size: 20}, 0)
	if err != nil {
		t.Fatal(err)
	}
	run := l.Lines[0].Runs[0]
	if run.Face.Family != "IBM Plex Sans JP" || run.Glyphs[0].Missing {
		t.Fatalf("fallback face %q", run.Face.Family)
	}
}

// Indexing in steps keeps the catalog order of a full index.
func TestLazyIndexKeepsPathOrder(t *testing.T) {
	src, _ := lazyFixture(t)
	c, err := Load(src)
	if err != nil {
		t.Fatal(err)
	}
	c.indexAll()
	paths, _ := src.Paths()
	full, err := SystemSource{}.IndexFiles(paths)
	if err != nil {
		t.Fatal(err)
	}
	if len(full) != len(c.Faces) {
		t.Fatalf("%d faces, want %d", len(c.Faces), len(full))
	}
	for i, f := range c.Faces {
		if f.path != full[i].Path || f.index != full[i].Index {
			t.Fatalf("face %d is %s/%d, want %s/%d", i, f.path, f.index, full[i].Path, full[i].Index)
		}
	}
}

func TestFileStem(t *testing.T) {
	for path, want := range map[string]string{
		"/f/NotoSans-Bold.ttf":       "notosans",
		"/f/NotoSans[wght].ttf":      "notosans",
		"/f/DejaVuSansMono.ttf":      "dejavusansmono",
		"/f/IBMPlexMono_Regular.otf": "ibmplexmono",
	} {
		if got := fileStem(path); got != want {
			t.Errorf("%s: %q, want %q", path, got, want)
		}
	}
}
