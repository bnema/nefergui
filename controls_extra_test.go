package nefergui

import (
	"github.com/bnema/nefergui/internal/css"
	"github.com/bnema/nefergui/internal/edit"
	"github.com/bnema/nefergui/internal/keyboard"
	"testing"
)

type clip struct{ data []byte }

func (c *clip) ReadText(limit int) ([]byte, error) { return c.data, nil }
func (c *clip) WriteText(v []byte) error           { c.data = append([]byte(nil), v...); return nil }
func TestRadioSliderAndEditingIdentity(t *testing.T) {
	r := newRuntime()
	r.styles = css.Compile(css.UA(), css.Parse(`input,radio,slider {height:20px}`))
	var choice string
	v := 3.0
	name := "e\u0301"
	order := []string{"a", "b"}
	view := func(f *Frame) {
		root := f.Root()
		for _, key := range order {
			root.Input(key, &name, Key(key))
		}
		root.Radio("One", "one", &choice, Key("r1"))
		root.Radio("Two", "two", &choice, Key("r2"))
		root.Slider("level", &v, 0, 10, 1, Key("s"))
	}
	r.Build(view)
	r.Queue(inputEvent{Target: r.Target(3), Kind: "activate"})
	r.Build(view)
	if choice != "two" {
		t.Fatal(choice)
	}
	r.route(key("Tab"))
	r.Build(view)
	for !r.state.focus.same(r.Target(4)) {
		r.route(key("Tab"))
		r.Build(view)
		if r.state.focus.same(r.Target(0)) {
			t.Fatalf("slider skipped: %v", func() []string {
				var ids []string
				for _, e := range r.focusOrder() {
					ids = append(ids, idKey(e.identity))
				}
				return ids
			}())
		}
	}
	if !r.state.focus.same(r.Target(4)) {
		t.Fatalf("slider focus: focus=%s order=%v", idKey(r.state.focus), func() []string {
			var ids []string
			for _, e := range r.focusOrder() {
				ids = append(ids, idKey(e.identity))
			}
			return ids
		}())
	}
	r.route(key("Right"))
	r.Build(view)
	if v != 4 {
		t.Fatal(v)
	}
	r.Queue(inputEvent{Target: r.Target(0), Kind: "edit-key", Text: "backspace"})
	r.Build(view)
	if name != "" {
		t.Fatal("grapheme delete", name)
	}
	order = []string{"b", "a"}
	r.Redraw()
	r.Build(view)
	if r.edits[idKey(r.Target(0))] == r.edits[idKey(r.Target(1))] {
		t.Fatal("state aliased")
	}
}
func TestDisabledEditorRejectsFocusAndText(t *testing.T) {
	r := newRuntime()
	r.styles = css.Compile(css.UA(), css.Parse(`input {height:20px}`))
	value := "keep"
	view := func(f *Frame) { f.Root().Input("name", &value, Key("name"), Disabled(true)) }
	r.Build(view)
	r.route(key("Tab"))
	r.Build(view)
	if r.state.focus != nil {
		t.Fatalf("disabled input focused: %s", idKey(r.state.focus))
	}
	r.route(platformInput{Kind: "key", Key: keyboard.Key{Name: "q", Text: "x", Pressed: true}})
	r.Build(view)
	if value != "keep" {
		t.Fatalf("disabled input edited: %q", value)
	}
}

func TestEditFocusIMEAndClipboard(t *testing.T) {
	r := newRuntime()
	r.styles = css.Compile(css.UA(), css.Parse(`input {height:20px}`))
	var value string
	view := func(f *Frame) { f.Root().Input("secret", &value, Key("secret"), Password(true)) }
	r.Build(view)
	r.clipboard = &clip{data: []byte("paste")}
	r.route(key("Tab"))
	r.Build(view)
	r.route(platformInput{Kind: "key", Key: keyboard.Key{Name: "q", Text: "x", Pressed: true}})
	r.Build(view)
	if value != "x" || r.committed.children[0].text != "•" {
		t.Fatalf("value=%q display=%q focus=%s", value, r.committed.children[0].text, idKey(r.state.focus))
	}
	r.route(platformInput{Kind: "ime-done", IME: edit.IMEBatch{Preedit: "候補"}})
	r.Build(view)
	if value != "x" {
		t.Fatal(value)
	}
	r.route(platformInput{Kind: "focus-out"})
	r.Build(view)
	if r.edits[idKey(r.Target(0))].Preedit != "" {
		t.Fatal("preedit survives blur")
	}
}
