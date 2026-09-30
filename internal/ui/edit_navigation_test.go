package ui

import (
	"github.com/bnema/nefergui/internal/css"
	"github.com/bnema/nefergui/internal/text"
	"testing"
)

func TestWrappedNavigationAndScroll(t *testing.T) {
	catalog, err := text.Load(text.DirectorySource("../../testdata/fonts"))
	if err != nil {
		t.Fatal(err)
	}
	r := newRuntime()
	r.setTextEngine(text.NewEngine(catalog))
	r.styles = css.Compile(css.UA(), css.Parse(`textarea {width: 45px; height: 20px; font-size:16px} input {width: 30px; height:20px; font-size:16px}`))
	value := "one two three four five six"
	single := "abcdefghijk"
	view := func(f *Frame) {
		root := f.Root()
		root.Textarea("multiline", &value, Key("multi"))
		root.Input("single", &single, Key("single"))
	}
	r.Build(view)
	n := r.output.Tree.Children[0]
	id := n.ID
	if len(n.Lines) < 3 {
		t.Fatalf("expected wraps, got %d", len(n.Lines))
	}
	r.route(key("Tab"))
	r.Build(view)
	s := r.edits[id]
	s.Move(0, false)
	r.Redraw()
	r.Build(view)
	r.route(key("Down"))
	r.Build(view)
	first := s.Cursor
	if first == 0 {
		t.Fatal("down did not move to wrapped line")
	}
	r.route(key("Down"))
	r.Build(view)
	if s.Cursor <= first || !s.PreferredXValid {
		t.Fatalf("second wrapped down: %d then %d", first, s.Cursor)
	}
	if r.output.Tree.Children[0].ScrollY <= 0 {
		t.Fatal("caret did not scroll textarea vertically")
	}
	r.route(key("Up"))
	r.Build(view)
	if s.Cursor != first {
		t.Fatalf("preferred-x return got %d want %d", s.Cursor, first)
	}
	r.route(key("Tab"))
	r.Build(view)
	singleNode := r.output.Tree.Children[1]
	if len(singleNode.Lines) != 1 {
		t.Fatalf("input wrapped: %d", len(singleNode.Lines))
	}
	if singleNode.ScrollX <= 0 || singleNode.ScrollY != 0 {
		t.Fatalf("input scroll %+v", singleNode)
	}
}
