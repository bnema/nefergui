package css

import (
	"strings"
	"testing"
)

func TestDeclarationDiagnostics(t *testing.T) {
	s := Parse("button {\n  unknown: 1;\n  color: not-a-color;\n  padding: 1px bogus;\n  color: var(--later);\n  color: blue;\n}")
	if len(s.Diagnostics) != 3 {
		t.Fatalf("diagnostics: %+v", s.Diagnostics)
	}
	for i, text := range []string{"unknown property", "invalid value", "invalid value"} {
		d := s.Diagnostics[i]
		if d.Line != i+2 || d.Col != 3 || !strings.Contains(d.Message, text) {
			t.Fatalf("diagnostic %d: %+v", i, d)
		}
	}
	if c := Compile(Sheet{}, s).Compute(&Element{Type: "button"}, nil).Style.Color; c != namedColors["blue"] {
		t.Fatalf("color: %+v", c)
	}
	_, ds := ParseInline("unknown: 1; color: bogus; color: var(--later)")
	if len(ds) != 2 || ds[0].Col != 1 || ds[1].Col != 13 {
		t.Fatalf("inline: %+v", ds)
	}
}
