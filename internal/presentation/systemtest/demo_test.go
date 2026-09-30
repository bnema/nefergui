//go:build linux

package systemtest

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bnema/nefergui/internal/harness"
)

func demoBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "demo")
	cmd := exec.Command("go", "build", "-o", bin, "./examples/demo")
	cmd.Dir = "../../.."
	cmd.Env = append(os.Environ(), "GOWORK=off", "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build demo: %v %s", err, out)
	}
	return bin
}
func runDemo(t *testing.T, bin string, scale, frames int, input string, minDuration time.Duration) harness.Result {
	t.Helper()
	root := filepath.Join(t.TempDir(), "artifacts")
	client := []string{bin, "--static", "--frames", fmt.Sprint(frames), "--min-duration", minDuration.String()}
	res, code := harness.Run(context.Background(), harness.Options{Size: fmt.Sprintf("%dx%d", 960*scale, 640*scale), Scale: fmt.Sprint(scale), Layout: "us", Background: background, Out: root, Input: input, Timeout: 60 * time.Second, ReadyTimeout: 10 * time.Second, Client: client})
	t.Logf("scale=%d frames=%d code=%d artifacts=%s", scale, frames, code, root)
	if code != harness.Pass {
		b, _ := os.ReadFile(filepath.Join(root, "logs", "client.stderr"))
		t.Fatalf("harness: %s stderr=%s", res.Error, b)
	}
	return res
}
func TestDemoStatic(t *testing.T) {
	requireHarness(t)
	bin := demoBinary(t)
	for _, scale := range []int{1, 2} {
		t.Run(fmt.Sprint(scale), func(t *testing.T) {
			res := runDemo(t, bin, scale, 24, "", 0)
			files, err := filepath.Glob(filepath.Join(res.Artifacts["debug"], "readback-*.png"))
			if err != nil || len(files) == 0 {
				t.Fatalf("readback files: %v %v", files, err)
			}
			img, err := harness.LoadPNG(files[len(files)-1])
			if err != nil {
				t.Fatal(err)
			}
			if img.Bounds().Dx() != 960*scale || img.Bounds().Dy() != 640*scale {
				t.Fatalf("size %v", img.Bounds())
			}
			for _, p := range []struct {
				name string
				x, y int
				want color.NRGBA
			}{
				{"background", 5, 5, color.NRGBA{255, 255, 255, 255}},
				{"button", 740, 185, color.NRGBA{212, 212, 216, 255}},
			} {
				x, y := p.x*scale, p.y*scale
				t.Logf("%s (%d,%d)=%v", p.name, x, y, color.NRGBAModel.Convert(img.At(x, y)))
				if err := harness.PixelProbe(img, x, y, p.want, 8); err != nil {
					t.Error(err)
				}
			}
			// Labels must reach the screen. Each region lies inside one control
			// fill: most pixels must match it (the control is where expected) and
			// enough must differ (glyph ink). Background probes alone once passed
			// with every control label missing.
			for _, l := range []struct {
				name           string
				x0, y0, x1, y1 int
				fill           color.NRGBA
			}{
				{"Apply", 810, 177, 866, 198, color.NRGBA{212, 212, 216, 255}},
				{"Untitled.txt", 24, 113, 115, 136, color.NRGBA{228, 228, 231, 255}},
				{"Dark mode", 850, 14, 935, 35, color.NRGBA{255, 255, 255, 255}},
				{"Name placeholder", 749, 130, 840, 152, color.NRGBA{250, 250, 250, 255}},
			} {
				n, total := ink(img, l.x0*scale, l.y0*scale, l.x1*scale, l.y1*scale, l.fill)
				if n < 40*scale*scale || n > total/2 {
					t.Errorf("%s: %d/%d ink pixels, label not painted in its control", l.name, n, total)
				}
			}
		})
	}
}

// ink counts pixels in [x0,x1)×[y0,y1) far from fill, and the region size.
func ink(img image.Image, x0, y0, x1, y1 int, fill color.NRGBA) (n, total int) {
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			if absDiff(c.R, fill.R)+absDiff(c.G, fill.G)+absDiff(c.B, fill.B) > 30 {
				n++
			}
			total++
		}
	}
	return n, total
}
func absDiff(a, b uint8) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}

// TestDemoCursorShapes hovers a menu button, the input and the background,
// then reads the cursor shapes NeferWL applied from its run log (the harness
// enables wayland debug logging). The shapes come from the demo's CSS: UA
// `cursor: pointer` on buttons and `cursor: text` on inputs.
func TestDemoCursorShapes(t *testing.T) {
	requireHarness(t)
	bin := demoBinary(t)
	script := filepath.Join(t.TempDir(), "input.txt")
	// Same cold-start allowance as TestDemoInputAndResources: the script
	// starts at compositor launch, not when the demo maps.
	if err := os.WriteFile(script, []byte("sleep 8s\nmove 80 123\nsleep 1s\nmove 800 140\nsleep 1s\nmove 100 580\nsleep 1s\n"), 0600); err != nil {
		t.Fatal(err)
	}
	res := runDemo(t, bin, 1, 400, script, 12*time.Second)
	data, err := os.ReadFile(res.Artifacts["compositor_run_log"])
	if err != nil {
		t.Fatal(err)
	}
	var shapes []string
	for _, line := range strings.Split(string(data), "\n") {
		var entry struct{ Message, Shape string }
		if json.Unmarshal([]byte(line), &entry) == nil && entry.Message == "cursor" && entry.Shape != "" {
			if len(shapes) == 0 || shapes[len(shapes)-1] != entry.Shape {
				shapes = append(shapes, entry.Shape)
			}
		}
	}
	if got := strings.Join(shapes, ","); got != "pointer,text,default" {
		t.Fatalf("cursor shapes %q, want pointer,text,default", got)
	}
}

func TestDemoInputAndResources(t *testing.T) {
	requireHarness(t)
	bin := demoBinary(t)
	script := filepath.Join(t.TempDir(), "input.txt")
	// The compositor starts the script at launch, not when the client maps;
	// wait past cold start (font discovery) before injecting input. The demo
	// keeps presenting for 12 s so the run outlasts the ~10 s script however
	// fast frames render.
	if err := os.WriteFile(script, []byte("sleep 8s\nmove 800 140\nsleep 200ms\nclick left\nsleep 200ms\ntype Example\nsleep 200ms\nmove 820 186\nsleep 200ms\nclick left\nsleep 1s\n"), 0600); err != nil {
		t.Fatal(err)
	}
	res := runDemo(t, bin, 1, 400, script, 12*time.Second)
	data, err := os.ReadFile(filepath.Join(res.Artifacts["debug"], "demo-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state struct{ Name, Status string }
	if err = json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if state.Name != "Example" || state.Status != "Author set to Example · session only" {
		layout, _ := os.ReadFile(filepath.Join(res.Artifacts["debug"], "layout.json"))
		t.Fatalf("state %+v layout=%s", state, layout)
	}
	var s summary
	data, err = os.ReadFile(filepath.Join(res.Artifacts["debug"], "resource-summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	t.Logf("frames=%d fd_start=%d fd_peak=%d fd_after=%d images_after=%d", s.Frames, s.FDStart, s.FDPeak, s.FDAfter, s.ImagesAfter)
	// FDStart > 0 proves the summary fields were actually decoded.
	if s.Frames < 400 || s.FDStart == 0 || s.ImagesAfter != 0 || s.FDPeak-s.FDStart > 2 || s.FDAfter > s.FDStart {
		t.Fatalf("resources %+v", s)
	}
}

// TestDemoIndicators returns to the editor, selects the second radio and
// clicks the slider track, then checks the model and the painted indicators
// in the captured frame: the selected radio and the slider fill up to the
// thumb use the accent color, the rest of the track does not.
func TestDemoIndicators(t *testing.T) {
	requireHarness(t)
	bin := demoBinary(t)
	script := filepath.Join(t.TempDir(), "input.txt")
	// Same cold-start allowance as TestDemoInputAndResources.
	if err := os.WriteFile(script, []byte("sleep 8s\nmove 80 166\nsleep 200ms\nclick left\nsleep 800ms\nmove 80 123\nsleep 200ms\nclick left\nsleep 500ms\nmove 800 312\nsleep 200ms\nclick left\nsleep 300ms\nmove 883 377\nclick left\nsleep 300ms\nmove 700 560\nsleep 1s\n"), 0600); err != nil {
		t.Fatal(err)
	}
	res := runDemo(t, bin, 1, 400, script, 13*time.Second)
	data, err := os.ReadFile(filepath.Join(res.Artifacts["debug"], "demo-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Page, Density string
		Volume        float64
	}
	if err = json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if state.Page != "home" || state.Density != "compact" || state.Volume != 70 {
		t.Fatalf("state %+v", state)
	}
	img := lastMappedFrame(t, res.Artifacts["frames"])
	accent := color.NRGBA{82, 82, 91, 255} // light theme accent
	// Muted paint is the demo text color at 45% alpha over the inspector. Each probe
	// expects a specific color, so missing paint (plain white) fails too.
	muted := color.NRGBA{151, 151, 152, 255}
	for _, p := range []struct {
		name string
		x, y int
		want color.NRGBA
	}{
		{"selected radio", 743, 304, accent},
		{"unselected radio outline", 741, 273, muted},
		{"slider fill", 790, 377, accent},
		{"slider rail", 930, 377, muted},
	} {
		t.Logf("%s (%d,%d)=%v", p.name, p.x, p.y, color.NRGBAModel.Convert(img.At(p.x, p.y)))
		if err := harness.PixelProbe(img, p.x, p.y, p.want, 12); err != nil {
			t.Errorf("%s: %v", p.name, err)
		}
	}
}

// lastMappedFrame is the newest compositor frame still showing the demo: the
// capture request fires before scripted input, and the final frames follow
// client exit.
func lastMappedFrame(t *testing.T, dir string) image.Image {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "frame-*.png"))
	if err != nil {
		t.Fatal(err)
	}
	for i := len(files) - 1; i >= 0; i-- {
		img, err := harness.LoadPNG(files[i])
		if err != nil {
			continue // possibly still being written
		}
		if harness.PixelProbe(img, 5, 5, color.NRGBA{255, 255, 255, 255}, 8) == nil || harness.PixelProbe(img, 5, 5, color.NRGBA{32, 32, 35, 255}, 8) == nil {
			return img
		}
	}
	t.Fatalf("no mapped demo frame in %s", dir)
	return nil
}
