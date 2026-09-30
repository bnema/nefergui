// Package text implements CPU-side font selection, shaping, measurement and grayscale glyph preparation.
// Catalog and Engine are not safe for concurrent use (go-text faces cache mutable state).
package text

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bnema/nefergui/internal/bidi"
	"github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"
	"golang.org/x/image/font/sfnt"
)

// FontSource is the only external boundary: it provides files, not font matching policy.
// Implementations must return a stable snapshot. Corrupt files are reported, not silently accepted.
type FontSource interface {
	Fonts() (map[string][]byte, error)
}

// DirectorySource reads font files recursively; paths are sorted before parsing.
type DirectorySource string

func (d DirectorySource) Fonts() (map[string][]byte, error) {
	out := map[string][]byte{}
	var problems []error
	_ = filepath.WalkDir(string(d), func(path string, entry fs.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", path, err))
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if !isFontPath(path) {
			return nil
		}
		b, e := os.ReadFile(path)
		if e != nil {
			problems = append(problems, fmt.Errorf("%s: %w", path, e))
			return nil
		}
		out[path] = b
		return nil
	})
	return out, errors.Join(problems...)
}

func isFontPath(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ttf", ".otf", ".ttc":
		return true
	}
	return false
}

// SystemSource searches XDG user and system font directories without fontconfig/cgo.
// Earlier locations win identical family/aspect ties.
type SystemSource struct{}

// Fonts remains available for callers that explicitly need byte snapshots; Load uses Index instead.
func (s SystemSource) Fonts() (map[string][]byte, error) {
	paths, err := s.paths()
	out := make(map[string][]byte, len(paths))
	for _, p := range paths {
		b, e := os.ReadFile(p)
		if e != nil {
			err = errors.Join(err, e)
		} else {
			out[p] = b
		}
	}
	return out, err
}

func (SystemSource) paths() ([]string, error) {
	home, _ := os.UserHomeDir()
	user := os.Getenv("XDG_DATA_HOME")
	if user == "" {
		user = filepath.Join(home, ".local/share")
	}
	dirs := []string{filepath.Join(user, "fonts"), filepath.Join(home, ".fonts")}
	data := os.Getenv("XDG_DATA_DIRS")
	if data == "" {
		data = "/usr/local/share:/usr/share"
	}
	for _, d := range strings.Split(data, ":") {
		if d != "" {
			dirs = append(dirs, filepath.Join(d, "fonts"))
		}
	}
	dirs = append(dirs, "/usr/share/fonts")
	seen := map[string]bool{}
	var paths []string
	var problems []error
	for _, d := range dirs {
		resolved, e := filepath.EvalSymlinks(d)
		if e != nil {
			if !errors.Is(e, os.ErrNotExist) {
				problems = append(problems, e)
			}
			continue
		}
		if seen[resolved] {
			continue
		}
		seen[resolved] = true
		var local []string
		_ = filepath.WalkDir(resolved, func(p string, entry fs.DirEntry, err error) error {
			if err != nil {
				problems = append(problems, err)
				return nil
			}
			if entry.IsDir() {
				return nil
			}
			if isFontPath(p) {
				local = append(local, p)
			}
			return nil
		})
		sort.Strings(local)
		for _, p := range local {
			resolvedPath, e := filepath.EvalSymlinks(p)
			if e != nil {
				problems = append(problems, e)
				continue
			}
			if !seen[resolvedPath] {
				seen[resolvedPath] = true
				paths = append(paths, p)
			}
		}
	}
	return paths, errors.Join(problems...)
}

// IndexSource supplies metadata without retaining font bytes. Open is invoked only
// when a face is selected. Sources must keep paths stable during the catalog lifetime.
type IndexSource interface {
	Index() ([]FontFile, error)
	Open(path string) (io.ReadCloser, error)
}

type FontFile struct {
	Path          string
	Family        string
	Aspect        font.Aspect
	Index         int
	UnicodeRanges [4]uint32 // OS/2 ulUnicodeRange; zero means unknown
}

const maxFontBytes = 64 << 20
const maxMetadataBytes = 1 << 20

// metadataReader limits total metadata I/O. Font files larger than the
// selected-face cap are excluded before go-text examines their tables.
type metadataReader struct {
	*os.File
	remaining int64
	exceeded  bool
}

func (m *metadataReader) ReadAt(p []byte, off int64) (int, error) {
	if len(p) > maxMetadataBytes || int64(len(p)) > m.remaining {
		m.exceeded = true
		return 0, errors.New("font metadata limit exceeded")
	}
	m.remaining -= int64(len(p))
	return m.File.ReadAt(p, off)
}
func (m *metadataReader) Read(p []byte) (int, error) {
	if len(p) > maxMetadataBytes || int64(len(p)) > m.remaining {
		m.exceeded = true
		return 0, errors.New("font metadata limit exceeded")
	}
	n, err := m.File.Read(p)
	m.remaining -= int64(n)
	return n, err
}

func (s SystemSource) Index() ([]FontFile, error) {
	paths, err := s.paths()
	var files []FontFile
	var buffer []byte
	for _, p := range paths {
		f, e := os.Open(p)
		if e != nil {
			err = errors.Join(err, fmt.Errorf("font %s: %w", p, e))
			continue
		}
		info, statErr := f.Stat()
		if statErr != nil || info.Size() > maxFontBytes {
			if statErr == nil {
				statErr = fmt.Errorf("font exceeds %d byte limit", maxFontBytes)
			}
			err = errors.Join(err, fmt.Errorf("font %s: %w", p, statErr))
			if closeErr := f.Close(); closeErr != nil {
				err = errors.Join(err, fmt.Errorf("font %s close: %w", p, closeErr))
			}
			continue
		}
		reader := &metadataReader{File: f, remaining: maxMetadataBytes}
		loaders, e := ot.NewLoaders(reader)
		if e == nil {
			for i, loader := range loaders {
				var desc font.Description
				// Describe ignores table read errors; a failed bounded read
				// yields no usable family and is excluded from the index.
				desc, buffer = font.Describe(loader, buffer)
				if reader.exceeded {
					err = errors.Join(err, fmt.Errorf("font %s face %d: metadata limit exceeded", p, i))
					break
				}
				if desc.Family != "" {
					entry := FontFile{Path: p, Family: desc.Family, Aspect: desc.Aspect, Index: i}
					if raw, tableErr := loader.RawTable(ot.MustNewTag("OS/2")); tableErr == nil && len(raw) >= 58 {
						for j := range entry.UnicodeRanges {
							entry.UnicodeRanges[j] = binary.BigEndian.Uint32(raw[42+j*4:])
						}
					}
					if reader.exceeded {
						err = errors.Join(err, fmt.Errorf("font %s face %d: metadata limit exceeded", p, i))
						break
					}
					files = append(files, entry)
				}
			}
		} else {
			err = errors.Join(err, fmt.Errorf("font %s: %w", p, e))
		}
		if closeErr := f.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("font %s close: %w", p, closeErr))
		}
	}
	return files, err
}
func (SystemSource) Open(path string) (io.ReadCloser, error) { return os.Open(path) }

// Face is a parsed font, paired with its bytes for static TrueType rasterization.
type Face struct {
	ID            string
	Family        string
	Aspect        font.Aspect
	Shape         *font.Face
	Raster        *sfnt.Font
	Data          []byte
	Variations    []font.Variation // immutable after construction; use WithVariations to clone
	Variable      bool             // fvar table present
	path          string
	index         int
	unicodeRanges [4]uint32
	catalog       *Catalog
	bad           bool
	familyKey     string // font.NormalizeFamily(Family), computed once by Load
}

// WithVariations clones a face, preserving the font bytes and sfnt handle while
// configuring go-text's variation-aware outline and shaping caches independently.
func (f *Face) WithVariations(v []font.Variation) *Face {
	clone := *f
	clone.Shape = font.NewFace(f.Shape.Font)
	clone.Shape.SetVariations(v)
	clone.Variations = append([]font.Variation(nil), v...)
	return &clone
}

type Catalog struct {
	Faces       []*Face
	source      IndexSource
	Diagnostics []error
}

func Load(source FontSource) (*Catalog, error) {
	if indexed, ok := source.(IndexSource); ok {
		entries, err := indexed.Index()
		c := &Catalog{source: indexed}
		if err != nil {
			c.Diagnostics = append(c.Diagnostics, err)
		}
		for _, entry := range entries {
			c.Faces = append(c.Faces, &Face{ID: fmt.Sprintf("%s/%d", entry.Path, entry.Index), Family: entry.Family, familyKey: font.NormalizeFamily(entry.Family), Aspect: entry.Aspect, path: entry.Path, index: entry.Index, unicodeRanges: entry.UnicodeRanges, catalog: c})
		}
		if len(c.Faces) == 0 {
			return nil, fmt.Errorf("no usable fonts: %w", err)
		}
		return c, nil
	}

	files, err := source.Fonts()
	var problems []error
	if err != nil {
		problems = append(problems, err)
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	c := &Catalog{}
	for _, name := range names {
		data := files[name]
		loaders, e := ot.NewLoaders(bytes.NewReader(data))
		if e != nil {
			problems = append(problems, fmt.Errorf("font %s: %w", name, e))
			continue
		}
		collection, e := sfnt.ParseCollection(data)
		if e != nil {
			problems = append(problems, fmt.Errorf("raster font %s: %w", name, e))
			continue
		}
		if collection.NumFonts() != len(loaders) {
			problems = append(problems, fmt.Errorf("font %s: mismatched collection length", name))
			continue
		}
		sum := sha256.Sum256(data)
		for index, loader := range loaders {
			parsed, err := font.NewFont(loader)
			if err != nil {
				problems = append(problems, fmt.Errorf("font %s face %d: %w", name, index, err))
				continue
			}
			raster, err := collection.Font(index)
			if err != nil {
				problems = append(problems, fmt.Errorf("raster font %s face %d: %w", name, index, err))
				continue
			}
			shape := font.NewFace(parsed)
			raw, tableErr := loader.RawTable(ot.MustNewTag("fvar"))
			desc := shape.Describe()
			id := fmt.Sprintf("%x/%d", sum, index)
			c.Faces = append(c.Faces, &Face{ID: id, Family: desc.Family, familyKey: font.NormalizeFamily(desc.Family), Aspect: desc.Aspect, Shape: shape, Raster: raster, Data: data, Variable: tableErr == nil && len(raw) > 0})
		}
	}
	c.Diagnostics = problems
	if len(c.Faces) == 0 {
		return nil, fmt.Errorf("no usable fonts: %w", errors.Join(problems...))
	}
	return c, nil
}

// ready loads a selected face once. A failed face is permanently excluded from matching.
func (f *Face) ready() bool {
	if f == nil || f.bad {
		return false
	}
	if f.Shape != nil {
		return true
	}
	if f.catalog == nil {
		return false
	}
	reader, err := f.catalog.source.Open(f.path)
	if err != nil {
		f.fail(err)
		return false
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxFontBytes+1))
	if closeErr := reader.Close(); closeErr != nil {
		f.catalog.Diagnostics = append(f.catalog.Diagnostics, fmt.Errorf("font %s close: %w", f.path, closeErr))
	}
	if err == nil && len(data) > maxFontBytes {
		err = fmt.Errorf("font exceeds %d byte limit", maxFontBytes)
	}
	if err != nil {
		f.fail(err)
		return false
	}
	loaders, err := ot.NewLoaders(bytes.NewReader(data))
	if err != nil {
		f.fail(err)
		return false
	}
	collection, err := sfnt.ParseCollection(data)
	if err != nil {
		f.fail(err)
		return false
	}
	if f.index >= len(loaders) || f.index >= collection.NumFonts() {
		f.fail(errors.New("face index out of range"))
		return false
	}
	parsed, err := font.NewFont(loaders[f.index])
	if err != nil {
		f.fail(err)
		return false
	}
	raster, err := collection.Font(f.index)
	if err != nil {
		f.fail(err)
		return false
	}
	raw, tableErr := loaders[f.index].RawTable(ot.MustNewTag("fvar"))
	f.Data, f.Shape, f.Raster = data, font.NewFace(parsed), raster
	f.Variable = tableErr == nil && len(raw) > 0
	return true
}
func (f *Face) fail(err error) {
	f.bad = true
	f.catalog.Diagnostics = append(f.catalog.Diagnostics, fmt.Errorf("font %s face %d: %w", f.path, f.index, err))
}

// Request specifies CSS font selection. Weight defaults to 400, Stretch to 1, Style normal.
type Request struct {
	Direction bidi.BaseDirection // Auto (default), LTR, or RTL
	Families  []string
	Weight    float32
	Stretch   float32
	Italic    bool
	Size      float64
	// LetterSpacing is the signed logical-pixel spacing between shaped clusters.
	LetterSpacing float64
	Variations    []font.Variation
}

// match takes a family already normalized with font.NormalizeFamily.
func (c *Catalog) match(family string, r Request) *Face {
	var best *Face
	score := math.Inf(1)
	for _, f := range c.Faces {
		if f.familyKey != family || f.bad {
			continue
		}
		stretch := r.Stretch
		if stretch == 0 {
			stretch = 1
		}
		weight := r.Weight
		if weight == 0 {
			weight = 400
		}
		// CSS subset: stretch first, style second, then nearest weight. On ties use source path order.
		s := math.Abs(float64(f.Aspect.Stretch)-float64(stretch)) * 1e6
		if (f.Aspect.Style == font.StyleItalic) != r.Italic {
			s += 1e4
		}
		diff := float64(f.Aspect.Weight) - float64(weight)
		if weight >= 400 && weight <= 500 {
			// CSS: [target..500], then below target, then above 500.
			switch {
			case diff >= 0 && float64(f.Aspect.Weight) <= 500:
				s += diff
			case diff < 0:
				s += 1000 - diff
			default:
				s += 2000 + diff
			}
		} else if weight < 400 {
			if diff > 0 {
				s += 1000 + diff
			} else {
				s -= diff
			}
		} else {
			if diff < 0 {
				s += 1000 - diff
			} else {
				s += diff
			}
		}
		if s < score {
			best = f
			score = s
		}
	}
	return best
}

// Generic family aliases are ordered, hard-coded and independent of installed fontconfig rules.
var generics = map[string][]string{
	"sans-serif": {"Noto Sans", "Adwaita Sans", "DejaVu Sans"},
	"system-ui":  {"Adwaita Sans", "Noto Sans", "DejaVu Sans"},
	"serif":      {"Noto Serif", "DejaVu Serif"},
	"monospace":  {"Noto Sans Mono", "IBM Plex Mono", "DejaVu Sans Mono"},
}

// Normalized once so matching never allocates per face.
var genericKeys = normalizeFamilies(generics)
var scriptFallbackKeys = normalizeList([]string{"Noto Sans Arabic", "Noto Sans Devanagari", "IBM Plex Sans JP", "Noto Sans Symbols 2"})

func normalizeList(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = font.NormalizeFamily(n)
	}
	return out
}
func normalizeFamilies(m map[string][]string) map[string][]string {
	out := make(map[string][]string, len(m))
	for k, v := range m {
		out[k] = normalizeList(v)
	}
	return out
}

func (c *Catalog) candidates(r Request) []*Face {
	seen := map[*Face]bool{}
	var out []*Face
	add := func(f *Face) {
		if f != nil && !seen[f] {
			out = append(out, f)
			seen[f] = true
		}
	}
	for _, family := range r.Families {
		family = strings.Trim(family, " \t\"'")
		if alias, ok := genericKeys[strings.ToLower(family)]; ok {
			for _, a := range alias {
				add(c.match(a, r))
			}
		} else {
			add(c.match(font.NormalizeFamily(family), r))
		}
	}
	for _, a := range genericKeys["sans-serif"] {
		add(c.match(a, r))
	}
	// Script fallback: deterministically prefer known Unicode families, never a color bitmap font.
	for _, a := range scriptFallbackKeys {
		add(c.match(a, r))
	}
	if c.source == nil {
		for _, f := range c.Faces {
			add(f)
		}
	}

	return out
}

const maxFallbackFaces = 16

// The OS/2 Unicode range bits are coarse hints, not glyph coverage. Unmapped
// blocks and absent ranges remain eligible, so NominalGlyph decides coverage.
func unicodeRangeBit(r rune) int {
	switch {
	case r <= 0x007f:
		return 0 // Basic Latin
	case r <= 0x00ff:
		return 1 // Latin-1
	case r >= 0x0100 && r <= 0x017f:
		return 2
	case r >= 0x0180 && r <= 0x024f:
		return 3
	case r >= 0x0300 && r <= 0x036f:
		return 6
	case r >= 0x0370 && r <= 0x03ff:
		return 7
	case r >= 0x0400 && r <= 0x052f:
		return 9
	case r >= 0x0590 && r <= 0x05ff:
		return 11
	case r >= 0x0600 && r <= 0x06ff:
		return 13
	case r >= 0x0900 && r <= 0x097f:
		return 15
	case r >= 0x3040 && r <= 0x309f:
		return 49
	case r >= 0x30a0 && r <= 0x30ff:
		return 50
	case r >= 0x4e00 && r <= 0x9fff:
		return 59
	case r >= 0xac00 && r <= 0xd7af:
		return 56
	case r >= 0x1f300 && r <= 0x1faff:
		return 57 // non-BMP (OS/2 bit 57)
	default:
		return -1
	}
}
func (f *Face) mayCover(r rune) bool {
	bits := f.unicodeRanges
	if bits == [4]uint32{} {
		return true
	}
	bit := unicodeRangeBit(r)
	return bit < 0 || bits[bit/32]&(1<<uint(bit%32)) != 0
}

// namedKeys lists every family key the request can name, in any aspect, so
// the fallback window excludes them whether or not their faces loaded.
func namedKeys(r Request) map[string]bool {
	keys := map[string]bool{}
	for _, family := range r.Families {
		family = strings.Trim(family, " \t\"'")
		if alias, ok := genericKeys[strings.ToLower(family)]; ok {
			for _, a := range alias {
				keys[a] = true
			}
		} else {
			keys[font.NormalizeFamily(family)] = true
		}
	}
	for _, a := range genericKeys["sans-serif"] {
		keys[a] = true
	}
	for _, a := range scriptFallbackKeys {
		keys[a] = true
	}
	return keys
}

// fallbackWindow is fixed by the request, text and metadata index. Faces with
// unknown range data remain eligible; loaded or failed state never moves the
// window, so later measures cannot admit a different face.
func (c *Catalog) fallbackWindow(text []rune, r Request) []*Face {
	named := namedKeys(r)
	out := make([]*Face, 0, maxFallbackFaces)
	for _, f := range c.Faces {
		if named[f.familyKey] {
			continue
		}
		for _, r := range text {
			if r != 0x200d && (r < 0xfe00 || r > 0xfe0f) && f.mayCover(r) {
				out = append(out, f)
				break
			}
		}
		if len(out) == maxFallbackFaces {
			break
		}
	}
	return out
}

// primary is the first loadable named candidate, else the first loadable face
// of the fallback window. A failed face is marked bad, so each step advances.
func (c *Catalog) primary(r Request) *Face {
	for {
		candidates := c.candidates(r)
		if len(candidates) == 0 {
			break
		}
		if candidates[0].ready() {
			return candidates[0]
		}
		if !candidates[0].bad {
			return nil
		}
	}
	for _, f := range c.fallbackWindow([]rune{'A'}, r) {
		if f.ready() {
			return f
		}
	}
	return nil
}
