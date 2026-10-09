package layout

import (
	"testing"

	"github.com/bnema/nefergui/internal/css"
	"github.com/bnema/nefergui/internal/text"
)

func naturalEngine(t *testing.T) *text.Engine {
	t.Helper()
	catalog, err := text.Load(text.DirectorySource("../../testdata/fonts"))
	if err != nil {
		t.Fatal(err)
	}
	return text.NewEngine(catalog)
}

func textNode(id, content string) *Node {
	n := node(id, Text, css.Style{Display: css.KeywordBlock, FontSize: px(16)})
	n.Content = content
	return n
}

func TestNaturalColumnOfTexts(t *testing.T) {
	engine := naturalEngine(t)
	short, long := textNode("a", "ab"), textNode("b", "a much longer line")
	column := node("c", Column, css.Style{Display: css.KeywordFlex, FlexDirection: css.KeywordColumn, Padding: css.LengthSides{Top: px(4), Right: px(6), Bottom: px(4), Left: px(6)}}, short, long)
	opts := Options{Width: 1000, Height: 1 << 20, TextEngine: engine}
	got, err := Natural(column, opts)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := Natural(short, opts)
	if err != nil {
		t.Fatal(err)
	}
	wl, err := Natural(long, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !(wl.W > ws.W) || ws.W <= 0 {
		t.Fatalf("text widths short=%g long=%g", ws.W, wl.W)
	}
	if got.W != wl.W+12 || got.H != ws.H+wl.H+8 {
		t.Fatalf("column = %+v, want %g x %g", got, wl.W+12, ws.H+wl.H+8)
	}
}

func TestNaturalRowSumsWithGap(t *testing.T) {
	engine := naturalEngine(t)
	a, b := textNode("a", "alpha"), textNode("b", "beta")
	opts := Options{Width: 1000, Height: 1 << 20, TextEngine: engine}
	wa, _ := Natural(a, opts)
	wb, _ := Natural(b, opts)
	row := node("r", Row, css.Style{Display: css.KeywordFlex, ColumnGap: px(10)}, a, b)
	got, err := Natural(row, opts)
	if err != nil {
		t.Fatal(err)
	}
	if got.W != wa.W+wb.W+10 || got.H != max(wa.H, wb.H) {
		t.Fatalf("row = %+v, want W %g H %g", got, wa.W+wb.W+10, max(wa.H, wb.H))
	}
}

func TestNaturalExplicitWidthWins(t *testing.T) {
	engine := naturalEngine(t)
	child := textNode("a", "alpha beta gamma delta epsilon zeta")
	box := node("box", Box, css.Style{Display: css.KeywordBlock, Width: px(90), Padding: css.LengthSides{Left: px(5), Right: px(5)}}, child)
	got, err := Natural(box, Options{Width: 1000, Height: 1 << 20, TextEngine: engine})
	if err != nil {
		t.Fatal(err)
	}
	if got.W != 100 {
		t.Fatalf("width = %g, want 100 (90 content + 10 padding)", got.W)
	}
	if got.H <= 16 {
		t.Fatalf("content did not wrap inside the explicit width: height %g", got.H)
	}
	bb := node("bb", Box, css.Style{Display: css.KeywordBlock, BoxSizing: css.KeywordBorderBox, Width: px(90), Height: px(30), Padding: css.LengthSides{Left: px(5), Right: px(5)}})
	got, err = Natural(bb, Options{Width: 1000, Height: 1000})
	if err != nil || got.W != 90 || got.H != 30 {
		t.Fatalf("border-box = %+v err=%v", got, err)
	}
}

func TestNaturalTextWrapsAtOptionsWidth(t *testing.T) {
	engine := naturalEngine(t)
	long := textNode("a", "alpha beta gamma delta epsilon zeta eta theta iota kappa")
	wide, err := Natural(long, Options{Width: 2000, Height: 1 << 20, TextEngine: engine})
	if err != nil {
		t.Fatal(err)
	}
	narrow, err := Natural(long, Options{Width: 100, Height: 1 << 20, TextEngine: engine})
	if err != nil {
		t.Fatal(err)
	}
	// A wrapped line may measure slightly over the limit (103.75 for 100 here);
	// callers clamp to the limit, as Renderer.Measure does.
	if narrow.W >= wide.W || narrow.H <= wide.H {
		t.Fatalf("narrow=%+v wide=%+v", narrow, wide)
	}
}

func TestNaturalNilAndHidden(t *testing.T) {
	if got, err := Natural(nil, Options{}); err != nil || got != (Size{}) {
		t.Fatalf("nil: %+v %v", got, err)
	}
	hidden := node("h", Box, css.Style{Display: css.KeywordNone})
	if got, err := Natural(hidden, Options{Width: 100}); err != nil || got != (Size{}) {
		t.Fatalf("hidden: %+v %v", got, err)
	}
}
