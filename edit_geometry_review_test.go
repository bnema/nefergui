package nefergui

import (
	"math"
	"strings"
	"testing"

	"github.com/bnema/nefergui/internal/edit"
	"github.com/bnema/nefergui/internal/layout"
)

func TestRTLParagraphCaretUsesPrecedingTrailingEdge(t *testing.T) {
	value := "مرحبا abc"
	r := geometryRuntime(t, `input {width:300px;height:28px;font-size:16px}`)
	view := func(f *Frame) { f.Root().Input("mixed", &value) }
	r.Build(view)
	r.route(key("Tab"))
	r.Build(view)
	n := r.output.Tree.Children[0]
	offset := len("مرحبا ")
	s := r.edits[n.ID]
	s.Move(offset, false)
	s.Preedit = "x"
	r.Redraw()
	r.Build(view)
	n = r.output.Tree.Children[0]
	want := rawTrailingEdge(t, r, n, 5)
	g := newEditorGeometry(s, n, r.output.Display, false)
	var first *textBoundary
	for i := range g.lines[0] {
		if g.lines[0][i].byteOffset == offset {
			first = &g.lines[0][i]
			break
		}
	}
	if first == nil || first.trailing || math.Abs(first.x-want) < 1 {
		t.Fatalf("fixture must encounter a distinct leading edge first: first=%+v trailing=%g", first, want)
	}
	for _, op := range []string{"caret", "preedit-text"} {
		cmd, ok := displayCommand(r, n.ID, op)
		if !ok || math.Abs(cmd.Rect.X-want) > .01 {
			t.Fatalf("%s x=%g want %g", op, cmd.Rect.X, want)
		}
	}
	// Place the right viewport edge between the two positions. Only the correct
	// trailing edge should require a horizontal scroll.
	if want <= first.x {
		t.Fatalf("fixture must place trailing edge right of leading edge")
	}
	n.Content.W = (first.x+want)/2 - n.Content.X
	n.ContentSize.W = 500
	r.state.scroll = map[string]layout.Size{n.ID: {}}
	expected := want - (n.Content.X + n.Content.W - 1)
	if !r.scrollCaret(r.committed, &r.output) || math.Abs(r.state.scroll[n.ID].W-expected) > .01 {
		t.Fatalf("scroll=%+v want %g", r.state.scroll[n.ID], expected)
	}
}

func TestPasswordWrappedNavigationUsesLogicalGraphemeOffsets(t *testing.T) {
	grapheme := "e\u0301"
	value := strings.Repeat(grapheme, 24)
	r := geometryRuntime(t, `textarea {width:45px;height:100px;font-size:16px}`)
	view := func(f *Frame) { f.Root().Textarea("secret", &value, Password(true)) }
	r.Build(view)
	r.route(key("Tab"))
	r.Build(view)
	n := r.output.Tree.Children[0]
	if len(n.Lines) < 3 {
		t.Fatalf("need wrapped password: %d lines", len(n.Lines))
	}
	s := r.edits[n.ID]
	s.Move(0, false)
	s.PreferredXValid = false
	// Each shaped source rune is one bullet, hence one logical grapheme. The
	// starts of the masked runs independently determine the expected offsets.
	logical := edit.Boundaries(value)
	want := logical[n.Lines[1].Runs[0].Start]
	r.route(key("Down"))
	r.Build(view)
	if s.Cursor != want {
		t.Fatalf("down cursor=%d want logical offset %d", s.Cursor, want)
	}
	r.route(key("Up"))
	r.Build(view)
	if s.Cursor != 0 {
		t.Fatalf("up cursor=%d want 0", s.Cursor)
	}
}
