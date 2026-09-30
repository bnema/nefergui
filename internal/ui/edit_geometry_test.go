package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/bnema/nefergui/internal/css"
	"github.com/bnema/nefergui/internal/edit"
	"github.com/bnema/nefergui/internal/layout"
	"github.com/bnema/nefergui/internal/text"
	"github.com/go-text/typesetting/di"
)

func geometryRuntime(t *testing.T, sheet string) *runtime {
	t.Helper()
	catalog, err := text.Load(text.DirectorySource("../../testdata/fonts"))
	if err != nil {
		t.Fatal(err)
	}
	r := newRuntime()
	r.setTextEngine(text.NewEngine(catalog))
	r.styles = css.Compile(css.UA(), css.Parse(sheet))
	return r
}

func displayCommand(r *runtime, id, op string) (layout.Command, bool) {
	for _, c := range r.output.Display {
		if c.ID == id && c.Op == op {
			return c, true
		}
	}
	return layout.Command{}, false
}

// rawTrailingEdge computes, independently of editorGeometry, the x where the
// character at source rune index `rune` ends in reading order: its right ink
// edge in an LTR run and its left edge in an RTL run. It reads the shaped
// glyphs of the painted text command and the line's run directions only.
func rawTrailingEdge(t *testing.T, r *runtime, n *layout.Result, rune int) float64 {
	t.Helper()
	cmd, ok := displayCommand(r, n.ID, "text")
	if !ok {
		t.Fatal("missing text command")
	}
	ri := 0
	for _, line := range n.Lines {
		for _, shaped := range line.Runs {
			run := cmd.Runs[ri]
			ri++
			low, high, seen := math.Inf(1), math.Inf(-1), false
			for _, g := range run.Glyphs {
				if g.Cluster == rune {
					seen = true
					low = math.Min(low, g.X)
					high = math.Max(high, g.X+g.Advance)
				}
			}
			if !seen {
				continue
			}
			if shaped.Direction == di.DirectionRTL {
				return low
			}
			return high
		}
	}
	t.Fatalf("no glyph for rune %d", rune)
	return 0
}

func TestDisplayTextMapsPasswordGraphemes(t *testing.T) {
	value := "e\u0301猫a"
	d := newDisplayText(value, true)
	if d.shown != "•••" {
		t.Fatalf("shown %q", d.shown)
	}
	bullet := len("•")
	for _, c := range []struct{ logical, shown int }{
		{0, 0}, {len("e\u0301"), bullet}, {len("e\u0301猫"), 2 * bullet}, {len(value), 3 * bullet},
		{1, 0}, // inside the first grapheme: start of its cluster
	} {
		if got := d.toShown(c.logical); got != c.shown {
			t.Errorf("toShown(%d) = %d, want %d", c.logical, got, c.shown)
		}
	}
	for _, c := range []struct{ shown, logical int }{{0, 0}, {bullet, len("e\u0301")}, {3 * bullet, len(value)}} {
		if got := d.toLogical(c.shown); got != c.logical {
			t.Errorf("toLogical(%d) = %d, want %d", c.shown, got, c.logical)
		}
	}
	plain := newDisplayText(value, false)
	if plain.shown != value || plain.toShown(3) != 3 || plain.toLogical(3) != 3 {
		t.Fatalf("plain text must map identically: %+v", plain)
	}
}

// At a bidi run boundary one offset has two visual positions. Painting,
// scrolling and preedit must all use the preceding character's trailing edge.
func TestMixedDirectionCaretPolicyAgreesAcrossCallers(t *testing.T) {
	value := "abc مرحبا def"
	view := func(f *Frame) { f.Root().Input("mixed", &value) }
	// offset -> source rune index of the character before it.
	for _, c := range []struct{ offset, before int }{{len("abc "), 3}, {len("abc مرحبا"), 8}} {
		offset := c.offset
		r := geometryRuntime(t, `input {width:300px;height:28px;font-size:16px}`)
		r.Build(view)
		r.route(key("Tab"))
		r.Build(view)
		n := r.output.Tree.Children[0]
		r.edits[n.ID].Move(offset, false)
		r.edits[n.ID].Preedit = "x"
		r.Redraw()
		r.Build(view)
		n = r.output.Tree.Children[0]

		g := newEditorGeometry(r.edits[n.ID], n, r.output.Display, false)
		var edges []textBoundary
		for _, b := range g.lines[0] {
			if b.byteOffset == offset {
				edges = append(edges, b)
			}
		}
		var leading textBoundary
		for _, b := range edges {
			if !b.trailing {
				leading = b
			}
		}
		want := rawTrailingEdge(t, r, n, c.before)
		if len(edges) < 2 || math.Abs(want-leading.x) < 1 {
			t.Fatalf("offset %d is not an ambiguous bidi boundary: %+v (preceding edge %g)", offset, edges, want)
		}
		caret, ok := displayCommand(r, n.ID, "caret")
		if !ok {
			t.Fatal("missing caret")
		}
		preedit, ok := displayCommand(r, n.ID, "preedit-text")
		if !ok {
			t.Fatal("missing preedit")
		}
		if math.Abs(caret.Rect.X-want) > 0.01 || math.Abs(preedit.Rect.X-want) > 0.01 {
			t.Fatalf("offset %d: caret %g preedit %g, want preceding trailing edge %g (leading %g)", offset, caret.Rect.X, preedit.Rect.X, want, leading.x)
		}
	}
}

// The scroll target must be the painted caret, not another duplicate offset.
func TestMixedDirectionScrollFollowsPaintedCaret(t *testing.T) {
	value := "abc مرحبا def"
	view := func(f *Frame) { f.Root().Input("mixed", &value) }
	// The trailing edge of "abc " is left of the leading edge of the RTL run.
	// A content box between the two edges scrolls only if the wrong one is used.
	r := geometryRuntime(t, `input {width:300px;height:28px;font-size:16px}`)
	r.Build(view)
	r.route(key("Tab"))
	r.Build(view)
	n := r.output.Tree.Children[0]
	// Expected edges come from raw glyphs. The space before the RTL run ends at
	// trailing; the RTL run's reading-order start (its right ink edge) is the
	// other, leading duplicate of the same offset.
	trailing := rawTrailingEdge(t, r, n, 3)
	leading := math.Inf(-1)
	cmd, _ := displayCommand(r, n.ID, "text")
	for i, run := range cmd.Runs {
		if n.Lines[0].Runs[i].Direction == di.DirectionRTL {
			for _, g := range run.Glyphs {
				leading = math.Max(leading, g.X+g.Advance)
			}
		}
	}
	if trailing >= leading {
		t.Fatalf("fixture changed: trailing %g leading %g", trailing, leading)
	}
	// Content wide enough for the preceding character's trailing edge but not
	// for the following run's leading edge.
	padding := 300 - n.Content.W
	contentW := math.Floor((trailing + leading) / 2)
	r = geometryRuntime(t, fmt.Sprintf("input {height:28px;font-size:16px;width:%gpx}", contentW+padding))
	r.Build(view)
	n = r.output.Tree.Children[0]
	r.edits[n.ID].Move(len("abc "), false) // before focus, so no earlier scroll persists
	r.route(key("Tab"))
	r.Build(view)
	n = r.output.Tree.Children[0]
	caret, _ := displayCommand(r, n.ID, "caret")
	if n.ScrollX != 0 || math.Abs(caret.Rect.X-trailing) > 0.01 || caret.Rect.X > n.Content.X+n.Content.W-1 {
		t.Fatalf("scroll %g caret %g want unscrolled at %g; content %+v", n.ScrollX, caret.Rect.X, trailing, n.Content)
	}
}

func TestPasswordCombiningGraphemesScrollAndPreedit(t *testing.T) {
	value := strings.Repeat("e\u0301", 12)
	view := func(f *Frame) { f.Root().Input("secret", &value, Password(true)) }
	r := geometryRuntime(t, `input {width:60px;height:28px;font-size:16px}`)
	r.Build(view)
	r.route(key("Tab"))
	r.Build(view)
	n := r.output.Tree.Children[0]
	s := r.edits[n.ID]
	if s.Cursor != len(value) {
		t.Fatalf("cursor %d", s.Cursor)
	}
	// 12 graphemes are 24 runes; counting runes used to miss the mask boundary.
	r.Redraw()
	r.Build(view)
	n = r.output.Tree.Children[0]
	if n.ScrollX <= 0 {
		t.Fatalf("caret at end of a long password did not scroll: %+v", n)
	}
	caret, ok := displayCommand(r, n.ID, "caret")
	if !ok || caret.Rect.X < n.Content.X-0.01 || caret.Rect.X > n.Content.X+n.Content.W {
		t.Fatalf("caret %+v outside content %+v", caret, n.Content)
	}

	// Move inside the value, then compose: origin is the painted caret.
	mid := len(strings.Repeat("e\u0301", 4))
	s.Move(mid, false)
	s.Preedit = "e\u0301猫"
	s.PreeditBegin = len("e\u0301")
	r.Redraw()
	r.Build(view)
	n = r.output.Tree.Children[0]
	caret, _ = displayCommand(r, n.ID, "caret")
	preedit, ok := displayCommand(r, n.ID, "preedit-text")
	// Independent expectation: the trailing edge of the 4th bullet (4 graphemes,
	// 8 runes, precede the caret) in the shaped masked text.
	want := rawTrailingEdge(t, r, n, 3)
	if !ok || math.Abs(caret.Rect.X-want) > 0.01 || math.Abs(preedit.Rect.X-want) > 0.01 {
		t.Fatalf("caret %g preedit %g, want after 4th bullet at %g", caret.Rect.X, preedit.Rect.X, want)
	}
	if preedit.Text != "••" {
		t.Fatalf("preedit text %q", preedit.Text)
	}
	data, err := json.Marshal(r.output.Display)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("e\u0301")) || bytes.Contains(data, []byte("猫")) {
		t.Fatalf("plaintext leaked: %s", data)
	}
	if value != strings.Repeat("e\u0301", 12) {
		t.Fatalf("model changed: %q", value)
	}
}

func TestPasswordPointerPlacementUsesGraphemeOffsets(t *testing.T) {
	value := "e\u0301a\u0300b"
	view := func(f *Frame) { f.Root().Input("secret", &value, Password(true)) }
	r := geometryRuntime(t, `input {width:200px;height:28px;font-size:16px}`)
	r.Build(view)
	n := r.output.Tree.Children[0]
	g := newEditorGeometry(&edit.State{Value: value}, n, r.output.Display, true)
	// Bullet k (0-based) ends the k+1'th grapheme; its raw trailing edge must
	// place the caret after that grapheme in the logical value.
	for k, want := range []int{len("e\u0301"), len("e\u0301a\u0300"), len(value)} {
		x := rawTrailingEdge(t, r, n, k)
		if got := g.nearest(x, n.Content.Y+1); got != want {
			t.Fatalf("nearest(%g) = %d, want %d", x, got, want)
		}
	}
	if got := g.nearest(n.Content.X, n.Content.Y+1); got != 0 {
		t.Fatalf("nearest at start = %d", got)
	}
}

const wrapValue = "one two three four five six"

// wrapFixture builds a focused, narrow textarea and returns the source offset
// shared by the end of visual line 0 and the start of line 1 ("one |two").
// The offset comes from the shaped runs, not from editorGeometry.
func wrapFixture(t *testing.T, height string) (*runtime, func(*Frame), *layout.Result, int) {
	t.Helper()
	r := geometryRuntime(t, "textarea {width:45px;height:"+height+";font-size:16px}")
	value := wrapValue
	view := func(f *Frame) { f.Root().Textarea("wrapped", &value) }
	r.Build(view)
	n := r.output.Tree.Children[0]
	if len(n.Lines) < 3 {
		t.Fatalf("expected wrapped lines, got %d", len(n.Lines))
	}
	first, second := n.Lines[0].Runs, n.Lines[1].Runs
	shared := first[len(first)-1].End
	if shared == 0 || shared != second[0].Start || shared >= len(value) {
		t.Fatalf("lines do not share a wrap offset: %+v %+v", first, second)
	}
	r.edits[n.ID].Move(shared, false) // ASCII: rune index == byte offset
	r.route(key("Tab"))
	r.Build(view)
	return r, view, r.output.Tree.Children[0], shared
}

// At a soft wrap the offset is the end of line 0 and the start of line 1.
// Painting and preedit use the end of line 0 (preceding character), and the
// scroll follows that same caret, while vertical navigation still treats the
// offset as being on the following line.
func TestSoftWrapCaretPolicyAgreesAcrossCallers(t *testing.T) {
	r, view, n, shared := wrapFixture(t, "100px")
	s := r.edits[n.ID]
	if s.Cursor != shared {
		t.Fatalf("cursor %d want %d", s.Cursor, shared)
	}
	wantX := rawTrailingEdge(t, r, n, shared-1)
	wantY := n.Content.Y
	nextLineY := n.Content.Y + n.Lines[0].Height
	s.Preedit = "x"
	r.Redraw()
	r.Build(view)
	n = r.output.Tree.Children[0]
	caret, ok := displayCommand(r, n.ID, "caret")
	if !ok {
		t.Fatal("missing caret")
	}
	preedit, ok := displayCommand(r, n.ID, "preedit-text")
	if !ok {
		t.Fatal("missing preedit")
	}
	for name, rect := range map[string]layout.Rect{"caret": caret.Rect, "preedit": preedit.Rect} {
		if math.Abs(rect.X-wantX) > 0.01 || math.Abs(rect.Y-wantY) > 0.01 || rect.Y == nextLineY {
			t.Errorf("%s at (%g,%g), want end of preceding line (%g,%g)", name, rect.X, rect.Y, wantX, wantY)
		}
	}
	if caret.Rect.H != n.Lines[0].Height {
		t.Errorf("caret height %g want line height %g", caret.Rect.H, n.Lines[0].Height)
	}
}

func TestSoftWrapScrollFollowsPrecedingLineCaret(t *testing.T) {
	// Content holds exactly one line. The caret at the wrap offset is on line
	// 0, so nothing scrolls; following line 1 instead would scroll.
	r, _, n, _ := wrapFixture(t, "22px")
	if n.Content.H >= 2*n.Lines[0].Height {
		t.Fatalf("fixture shows more than one line: %+v", n.Content)
	}
	if n.ScrollY != 0 {
		t.Fatalf("scrolled to the following line: %g", n.ScrollY)
	}
	if caret, ok := displayCommand(r, n.ID, "caret"); !ok || caret.Rect.Y != n.Content.Y {
		t.Fatalf("caret %+v content %+v", caret, n.Content)
	}
}

func TestSoftWrapVerticalNavigationKeepsFollowingLinePolicy(t *testing.T) {
	// Down from the shared offset moves off line 1 (to the start of line 2),
	// not off line 0; up moves off line 1 to line 0, whichever the caret paints.
	r, view, n, shared := wrapFixture(t, "100px")
	s := r.edits[n.ID]
	lineEnd := n.Lines[1].Runs[len(n.Lines[1].Runs)-1].End
	r.route(key("Down"))
	r.Build(view)
	if s.Cursor != lineEnd {
		t.Fatalf("down from %d = %d, want start of line 2 at %d", shared, s.Cursor, lineEnd)
	}
	if !s.PreferredXValid || s.PreferredX != n.Content.X {
		t.Fatalf("preferred x %v %g, want line-1 start %g", s.PreferredXValid, s.PreferredX, n.Content.X)
	}
	s.Move(shared, false)
	s.PreferredXValid = false
	r.route(key("Up"))
	r.Build(view)
	if s.Cursor != 0 {
		t.Fatalf("up from %d = %d, want start of line 0", shared, s.Cursor)
	}
}

// Scrolling uses the height of the caret's own line, not line 0's.
func TestScrollCaretUsesCaretLineHeight(t *testing.T) {
	r, _, n, _ := wrapFixture(t, "100px")
	id := n.ID
	// Line 0 is short and line 1 is tall. The caret is at the end of line 1.
	n.Lines[0].Height = 10
	n.Lines[1].Height = 30
	n.Content.H = 30
	n.ContentSize.H = 200
	s := r.edits[id]
	s.Move(n.Lines[1].Runs[len(n.Lines[1].Runs)-1].End-1, false) // before "two"'s trailing space edge: on line 1
	if r.state.scroll != nil {
		delete(r.state.scroll, id)
	}
	if !r.scrollCaret(r.committed, &r.output) {
		t.Fatal("caret below the viewport did not scroll")
	}
	// Caret line 1 spans y 10..40 in a 30px viewport: scroll by 10. Using line 0's
	// height (10) would end at 20 and not scroll at all.
	if got := r.state.scroll[id].H; math.Abs(got-10) > 0.01 {
		t.Fatalf("scroll %g, want 10 (caret line bottom 40 - viewport 30)", got)
	}
}
