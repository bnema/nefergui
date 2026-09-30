package layout

import (
	"fmt"
	"testing"

	"github.com/bnema/nefergui/internal/css"
	"github.com/bnema/nefergui/internal/text"
)

// paintFixture is a product-like tree: cards with background, border, shadows,
// a clip and shaped text children.
func paintFixture(tb testing.TB, cards int) (*Node, Options) {
	tb.Helper()
	catalog, err := text.Load(text.DirectorySource("../../testdata/fonts"))
	if err != nil {
		tb.Fatal(err)
	}
	engine := text.NewEngine(catalog)
	card := css.Style{
		Display: css.KeywordBlock, Height: px(60), Padding: css.LengthSides{Top: px(4), Left: px(4)},
		BackgroundColor: css.Color{B: 1, A: 1}, BorderWidth: css.LengthSides{Top: px(1), Right: px(1), Bottom: px(1), Left: px(1)},
		BorderStyle: css.KeywordSides{Top: css.KeywordSolid, Right: css.KeywordSolid, Bottom: css.KeywordSolid, Left: css.KeywordSolid},
		OverflowX:   css.KeywordHidden, OverflowY: css.KeywordHidden,
		BoxShadow: []css.Shadow{{Y: px(2), Blur: px(4), Color: css.Color{A: .3}}, {Inset: true, Blur: px(2), Color: css.Color{A: .2}}},
	}
	label := css.Style{Display: css.KeywordBlock, FontSize: px(14), Color: css.Color{A: 1}}
	children := make([]*Node, cards)
	for i := range children {
		t1 := node("t", Text, label)
		t1.Content = fmt.Sprintf("Élément %d — une interface native écrite en Go", i)
		children[i] = node("card", Box, card, t1)
	}
	return node("root", Column, css.Style{Display: css.KeywordBlock}, children...), Options{Width: 400, Height: 3000, TextEngine: engine}
}

// BenchmarkPaint measures only context.paint on an already placed tree.
func BenchmarkPaint(b *testing.B) {
	root, opts := paintFixture(b, 100)
	ctx := context{options: opts}
	tree, err := ctx.place(root, 0, 0, opts.Width, opts.Height, nil, nil, 0)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if display := ctx.display(root, tree); len(display) == 0 {
			b.Fatal("empty")
		}
	}
}

func BenchmarkLayoutPaintTree(b *testing.B) {
	root, opts := paintFixture(b, 100)
	if _, err := Layout(root, opts); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Layout(root, opts); err != nil {
			b.Fatal(err)
		}
	}
}
