package text

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/mock"
)

const fixtureDir = "../../testdata/fonts"

// lazySource is a LazySource over paths. Each path indexes and opens as the
// fixture file named by files (a missing entry indexes to nothing), so a test
// controls file names independently of font families. indexed records every
// path handed to IndexFiles.
func lazySource(t *testing.T, paths []string, files map[string]string) (*MockLazySource, *[]string) {
	t.Helper()
	var indexed []string
	src := NewMockLazySource(t)
	src.EXPECT().Paths().Return(paths, nil)
	src.EXPECT().IndexFiles(mock.Anything).RunAndReturn(func(ps []string) ([]FontFile, error) {
		indexed = append(indexed, ps...)
		var out []FontFile
		for _, p := range ps {
			file, ok := files[p]
			if !ok {
				continue
			}
			entries, err := SystemSource{}.IndexFiles([]string{filepath.Join(fixtureDir, file)})
			if err != nil {
				return nil, err
			}
			for _, e := range entries {
				e.Path = p
				out = append(out, e)
			}
		}
		return out, nil
	}).Maybe()
	src.EXPECT().Open(mock.Anything).RunAndReturn(func(p string) (io.ReadCloser, error) {
		b, err := os.ReadFile(filepath.Join(fixtureDir, files[p]))
		return io.NopCloser(bytes.NewReader(b)), err
	}).Maybe()
	return src, &indexed
}

// fixtureSource lists the fixture fonts under their own names, after extra
// paths that index to nothing.
func fixtureSource(t *testing.T, extra ...string) (*MockLazySource, *[]string) {
	t.Helper()
	fonts, err := filepath.Glob(filepath.Join(fixtureDir, "*.ttf"))
	if err != nil || len(fonts) == 0 {
		t.Fatalf("fixture fonts: %v", err)
	}
	paths := slices.Clone(extra)
	files := map[string]string{}
	for _, f := range fonts {
		p := "/fonts/" + filepath.Base(f)
		paths = append(paths, p)
		files[p] = filepath.Base(f)
	}
	return lazySource(t, paths, files)
}

func bases(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = filepath.Base(p)
	}
	slices.Sort(out)
	return out
}

func TestLazyIndexReadsOnlyNamedFamilies(t *testing.T) {
	src, indexed := fixtureSource(t, "/fonts/Unrelated-Regular.ttf")
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
	if got := bases(*indexed); !slices.Equal(got, want) {
		t.Fatalf("indexed %v, want %v", got, want)
	}
	if c.complete {
		t.Fatal("catalog fully indexed for a covered request")
	}
}

func TestLazyIndexScansAllForUnknownFamily(t *testing.T) {
	src, indexed := fixtureSource(t, "/fonts/Unrelated-Regular.ttf")
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
	src, _ := fixtureSource(t)
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

// A named script fallback family stored under an unrelated file name is found
// on the first measure, not only after another request indexed every file.
func TestLazyIndexFirstMeasureFindsHiddenNamedFamily(t *testing.T) {
	src, _ := lazySource(t, []string{"/fonts/AdwaitaSans-Regular.ttf", "/fonts/Arabic.ttf"},
		map[string]string{"/fonts/AdwaitaSans-Regular.ttf": "AdwaitaSans-Regular.ttf", "/fonts/Arabic.ttf": "NotoSansArabic-Regular.ttf"})
	c, err := Load(src)
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine(c)
	for i := range 2 {
		l, err := e.Measure("سلام", Request{Families: []string{"Adwaita Sans"}, Size: 20}, 0)
		if err != nil {
			t.Fatal(err)
		}
		run := l.Lines[0].Runs[0]
		if run.Face.Family != "Noto Sans Arabic" || run.Glyphs[0].Missing {
			t.Fatalf("measure %d: face %q missing %v", i, run.Face.Family, run.Glyphs[0].Missing)
		}
		e.EndFrame()
	}
}

// A file whose stem adds style words to the family name belongs to it.
func TestLazyIndexFindsStyleSuffixedFiles(t *testing.T) {
	src, indexed := lazySource(t, []string{"/fonts/NotoSans-Regular.ttf", "/fonts/NotoSansCondensed.ttf", "/fonts/NotoSansArabic-Regular.ttf"},
		map[string]string{"/fonts/NotoSans-Regular.ttf": "NotoSans-Regular.ttf", "/fonts/NotoSansCondensed.ttf": "NotoSans-Regular.ttf", "/fonts/NotoSansArabic-Regular.ttf": "NotoSansArabic-Regular.ttf"})
	c, err := Load(src)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(*indexed, "/fonts/NotoSansCondensed.ttf") || slices.Contains(*indexed, "/fonts/NotoSansArabic-Regular.ttf") {
		t.Fatalf("indexed %v", *indexed)
	}
	if c.complete {
		t.Fatal("catalog fully indexed at load")
	}
}

func TestStemMatches(t *testing.T) {
	for _, tc := range []struct {
		stem, key string
		want      bool
	}{
		{"notosans", "notosans", true},
		{"dejavusanscondensed", "dejavusans", true},
		{"dejavusansboldoblique", "dejavusans", true},
		{"dejavusansmono", "dejavusans", false},
		{"notosansarabic", "notosans", false},
		{"noto", "notosans", false},
	} {
		if got := stemMatches(tc.stem, tc.key); got != tc.want {
			t.Errorf("stemMatches(%q, %q) = %v", tc.stem, tc.key, got)
		}
	}
}

// Indexing in steps keeps the catalog order of a full index.
func TestLazyIndexKeepsPathOrder(t *testing.T) {
	fonts, err := filepath.Glob(filepath.Join(fixtureDir, "*.ttf"))
	if err != nil {
		t.Fatal(err)
	}
	src := NewMockLazySource(t)
	src.EXPECT().Paths().Return(fonts, nil)
	src.EXPECT().IndexFiles(mock.Anything).RunAndReturn(SystemSource{}.IndexFiles)
	c, err := Load(src)
	if err != nil {
		t.Fatal(err)
	}
	c.indexAll()
	full, err := SystemSource{}.IndexFiles(fonts)
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

func TestLazyLoadReportsPathsError(t *testing.T) {
	fonts, err := filepath.Glob(filepath.Join(fixtureDir, "NotoSans-*.ttf"))
	if err != nil {
		t.Fatal(err)
	}
	listing := errors.New("listing failed")
	src := NewMockLazySource(t)
	src.EXPECT().Paths().Return(fonts, listing)
	src.EXPECT().IndexFiles(mock.Anything).RunAndReturn(SystemSource{}.IndexFiles)
	c, err := Load(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Diagnostics) == 0 || !errors.Is(c.Diagnostics[0], listing) {
		t.Fatalf("diagnostics %v", c.Diagnostics)
	}
}

// More files than workers come back in path order, and failures are joined.
func TestIndexFilesOrderAndErrors(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(fixtureDir, "NotoSans-Regular.ttf"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	var paths []string
	for i := range indexWorkers + 8 {
		p := filepath.Join(dir, fmt.Sprintf("font%02d.ttf", i))
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	missing := filepath.Join(dir, "missing.ttf")
	paths = slices.Insert(paths, 3, missing)
	files, err := SystemSource{}.IndexFiles(paths)
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("error %v", err)
	}
	if len(files) != len(paths)-1 {
		t.Fatalf("%d entries, want %d", len(files), len(paths)-1)
	}
	want := slices.DeleteFunc(slices.Clone(paths), func(p string) bool { return p == missing })
	for i, f := range files {
		if f.Path != want[i] {
			t.Fatalf("entry %d is %s, want %s", i, f.Path, want[i])
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
