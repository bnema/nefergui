package css

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestMemoIdentityAndEviction(t *testing.T) {
	en := Compile(UA(), Parse(`.on button {color:red} .on:hover > button {background: blue} #x {opacity:.5} button {color:green}`))
	root := Element{Type: "app", Classes: []string{"on"}}
	child := Element{Type: "button"}
	a := en.Compute(&root, nil)
	first := en.Compute(&child, a)
	en.EndFrame()
	if en.Compute(&root, nil) != a || en.Compute(&child, a) != first {
		t.Fatal("memo miss")
	}
	root.State = Hover
	b := en.Compute(&root, nil)
	second := en.Compute(&child, b)
	if second == first || second.Style.BackgroundColor != namedColors["blue"] {
		t.Fatal("state invalidation", second.Style)
	}
	root.State = 0
	root.Classes = []string{"off"}
	c := en.Compute(&root, nil)
	third := en.Compute(&child, c)
	if third == first || third.Style.Color != namedColors["green"] {
		t.Fatal("class invalidation")
	}
	inline1, ds := ParseInline("color: red; opacity: .2")
	if len(ds) > 0 {
		t.Fatal(ds)
	}
	inline2, _ := ParseInline("color: blue; opacity: .2")
	child.Inline = inline1
	fourth := en.Compute(&child, a)
	child.Inline = inline2
	fifth := en.Compute(&child, a)
	if fourth == fifth || fourth.Style.Color != namedColors["red"] || fifth.Style.Color != namedColors["blue"] {
		t.Fatal("inline pointer invalidation")
	}
	for i := 0; i < 4; i++ {
		en.EndFrame()
	}
	if len(en.memo) != 0 {
		t.Fatalf("retained %d stale nodes", len(en.memo))
	}
}
func TestCascadeVariablesAndSpecificity(t *testing.T) {
	tests := []struct {
		css, inline string
		want        Color
		padding     float64
	}{
		{`.x {color:red} .x {color:blue}`, "", namedColors["blue"], 0},
		{`.x {color:red!important} #id {color:blue}`, "", namedColors["red"], 0},
		{`.x {color:red!important}`, "color: blue", namedColors["red"], 0},
		{`.x {color:red!important}`, "color: blue!important", namedColors["blue"], 0},
		{`.x {--a: 3px; padding: var(--a); color:var(--missing,red)}`, "", namedColors["red"], 3},
		{`.x {color:green;color:var(--missing)}`, "", Color{A: 1}, 0},
	}
	for _, tt := range tests {
		en := Compile(UA(), Parse(tt.css))
		d, _ := ParseInline(tt.inline)
		s := en.Compute(&Element{Type: "box", ID: "id", Classes: []string{"x"}, Inline: d}, nil).Style
		if s.Color != tt.want || s.Padding.Top.Value != tt.padding {
			t.Fatalf("%s %s: %v padding %v", tt.css, tt.inline, s.Color, s.Padding.Top)
		}
	}
}
func TestClassSetOrder(t *testing.T) {
	en := Compile(UA(), Parse(`.a.b {color:red}`))
	a := en.Compute(&Element{Classes: []string{"a", "b"}}, nil)
	b := en.Compute(&Element{Classes: []string{"b", "a"}}, nil)
	if a != b || a.Style.Color != namedColors["red"] {
		t.Fatal("class sets must be order independent")
	}
}

func benchmarkTree() ([]Element, []int, Sheet) {
	var src strings.Builder
	types := []string{"app", "row", "column", "button", "text", "box"}
	for i := 0; i < 200; i++ {
		switch i % 5 {
		case 0:
			fmt.Fprintf(&src, "%s {padding:2px; color:#123}\n", types[i%len(types)])
		case 1:
			fmt.Fprintf(&src, ".c%d > button:hover {color:red}\n", i%12)
		case 2:
			fmt.Fprintf(&src, "#id%d {opacity:.5}\n", i%24)
		case 3:
			fmt.Fprintf(&src, ".c%d text {font-size:18px}\n", i%12)
		case 4:
			fmt.Fprintf(&src, ".c%d {margin:1px}\n", i%12)
		}
	}
	nodes := make([]Element, 1000)
	parents := make([]int, len(nodes))
	for i := range nodes {
		nodes[i].Type = types[i%len(types)]
		nodes[i].Classes = []string{fmt.Sprintf("c%d", i%12)}
		if i%7 == 0 {
			nodes[i].ID = fmt.Sprintf("id%d", i%24)
		}
		if i%11 == 0 {
			nodes[i].State = Hover
		}
		parents[i] = (i - 1) / 3
	}
	return nodes, parents, Parse(src.String())
}
func computeTree(en *Engine, nodes []Element, parents []int, results []*Computed) {
	for i := range nodes {
		var parent *Computed
		if i > 0 {
			parent = results[parents[i]]
		}
		results[i] = en.Compute(&nodes[i], parent)
	}
}
func BenchmarkEngine(b *testing.B) {
	nodes, parents, sheet := benchmarkTree()
	for _, mode := range []string{"cold", "warm", "state-change"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			en := Compile(UA(), sheet)
			results := make([]*Computed, len(nodes))
			computeTree(en, nodes, parents, results)
			en.EndFrame()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if mode == "cold" {
					en = Compile(UA(), sheet)
				}
				if mode == "state-change" {
					nodes[10].State ^= Hover
				}
				computeTree(en, nodes, parents, results)
				en.EndFrame()
			}
		})
	}
}

func TestTypedPropertiesAndVariables(t *testing.T) {
	en := Compile(UA(), Parse(`app {color:#123;font-size:20px; --pair: 1em 2px; --ink: rebeccapurple} app > button {padding:var(--pair);border:2px solid var(--ink);font-family:"A B", sans-serif;box-shadow:1px 2px 3px red;line-height:1.5} button:focus-visible {opacity:.4}`))
	root := en.Compute(&Element{Type: "app"}, nil)
	c := en.Compute(&Element{Type: "button", State: FocusVisible}, root).Style
	if c.Padding.Top.Value != 20 || c.Padding.Right.Value != 2 || c.BorderWidth.Left.Value != 2 || c.BorderColor.Left != namedColors["rebeccapurple"] || c.BorderStyle.Left != KeywordSolid || c.Opacity != .4 || len(c.FontFamily) != 2 || c.FontFamily[0] != "A B" || len(c.BoxShadow) != 1 || c.LineHeightKind != KeywordNumber || c.LineHeightNumber != 1.5 {
		t.Fatalf("typed style: %+v", c)
	}
}
func TestDuplicateSelectorListUsesHighestMatchingSpecificity(t *testing.T) {
	en := Compile(Sheet{}, Parse(`button, #x {color:red} button {color:blue}`))
	if got := en.Compute(&Element{Type: "button", ID: "x"}, nil).Style.Color; got != namedColors["red"] {
		t.Fatal(got)
	}
}
func TestEndFrameKeepsAncestorsOfLiveDescendant(t *testing.T) {
	en := Compile(UA(), Parse(`.a button {color:red}`))
	p := en.Compute(&Element{Type: "app", Classes: []string{"a"}}, nil)
	c := en.Compute(&Element{Type: "button"}, p)
	for i := 0; i < 5; i++ {
		en.Compute(&Element{Type: "button"}, p)
		en.EndFrame()
	}
	if en.memo[p.node.key] != p || en.Compute(&Element{Type: "button"}, p) != c {
		t.Fatal("lost live ancestor")
	}
	for i := 0; i < 3; i++ {
		en.EndFrame()
	}
	if len(en.memo) != 0 {
		t.Fatal("eviction leak", len(en.memo))
	}
}

func TestEvictionBoundAcrossChangingStates(t *testing.T) {
	en := Compile(UA(), Parse(`.a:hover button{color:red}`))
	root := Element{Type: "app", Classes: []string{"a"}}
	child := Element{Type: "button"}
	for frame := 0; frame < 100; frame++ {
		root.State = StateFlags(frame%2) * Hover
		p := en.Compute(&root, nil)
		en.Compute(&child, p)
		en.EndFrame()
		if len(en.memo) > 4 {
			t.Fatalf("frame %d: %d nodes", frame, len(en.memo))
		}
	}
}

func TestVariableCSSWideAndInvalidShorthand(t *testing.T) {
	en := Compile(Sheet{}, Parse(`app{color:blue;font-size:22px} button{--s:inherit;color:var(--s);--x:bad; margin:7px;margin:var(--x); border:1px solid red;border:var(--x)}`))
	p := en.Compute(&Element{Type: "app"}, nil)
	s := en.Compute(&Element{Type: "button"}, p).Style
	if s.Color != p.Style.Color || s.Margin.Top.Value != 0 || s.BorderWidth.Top.Value != 0 {
		t.Fatalf("variable cascade: %+v", s)
	}
}

func TestInlineInvalidAndSheetSnapshot(t *testing.T) {
	sheet := Parse(`button{color:red}`)
	en := Compile(Sheet{}, sheet)
	sheet.Rules[0].Declarations[0].Value = "blue"
	d, _ := ParseInline("color: nonsense; opacity:.2")
	got := en.Compute(&Element{Type: "button", Inline: d}, nil).Style
	if got.Color != namedColors["red"] || got.Opacity != .2 {
		t.Fatalf("invalid inline or sheet mutation: %+v", got)
	}
}

func TestStylePropertyCoverage(t *testing.T) {
	if len(propertyNames) != int(propertyCount) {
		t.Fatal("property enum and table diverged")
	}
	for id, name := range propertyNames {
		if got, ok := propertyIndex(name); !ok || int(got) != id {
			t.Fatalf("property %q: %d %v", name, got, ok)
		}
		v, ok := parseValue(name, initial(name), Color{A: 1}, 16, 16)
		if !ok {
			t.Fatalf("invalid default for %q", name)
		}
		s := &Style{}
		s.set(propertyID(id), v)
		want := s.value(propertyID(id))
		if name == "line-height" && want.Keyword != "normal" {
			t.Fatalf("line-height default: %+v", want)
		}
	}
}

func TestKeywordAndCurrentColorInheritance(t *testing.T) {
	en := Compile(Sheet{}, Parse(`app{color:blue; font-style:italic; font-size:20px} button{color:currentColor; border-color:currentColor; border-style:solid; font-style:oblique; font-size:1em; line-height:2; letter-spacing:normal}`))
	p := en.Compute(&Element{Type: "app"}, nil)
	s := en.Compute(&Element{Type: "button"}, p).Style
	if s.Color != p.Style.Color || s.BorderColor.Top != s.Color || s.BorderStyle.Top != KeywordSolid || s.FontStyle != KeywordOblique || s.FontSize.Value != 20 || s.LineHeightKind != KeywordNumber || s.LineHeightNumber != 2 || !s.LetterSpacingNormal {
		t.Fatalf("keyword/color style %+v", s)
	}
}

func TestVariableInvalidatesWholeWinningShorthand(t *testing.T) {
	en := Compile(Sheet{}, Parse(`button{--p:5px nonsense;padding:3px;padding:var(--p)}`))
	s := en.Compute(&Element{Type: "button"}, nil).Style
	if s.Padding.Top.Value != 0 || s.Padding.Right.Value != 0 {
		t.Fatal("invalid shorthand did not reset", s.Padding)
	}
}

func TestInvalidStaticShorthandIgnored(t *testing.T) {
	en := Compile(Sheet{}, Parse(`button{padding:3px;padding:5px nonsense;border:2px solid red;border:garbage green}`))
	s := en.Compute(&Element{Type: "button"}, nil).Style
	if s.Padding.Top.Value != 3 || s.BorderWidth.Top.Value != 2 || s.BorderColor.Top != namedColors["red"] {
		t.Fatalf("partial invalid shorthand: %+v", s)
	}
}

func TestCSSWideShorthand(t *testing.T) {
	en := Compile(Sheet{}, Parse(`app{color:blue} button{padding:5px; padding:initial; border:2px solid red; border:unset; color:red; color:inherit}`))
	p := en.Compute(&Element{Type: "app"}, nil)
	s := en.Compute(&Element{Type: "button"}, p).Style
	if s.Padding.Top.Value != 0 || s.BorderWidth.Top.Value != 0 || s.Color != p.Style.Color {
		t.Fatalf("wide shorthand: %+v", s)
	}
}

func TestCSSWideVariableShorthand(t *testing.T) {
	en := Compile(Sheet{}, Parse(`button{--x:initial; padding:9px; padding:var(--x); border:2px solid red;border:var(--x)}`))
	s := en.Compute(&Element{Type: "button"}, nil).Style
	if s.Padding.Top.Value != 0 || s.BorderWidth.Top.Value != 0 || s.BorderStyle.Top != KeywordNone {
		t.Fatalf("wide var shorthand: %+v", s)
	}
}

func TestUnusedMemoEvictedAfterTwoFrames(t *testing.T) {
	en := Compile(Sheet{}, Sheet{})
	en.Compute(&Element{Type: "app"}, nil)
	en.EndFrame()
	if len(en.memo) != 1 {
		t.Fatal("evicted too early")
	}
	en.EndFrame()
	if len(en.memo) != 0 {
		t.Fatal("evicted too late")
	}
}

func TestStyleInitialMatchesPropertyDefaults(t *testing.T) {
	s := Compile(Sheet{}, Sheet{}).Compute(&Element{Type: "app"}, nil).Style
	for id, name := range propertyNames {
		want, ok := parseValue(name, initial(name), s.Color, 16, 16)
		if !ok {
			t.Fatalf("invalid initial %s", name)
		}
		got := s.value(propertyID(id))
		// Slices are shared immutable values and not directly comparable.
		switch name {
		case "font-family":
			if !reflect.DeepEqual(got.Families, want.Families) {
				t.Fatalf("%s: %+v vs %+v", name, got, want)
			}
		case "box-shadow":
			if len(got.Shadows) != len(want.Shadows) {
				t.Fatalf("%s: %+v vs %+v", name, got, want)
			}
		default:
			if got.Length != want.Length || got.Color != want.Color || got.Number != want.Number || got.Keyword != want.Keyword {
				t.Fatalf("%s: %+v vs %+v", name, got, want)
			}
		}
	}
}

func TestInheritedFieldsAndCurrentColorDefaults(t *testing.T) {
	en := Compile(Sheet{}, Parse(`app{color:red; font-family:serif; font-size:22px; font-style:italic; cursor:pointer; text-align:center;line-height:2;letter-spacing:1px;accent-color:green} button{background-color:blue}`))
	p := en.Compute(&Element{Type: "app"}, nil)
	c := en.Compute(&Element{Type: "button"}, p).Style
	if c.Color != p.Style.Color || c.AccentColor != namedColors["green"] || c.BorderColor.Top != c.Color || c.OutlineColor != c.Color || c.FontSize != p.Style.FontSize || c.FontStyle != KeywordItalic || c.FontFamily[0] != "serif" || c.Cursor != KeywordPointer || c.TextAlign != KeywordCenter || c.LineHeightNumber != 2 || c.LetterSpacing.Value != 1 || c.BackgroundColor != namedColors["blue"] {
		t.Fatalf("inheritance: %+v", c)
	}
}

func TestComputedInitialInheritsColorButResetsBackground(t *testing.T) {
	en := Compile(Sheet{}, Parse(`app{color:red;background:blue}button{background:initial}`))
	p := en.Compute(&Element{Type: "app"}, nil)
	c := en.Compute(&Element{Type: "button"}, p).Style
	if c.Color != namedColors["red"] || c.BackgroundColor != (Color{}) || c.BorderColor.Top != c.Color {
		t.Fatalf("computed initial: %+v", c)
	}
}

func TestChangedStateMissesOnlySubtree(t *testing.T) {
	nodes, parents, sheet := benchmarkTree()
	en := Compile(UA(), sheet)
	results := make([]*Computed, len(nodes))
	computeTree(en, nodes, parents, results)
	before := append([]*Computed(nil), results...)
	en.EndFrame()
	const changed = 10
	nodes[changed].State ^= Hover
	computeTree(en, nodes, parents, results)
	descendants := make([]bool, len(nodes))
	descendants[changed] = true
	expected, misses := 0, 0
	for i := range nodes {
		if i > changed && descendants[parents[i]] {
			descendants[i] = true
		}
		if descendants[i] {
			expected++
		}
		if results[i] != before[i] {
			misses++
			if !descendants[i] {
				t.Fatalf("unrelated node %d recomputed", i)
			}
		} else if descendants[i] {
			t.Fatalf("descendant %d reused stale result", i)
		}
	}
	if misses != expected {
		t.Fatalf("Compute misses %d; affected subtree size %d", misses, expected)
	}
}

func TestPendingVariableShorthandsAtomic(t *testing.T) {
	cases := []struct {
		property, base, valid, invalid string
		targets                        []string
	}{
		{"background", "blue", "red", "no-color", []string{"background-color"}},
		{"margin", "9px", "1px 2px 3px", "1px nope", shorthandTargets("margin")},
		{"padding", "9px", "1px 2px 3px", "1px nope", shorthandTargets("padding")},
		{"border-width", "9px", "1px 2px 3px", "1px nope", shorthandTargets("border-width")},
		{"border-style", "none", "solid", "solid dotted", shorthandTargets("border-style")},
		{"border-color", "blue", "red green", "red nope", shorthandTargets("border-color")},
		{"border-radius", "9px", "1px 2px 3px", "1px nope", shorthandTargets("border-radius")},
		{"border-top", "2px solid blue", "3px solid red", "3px dotted red", shorthandTargets("border-top")},
		{"border-right", "2px solid blue", "3px solid red", "3px dotted red", shorthandTargets("border-right")},
		{"border-bottom", "2px solid blue", "3px solid red", "3px dotted red", shorthandTargets("border-bottom")},
		{"border-left", "2px solid blue", "3px solid red", "3px dotted red", shorthandTargets("border-left")},
		{"border", "2px solid blue", "3px solid red", "3px dotted red", shorthandTargets("border")},
		{"outline", "2px solid blue", "3px solid red", "3px dotted red", shorthandTargets("outline")},
		{"flex", "none", "2 3 10px", "2 garbage", shorthandTargets("flex")},
		{"gap", "9px", "1px 2px", "1px nope", shorthandTargets("gap")},
		{"overflow", "hidden", "scroll auto", "scroll garbage", shorthandTargets("overflow")},
	}
	baseline := Compile(Sheet{}, Sheet{}).Compute(&Element{Type: "button"}, nil).Style
	for _, tt := range cases {
		t.Run(tt.property, func(t *testing.T) {
			css := func(v string) *Style {
				src := "button{--x:" + v + ";" + tt.property + ":" + tt.base + ";" + tt.property + ":var(--x)}"
				return Compile(Sheet{}, Parse(src)).Compute(&Element{Type: "button"}, nil).Style
			}
			good, bad := css(tt.valid), css(tt.invalid)
			for _, name := range tt.targets {
				id, ok := propertyIndex(name)
				if !ok {
					t.Fatalf("unknown target %s", name)
				}
				if reflect.DeepEqual(good.value(id), baseline.value(id)) {
					t.Errorf("valid %s did not override %s", tt.valid, name)
				}
				if !reflect.DeepEqual(bad.value(id), baseline.value(id)) {
					t.Errorf("invalid %s did not reset %s: %+v", tt.invalid, name, bad.value(id))
				}
			}
		})
	}
}

func TestPendingShorthandWinnerPerLonghand(t *testing.T) {
	en := Compile(Sheet{}, Parse(`button{--pair:1px 2px; padding:var(--pair); padding-left:7px; padding-right:9px!important}`))
	s := en.Compute(&Element{Type: "button"}, nil).Style
	if s.Padding.Top.Value != 1 || s.Padding.Bottom.Value != 1 || s.Padding.Left.Value != 7 || s.Padding.Right.Value != 9 {
		t.Fatal(s.Padding)
	}
}

func TestPendingShorthandInheritedCustomAndInline(t *testing.T) {
	en := Compile(Sheet{}, Parse(`app{--space:1em 2px;color:red}button{padding:var(--space);margin:9px;margin:var(--missing);border:1px solid blue;border:var(--missing)}`))
	p := en.Compute(&Element{Type: "app"}, nil)
	in, _ := ParseInline(`padding:var(--space);border:var(--missing, 2px solid currentColor)`)
	c := en.Compute(&Element{Type: "button", Inline: in}, p).Style
	if c.Padding.Top.Value != 16 || c.Padding.Right.Value != 2 || c.Margin.Top.Value != 0 || c.BorderWidth.Left.Value != 2 || c.BorderColor.Left != namedColors["red"] {
		t.Fatalf("pending group inheritance/inline %+v", c)
	}
}
