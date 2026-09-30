package ui

import (
	"fmt"
	"testing"

	"github.com/bnema/nefergui/internal/css"
	"github.com/bnema/nefergui/internal/text"
)

func TestEmptyLabelsDoNotReserveEditorLine(t *testing.T) {
	catalog, err := text.Load(text.DirectorySource("../../testdata/fonts"))
	if err != nil {
		t.Fatal(err)
	}
	r := newRuntime()
	r.setTextEngine(text.NewEngine(catalog))
	r.Build(func(f *Frame) {
		root := f.Root()
		root.Text("")
		root.Heading("")
	})
	if len(r.output.Tree.Children) != 2 {
		t.Fatalf("empty labels: got %d children, want 2", len(r.output.Tree.Children))
	}
	for _, child := range r.output.Tree.Children {
		if child.Content.H != 0 || len(child.Lines) != 0 {
			t.Fatalf("empty label reserves line: %+v", child)
		}
	}
}

func TestEmptyTextareaCaretUsesLineHeight(t *testing.T) {
	catalog, err := text.Load(text.DirectorySource("../../testdata/fonts"))
	if err != nil {
		t.Fatal(err)
	}
	for _, height := range []string{"normal", "1.5", "30px", "1.5em", "150%"} {
		t.Run(height, func(t *testing.T) {
			r := newRuntime()
			r.setTextEngine(text.NewEngine(catalog))
			r.styles = css.Compile(css.UA(), css.Parse(fmt.Sprintf(`textarea {height:400px;width:300px;font-size:20px;line-height:%s}`, height)))
			value := ""
			view := func(f *Frame) { f.Root().Textarea("Document", &value) }
			r.Build(view)
			r.route(key("Tab"))
			r.Build(view)
			caretHeight := func() float64 {
				for _, cmd := range r.output.Display {
					if cmd.Op == "caret" {
						return cmd.Rect.H
					}
				}
				t.Fatal("missing caret")
				return 0
			}
			empty := caretHeight()
			if empty <= 0 || empty >= 100 {
				t.Fatalf("empty caret height=%g", empty)
			}
			value = "x"
			r.Redraw()
			r.Build(view)
			if got := caretHeight(); got != empty {
				t.Fatalf("empty=%g populated=%g", empty, got)
			}
		})
	}
}
