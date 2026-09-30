package layout

import (
	"encoding/json"
	"image"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bnema/nefergui/internal/css"
	"github.com/bnema/nefergui/internal/text"
)

func px(v float64) css.Length { return css.Length{Value: v, Unit: "px"} }
func node(id string, k Kind, s css.Style, children ...*Node) *Node {
	return &Node{ID: id, Kind: k, Style: &s, Children: children}
}
func snapshot(t *testing.T, name string, out Output) {
	t.Helper()
	got, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", name+".json")
	if os.Getenv("NEFERGUI_UPDATE_SNAPSHOTS") == "1" {
		if err := os.MkdirAll("testdata", 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(got, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		t.Fatalf("snapshot missing %s; computed: %s", path, got)
	}
	if err != nil {
		t.Fatal(err)
	}
	var a, b any
	if err = json.Unmarshal(got, &a); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(want, &b); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("snapshot %s differs:\n%s", name, got)
	}
}
func TestNestedSnapshot(t *testing.T) {
	e := css.Compile(css.UA(), css.Parse(`box {padding: 4px; border: 2px solid red; background: blue} image {width:50%;height:20px;margin: 3px}`))
	root := e.Compute(&css.Element{Type: "box"}, nil)
	child := e.Compute(&css.Element{Type: "image"}, root)
	out, err := Layout(node("root", Box, *root.Style, node("pic", Image, *child.Style)), Options{Width: 200, Height: 100})
	if err != nil {
		t.Fatal(err)
	}
	if out.Tree.Content.X != 6 || out.Tree.Children[0].Border.W != 94 {
		t.Fatalf("unexpected box: %+v", out.Tree)
	}
	snapshot(t, "nested", out)
}
func TestFlex(t *testing.T) {
	s := css.Style{Display: css.KeywordFlex, FlexDirection: css.KeywordRow, ColumnGap: px(10), FlexWrap: css.KeywordWrap}
	child := css.Style{Display: css.KeywordBlock, Width: px(50), Height: px(12), FlexGrow: 1, FlexShrink: 1}
	root := node("r", Row, s, node("a", Box, child), node("b", Box, child), node("c", Box, child))
	out, err := Layout(root, Options{Width: 120, Height: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Tree.Children) != 3 || out.Tree.Children[0].Border.W != 55 || out.Tree.Children[1].Border.X != 65 || out.Tree.Children[2].Border.Y != 12 {
		t.Fatalf("flex wrap: %+v", out.Tree.Children)
	}
	snapshot(t, "flex", out)
	s.FlexWrap = css.KeywordNowrap
	root.Style = &s
	out, err = Layout(root, Options{Width: 120, Height: 50})
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(out.Tree.Children[0].Border.W-100.0/3) > 1e-6 {
		t.Fatalf("shrink: %v", out.Tree.Children[0].Border.W)
	}
}
func TestStackScrollHit(t *testing.T) {
	child := css.Style{Display: css.KeywordBlock, Width: px(50), Height: px(50), BackgroundColor: css.Color{R: 1, A: 1}}
	s := css.Style{Display: css.KeywordBlock, Width: px(30), Height: px(30), OverflowX: css.KeywordHidden, OverflowY: css.KeywordScroll}
	root := node("scroll", Scroll, s, node("stack", Stack, css.Style{Display: css.KeywordBlock}, node("back", Box, child), node("front", Box, child)))
	out, err := Layout(root, Options{Width: 100, Height: 100})
	if err != nil {
		t.Fatal(err)
	}
	if Hit(out.Tree, 10, 10) != "front" || Hit(out.Tree, 45, 10) == "front" {
		t.Fatal("paint order/clip", out.Tree)
	}
	root.ScrollY = 10
	out, err = Layout(root, Options{Width: 100, Height: 100})
	if err != nil {
		t.Fatal(err)
	}
	if out.Tree.ScrollY != 10 || out.Tree.Children[0].Border.Y != -10 {
		t.Fatalf("scroll: %+v", out.Tree)
	}
	snapshot(t, "scroll", out)
}
func TestTextAndLimits(t *testing.T) {
	m := func(w float64) (Size, []text.Line, error) {
		if w > 20 {
			return Size{W: 20, H: 10}, nil, nil
		}
		return Size{W: 20, H: 30}, nil, nil
	}
	s := css.Style{Display: css.KeywordBlock, Width: px(15), Padding: css.LengthSides{Left: px(2), Right: px(2)}}
	n := node("text", Text, s)
	n.Content = "three lines"
	n.Measure = m
	out, err := Layout(n, Options{Width: 50, Height: 100})
	if err != nil {
		t.Fatal(err)
	}
	if out.Tree.Border.H != 30 || out.Tree.Content.W != 15 {
		t.Fatalf("wrap: %+v", out.Tree)
	}
	s.Width = px(math.NaN())
	s.Height = px(math.Inf(1))
	n.Style = &s
	out, err = Layout(n, Options{Width: math.Inf(1), Height: math.NaN()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = json.Marshal(out); err != nil {
		t.Fatal("non-finite output", err)
	}
}
func TestScale(t *testing.T) {
	for _, v := range []struct {
		scale float64
		x, w  int
	}{{1, 1, 3}, {1.25, 1, 4}, {1.5, 2, 4}, {2, 2, 6}} {
		x, _, w, _ := Physical(Rect{X: 1, Y: 0, W: 3, H: 4}, v.scale)
		if x != v.x || w != v.w {
			t.Fatalf("scale %v: %d,%d", v.scale, x, w)
		}
	}
}
func TestCSSMalformed(t *testing.T) {
	sheet := css.Parse(`box {width: nope; padding: 5px; border: bad} image {height: 10px}`)
	if len(sheet.Diagnostics) == 0 {
		t.Fatal("missing diagnostics")
	}
	e := css.Compile(css.UA(), sheet)
	root := e.Compute(&css.Element{Type: "box"}, nil)
	pic := e.Compute(&css.Element{Type: "image"}, root)
	out, err := Layout(node("r", Box, *root.Style, node("i", Image, *pic.Style)), Options{Width: 50, Height: 50})
	if err != nil || out.Tree.Children[0].Border.H != 10 {
		t.Fatalf("malformed CSS: %+v %v", out, err)
	}
}
func FuzzLayout(f *testing.F) {
	f.Add(float64(1), float64(10), float64(100))
	f.Add(math.NaN(), math.Inf(1), float64(-1))
	f.Fuzz(func(t *testing.T, w, p, g float64) {
		s := css.Style{Display: css.KeywordFlex, Width: px(w), Padding: css.LengthSides{Left: px(p)}, ColumnGap: px(g)}
		n := node("r", Row, s, node("child", Image, css.Style{Display: css.KeywordBlock}, nil))
		out, err := Layout(n, Options{Width: 200, Height: 100})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = json.Marshal(out); err != nil {
			t.Fatal(err)
		}
	})
}
func BenchmarkLayout1000(b *testing.B) {
	children := make([]*Node, 1000)
	s := css.Style{Display: css.KeywordBlock, Height: px(2)}
	for i := range children {
		children[i] = node("child", Box, s)
	}
	root := node("root", Column, css.Style{Display: css.KeywordFlex, FlexDirection: css.KeywordColumn}, children...)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := Layout(root, Options{Width: 600, Height: 2200}); err != nil {
			b.Fatal(err)
		}
	}
}

func TestTextEngineWrap(t *testing.T) {
	catalog, err := text.Load(text.DirectorySource("../../testdata/fonts"))
	if err != nil {
		t.Fatal(err)
	}
	engine := text.NewEngine(catalog)
	s := css.Compile(css.UA(), css.Parse("text {width: 60px; font-size: 16px}")).Compute(&css.Element{Type: "text"}, nil).Style
	n := node("words", Text, *s)
	n.Content = "alpha beta gamma delta"
	out, err := Layout(n, Options{Width: 200, Height: 200, TextEngine: engine})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Tree.Lines) < 2 || out.Tree.Content.H <= 16 {
		t.Fatalf("text did not wrap: %+v", out.Tree)
	}
	found := false
	for _, cmd := range out.Display {
		if cmd.Op == "text" {
			found = len(cmd.Runs) > 0
			for _, run := range cmd.Runs {
				if run.Face == nil || run.FaceID != run.Face.ID || run.Size != 16 {
					t.Fatalf("missing raster identity: %+v", run)
				}
				data, err := json.Marshal(run)
				if err != nil {
					t.Fatal(err)
				}
				var encoded map[string]any
				if err := json.Unmarshal(data, &encoded); err != nil {
					t.Fatal(err)
				}
				if _, leaked := encoded["Face"]; leaked || encoded["FaceID"] != run.FaceID || encoded["Size"] != 16.0 {
					t.Fatalf("serialized raster identity: %s", data)
				}
			}
		}
	}
	if !found {
		t.Fatal("missing glyph runs")
	}
}

func TestPaintStylesAndFlexDefaults(t *testing.T) {
	en := css.Compile(css.UA(), css.Parse(`row {height: 24px; align-items: center; gap: 4px} box { width: 12px; height: 4px; background: red; border: 1px solid green; border-radius: 3px; outline: 2px solid blue; box-shadow: 1px 2px 2px black}`))
	parent := en.Compute(&css.Element{Type: "row"}, nil)
	item := en.Compute(&css.Element{Type: "box"}, parent)
	out, err := Layout(node("row", Row, *parent.Style, node("item", Box, *item.Style)), Options{Width: 100, Height: 50})
	if err != nil {
		t.Fatal(err)
	}
	if out.Tree.Children[0].Border.Y != 9 {
		t.Fatalf("align center: %v", out.Tree.Children[0].Border.Y)
	}
	ops := []string{}
	for _, cmd := range out.Display {
		ops = append(ops, cmd.Op)
		if cmd.Op == "border" && (cmd.Colors.Top.G != 128.0/255 || cmd.Radii[0] != 3) {
			t.Fatalf("border: %+v", cmd)
		}
	}
	if !reflect.DeepEqual(ops, []string{"shadow", "rect", "border", "outline"}) {
		t.Fatal(ops)
	}
}
func TestFlexOverridesAndConstraints(t *testing.T) {
	e := css.Compile(css.UA(), css.Parse(`row {display:block} box {width:50%; min-width: 60px; max-width:80px; height:10px; box-sizing:border-box; padding:5px}`))
	p := e.Compute(&css.Element{Type: "row"}, nil)
	ch := e.Compute(&css.Element{Type: "box"}, p)
	out, err := Layout(node("r", Row, *p.Style, node("one", Box, *ch.Style), node("two", Box, *ch.Style)), Options{Width: 100, Height: 100})
	if err != nil {
		t.Fatal(err)
	}
	if out.Tree.Children[0].Border.W != 60 || out.Tree.Children[1].Border.Y != 10 {
		t.Fatalf("override constraints: %+v", out.Tree.Children)
	}
}

func TestIntrinsicImageRatio(t *testing.T) {
	n := node("img", Image, css.Style{Display: css.KeywordBlock, Width: px(80)})
	n.ImageSize = Size{W: 40, H: 20}
	img := image.NewRGBA(image.Rect(0, 0, 40, 20))
	n.Image = img
	out, err := Layout(n, Options{Width: 100, Height: 100})
	if err != nil {
		t.Fatal(err)
	}
	if out.Tree.Border.W != 80 || out.Tree.Border.H != 40 {
		t.Fatalf("ratio: %+v", out.Tree.Border)
	}
	found := false
	for _, cmd := range out.Display {
		if cmd.Op == "image" {
			found = true
			if cmd.Image != img || cmd.Rect != out.Tree.Content {
				t.Fatalf("image command: %+v", cmd)
			}
		}
	}
	if !found {
		t.Fatal("missing image command")
	}
	n.Style = &css.Style{Display: css.KeywordBlock, Height: px(10)}
	out, err = Layout(n, Options{Width: 100, Height: 100})
	if err != nil {
		t.Fatal(err)
	}
	if out.Tree.Border.W != 20 || out.Tree.Border.H != 10 {
		t.Fatalf("ratio: %+v", out.Tree.Border)
	}
}

func TestSizingSpaceAndBorderStyle(t *testing.T) {
	for _, tc := range []struct {
		box  css.Keyword
		want float64
	}{{css.KeywordContentBox, 80}, {css.KeywordBorderBox, 60}} {
		s := css.Style{Display: css.KeywordBlock, BoxSizing: tc.box, Width: px(50), MinWidth: px(60), Padding: css.LengthSides{Left: px(10), Right: px(10)}}
		out, err := Layout(node("b", Box, s), Options{Width: 100, Height: 100})
		if err != nil {
			t.Fatal(err)
		}
		if out.Tree.Border.W != tc.want {
			t.Fatalf("box sizing %v: %v != %v", tc.box, out.Tree.Border.W, tc.want)
		}
	}
	s := css.Style{Display: css.KeywordBlock, Width: px(50), BorderWidth: css.LengthSides{Top: px(9), Right: px(9), Bottom: px(9), Left: px(9)}, BorderStyle: css.KeywordSides{Top: css.KeywordSolid}}
	out, err := Layout(node("b", Box, s), Options{Width: 100, Height: 100})
	if err != nil {
		t.Fatal(err)
	}
	if out.Tree.Border.W != 50 || out.Tree.Content.Y != 9 || out.Tree.BorderWidths != (Edges{Top: 9}) {
		t.Fatalf("none border reserved: %+v", out.Tree)
	}
	for _, cmd := range out.Display {
		if cmd.Op == "border" && cmd.Widths != (Edges{Top: 9}) {
			t.Fatalf("none painted: %+v", cmd)
		}
	}
}

func TestFlexFreezeAndInsets(t *testing.T) {
	parent := css.Style{Display: css.KeywordFlex, FlexDirection: css.KeywordRow}
	first := css.Style{Display: css.KeywordBlock, Width: px(50), Height: px(10), FlexGrow: 1, FlexShrink: 1, MaxWidth: px(60)}
	second := first
	second.MaxWidth = css.Length{}
	root := node("r", Row, parent, node("a", Box, first), node("b", Box, second))
	out, err := Layout(root, Options{Width: 200, Height: 30})
	if err != nil {
		t.Fatal(err)
	}
	a, b := out.Tree.Children[0].Border, out.Tree.Children[1].Border
	if a.W != 60 || b.W != 140 || b.X != 60 {
		t.Fatalf("grow freeze: %+v %+v", a, b)
	}
	first.MaxWidth = css.Length{}
	first.MinWidth = px(40)
	second.MinWidth = css.Length{}
	root.Children[0].Style = &first
	root.Children[1].Style = &second
	out, err = Layout(root, Options{Width: 60, Height: 30})
	if err != nil {
		t.Fatal(err)
	}
	a, b = out.Tree.Children[0].Border, out.Tree.Children[1].Border
	if a.W != 40 || b.W != 20 || b.X != 40 {
		t.Fatalf("shrink freeze: %+v %+v", a, b)
	}
	first = css.Style{Display: css.KeywordBlock, Width: px(50), Height: px(10), Padding: css.LengthSides{Left: px(10), Right: px(10)}}
	root.Children = root.Children[:1]
	root.Children[0].Style = &first
	out, err = Layout(root, Options{Width: 70, Height: 30})
	if err != nil {
		t.Fatal(err)
	}
	if out.Tree.Children[0].Border.W != 70 || out.Tree.Children[0].Content.W != 50 {
		t.Fatalf("flex insets: %+v", out.Tree.Children[0])
	}
}

func TestOverflowAxes(t *testing.T) {
	s := css.Style{Display: css.KeywordBlock, Width: px(30), Height: px(30), OverflowX: css.KeywordVisible, OverflowY: css.KeywordHidden}
	child := css.Style{Display: css.KeywordBlock, Width: px(50), Height: px(50)}
	n := node("parent", Box, s, node("child", Box, child))
	n.ScrollX = 10
	n.ScrollY = 10
	out, err := Layout(n, Options{Width: 100, Height: 100})
	if err != nil {
		t.Fatal(err)
	}
	if out.Tree.ScrollX != 0 || out.Tree.ScrollY != 0 || out.Tree.Children[0].Border.X != 0 || out.Tree.Clip.W != 2*limit || out.Tree.Clip.H != 30 {
		t.Fatalf("axes: %+v", out.Tree)
	}
	if Hit(out.Tree, 40, 10) != "child" || Hit(out.Tree, 10, 40) == "child" {
		t.Fatalf("clip hit: %s %s", Hit(out.Tree, 40, 10), Hit(out.Tree, 10, 40))
	}
	if out.Display[0].Op != "clip-push" || out.Display[0].Rect.W != 2*limit {
		t.Fatalf("clip command: %+v", out.Display[0])
	}
}

func TestTextCSSMetrics(t *testing.T) {
	catalog, err := text.Load(text.DirectorySource("../../testdata/fonts"))
	if err != nil {
		t.Fatal(err)
	}
	engine := text.NewEngine(catalog)
	st := css.Compile(css.UA(), css.Parse("text {width:60px;font-size:16px}")).Compute(&css.Element{Type: "text"}, nil).Style
	n := node("text", Text, *st)
	n.Content = "alpha beta gamma delta"
	base, err := Layout(n, Options{Width: 100, Height: 100, TextEngine: engine})
	if err != nil {
		t.Fatal(err)
	}
	if len(base.Tree.Lines) < 2 {
		t.Fatalf("expected wrapping: %+v", base.Tree)
	}
	baseLine1, baseH := base.Tree.Lines[1].Baseline, base.Tree.Border.H // values, not shared slices
	st.LineHeightKind = css.KeywordNumber
	st.LineHeightNumber = 2
	n.Style = st
	spaced, err := Layout(n, Options{Width: 100, Height: 100, TextEngine: engine})
	if err != nil {
		t.Fatal(err)
	}
	if len(spaced.Tree.Lines) < 2 || spaced.Tree.Lines[0].Height != 32 || spaced.Tree.Border.H != float64(len(spaced.Tree.Lines))*32 || spaced.Tree.Lines[1].Baseline-spaced.Tree.Lines[0].Baseline != 32 {
		t.Fatalf("line height: %+v", spaced.Tree)
	}
	// The engine caches measurements: the line-height adjustment must not leak
	// into later layouts of the same text.
	st.LineHeightKind = css.KeywordNormal
	n.Style = st
	again, err := Layout(n, Options{Width: 100, Height: 100, TextEngine: engine})
	if err != nil {
		t.Fatal(err)
	}
	if again.Tree.Lines[1].Baseline != baseLine1 || again.Tree.Border.H != baseH {
		t.Fatalf("cached measurement mutated: %v vs %v", again.Tree.Lines[1].Baseline, baseLine1)
	}
	st.LineHeightKind = css.KeywordNumber
	n.Style = st
	st.TextAlign = css.KeywordCenter
	aligned, err := Layout(n, Options{Width: 100, Height: 100, TextEngine: engine})
	if err != nil {
		t.Fatal(err)
	}
	var original, center Command
	for _, cmd := range spaced.Display {
		if cmd.Op == "text" {
			original = cmd
		}
	}
	for _, cmd := range aligned.Display {
		if cmd.Op == "text" {
			center = cmd
		}
	}
	want := safe(aligned.Tree.Content.W-aligned.Tree.Lines[0].Width) / 2
	if want <= 0 || math.Abs(center.Runs[0].X-original.Runs[0].X-want) > 1e-6 || math.Abs(center.Runs[0].Glyphs[0].X-original.Runs[0].Glyphs[0].X-want) > 1e-6 {
		t.Fatalf("align offset %v: %v %v", want, original.Runs[0].X, center.Runs[0].X)
	}
	st.LetterSpacing = px(4)
	st.LetterSpacingNormal = false
	st.TextAlign = css.KeywordStart
	n.Style = st
	letter, err := Layout(n, Options{Width: 100, Height: 100, TextEngine: engine})
	if err != nil {
		t.Fatal(err)
	}
	if letter.Tree.Lines[0].Width <= base.Tree.Lines[0].Width || letter.Tree.ContentSize.H < base.Tree.ContentSize.H {
		t.Fatalf("letter spacing: %v -> %v", base.Tree.Lines[0].Width, letter.Tree.Lines[0].Width)
	}
}

func TestFlexZeroFreeSpaceViolations(t *testing.T) {
	a := css.Style{Display: css.KeywordBlock, Width: px(50), Height: px(10), MinWidth: px(80), FlexShrink: 1}
	b := css.Style{Display: css.KeywordBlock, Width: px(50), Height: px(10), MaxWidth: px(20), FlexShrink: 1}
	root := node("row", Row, css.Style{Display: css.KeywordFlex}, node("min", Box, a), node("max", Box, b))
	out, err := Layout(root, Options{Width: 100, Height: 20})
	if err != nil {
		t.Fatal(err)
	}
	first, second := out.Tree.Children[0].Border, out.Tree.Children[1].Border
	if first.W != 80 || second.W != 20 || second.X != 80 {
		t.Fatalf("zero-free-space violations: %+v %+v", first, second)
	}
}
func TestFlexContentBoxMinLessThanInset(t *testing.T) {
	child := css.Style{Display: css.KeywordBlock, Width: px(5), MinWidth: px(10), Padding: css.LengthSides{Left: px(10), Right: px(10)}}
	root := node("row", Row, css.Style{Display: css.KeywordFlex}, node("child", Box, child))
	out, err := Layout(root, Options{Width: 30, Height: 20})
	if err != nil {
		t.Fatal(err)
	}
	if got := out.Tree.Children[0].Border.W; got != 30 {
		t.Fatalf("content-box min 10 + insets 20: got %v want 30", got)
	}
}

func TestDirectionalTextAlignment(t *testing.T) {
	catalog, err := text.Load(text.DirectorySource("../../testdata/fonts"))
	if err != nil {
		t.Fatal(err)
	}
	engine := text.NewEngine(catalog)
	for _, content := range []string{"سلام", "שלום", "abc"} {
		st := css.Compile(css.UA(), css.Parse("text {width:120px;font-size:16px}")).Compute(&css.Element{Type: "text"}, nil).Style
		n := node("text", Text, *st)
		n.Content = content
		st.TextAlign = css.KeywordLeft
		n.Style = st
		left, err := Layout(n, Options{Width: 200, Height: 100, TextEngine: engine})
		if err != nil {
			t.Fatal(err)
		}
		st.TextAlign = css.KeywordStart
		start, err := Layout(n, Options{Width: 200, Height: 100, TextEngine: engine})
		if err != nil {
			t.Fatal(err)
		}
		st.TextAlign = css.KeywordEnd
		end, err := Layout(n, Options{Width: 200, Height: 100, TextEngine: engine})
		if err != nil {
			t.Fatal(err)
		}
		var leftX, startX, endX float64
		for _, cmd := range left.Display {
			if cmd.Op == "text" {
				leftX = cmd.Runs[0].X
			}
		}
		for _, cmd := range start.Display {
			if cmd.Op == "text" {
				startX = cmd.Runs[0].X
			}
		}
		for _, cmd := range end.Display {
			if cmd.Op == "text" {
				endX = cmd.Runs[0].X
			}
		}
		gap := safe(start.Tree.Content.W - start.Tree.Lines[0].Width)
		if gap <= 0 {
			t.Fatalf("%q: no free space", content)
		}
		if content == "abc" {
			if startX != leftX || math.Abs(endX-leftX-gap) > 1e-6 {
				t.Fatalf("LTR %q: left=%v start=%v end=%v gap=%v", content, leftX, startX, endX, gap)
			}
		} else if math.Abs(startX-leftX-gap) > 1e-6 || endX != leftX {
			t.Fatalf("RTL %q: left=%v start=%v end=%v gap=%v", content, leftX, startX, endX, gap)
		}
	}
}

func TestFlexStackRejectsRect(t *testing.T) {
	for _, direction := range []css.Keyword{css.KeywordRow, css.KeywordColumn} {
		for _, nested := range []bool{false, true} {
			child := node("rect", Box, css.Style{Display: css.KeywordBlock})
			child.Rect, child.HasRect = Rect{X: 10, Y: 20, W: 30, H: 40}, true
			stack := node("stack", Stack, css.Style{Display: css.KeywordFlex, FlexDirection: direction}, child)
			root := stack
			if nested {
				root = node("root", Row, css.Style{Display: css.KeywordFlex}, stack)
			}
			if _, err := Layout(root, Options{Width: 200, Height: 100}); err == nil || err.Error() != "layout: Rect is unsupported in a flex-styled Stack" {
				t.Fatalf("direction=%v nested=%v: err=%v", direction, nested, err)
			}
			// Hidden children do not participate in layout or validation.
			child.Style.Display = css.KeywordNone
			if _, err := Layout(root, Options{Width: 200, Height: 100}); err != nil {
				t.Fatalf("hidden Rect: %v", err)
			}
		}
	}
}

func TestStackAbsoluteRect(t *testing.T) {
	margin := css.Style{Display: css.KeywordBlock, Margin: css.LengthSides{Top: px(7), Left: px(9)}, Width: px(500), Height: px(500)}
	a := node("a", Box, margin)
	a.Rect, a.HasRect = Rect{X: 10.5, Y: 20, W: 30, H: 40}, true
	flow := node("flow", Box, css.Style{Display: css.KeywordBlock, Height: px(12)})
	stack := node("s", Stack, css.Style{Display: css.KeywordBlock, Padding: css.LengthSides{Top: px(3), Left: px(5)}}, a, flow)
	out, err := Layout(node("root", Column, css.Style{Display: css.KeywordBlock}, stack), Options{Width: 200, Height: 100})
	if err != nil {
		t.Fatal(err)
	}
	kids := out.Tree.Children[0].Children
	// Rect is border-box relative to the stack content box; authored size and margin do not apply.
	if got, want := kids[0].Border, (Rect{X: 5 + 10.5, Y: 3 + 20, W: 30, H: 40}); got != want {
		t.Fatalf("absolute border %+v want %+v", got, want)
	}
	// Non-Rect children keep normal stack placement at the content origin.
	if got := kids[1].Border; got.X != 5 || got.Y != 3 || got.H != 12 {
		t.Fatalf("flow child %+v", got)
	}
	// The stack's auto size covers the absolute child's extent.
	if got := out.Tree.Children[0].ContentSize; got.W < 40.5 || got.H < 60 {
		t.Fatalf("content size %+v", got)
	}
	// HasRect is ignored outside a Stack.
	b := node("b", Box, css.Style{Display: css.KeywordBlock, Height: px(9)})
	b.Rect, b.HasRect = Rect{X: 50, Y: 50, W: 5, H: 5}, true
	out, err = Layout(node("col", Column, css.Style{Display: css.KeywordBlock}, b), Options{Width: 200, Height: 100})
	if err != nil {
		t.Fatal(err)
	}
	if got := out.Tree.Children[0].Border; got.X != 0 || got.H != 9 {
		t.Fatalf("Rect leaked outside Stack: %+v", got)
	}
}
