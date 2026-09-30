package css

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestFixture(t *testing.T) {
	src, e := os.ReadFile("testdata/basic.css")
	if e != nil {
		t.Fatal(e)
	}
	want, e := os.ReadFile("testdata/basic.json")
	if e != nil {
		t.Fatal(e)
	}
	root := Element{Type: "app"}
	child := Element{Type: "row"}
	sheet := Parse(string(src))
	engine := Compile(UA(), sheet)
	p := engine.Compute(&root, nil)
	got := engine.Compute(&child, p).Style
	v := map[string]any{"display": keywordNames[got.Display], "padding-top": got.Padding.Top, "padding-right": got.Padding.Right, "background-color": got.BackgroundColor, "font-size": got.FontSize}
	b, _ := json.Marshal(v)
	var a, c any
	_ = json.Unmarshal(b, &a)
	_ = json.Unmarshal(want, &c)
	if !reflect.DeepEqual(a, c) {
		t.Fatalf("got %s want %s", b, want)
	}
}
func TestColors(t *testing.T) {
	tests := []struct {
		s string
		c Color
	}{{"#f00", Color{1, 0, 0, 1}}, {"#f008", Color{1, 0, 0, 136.0 / 255}}, {"#ff0000", Color{1, 0, 0, 1}}, {"#ff000080", Color{1, 0, 0, 128.0 / 255}}, {"rgb(255,0,0)", Color{1, 0, 0, 1}}, {"rgba(255 0 0 / 50%)", Color{1, 0, 0, .5}}, {"hsl(0 100% 50%)", Color{1, 0, 0, 1}}, {"hsla(0,100%,50%,.5)", Color{1, 0, 0, .5}}, {"red", Color{1, 0, 0, 1}}, {"transparent", Color{}}}
	for _, tt := range tests {
		t.Run(tt.s, func(t *testing.T) {
			v, ok := parseColor(tt.s, Color{})
			if !ok || v != tt.c {
				t.Fatalf("%v %v", v, ok)
			}
		})
	}
}
func TestCascade(t *testing.T) {
	tests := []struct {
		css, typ, id string
		hover        bool
		want         string
	}{{"row{display:block}", "row", "", false, "block"}, {"row{display:none} row{display:flex}", "row", "", false, "flex"}, {"#x {display:none} row {display:flex}", "row", "x", false, "none"}, {"row:hover{display:none} row{display:flex}", "row", "", true, "none"}, {"row{display:none!important} #x{display:flex}", "row", "x", false, "none"}}
	for _, tt := range tests {
		t.Run(tt.css, func(t *testing.T) {
			e := Element{Type: tt.typ, ID: tt.id}
			if tt.hover {
				e.State = Hover
			}
			s := Compile(UA(), Parse(tt.css)).Compute(&e, nil).Style
			if keywordNames[s.Display] != tt.want {
				t.Fatalf("got %s want %s", keywordNames[s.Display], tt.want)
			}
		})
	}
}
func TestInvalidAndInheritance(t *testing.T) {
	p := Element{Type: "app"}
	e := Element{Type: "button"}
	sheet := Parse("app {color: blue; font-size: 20px; --a: var(--b); --b: var(--a)} button { color: var(--a); padding: 1em 2px; opacity: 1e999; unknown: 1; background: var(--a, red)}")
	engine := Compile(UA(), sheet)
	ps := engine.Compute(&p, nil)
	s := engine.Compute(&e, ps).Style
	if s.Color != ps.Style.Color || s.Padding.Top.Value != 20 || s.Opacity != 1 || s.BackgroundColor.R != 1 {
		t.Fatalf("style %#v", s)
	}
}
func TestRecovery(t *testing.T) {
	s := Parse("button { wat; color:red } bad:hover::foo {color:blue} button { color: green }")
	if len(s.Rules) != 2 || len(s.Diagnostics) < 2 {
		t.Fatalf("%+v", s)
	}
	e := Element{Type: "button"}
	v := Compile(UA(), s).Compute(&e, nil).Style
	if v.Color.G != namedColors["green"].G {
		t.Fatal(v.Color)
	}
}
func TestSelector(t *testing.T) {
	p := Element{Type: "app"}
	e := Element{Type: "button", Classes: []string{"ok"}, State: FocusVisible | Checked}
	for _, tt := range []struct {
		s     string
		match bool
	}{{"app > button.ok:focus-visible", true}, {"app button", true}, {"row > button", false}, {"app > button:disabled", false}, {"button:checked", true}} {
		s := Parse(tt.s + " {color:red}")
		en := Compile(Sheet{}, s)
		pv := en.Compute(&p, nil)
		got := en.Compute(&e, pv).Style.Color == namedColors["red"]
		if len(s.Rules) != 1 || got != tt.match {
			t.Fatalf("%s %+v", tt.s, s)
		}
	}
}
func FuzzTokenizerParser(f *testing.F) {
	for _, s := range []string{"a { color:red }", "\xff{bad:'\n; background: rgb(1,2,3)}", "/*", "a{--x:var(--x); color:var(--x)}"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 1<<16 {
			t.Skip()
		}
		_ = Tokenize(s)
		sheet := Parse(s)
		e := Element{Type: "a"}
		_ = Compile(UA(), sheet).Compute(&e, nil)
	})
}
func TestHugeNumbers(t *testing.T) {
	for _, v := range []string{"1e999", "NaN", "Inf", "1e308em"} {
		e := Element{Type: "app"}
		s := Compile(UA(), Parse("app{width:"+v+";font-size:"+v+"; opacity:"+v+"}")).Compute(&e, nil).Style
		if s.Width.Unit != "auto" || s.FontSize.Value != 16 || s.Opacity != 1 {
			t.Fatalf("%s: %+v", v, s)
		}
	}
}
func TestTokenRecovery(t *testing.T) {
	ts := Tokenize("a {color:'broken\n; background:url(foo); width:2px}")
	found := false
	for _, tok := range ts {
		if tok.Kind == "bad-string" {
			found = true
		}
	}
	if !found {
		t.Fatal(ts)
	}
}
func TestEscapesAndBadURL(t *testing.T) {
	for _, tt := range []struct{ src, kind, value string }{{`b\75 tton`, "ident", "button"}, {`#\78 `, "hash", "x"}, {`url(foo)`, "url", "foo"}, {`url(foo"bar);a`, "bad-url", "foo\"bar"}} {
		ts := Tokenize(tt.src)
		if len(ts) == 0 || ts[0].Kind != tt.kind || ts[0].Value != tt.value {
			t.Errorf("%q: %+v", tt.src, ts)
		}
	}
	e := Element{Type: "button"}
	s := Parse(`b\75 tton {color:red}`)
	if len(s.Rules) != 1 || Compile(Sheet{}, s).Compute(&e, nil).Style.Color != namedColors["red"] {
		t.Fatal(s)
	}
}
func TestRecoveryNested(t *testing.T) {
	tests := []struct {
		src          string
		rules, decls int
	}{{`button {broken: fn(a;b); color: red;}`, 1, 2}, {`button {bad { x:y; }; color:red} row{display:flex}`, 2, 1}, {`button:unknown { color:red } button {color:blue}`, 1, 1}, {`button { color:red`, 0, 0}}
	for _, tt := range tests {
		s := Parse(tt.src)
		if len(s.Rules) != tt.rules {
			t.Errorf("%q rules=%d diagnostics=%v", tt.src, len(s.Rules), s.Diagnostics)
			continue
		}
		if tt.rules > 0 && len(s.Rules[0].Declarations) != tt.decls {
			t.Errorf("%q decls=%d diagnostics=%v", tt.src, len(s.Rules[0].Declarations), s.Diagnostics)
		}
	}
}
