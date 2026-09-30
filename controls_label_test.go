package nefergui

import (
	"testing"

	"github.com/bnema/nefergui/internal/css"
	"github.com/bnema/nefergui/internal/layout"
	"github.com/bnema/nefergui/internal/text"
)

// Every visible string a view declares must reach the display list as shaped
// glyphs. Snapshots alone once froze labelless controls; this invariant does not
// depend on a golden file.
func TestDeclaredTextIsPainted(t *testing.T) {
	catalog, err := text.Load(text.DirectorySource("testdata/fonts"))
	if err != nil {
		t.Fatal(err)
	}
	r := newRuntime()
	r.setTextEngine(text.NewEngine(catalog))
	r.styles = css.Compile(css.UA(), css.Parse(`input,textarea{width:200px;height:40px}`))
	checked, choice, v := false, "", 3.0
	name, notes, filled := "", "", "set"
	view := func(f *Frame) {
		root := f.Root()
		root.Button("Continuer", Key("button"))
		root.Checkbox("Thème sombre", &checked, Key("checkbox"))
		root.Radio("Option", "a", &choice, Key("radio"))
		root.Slider("Volume", &v, 0, 10, 1, Key("slider"))
		root.Input("Nom", &name, Key("input"), Placeholder("Ada"))
		root.Textarea("Notes", &notes, Key("textarea"), Placeholder("Écrire…"))
		root.Input("Rempli", &filled, Key("filled"), Placeholder("hidden"))
		root.Button("Off", Key("disabled"), Disabled(true))
	}
	r.Build(view)
	painted := map[string]layout.Command{}
	for _, cmd := range r.output.Display {
		if cmd.Op == "text" && glyphCount(cmd) > 0 {
			painted[cmd.Text] = cmd
		}
	}
	for _, want := range []string{"Continuer", "Thème sombre", "Option", "Volume", "Ada", "Écrire…", "set", "Off"} {
		if _, ok := painted[want]; !ok {
			t.Errorf("%q declared but not painted; painted: %v", want, keys(painted))
		}
	}
	if _, ok := painted["hidden"]; ok {
		t.Error("placeholder painted over a non-empty value")
	}
	if p := painted["Ada"]; p.Opacity >= 1 || p.Opacity <= 0 {
		t.Errorf("placeholder opacity %v, want dimmed", p.Opacity)
	}
	// The placeholder never enters the model, and typing replaces it.
	if name != "" {
		t.Fatalf("placeholder leaked into value: %q", name)
	}
	r.route(key("Tab"))
	r.Build(view)
	for i := 0; i < 4; i++ {
		r.route(key("Tab"))
		r.Build(view)
	}
	r.route(typed("b", "b"))
	r.Build(view)
	for _, cmd := range r.output.Display {
		if cmd.Text == "Ada" {
			t.Fatalf("placeholder still painted after typing %q", name)
		}
	}
	if name != "b" {
		t.Fatalf("typed into wrong control: name=%q", name)
	}
}

// Placeholders follow editor text geometry: text-align and RTL position,
// letter-spacing, and textarea wrapping.
func TestPlaceholderGeometry(t *testing.T) {
	catalog, err := text.Load(text.DirectorySource("testdata/fonts"))
	if err != nil {
		t.Fatal(err)
	}
	placeholder := func(sheet, typ, p string) layout.Command {
		t.Helper()
		r := newRuntime()
		r.setTextEngine(text.NewEngine(catalog))
		r.styles = css.Compile(css.UA(), css.Parse(sheet))
		value := ""
		r.Build(func(f *Frame) {
			if typ == "textarea" {
				f.Root().Textarea("", &value, Placeholder(p))
			} else {
				f.Root().Input("", &value, Placeholder(p))
			}
		})
		for _, cmd := range r.output.Display {
			if cmd.Text == p {
				return cmd
			}
		}
		t.Fatalf("placeholder %q not painted", p)
		return layout.Command{}
	}
	span := func(cmd layout.Command) (lo, hi float64, lines int) {
		lo, hi = 1e9, -1e9
		ys := map[float64]bool{}
		for _, run := range cmd.Runs {
			ys[run.Y] = true
			for _, g := range run.Glyphs {
				lo, hi = min(lo, g.X), max(hi, g.X+g.Advance)
			}
		}
		return lo, hi, len(ys)
	}
	box := `input,textarea{width:300px;height:80px;font-size:16px}`
	leftLo, leftHi, _ := span(placeholder(box, "input", "Ada"))
	if leftLo > 1 {
		t.Errorf("start-aligned LTR placeholder at x=%v", leftLo)
	}
	if lo, hi, _ := span(placeholder(box+`input{text-align:right}`, "input", "Ada")); hi < 299 || lo < 200 {
		t.Errorf("right-aligned placeholder spans %v..%v", lo, hi)
	}
	if lo, hi, _ := span(placeholder(box, "input", "مرحبا")); hi < 299 || lo < 200 {
		t.Errorf("RTL start-aligned placeholder spans %v..%v", lo, hi)
	}
	if lo, hi, _ := span(placeholder(box+`input{letter-spacing:4px}`, "input", "Ada")); hi-lo < leftHi-leftLo+8 {
		t.Errorf("letter-spacing ignored: width %v vs %v", hi-lo, leftHi-leftLo)
	}
	long := "Une longue phrase d'exemple qui dépasse la largeur du champ de texte"
	if _, hi, lines := span(placeholder(box, "textarea", long)); lines < 2 || hi > 300.5 {
		t.Errorf("textarea placeholder: %d lines, right edge %v", lines, hi)
	}
}

func glyphCount(cmd layout.Command) int {
	n := 0
	for _, run := range cmd.Runs {
		n += len(run.Glyphs)
	}
	return n
}

func keys(m map[string]layout.Command) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
