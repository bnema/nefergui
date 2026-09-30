package ui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"testing"

	"github.com/bnema/nefergui/internal/css"
	"github.com/bnema/nefergui/internal/edit"
	"github.com/bnema/nefergui/internal/text"
)

// The golden is the serialized display list, not a rendered image; each case
// still checks the intended pseudo-state/event before comparing exact JSON.
func TestControlDisplaySnapshots(t *testing.T) {
	catalog, err := text.Load(text.DirectorySource("../../testdata/fonts"))
	if err != nil {
		t.Fatal(err)
	}
	controls := []string{"text", "heading", "image", "icon", "separator", "spacer", "button", "checkbox", "radio", "slider", "input", "textarea"}
	states := []string{"normal", "hover", "focus-visible", "disabled", "checked"}
	for _, kind := range controls {
		for _, state := range states {
			if (state == "checked" && kind != "checkbox" && kind != "radio") || ((state == "focus-visible" || state == "disabled") && kind != "button" && kind != "checkbox" && kind != "radio" && kind != "slider" && kind != "input" && kind != "textarea") {
				continue
			}
			t.Run(kind+"/"+state, func(t *testing.T) {
				r := newRuntime()
				r.setTextEngine(text.NewEngine(catalog))
				r.styles = css.Compile(css.UA(), css.Parse(`text,heading,image,icon,separator,spacer,button,checkbox,radio,slider,input,textarea {width:120px;height:28px;font-size:16px;background:#ddeeff} button:hover,checkbox:hover,radio:hover,slider:hover,input:hover,textarea:hover {background:#aaccee} button:focus-visible,checkbox:focus-visible,radio:focus-visible,slider:focus-visible,input:focus-visible,textarea:focus-visible {outline:2px solid red} button:disabled,checkbox:disabled,radio:disabled,slider:disabled,input:disabled,textarea:disabled {opacity:0.5}`))
				checked := state == "checked"
				choice := ""
				if checked {
					choice = "yes"
				}
				v := 3.0
				value := "abc"
				disabled := state == "disabled"
				view := func(f *Frame) {
					root := f.Root()
					switch kind {
					case "text":
						root.Text("abc")
					case "heading":
						root.Heading("abc")
					case "image":
						root.Image(image.NewRGBA(image.Rect(0, 0, 12, 12)))
					case "icon":
						root.Icon("star")
					case "separator":
						root.Separator()
					case "spacer":
						root.Spacer()
					case "button":
						root.Button("abc", Disabled(disabled))
					case "checkbox":
						root.Checkbox("abc", &checked, Disabled(disabled))
					case "radio":
						root.Radio("abc", "yes", &choice, Disabled(disabled))
					case "slider":
						root.Slider("abc", &v, 0, 10, 1, Disabled(disabled))
					case "input":
						root.Input("abc", &value, Disabled(disabled))
					case "textarea":
						root.Textarea("abc", &value, Disabled(disabled))
					}
				}
				r.Build(view)
				switch state {
				case "hover":
					r.route(platformInput{Kind: "motion", X: 2, Y: 2})
					r.Build(view)
					if r.state.hover == nil {
						t.Fatal("no hover")
					}
				case "focus-visible":
					r.route(key("Tab"))
					r.Build(view)
					if !r.state.visible {
						t.Fatal("no keyboard focus")
					}
				}
				data, err := json.MarshalIndent(r.output.Display, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				assertSnapshot(t, kind+"_"+state, data)
			})
		}
	}
	for _, name := range []string{"selection-ltr", "selection-arabic", "preedit"} {
		t.Run("input/"+name, func(t *testing.T) {
			r := newRuntime()
			r.setTextEngine(text.NewEngine(catalog))
			r.styles = css.Compile(css.UA(), css.Parse(`input {width:120px;height:28px;font-size:16px;background:#ddeeff}`))
			value := "abc"
			if name == "selection-arabic" {
				value = "مرحبا"
			}
			view := func(f *Frame) { f.Root().Input("", &value) }
			r.Build(view)
			r.route(key("Tab"))
			r.Build(view)
			s := r.edits[idKey(r.Target(0))]
			if name == "preedit" {
				r.route(platformInput{Kind: "ime-done", IME: edit.IMEBatch{Preedit: "é", Begin: len("é")}})
				r.Build(view)
			} else {
				s.Select(0, len(value))
				r.Redraw()
				r.Build(view)
			}
			data, err := json.MarshalIndent(r.output.Display, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			assertSnapshot(t, "input_"+name, data)
		})
	}
}
func assertSnapshot(t *testing.T, name string, data []byte) {
	t.Helper()
	path := filepath.Join("testdata", "control-display", name+".json")
	if os.Getenv("NEFERGUI_WRITE_SNAPSHOTS") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing snapshot %s: %v", name, err)
	}
	if !bytes.Equal(bytes.TrimSpace(want), bytes.TrimSpace(data)) {
		t.Fatalf("snapshot %s changed (got %d bytes, want %d): %s", name, len(data), len(want), fmt.Sprintf("first 120: %s", data[:min(len(data), 120)]))
	}
}
