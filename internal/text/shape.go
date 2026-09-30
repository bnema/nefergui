package text

import (
	"errors"
	"math"

	"github.com/bnema/nefergui/internal/bidi"
	"github.com/go-text/typesetting/di"
	"github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/segmenter"
	"github.com/go-text/typesetting/shaping"
	"golang.org/x/image/math/fixed"
)

// Glyph contains a positioned glyph in logical pixels, with its original shaping metrics.
type Glyph struct {
	ID      font.GID
	X, Y    float64
	Advance float64
	// Cluster is the rune index, in the original Measure input, of the first
	// rune of the glyph's cluster. It is not a UTF-8 byte offset.
	Cluster int
	Missing bool
}

// Run is a shaped span with one face, direction and embedding level.
type Run struct {
	Face      *Face
	Direction di.Direction
	Level     uint8 // resolved embedding level after L1
	// Start and End are rune indices in the original Measure input; End is
	// exclusive. A CRLF separator counts as two runes.
	Start, End    int
	X, Y, Advance float64
	Glyphs        []Glyph
}
type Line struct {
	Runs                    []Run
	Direction               di.Direction // resolved paragraph base direction
	Width, Baseline, Height float64
}
type Layout struct {
	Lines                                                       []Line
	Direction                                                   di.Direction // paragraph basis, auto resolved by bidi analysis
	Width, Height, MinContent, MaxContent, Baseline, LineHeight float64
}

// Engine is single-goroutine only: Measure and EndFrame are not synchronized.
// Its catalog is fixed at NewEngine and must not change afterwards, because
// cached measurements depend on its faces.
type Engine struct {
	catalog *Catalog
	seg     shaping.Segmenter
	shaper  shaping.HarfbuzzShaper
	wrapper shaping.LineWrapper
	cache   measureCache
}

func NewEngine(c *Catalog) *Engine { return &Engine{catalog: c} }

type constantFace struct{ face *font.Face }

func (m constantFace) ResolveFace(r rune) *font.Face { return m.face }

// clusterFaces selects a single face for every UAX#29 extended grapheme. Format
// characters (ZWJ and variation selectors) do not need outline coverage; they
// remain in the cluster to let HarfBuzz apply substitutions.
func clusterFaces(text []rune, choices []*Face) []*font.Face {
	selected := make([]*font.Face, len(text))
	var seg segmenter.Segmenter
	seg.Init(text)
	it := seg.GraphemeIterator()
	for it.Next() {
		g := it.Grapheme()
		f := choices[0]
		for _, candidate := range choices {
			all := true
			for _, r := range g.Text {
				if r == 0x200d || r >= 0xfe00 && r <= 0xfe0f {
					continue
				}
				if _, ok := candidate.Shape.NominalGlyph(r); !ok {
					all = false
					break
				}
			}
			if all {
				f = candidate
				break
			}
		}
		for i := range g.Text {
			selected[g.Offset+i] = f.Shape
		}
	}
	return selected
}

// faceCovers checks an entire grapheme, ignoring format selectors.
func faceCovers(f *Face, cluster []rune) bool {
	for _, ch := range cluster {
		if ch == 0x200d || ch >= 0xfe00 && ch <= 0xfe0f {
			continue
		}
		if _, ok := f.Shape.NominalGlyph(ch); !ok {
			return false
		}
	}
	return true
}

// split cluster-aligned face runs after go-text's bidi/script itemization.
func splitFaces(items []shaping.Input, faceFor []*font.Face) []shaping.Input {
	var out []shaping.Input
	for _, item := range items {
		if item.RunStart >= item.RunEnd {
			continue
		}
		start := item.RunStart
		face := faceFor[start]
		for i := start + 1; i < item.RunEnd; i++ {
			if faceFor[i] != face {
				part := item
				part.RunStart = start
				part.RunEnd = i
				part.Face = face
				out = append(out, part)
				start = i
				face = faceFor[i]
			}
		}
		item.RunStart = start
		item.Face = face
		out = append(out, item)
	}
	return out
}
func (e *Engine) shape(text []rune, r Request, direction di.Direction, levels []uint8) ([]shaping.Output, map[*font.Face]*Face, error) {
	if e == nil || e.catalog == nil || len(e.catalog.Faces) == 0 {
		return nil, nil, errors.New("no font catalog")
	}
	if r.Size <= 0 || math.IsNaN(r.Size) || math.IsInf(r.Size, 0) || r.Size > 4096 {
		return nil, nil, errors.New("invalid font size")
	}
	choices := e.catalog.candidates(r)
	if e.catalog.source != nil {
		// Load named faces only as required for coverage. A failing face is
		// excluded permanently and ranking is recomputed to admit its runner-up.
		named := choices
		var seg segmenter.Segmenter
		seg.Init(text)
		it := seg.GraphemeIterator()
		var missing []rune
		for it.Next() {
			cluster := it.Grapheme().Text
			covered := false
			for {
				named = e.catalog.candidates(r)
				failed := false
				for _, f := range named {
					if !f.ready() {
						failed = true
						break
					}
					if faceCovers(f, cluster) {
						covered = true
						break
					}
				}
				if covered || !failed {
					break
				}
			}
			if !covered {
				missing = append(missing, cluster...)
			}
		}
		// Rebuild after failed loads. The fallback window excludes named
		// families by key, so it never depends on load or failure state.
		named = e.catalog.candidates(r)
		window := e.catalog.fallbackWindow(missing, r)
		choices = nil
		for _, f := range named {
			if f.Shape != nil && !f.bad {
				choices = append(choices, f)
			}
		}
		if len(missing) != 0 {
			for _, f := range window {
				if !f.ready() {
					continue
				}
				choices = append(choices, f)
				complete := true
				seg.Init(missing)
				it = seg.GraphemeIterator()
				for it.Next() {
					covered := false
					for _, candidate := range choices {
						if faceCovers(candidate, it.Grapheme().Text) {
							covered = true
							break
						}
					}
					if !covered {
						complete = false
						break
					}
				}
				if complete {
					break
				}
			}
		}
		// Nothing selected (empty text, or only uncovered runes with a failed
		// window): the primary face shapes the text and supplies deterministic tofu.
		if len(choices) == 0 {
			if f := e.catalog.primary(r); f != nil {
				choices = append(choices, f)
			}
		}
	}
	if len(choices) == 0 {
		return nil, nil, errors.New("no usable fonts")
	}
	reverse := map[*font.Face]*Face{}
	faces := make([]*Face, 0, len(choices))
	// Variations belong to the engine invocation; do not mutate the catalog's shared faces.
	for _, f := range choices {
		if len(r.Variations) > 0 {
			f = f.WithVariations(r.Variations)
		}
		faces = append(faces, f)
		reverse[f.Shape] = f
	}
	faceFor := clusterFaces(text, faces)
	input := shaping.Input{Text: text, RunEnd: len(text), Direction: direction, Size: fixed.Int26_6(math.Round(r.Size * 64))}
	items := splitFaces(e.seg.Split(input, constantFace{face: faces[0].Shape}), faceFor)
	items = splitLevels(items, levels)
	out := make([]shaping.Output, 0, len(items))
	for _, item := range items {
		if item.RunEnd > item.RunStart {
			out = append(out, e.shaper.Shape(item))
		}
	}
	return out, reverse, nil
}

// Measure shapes, wraps (UAX#14) and positions runs. Width <= 0 means unbounded.
// MinContent is the largest unbreakable UAX#14 segment; max-content ignores soft breaks.
// Every UAX#9 paragraph resolves its own direction; line baselines stack in logical px.
//
// Results are cached per (text, request, width) until EndFrame evicts them.
// The returned Lines, Runs and Glyphs are shared with the cache: treat them as
// immutable and copy before changing positions (see Layout.Clone).
func (e *Engine) Measure(s string, r Request, width float64) (Layout, error) {
	if l, ok := e.cache.get(s, r, width); ok {
		return l, nil
	}
	l, err := e.measure(s, r, width)
	if err == nil {
		e.cache.put(s, r, width, l)
	}
	return l, err
}

// EndFrame evicts measurements not used during the last two frames. Call it
// once after each frame that may have measured text.
func (e *Engine) EndFrame() { e.cache.endFrame() }

func (e *Engine) measure(s string, r Request, width float64) (Layout, error) {
	var result Layout
	if math.IsNaN(width) || math.IsInf(width, 0) || width > 1<<24 {
		return result, errors.New("invalid width")
	}
	if math.IsNaN(r.LetterSpacing) || math.IsInf(r.LetterSpacing, 0) || math.Abs(r.LetterSpacing) > 4096 {
		return result, errors.New("invalid letter spacing")
	}
	text := []rune(s)
	if paragraphs, split := paragraphRanges(text); split {
		return e.measureParagraphs(text, paragraphs, r, width)
	}
	if r.Direction > bidi.RTL {
		return result, errors.New("invalid paragraph direction")
	}
	paraLevels, baseLevel, err := bidi.Resolve(text, r.Direction, fullBreak(text))
	if err != nil {
		return result, err
	}
	direction := di.DirectionLTR
	if baseLevel%2 == 1 {
		direction = di.DirectionRTL
	}
	result.Direction = direction
	outs, reverse, err := e.shape(text, r, direction, paraLevels)
	if err != nil {
		return result, err
	}
	if r.LetterSpacing != 0 {
		shaping.AddSpacing(outs, text, 0, fixed.Int26_6(math.Round(r.LetterSpacing*64)))
	}
	baseline := r.Size * 0.8
	height := r.Size * 1.2
	if ext, ok := e.catalog.primary(r).Shape.FontHExtents(); ok {
		unit := r.Size / float64(e.catalog.primary(r).Shape.Upem())
		baseline = float64(ext.Ascender) * unit
		height = float64(ext.Ascender-ext.Descender+ext.LineGap) * unit
	}
	if height <= 0 {
		height = r.Size * 1.2
	}
	result.Baseline = baseline
	result.LineHeight = height
	if len(text) == 0 {
		return result, nil
	}
	var minWidth float64
	// Compute exact word widths even when a shaped run crosses a break opportunity.
	// Wrapper's Never policy at zero width supplies min-content using its native glyph-cluster mapping.
	var never shaping.LineWrapper
	words, _ := never.WrapParagraphF(shaping.WrapConfig{Direction: direction, BreakPolicy: shaping.Never, DisableTrailingWhitespaceTrim: true}, 1, text, shaping.NewSliceIterator(outs))
	for _, line := range words {
		w := 0.0
		for _, o := range line {
			w += float64(o.Advance) / 64
		}
		if w > minWidth {
			minWidth = w
		}
	}
	result.MinContent = minWidth
	maxLines, _ := e.wrapper.WrapParagraphF(shaping.WrapConfig{Direction: direction, DisableTrailingWhitespaceTrim: true}, fixed.Int26_6(1<<30), text, shaping.NewSliceIterator(outs))
	for _, line := range maxLines {
		w := 0.0
		for _, o := range line {
			w += float64(o.Advance) / 64
		}
		if w > result.MaxContent {
			result.MaxContent = w
		}
	}
	limit := fixed.Int26_6(1 << 30)
	if width > 0 {
		limit = fixed.Int26_6(math.Round(width * 64))
	}
	wrapped, _ := e.wrapper.WrapParagraphF(shaping.WrapConfig{Direction: direction, DisableTrailingWhitespaceTrim: true}, limit, text, shaping.NewSliceIterator(outs))
	for idx, line := range wrapped {
		ordered, levelFor, err := visualRuns(text, line, r.Direction)
		if err != nil {
			return result, err
		}
		dst := Line{Baseline: baseline + float64(idx)*height, Height: height, Direction: direction}
		for _, o := range ordered {
			f := reverse[o.Face]
			level := levelFor[o.Runes.Offset]
			run := Run{Face: f, Direction: o.Direction, Level: level, Start: o.Runes.Offset, End: o.Runes.Offset + o.Runes.Count, X: dst.Width, Y: dst.Baseline, Advance: float64(o.Advance) / 64}
			cursor := 0.0
			for _, g := range o.Glyphs {
				run.Glyphs = append(run.Glyphs, Glyph{ID: g.GlyphID, X: run.X + cursor + float64(g.XOffset)/64, Y: run.Y - float64(g.YOffset)/64, Advance: float64(g.Advance) / 64, Cluster: g.TextIndex(), Missing: g.GlyphID == 0})
				cursor += float64(g.Advance) / 64
			}
			dst.Runs = append(dst.Runs, run)
			dst.Width += run.Advance
		}
		if dst.Width > result.Width {
			result.Width = dst.Width
		}
		result.Lines = append(result.Lines, dst)
	}
	result.Height = float64(len(result.Lines)) * height
	return result, nil
}
