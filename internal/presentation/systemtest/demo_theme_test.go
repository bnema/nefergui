//go:build linux

package systemtest

import (
	"encoding/json"
	"image/color"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bnema/nefergui/internal/harness"
)

func TestDemoThemeAndPreferences(t *testing.T) {
	requireHarness(t)
	bin := demoBinary(t)
	script := filepath.Join(t.TempDir(), "input.txt")
	if err := os.WriteFile(script, []byte("sleep 8s\nmove 890 24\nclick left\nsleep 500ms\nmove 80 166\nclick left\nsleep 500ms\nmove 600 580\nsleep 1s\n"), 0600); err != nil {
		t.Fatal(err)
	}
	res := runDemo(t, bin, 1, 400, script, 12*time.Second)
	data, err := os.ReadFile(filepath.Join(res.Artifacts["debug"], "demo-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Page     string
		DarkMode bool
	}
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if !state.DarkMode || state.Page != "settings" {
		t.Fatalf("theme/navigation did not update: %+v", state)
	}
	img := lastMappedFrame(t, res.Artifacts["frames"])
	for _, p := range []struct {
		x, y int
		want color.NRGBA
	}{{600, 80, color.NRGBA{24, 24, 27, 255}}, {240, 230, color.NRGBA{24, 24, 27, 255}}} {
		if err := harness.PixelProbe(img, p.x, p.y, p.want, 3); err != nil {
			t.Error(err)
		}
	}
}
