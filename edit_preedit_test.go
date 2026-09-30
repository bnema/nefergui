package nefergui

import (
	"github.com/bnema/nefergui/internal/css"
	"github.com/bnema/nefergui/internal/edit"
	"github.com/bnema/nefergui/internal/text"
	"testing"
)

func TestPreeditInlineWithoutModelMutation(t *testing.T) {
	catalog, err := text.Load(text.DirectorySource("testdata/fonts"))
	if err != nil {
		t.Fatal(err)
	}
	r := newRuntime()
	r.setTextEngine(text.NewEngine(catalog))
	r.styles = css.Compile(css.UA(), css.Parse(`input {width:250px;height:28px;font-size:16px}`))
	value := "ab"
	view := func(f *Frame) { f.Root().Input("preedit", &value) }
	r.Build(view)
	r.route(key("Tab"))
	r.Build(view)
	id := idKey(r.Target(0))
	r.edits[id].Move(1, false)
	r.route(platformInput{Kind: "ime-done", IME: edit.IMEBatch{Preedit: "éx", Begin: len("é")}})
	r.Build(view)
	if value != "ab" || r.committed.children[0].text != "ab" {
		t.Fatalf("composition changed model/display text: %q %q", value, r.committed.children[0].text)
	}
	seen := map[string]bool{}
	var start, width, caret float64
	for _, cmd := range r.output.Display {
		if cmd.ID != id {
			continue
		}
		seen[cmd.Op] = true
		switch cmd.Op {
		case "preedit-text":
			if cmd.Text != "éx" || len(cmd.Runs) == 0 {
				t.Fatalf("unshaped preedit: %+v", cmd)
			}
			start = cmd.Rect.X
			width = cmd.Rect.W
		case "preedit-caret":
			caret = cmd.Rect.X
		case "preedit-underline":
			if cmd.Rect.W <= 0 || cmd.Rect.H != 1 {
				t.Fatalf("bad underline %+v", cmd)
			}
		}
	}
	if !seen["preedit-text"] || !seen["preedit-underline"] || !seen["preedit-caret"] || !(caret > start && caret < start+width) {
		t.Fatalf("preedit commands %v x=%g width=%g cursor=%g", seen, start, width, caret)
	}
}
