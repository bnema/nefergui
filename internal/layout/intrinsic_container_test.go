package layout

import (
	"testing"

	"github.com/bnema/nefergui/internal/css"
)

func TestAutoFlexColumnReservesChildrenAndGaps(t *testing.T) {
	leaf := func(id string) *Node {
		return node(id, Box, css.Style{Display: css.KeywordBlock, Height: px(20), Width: px(80)})
	}
	column := node("column", Column, css.Style{Display: css.KeywordFlex, FlexDirection: css.KeywordColumn, RowGap: px(8)}, leaf("one"), leaf("two"))
	out, err := Layout(node("root", Box, css.Style{Display: css.KeywordBlock, Width: px(300), Height: px(200)}, column), Options{Width: 300, Height: 200})
	if err != nil {
		t.Fatal(err)
	}
	got := out.Tree.Children[0]
	if got.Content.H != 48 || got.Children[1].Border.Y != 28 {
		t.Fatalf("column geometry: %+v", got)
	}
}

func TestNestedFlexPercentWidthUsesNaturalSize(t *testing.T) {
	leaf := node("leaf", Box, css.Style{Display: css.KeywordBlock, Width: px(80), Height: px(24)})
	percent := node("percent", Box, css.Style{Display: css.KeywordBlock, Width: css.Length{Value: 100, Unit: "%"}}, leaf)
	nested := node("nested", Column, css.Style{Display: css.KeywordFlex, FlexDirection: css.KeywordColumn}, percent)
	out, err := Layout(node("root", Row, css.Style{Display: css.KeywordFlex, Width: px(300), Height: px(100)}, nested), Options{Width: 300, Height: 100})
	if err != nil {
		t.Fatal(err)
	}
	if got := out.Tree.Children[0].Border.W; got != 80 {
		t.Fatalf("nested width=%g", got)
	}
}

func TestAutoFlexHeightBoundsBeforeDistribution(t *testing.T) {
	for _, tc := range []struct {
		name        string
		min, max    css.Length
		child, want float64
	}{{"minimum", px(200), css.Length{}, 0, 200}, {"maximum", css.Length{}, px(40), 100, 40}} {
		t.Run(tc.name, func(t *testing.T) {
			child := node("child", Box, css.Style{Display: css.KeywordBlock, Height: px(tc.child), FlexGrow: 1, FlexShrink: 1})
			column := node("column", Column, css.Style{Display: css.KeywordFlex, FlexDirection: css.KeywordColumn, MinHeight: tc.min, MaxHeight: tc.max}, child)
			out, err := Layout(node("root", Box, css.Style{Display: css.KeywordBlock, Width: px(300), Height: px(300)}, column), Options{Width: 300, Height: 300})
			if err != nil {
				t.Fatal(err)
			}
			got := out.Tree.Children[0]
			if got.Border.H != tc.want || got.Children[0].Border.H != tc.want {
				t.Fatalf("container=%g child=%g want=%g", got.Border.H, got.Children[0].Border.H, tc.want)
			}
		})
	}
}
