package layout

import (
	"reflect"
	"testing"

	"github.com/bnema/nefergui/internal/css"
)

// Layout output is detached: neither the shared paint storage nor a later
// Layout call, nor a mutation of the input style, may change earlier results.
func TestPaintOutputDetached(t *testing.T) {
	root, opts := paintFixture(t, 6)
	first, err := Layout(root, opts)
	if err != nil {
		t.Fatal(err)
	}
	want := deepCopyDisplay(first.Display)
	shadows := 0
	for _, cmd := range first.Display {
		if cmd.Shadow != nil {
			shadows++
		}
	}
	if shadows != 12 {
		t.Fatalf("fixture shadows=%d, want 12", shadows)
	}
	// Mutating the input styles must not reach painted shadows.
	for _, card := range root.Children {
		card.Style.BoxShadow[0].Blur = px(99)
	}
	second, err := Layout(root, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Display, want) {
		t.Fatal("first output changed after style mutation and second Layout")
	}
	if reflect.DeepEqual(second.Display, want) {
		t.Fatal("second output should reflect the mutated style")
	}
	// Writing through one command's storage must not change any other.
	for i := range second.Display {
		if second.Display[i].Shadow != nil {
			second.Display[i].Shadow.X = px(-7)
		}
	}
	if !reflect.DeepEqual(first.Display, want) {
		t.Fatal("outputs share shadow storage")
	}
}

func deepCopyDisplay(in []Command) []Command {
	out := make([]Command, len(in))
	for i, c := range in {
		if c.Shadow != nil {
			s := *c.Shadow
			c.Shadow = &s
		}
		c.Runs = append([]Run(nil), c.Runs...)
		for j := range c.Runs {
			c.Runs[j].Glyphs = append([]Glyph(nil), c.Runs[j].Glyphs...)
		}
		out[i] = c
	}
	return out
}

// Runs and glyphs come from shared blocks; a caller appending to one must
// reallocate rather than overwrite the next run or command.
func TestPaintRunAndGlyphCapacityIsolated(t *testing.T) {
	root, opts := paintFixture(t, 3)
	out, err := Layout(root, opts)
	if err != nil {
		t.Fatal(err)
	}
	want := deepCopyDisplay(out.Display)
	texts := 0
	for i := range out.Display {
		cmd := &out.Display[i]
		if cmd.Op != "text" {
			continue
		}
		texts++
		if len(cmd.Runs) == 0 || cap(cmd.Runs) != len(cmd.Runs) {
			t.Fatalf("runs len=%d cap=%d", len(cmd.Runs), cap(cmd.Runs))
		}
		for j := range cmd.Runs {
			if g := cmd.Runs[j].Glyphs; len(g) == 0 || cap(g) != len(g) {
				t.Fatalf("glyphs len=%d cap=%d", len(g), cap(g))
			}
		}
		_ = append(cmd.Runs, Run{Start: -1})
		for j := range cmd.Runs {
			_ = append(cmd.Runs[j].Glyphs, Glyph{ID: 0xdead})
		}
	}
	if texts != 3 || !reflect.DeepEqual(out.Display, want) {
		t.Fatal("append through a command overwrote neighbouring data")
	}
}

// countCommands must match paint exactly, otherwise the single-allocation
// guarantee silently degrades.
func TestPaintCountsMatch(t *testing.T) {
	root, opts := paintFixture(t, 10)
	ctx := context{options: opts}
	tree, err := ctx.place(root, 0, 0, opts.Width, opts.Height, nil, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	n := countCommands(root, tree)
	out := ctx.display(root, tree)
	runs, glyphs, shadows := 0, 0, 0
	for _, cmd := range out {
		runs += len(cmd.Runs)
		for _, r := range cmd.Runs {
			glyphs += len(r.Glyphs)
		}
		if cmd.Shadow != nil {
			shadows++
		}
	}
	if n.commands != len(out) || cap(out) != len(out) || n.runs != runs || n.glyphs != glyphs || n.shadows != shadows {
		t.Fatalf("counts %+v vs commands=%d cap=%d runs=%d glyphs=%d shadows=%d", n, len(out), cap(out), runs, glyphs, shadows)
	}
}

func TestPaintAllocations(t *testing.T) {
	root, opts := paintFixture(t, 50)
	ctx := context{options: opts}
	tree, err := ctx.place(root, 0, 0, opts.Width, opts.Height, nil, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	// One block each for commands, shadows, runs and glyphs, whatever the tree size.
	if n := testing.AllocsPerRun(20, func() { ctx.display(root, tree) }); n > 4 {
		t.Fatalf("paint allocs=%v, want <= 4", n)
	}
}

func TestPaintEmptyAndShadowOrder(t *testing.T) {
	out, err := Layout(&Node{ID: "n", Kind: Box, Style: &css.Style{Display: css.KeywordBlock}}, Options{Width: 10, Height: 10})
	if err != nil || out.Display != nil {
		t.Fatalf("empty paint: %+v %v", out.Display, err)
	}
	s := css.Style{Display: css.KeywordBlock, Width: px(10), Height: px(10), BackgroundColor: css.Color{A: 1}, BoxShadow: []css.Shadow{
		{Inset: true, Blur: px(1)}, {Blur: px(2)}, {Blur: px(3)}, {Inset: true, Blur: px(4)},
	}}
	out, err = Layout(node("n", Box, s), Options{Width: 10, Height: 10})
	if err != nil {
		t.Fatal(err)
	}
	var got []float64
	var ops []string
	for _, c := range out.Display {
		ops = append(ops, c.Op)
		if c.Shadow != nil {
			got = append(got, c.Shadow.Blur.Value)
		}
	}
	if !reflect.DeepEqual(got, []float64{2, 3, 1, 4}) || !reflect.DeepEqual(ops, []string{"shadow", "shadow", "rect", "shadow", "shadow"}) {
		t.Fatalf("order ops=%v blur=%v", ops, got)
	}
}
