package css

import "testing"

func TestDocumentedGrammar(t *testing.T) {
	selectors := []struct {
		selector string
		match    bool
	}{
		{"button", true}, {"BUTTON", true}, {".ok", true}, {"#x", true},
		{"button.ok#x:hover", true}, {"app button", true}, {"app > button", true},
		{"button, row", true}, {"button:active", false}, {"button:focus", false},
		{"button:focus-visible", false}, {"button:disabled", false}, {"button:checked", false},
		{"app > row > button", false}, {"row button", false},
	}
	for _, tt := range selectors {
		t.Run(tt.selector, func(t *testing.T) {
			en := Compile(Sheet{}, Parse(tt.selector+" {color:red}"))
			p := en.Compute(&Element{Type: "app"}, nil)
			c := en.Compute(&Element{Type: "button", ID: "x", Classes: []string{"ok"}, State: Hover}, p).Style.Color == namedColors["red"]
			if c != tt.match {
				t.Fatalf("match=%v expected %v", c, tt.match)
			}
		})
	}
	bad := []string{"*", "[type=button]", "button+row", "button~row", "button:not(.x)", "button::before", "button:future", "button, :future"}
	for _, s := range bad {
		t.Run("invalid-"+s, func(t *testing.T) {
			parsed := Parse(s + "{color:red} button{color:blue}")
			if len(parsed.Rules) != 1 || len(parsed.Diagnostics) == 0 || Compile(Sheet{}, parsed).Compute(&Element{Type: "button"}, nil).Style.Color != namedColors["blue"] {
				t.Fatalf("did not drop whole bad rule: %+v", parsed)
			}
		})
	}
	t.Run("unclosed EOF", func(t *testing.T) {
		parsed := Parse(`button{color:red`)
		if len(parsed.Rules) != 0 || len(parsed.Diagnostics) == 0 {
			t.Fatal(parsed)
		}
	})
	cases := []struct {
		src  string
		want string
	}{
		{`button{bad: rgb(1;2;3); color:red}`, "red"},
		{`button{bad: [1;2;3]; color:blue}`, "blue"},
		{`button{bad:{one;two}; color:green}`, "green"},
		{`button{no-colon; color:red}`, "red"},
	}
	for _, tt := range cases {
		t.Run(tt.src, func(t *testing.T) {
			p := Parse(tt.src)
			if len(p.Rules) != 1 {
				t.Fatal(p)
			}
			want := namedColors[tt.want]
			if c := Compile(Sheet{}, p).Compute(&Element{Type: "button"}, nil).Style.Color; c != want {
				t.Fatalf("got %+v, want %+v; parse %+v", c, want, p)
			}
		})
	}
}

func TestDocumentedValueGrammar(t *testing.T) {
	colors := []struct{ valid, invalid string }{
		{"red", "not-a-color"}, {"transparent", "rgb(nan,0,0)"}, {"currentColor", "#xx0"},
		{"#abc", "#12"}, {"#abcd", "#12345"}, {"#112233", "#1234567"}, {"#11223344", "#123456789"},
		{"rgb(255,0,0)", "rgb(1,2)"}, {"rgba(255 0 0 / 50%)", "rgba(1,2,3)"},
		{"hsl(0 100% 50%)", "hsl(0 50 50%)"}, {"hsla(0,100%,50%,.5)", "hsla(0,100%,50%)"},
	}
	for _, tt := range colors {
		t.Run(tt.valid, func(t *testing.T) {
			if _, ok := parseColor(tt.valid, Color{A: 1}); !ok {
				t.Error("valid color rejected")
			}
			if _, ok := parseColor(tt.invalid, Color{A: 1}); ok {
				t.Errorf("invalid color accepted %s", tt.invalid)
			}
		})
	}
	lengths := []struct{ valid, invalid string }{
		{"2px", "2pt"}, {"2%", "2vw"}, {"2em", "2ex"}, {"2rem", "2vh"}, {"0", "2"},
	}
	for _, tt := range lengths {
		t.Run(tt.valid, func(t *testing.T) {
			if _, ok := length(tt.valid, false, false); !ok {
				t.Fatal("valid length rejected")
			}
			if _, ok := length(tt.invalid, false, false); ok {
				t.Fatalf("invalid length accepted %s", tt.invalid)
			}
		})
	}
	if _, ok := length("auto", false, true); !ok {
		t.Fatal("auto rejected where allowed")
	}
	if _, ok := length("auto", false, false); ok {
		t.Fatal("auto accepted where disallowed")
	}
	if _, ok := length("-1px", true, false); !ok {
		t.Fatal("signed length rejected")
	}
	if _, ok := length("-1px", false, false); ok {
		t.Fatal("negative length accepted")
	}
}
