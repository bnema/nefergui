package css

import "testing"

func TestShadowColorFunctionCommas(t *testing.T) {
	v, ok := parseValue("box-shadow", "1px 2px rgb(255,0,0), 3px 4px rgba(0,0,255,.5)", Color{}, 16, 16)
	if !ok || len(v.Shadows) != 2 || v.Shadows[0].Color != namedColors["red"] || v.Shadows[1].Color.B != 1 {
		t.Fatalf("shadows: %+v %v", v, ok)
	}
}

func TestVarInsideQuotedValue(t *testing.T) {
	v, ok := resolveVars(`"var(--missing)" var(--x)`, map[string]string{"--x": "red"}, map[string]bool{}, 0)
	if !ok || v != `"var(--missing)" red` {
		t.Fatalf("got %q %v", v, ok)
	}
	v, ok = resolveVars(`var(--x, "var(--missing)")`, nil, map[string]bool{}, 0)
	if !ok || v != `"var(--missing)"` {
		t.Fatalf("fallback got %q %v", v, ok)
	}
}
