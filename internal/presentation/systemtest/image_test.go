//go:build linux

package systemtest

import (
	"context"
	"encoding/json"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"
	"time"

	"github.com/bnema/nefergui/internal/harness"
)

func TestListImage(t *testing.T) {
	requireHarness(t)
	bin := binary(t)
	for _, scale := range []int{1, 2} {
		t.Run(fmt.Sprint(scale), func(t *testing.T) {
			for _, changing := range []bool{false, true} {
				name := "static"
				frames := 24
				if changing {
					name = "changing"
					frames = 120
				}
				t.Run(name, func(t *testing.T) {
					root := filepath.Join(t.TempDir(), "artifacts")
					args := []string{bin, "--static", "--image", "--scale", fmt.Sprint(scale), "--frames", fmt.Sprint(frames)}
					if changing {
						args = append(args, "--changing-image")
					}
					res, code := harness.Run(context.Background(), harness.Options{Size: "800x600", Scale: "1", Layout: "us", Background: background, Out: root, Timeout: 60 * time.Second, ReadyTimeout: 10 * time.Second, Client: args})
					t.Logf("artifacts=%s code=%d status=%s", root, code, res.Status)
					if code != harness.Pass {
						data, _ := os.ReadFile(filepath.Join(root, "logs", "client.stderr"))
						t.Fatalf("harness: %s %s", res.Error, data)
					}
					files, _ := filepath.Glob(filepath.Join(res.Artifacts["debug"], "readback-*.png"))
					sort.Strings(files)
					if len(files) == 0 {
						t.Fatal("missing readback")
					}
					img, err := harness.LoadPNG(files[len(files)-1])
					if err != nil {
						t.Fatal(err)
					}
					if !changing {
						bg := color.NRGBA{17, 17, 17, 255}
						probes := []struct {
							name string
							x, y int
							want color.NRGBA
						}{
							{"red", 108, 108, color.NRGBA{136, 8, 8, 255}},
							{"green", 148, 108, color.NRGBA{8, 136, 8, 255}},
							{"blue", 108, 148, color.NRGBA{8, 8, 136, 255}},
							{"yellow", 148, 148, color.NRGBA{136, 136, 8, 255}},
							{"round corner", 100, 100, bg},
							{"between images", 170, 130, bg},
							{"second image", 220, 120, color.NRGBA{255, 128, 0, 255}},
						}
						for _, p := range probes {
							x, y := p.x*scale, p.y*scale
							t.Logf("scale=%d %s (%d,%d)=%v want=%v", scale, p.name, x, y, color.NRGBAModel.Convert(img.At(x, y)), p.want)
							if err := harness.PixelProbe(img, x, y, p.want, 4); err != nil {
								t.Error(err)
							}
						}
					}
					if changing {
						data, err := os.ReadFile(filepath.Join(res.Artifacts["debug"], "resource-summary.json"))
						if err != nil {
							t.Fatal(err)
						}
						var sum summary
						if err = json.Unmarshal(data, &sum); err != nil {
							t.Fatal(err)
						}
						t.Logf("resources=%+v", sum)
						if sum.Frames != 120 || sum.ImagesAfter != 0 || sum.FDPeak-sum.FDStart > 2 || sum.FDAfter > sum.FDStart {
							t.Errorf("unstable resources: %+v", sum)
						}
						rows := jsonLines[timing](t, filepath.Join(res.Artifacts["debug"], "frame-timings.jsonl"))
						if len(rows) != 120 {
							t.Errorf("frame timings=%d", len(rows))
						}
						for _, row := range rows {
							if row.Images < 1 || row.Images > 6 || row.FDCount > sum.FDStart+2 {
								t.Errorf("unstable frame: %+v", row)
								break
							}
						}
					}
					if os.Getenv("NEFERGUI_VULKAN_VALIDATION") == "1" {
						data, err := os.ReadFile(res.Artifacts["client_stderr"])
						if err != nil {
							t.Fatal(err)
						}
						if regexp.MustCompile(`(?i)(NEFERGUI_VULKAN_VALIDATION_(ERROR|WARNING)|validation (error|warning))`).Match(data) {
							t.Errorf("validation: %s", data)
						}
					}
				})
			}
		})
	}
}
