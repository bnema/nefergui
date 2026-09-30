package nefergui

import (
	"encoding/json"
	"strings"
	"testing"
)

// Semantic controls must contribute CSS-computed paint rather than Go-side colors.
func TestNewControlDisplayListJSON(t *testing.T) {
	r := newRuntime()
	var selected string
	v := 0.0
	view := func(f *Frame) {
		n := f.Root()
		n.Separator(Key("sep"))
		n.Spacer(Key("gap"))
		n.Icon("star", Key("icon"))
		n.Radio("option", "one", &selected, Key("radio"))
		n.Slider("volume", &v, 0, 10, 1, Key("slider"))
	}
	if !r.Build(view) {
		t.Fatal("build")
	}
	data, err := json.Marshal(r.output.Display)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"Op":"rect","ID":"root/k3:sep"`) || !strings.Contains(string(data), `"R":0.5019607843137255`) {
		t.Fatalf("separator should use UA computed background: %s", data)
	}
	for _, id := range []string{"root/k3:sep", "root/k3:gap", "root/k4:icon", "root/k5:radio", "root/k6:slider"} {
		if resultByID(r.output.Tree, id) == nil {
			t.Fatalf("missing semantic node %s", id)
		}
	}
}
