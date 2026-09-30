//go:build linux

package systemtest

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"
	"time"

	"github.com/bnema/nefergui/internal/css"
	"github.com/bnema/nefergui/internal/harness"
	"github.com/bnema/nefergui/internal/layout"
	"github.com/bnema/nefergui/internal/presentation/vkdevice"
	"github.com/bnema/nefergui/internal/render"
	"github.com/bnema/nefergui/internal/text"
)

const sampleText = "office سلام 日本語"

func TestListText(t *testing.T) {
	requireHarness(t)
	bin := binary(t)
	catalog, err := text.Load(text.DirectorySource("../../../testdata/fonts"))
	if err != nil {
		t.Fatal(err)
	}
	measured, err := text.NewEngine(catalog).Measure(sampleText, text.Request{Families: []string{"Noto Sans"}, Size: 32}, 0)
	if err != nil {
		t.Fatal(err)
	}
	cmd := layout.Command{Op: "text", Color: css.Color{R: 1, G: 1, B: 1, A: 1}, Opacity: 1}
	for _, line := range measured.Lines {
		for _, shaped := range line.Runs {
			r := layout.Run{Face: shaped.Face, FaceID: shaped.Face.ID, Size: 32}
			for _, g := range shaped.Glyphs {
				r.Glyphs = append(r.Glyphs, layout.Glyph{ID: uint32(g.ID), X: 20 + g.X, Y: 40 + g.Y})
			}
			cmd.Runs = append(cmd.Runs, r)
		}
	}
	for _, scale := range []int{1, 2} {
		t.Run(fmt.Sprint(scale), func(t *testing.T) {
			prep, err := render.NewPreparer(vkdevice.AtlasSize, vkdevice.AtlasPages)
			if err != nil {
				t.Fatal(err)
			}
			frame, err := prep.Prepare([]layout.Command{cmd}, float64(scale), 800, 600)
			if err != nil {
				t.Fatal(err)
			}
			if len(frame.Quads) == 0 || len(frame.Uploads) == 0 {
				t.Fatal("no glyphs or uploads")
			}
			root := filepath.Join(t.TempDir(), "artifacts")
			res, code := harness.Run(context.Background(), harness.Options{Size: "800x600", Scale: "1", Layout: "us", Background: background, Out: root, Timeout: 60 * time.Second, ReadyTimeout: 10 * time.Second, Client: []string{bin, "--static", "--text", sampleText, "--scale", fmt.Sprint(scale), "--frames", "120"}})
			t.Logf("artifacts=%s code=%d status=%s", root, code, res.Status)
			if code != harness.Pass {
				data, _ := os.ReadFile(filepath.Join(root, "logs", "client.stderr"))
				t.Fatalf("harness: %s: %s", res.Error, data)
			}
			files, _ := filepath.Glob(filepath.Join(res.Artifacts["debug"], "readback-*.png"))
			sort.Strings(files)
			if len(files) == 0 {
				t.Fatal("no readback")
			}
			img, err := harness.LoadPNG(files[len(files)-1])
			if err != nil {
				t.Fatal(err)
			}
			var sum summary
			data, err := os.ReadFile(filepath.Join(res.Artifacts["debug"], "resource-summary.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(data, &sum); err != nil {
				t.Fatal(err)
			}
			if sum.Frames != 120 || sum.ImagesAfter != 0 || sum.FDPeak-sum.FDStart > 2 || sum.FDAfter > sum.FDStart {
				t.Fatalf("resource counts: %+v", sum)
			}
			timing := jsonLines[timing](t, filepath.Join(res.Artifacts["debug"], "frame-timings.jsonl"))
			if len(timing) != 120 {
				t.Fatalf("timings=%d", len(timing))
			}
			for _, row := range timing {
				if row.Images < 1 || row.Images > 6 || row.FDCount > sum.FDStart+2 {
					t.Fatalf("unstable frame: %+v", row)
				}
			}
			if os.Getenv("NEFERGUI_VULKAN_VALIDATION") == "1" {
				data, err := os.ReadFile(res.Artifacts["client_stderr"])
				if err != nil {
					t.Fatal(err)
				}
				if regexp.MustCompile(`(?i)(NEFERGUI_VULKAN_VALIDATION_(ERROR|WARNING)|validation (error|warning))`).Match(data) {
					t.Fatalf("validation: %s", data)
				}
			}
			// Reconstruct the CPU coverage directly from the same rasterized masks
			// that Prepare packed into atlas upload rectangles.
			pages := make([][]byte, vkdevice.AtlasPages)
			for i := range pages {
				pages[i] = make([]byte, vkdevice.AtlasSize*vkdevice.AtlasSize)
			}
			for _, u := range frame.Uploads {
				for y := 0; y < u.Rect.Dy(); y++ {
					copy(pages[u.Page][(u.Rect.Min.Y+y)*vkdevice.AtlasSize+u.Rect.Min.X:], u.Bytes[y*u.Rect.Dx():(y+1)*u.Rect.Dx()])
				}
			}
			var box image.Rectangle
			expected := make([]byte, 800*600)
			for _, q := range frame.Quads {
				if q.Op != "glyph" {
					continue
				}
				r := image.Rect(int(q.Rect.X), int(q.Rect.Y), int(q.Rect.X+q.Rect.W), int(q.Rect.Y+q.Rect.H))
				box = box.Union(r)
				for y := 0; y < r.Dy(); y++ {
					for x := 0; x < r.Dx(); x++ {
						expected[(r.Min.Y+y)*800+r.Min.X+x] = pages[q.Glyph.Page][(q.Glyph.Rect.Min.Y+y)*vkdevice.AtlasSize+q.Glyph.Rect.Min.X+x]
					}
				}
			}
			var ink image.Rectangle
			cpu, gpu := 0.0, 0.0
			for y := 0; y < 600; y++ {
				for x := 0; x < 800; x++ {
					p := img.At(x, y)
					r16, g16, b16, a16 := p.RGBA()
					r, g, b, a := r16>>8, g16>>8, b16>>8, a16>>8
					if expected[y*800+x] > 0 {
						cpu += float64(expected[y*800+x]) / 255
					}
					if !image.Pt(x, y).In(box) && (r != 17 || g != 17 || b != 17 || a != 255) {
						t.Fatalf("outside ink box at %d,%d: %v", x, y, p)
					}
					if r > 17 {
						gpu += float64(r-17) / 238
						ink = ink.Union(image.Rect(x, y, x+1, y+1))
					}
				}
			}
			if ink.Empty() || math.Abs(float64(ink.Min.X-box.Min.X)) > 2 || math.Abs(float64(ink.Min.Y-box.Min.Y)) > 2 || math.Abs(float64(ink.Max.X-box.Max.X)) > 2 || math.Abs(float64(ink.Max.Y-box.Max.Y)) > 2 || math.Abs(gpu-cpu) > cpu*.1 {
				t.Fatalf("ink=%v measure/raster box=%v coverage gpu=%.2f cpu=%.2f", ink, box, gpu, cpu)
			}
			t.Logf("scale=%d ink=%v measure/raster box=%v gpu coverage=%.2f cpu coverage=%.2f resources=%+v", scale, ink, box, gpu, cpu, sum)
		})
	}
}
