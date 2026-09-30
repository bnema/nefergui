//go:build nefergui_debug

package ui

import (
	"strings"
	"testing"
)

func TestNodeRectOutsideStackDiagnostic(t *testing.T) {
	r := newRuntime()
	var diags []string
	r.Build(func(f *Frame) {
		root := f.Root()
		root.Box(Key("bad")).Rect(1, 2, 3, 4)
		root.Stack().Box(Key("ok")).Rect(1, 2, 3, 4)
		diags = f.Diagnostics()
	})
	if len(diags) != 1 || !strings.Contains(diags[0], "not a Stack") {
		t.Fatalf("diagnostics %q", diags)
	}
}
