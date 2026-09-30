package css

import (
	"fmt"
	"testing"
)

func TestInternEvictionAndSelectorIdentity(t *testing.T) {
	en := Compile(Sheet{}, Parse(".keep{color:red} .other{color:blue} app .keep{opacity:.5}"))
	root := en.Compute(&Element{Type: "app"}, nil)
	for frame := 0; frame < 80; frame++ {
		en.Compute(&Element{Type: "button", ID: fmt.Sprint(frame), Classes: []string{fmt.Sprintf("transient-%d", frame)}}, root)
		en.EndFrame()
		root = en.Compute(&Element{Type: "app"}, nil)
		keep := en.Compute(&Element{Type: "button", Classes: []string{"keep"}}, root)
		if keep.Style.Color != namedColors["red"] || keep.Style.Opacity != .5 {
			t.Fatalf("selector lost at frame %d: %+v", frame, keep.Style)
		}
	}
	en.EndFrame()
	en.EndFrame()
	if len(en.ids) > 10 || len(en.sets) > 2 || len(en.classMembers) > 2 || len(en.memo) > 3 {
		t.Fatalf("tables not bounded: ids=%d sets=%d members=%d memo=%d", len(en.ids), len(en.sets), len(en.classMembers), len(en.memo))
	}
	en.Compute(&Element{Type: "button", Classes: []string{"other"}}, nil)
	if got := en.Compute(&Element{Type: "button", Classes: []string{"keep"}}, nil).Style.Color; got != namedColors["red"] {
		t.Fatalf("reused class matched wrong rule: %+v", got)
	}
}
