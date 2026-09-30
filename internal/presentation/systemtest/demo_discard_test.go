//go:build linux

package systemtest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDemoDiscardProtection(t *testing.T) {
	requireHarness(t)
	bin := demoBinary(t)
	for _, tc := range []struct {
		name, input    string
		empty, pending bool
	}{
		{"armed", "move 140 24\nclick left\n", false, true},
		{"cancel", "move 140 24\nclick left\nsleep 300ms\nmove 275 24\nclick left\n", false, false},
		{"confirmed", "move 140 24\nclick left\nsleep 300ms\nmove 140 24\nclick left\n", true, false},
		{"edited", "move 140 24\nclick left\nsleep 300ms\nmove 300 120\nclick left\ntype Added\n", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := filepath.Join(t.TempDir(), "input.txt")
			if err := os.WriteFile(script, []byte("sleep 8s\n"+tc.input+"sleep 1s\n"), 0600); err != nil {
				t.Fatal(err)
			}
			res := runDemo(t, bin, 1, 400, script, 11*time.Second)
			data, err := os.ReadFile(filepath.Join(res.Artifacts["debug"], "demo-state.json"))
			if err != nil {
				t.Fatal(err)
			}
			var state struct {
				Notes      string
				NewPending bool
			}
			if err := json.Unmarshal(data, &state); err != nil {
				t.Fatal(err)
			}
			if (state.Notes == "") != tc.empty || state.NewPending != tc.pending {
				t.Fatalf("discard state: %+v", state)
			}
			if tc.name == "edited" && !strings.Contains(state.Notes, "Added") {
				t.Fatal("editor input not delivered")
			}
		})
	}
}
