package css

import (
	"reflect"
	"testing"
)

// Every row in docs/css.md has at least one accepted and one rejected example.
// A valid example must override the row's initial; an invalid example must not.
func TestDocumentedPropertyRows(t *testing.T) {
	rows := []struct{ name, valid, invalid string }{
		{"color", "red", "rgb(nope)"}, {"opacity", ".5", "2"},
		{"background-color", "#abc", "#xyq"}, {"background", "red", "garbage"},
		{"border-width", "1px 2px 3px 4px", "1px 2px 3px 4px 5px"},
		{"border-style", "solid none", "dotted"}, {"border-color", "red blue", "invalid"},
		{"border", "2px solid red", "2px dashed red"},
		{"border-radius", "1px 2px 3px 4px", "2px / 1px"},
		{"outline", "1px solid red", "1px dotted"},
		{"outline-offset", "-2px", "1%"},
		{"box-shadow", "inset 1px 2px 3px -4px red, 0 0 blue", "1px 2px -3px red"},
		{"margin", "auto -1px 2% 3rem", "1px 2px 3px 4px 5px"},
		{"padding", "1px 2px 3px 4px", "-1px"},
		{"box-sizing", "border-box", "padding-box"},
		{"display", "flex", "grid"},
		{"flex-direction", "column", "diagonal"},
		{"flex-wrap", "wrap", "reverse"},
		{"flex", "2 3 10px", "2 nope"},
		{"gap", "1px 2px", "1px 2px 3px"},
		{"justify-content", "space-evenly", "between"},
		{"overflow", "hidden auto", "clip"},
		{"font-family", `"A B", serif`, "serif,,monospace"},
		{"font-size", "120%", "-1px"},
		{"font-weight", "bold", "550"},
		{"font-style", "italic", "solid"},
		{"line-height", "1.5", "-1px"},
		{"text-align", "center", "justify"},
		{"letter-spacing", "-1px", "1%"},
		{"cursor", "pointer", "grab"},
		{"accent-color", "red", "notacolor"},
		{"--accent", "red", "var(--missing)"},
	}
	for _, side := range sideNames {
		rows = append(rows,
			struct{ name, valid, invalid string }{"border-" + side + "-width", "2px", "-1px"},
			struct{ name, valid, invalid string }{"border-" + side + "-style", "solid", "dotted"},
			struct{ name, valid, invalid string }{"border-" + side + "-color", "red", "notacolor"},
			struct{ name, valid, invalid string }{"border-" + side, "2px solid red", "2px dashed red"},
			struct{ name, valid, invalid string }{"margin-" + side, "-1em", "banana"},
			struct{ name, valid, invalid string }{"padding-" + side, "2px", "-1px"},
		)
	}
	for _, corner := range cornerNames {
		rows = append(rows, struct{ name, valid, invalid string }{"border-" + corner + "-radius", "2%", "-1px"})
	}
	for _, name := range []string{"width", "height", "min-width", "min-height", "max-width", "max-height", "flex-basis"} {
		rows = append(rows, struct{ name, valid, invalid string }{name, "2rem", "-1px"})
	}
	for _, name := range []string{"flex-grow", "flex-shrink"} {
		rows = append(rows, struct{ name, valid, invalid string }{name, "2.5", "-1"})
	}
	for _, name := range []string{"row-gap", "column-gap"} {
		rows = append(rows, struct{ name, valid, invalid string }{name, "2px", "-2px"})
	}
	for _, name := range []string{"align-items", "align-self"} {
		rows = append(rows, struct{ name, valid, invalid string }{name, "center", "baseline"})
	}
	for _, name := range []string{"overflow-x", "overflow-y"} {
		rows = append(rows, struct{ name, valid, invalid string }{name, "scroll", "clip"})
	}
	for _, name := range []string{"outline-width"} {
		rows = append(rows, struct{ name, valid, invalid string }{name, "1px", "-1px"})
	}
	for _, name := range []string{"outline-style"} {
		rows = append(rows, struct{ name, valid, invalid string }{name, "solid", "dashed"})
	}
	for _, name := range []string{"outline-color"} {
		rows = append(rows, struct{ name, valid, invalid string }{name, "red", "nonsense"})
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			if r.name == "--accent" {
				en := Compile(Sheet{}, Parse(`button{--accent:red;color:var(--accent)}`))
				if en.Compute(&Element{Type: "button"}, nil).Style.Color != namedColors["red"] {
					t.Fatal("custom property not resolved")
				}
				bad := Compile(Sheet{}, Parse(`button{--accent:var(--missing);color:var(--accent)}`))
				if bad.Compute(&Element{Type: "button"}, nil).Style.Color != (Color{A: 1}) {
					t.Fatal("invalid custom property not handled")
				}
				return
			}
			initial := Compile(Sheet{}, Sheet{}).Compute(&Element{Type: "button"}, nil).Style
			valid := Compile(Sheet{}, Parse("button{"+r.name+":"+r.valid+"}")).Compute(&Element{Type: "button"}, nil).Style
			invalid := Compile(Sheet{}, Parse("button{"+r.name+":"+r.invalid+"}")).Compute(&Element{Type: "button"}, nil).Style
			if reflect.DeepEqual(*valid, *initial) {
				t.Errorf("valid value %q did not alter style", r.valid)
			}
			if !reflect.DeepEqual(*invalid, *initial) {
				t.Errorf("invalid value %q changed style", r.invalid)
			}
		})
	}
}
