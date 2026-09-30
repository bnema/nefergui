//go:build linux

package systemtest

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/bnema/nefergui/internal/harness"
)

const background = "#111111"

var opaque = color.NRGBA{255, 128, 0, 255}
var transparent = color.NRGBA{128, 64, 0, 128}

func requireHarness(t *testing.T) {
	t.Helper()
	if os.Getenv("NEFERGUI_HARNESS") != "1" {
		t.Skip("set NEFERGUI_HARNESS=1 to run headless Vulkan/Wayland acceptance")
	}
}

func binary(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rect")
	cmd := exec.Command("go", "build", "-o", path, "./examples/rect")
	cmd.Dir = filepath.Join("..", "..", "..")
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if data, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build rect: %v\n%s", err, data)
	}
	return path
}

type run struct {
	root              string
	result            harness.Result
	capture, readback image.Image
	timing            []timing
	lifecycle         []lifecycle
	summary           summary
}
type timing struct {
	Frame, Images, FDCount int
	Buffer, Generation     uint64
}
type lifecycle struct {
	Frame                            int
	Buffer, Generation, ReleasePoint uint64
	Event                            string
}
type summary struct {
	Frames      int `json:"frames"`
	FDStart     int `json:"fd_start"`
	FDPeak      int `json:"fd_peak"`
	FDAfter     int `json:"fd_after"`
	ImagesAfter int `json:"images_after"`
}

func jsonLines[T any](t *testing.T, path string) []T {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var rows []T
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var row T
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		rows = append(rows, row)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatalf("empty trace: %s", path)
	}
	return rows
}

func start(t *testing.T, bin, scale string, frames int, alpha bool) run {
	return startSized(t, bin, scale, frames, alpha, "800x600")
}
func startSized(t *testing.T, bin, scale string, frames int, alpha bool, output string) run {
	return startSizedMode(t, bin, scale, frames, alpha, output, false)
}
func startSizedMode(t *testing.T, bin, scale string, frames int, alpha bool, output string, list bool) run {
	t.Helper()
	root := filepath.Join(t.TempDir(), "artifacts")
	args := []string{bin, "--static", "--frames", fmt.Sprint(frames)}
	if list {
		args = append(args, "--list")
	}
	if alpha {
		args = append(args, "--transparent")
	}
	res, code := harness.Run(context.Background(), harness.Options{Size: output, Scale: scale, Layout: "us", Background: background, Out: root, Timeout: 60 * time.Second, ReadyTimeout: 10 * time.Second, Client: args})
	t.Logf("artifacts=%s code=%d status=%s", root, code, res.Status)
	if code != harness.Pass {
		data, _ := os.ReadFile(filepath.Join(root, "logs", "client.stderr"))
		t.Fatalf("harness: code=%d error=%s client stderr=%s", code, res.Error, data)
	}
	if res.Artifacts["capture"] == "" {
		t.Fatal("static client did not request a mapped capture")
	}
	capture, err := harness.LoadPNG(res.Artifacts["capture"])
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(res.Artifacts["debug"], "readback-*.png"))
	if err != nil || len(files) == 0 {
		t.Fatalf("readback: %v %v", files, err)
	}
	sort.Strings(files)
	readback, err := harness.LoadPNG(files[len(files)-1])
	if err != nil {
		t.Fatal(err)
	}
	var s summary
	data, err := os.ReadFile(filepath.Join(res.Artifacts["debug"], "resource-summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	return run{root, res, capture, readback, jsonLines[timing](t, filepath.Join(res.Artifacts["debug"], "frame-timings.jsonl")), jsonLines[lifecycle](t, filepath.Join(res.Artifacts["debug"], "buffer-lifecycle.jsonl")), s}
}

func size(img image.Image, w, h int) error {
	if img.Bounds().Dx() != w || img.Bounds().Dy() != h {
		return fmt.Errorf("size %v, want %dx%d", img.Bounds(), w, h)
	}
	return nil
}
func pixel(img image.Image, x, y int, want color.NRGBA) error {
	return harness.PixelProbe(img, x, y, want, 2)
}
func check(t *testing.T, img image.Image, x, y int, want color.NRGBA) {
	t.Helper()
	if err := pixel(img, x, y, want); err != nil {
		t.Fatal(err)
	}
}

// NeferWL configures the example's 400x300 toplevel to 800x600 logical.
func finalSize() (int, int) { return 800, 600 }

func TestOpaque(t *testing.T) {
	requireHarness(t)
	r := start(t, binary(t), "1", 24, false)
	w, h := finalSize()
	if err := size(r.readback, w, h); err != nil {
		t.Fatal(err)
	}
	check(t, r.readback, 0, 0, color.NRGBA{})
	check(t, r.readback, w/2, h/2, opaque)
	if err := size(r.capture, w, h); err != nil {
		t.Fatal(err)
	}
	// The opaque window covers the output: its clear is black. NeferWL's
	// presentation path applies a color transform to the orange rect.
	check(t, r.capture, 0, 0, color.NRGBA{0, 0, 0, 255})
	check(t, r.capture, w/2, h/2, color.NRGBA{251, 158, 65, 255})
}
func TestTransparent(t *testing.T) {
	requireHarness(t)
	r := start(t, binary(t), "1", 24, true)
	w, h := finalSize()
	if err := size(r.readback, w, h); err != nil {
		t.Fatal(err)
	}
	check(t, r.readback, 0, 0, color.NRGBA{})
	// NRGBA conversion unpremultiplies; inspect the encoded bytes directly.
	rgba, ok := r.readback.(*image.NRGBA)
	if !ok {
		t.Fatalf("readback format %T", r.readback)
	}
	got := rgba.NRGBAAt(w/2, h/2)
	for i, pair := range [][2]uint8{{got.R, transparent.R}, {got.G, transparent.G}, {got.B, transparent.B}, {got.A, transparent.A}} {
		if math.Abs(float64(pair[0])-float64(pair[1])) > 2 {
			t.Fatalf("premultiplied channel %d: got %v want %v", i, got, transparent)
		}
	}
}
func TestResize(t *testing.T) {
	requireHarness(t)
	r := start(t, binary(t), "1", 24, false)
	max := uint64(0)
	old := map[uint64]bool{}
	retired := map[uint64]bool{}
	for _, v := range r.timing {
		if v.Generation > max {
			max = v.Generation
		}
		if v.Generation == 1 {
			old[v.Buffer] = true
		}
	}
	if max <= 1 || len(old) == 0 {
		t.Fatalf("missing configure: generation=%d old=%v", max, old)
	}
	for _, v := range r.lifecycle {
		if old[v.Buffer] && (v.Event == "retired" || v.Event == "destroyed") {
			retired[v.Buffer] = true
		}
	}
	if len(retired) != len(old) {
		t.Fatalf("old images not retired: old=%v retired=%v", old, retired)
	}
	w, h := finalSize()
	if err := size(r.readback, w, h); err != nil {
		t.Fatal(err)
	}
}
func TestScale(t *testing.T) {
	requireHarness(t)
	bin := binary(t)
	for _, tc := range []struct {
		name   string
		factor float64
	}{{"1.5", 1.5}, {"2", 2}} {
		t.Run(tc.name, func(t *testing.T) {
			// Use a physical output of 800x600 logical pixels at this scale.
			// The compositor's --size is in physical output pixels.
			r := startSized(t, bin, tc.name, 24, false, fmt.Sprintf("%dx%d", int(math.Ceil(800*tc.factor)), int(math.Ceil(600*tc.factor))))
			w, h := int(math.Ceil(800*tc.factor)), int(math.Ceil(600*tc.factor))
			if err := size(r.readback, w, h); err != nil {
				t.Fatal(err)
			}
			check(t, r.readback, 0, 0, color.NRGBA{})
			check(t, r.readback, w/2, h/2, opaque)
			// Two pixels inside/outside the scaled 0.25 x boundary.
			check(t, r.readback, w/4-3, h/2, color.NRGBA{})
			check(t, r.readback, w/4+3, h/2, opaque)
		})
	}
}
func TestResources(t *testing.T) {
	requireHarness(t)
	r := start(t, binary(t), "1", 120, false)
	s := r.summary
	t.Logf("frames=%d fd_start=%d fd_peak=%d fd_after=%d images_after=%d", s.Frames, s.FDStart, s.FDPeak, s.FDAfter, s.ImagesAfter)
	if s.Frames != 120 || len(r.timing) != 120 || s.ImagesAfter != 0 || s.FDPeak-s.FDStart > 2 || s.FDAfter > s.FDStart {
		t.Fatalf("unbounded or leaked: %+v trace=%d", s, len(r.timing))
	}
	for _, v := range r.timing {
		if v.Images > 6 || v.Images < 1 || v.FDCount > s.FDStart+2 {
			t.Fatalf("frame %+v", v)
		}
	}
}
func TestValidation(t *testing.T) {
	requireHarness(t)
	if os.Getenv("NEFERGUI_VULKAN_VALIDATION") != "1" {
		t.Skip("set NEFERGUI_VULKAN_VALIDATION=1 for Vulkan layer acceptance")
	}
	// vkconfig/vulkaninfo are not required: the loader resolves installed layers.
	// A missing layer is a skip, not an untested successful run.
	if !validationLayerAvailable() {
		t.Skip("VK_LAYER_KHRONOS_validation not installed")
	}
	r := start(t, binary(t), "1", 30, false)
	if r.summary.ImagesAfter != 0 || r.summary.FDAfter > r.summary.FDStart {
		t.Fatalf("leaked objects: %+v", r.summary)
	}
	data, err := os.ReadFile(r.result.Artifacts["client_stderr"])
	if err != nil {
		t.Fatal(err)
	}
	if regexp.MustCompile(`(?i)(NEFERGUI_VULKAN_VALIDATION_(ERROR|WARNING)|validation (error|warning))`).Match(data) {
		t.Fatalf("validation messages: %s", data)
	}
}
func validationLayerAvailable() bool {
	paths := []string{"/usr/share/vulkan/explicit_layer.d", "/etc/vulkan/explicit_layer.d"}
	if v := os.Getenv("VK_LAYER_PATH"); v != "" {
		paths = append(strings.Split(v, string(os.PathListSeparator)), paths...)
	}
	for _, dir := range paths {
		files, _ := filepath.Glob(filepath.Join(dir, "*validation*.json"))
		if len(files) > 0 {
			return true
		}
	}
	return false
}

func TestCheckersNonVacuous(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 4, 4))
	img.SetNRGBA(2, 2, opaque)
	path := filepath.Join(t.TempDir(), "synthetic.png")
	if err := harness.SavePNG(path, img); err != nil {
		t.Fatal(err)
	}
	decoded, err := harness.LoadPNG(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := pixel(decoded, 2, 2, opaque); err != nil {
		t.Fatal(err)
	}
	if err := pixel(decoded, 2, 2, color.NRGBA{0, 0, 255, 255}); err == nil {
		t.Fatal("wrong color accepted")
	}
	if err := size(decoded, 4, 4); err != nil {
		t.Fatal(err)
	}
	if err := size(decoded, 5, 4); err == nil {
		t.Fatal("wrong size accepted")
	}
}

func TestListQuads(t *testing.T) {
	requireHarness(t)
	r := startSizedMode(t, binary(t), "1", 24, false, "800x600", true)
	img := r.readback
	probes := []struct {
		x, y int
		want color.NRGBA
	}{
		{100, 100, color.NRGBA{}},                 // rounded corner
		{120, 120, color.NRGBA{255, 0, 0, 255}},   // inside round
		{340, 160, color.NRGBA{}},                 // outside green clip
		{250, 160, color.NRGBA{0, 255, 0, 255}},   // clipped green fill
		{160, 200, color.NRGBA{128, 0, 128, 255}}, // half-blue over red
	}
	for _, p := range probes {
		got := color.NRGBAModel.Convert(img.At(p.x, p.y)).(color.NRGBA)
		t.Logf("pixel (%d,%d)=%v want=%v", p.x, p.y, got, p.want)
		for _, pair := range [][2]uint8{{got.R, p.want.R}, {got.G, p.want.G}, {got.B, p.want.B}, {got.A, p.want.A}} {
			if math.Abs(float64(pair[0])-float64(pair[1])) > 3 {
				t.Fatalf("pixel (%d,%d)=%v want=%v", p.x, p.y, got, p.want)
			}
		}
	}
}

func TestListStyles(t *testing.T) {
	requireHarness(t)
	bin := binary(t)
	for _, scale := range []int{1, 2} {
		t.Run(fmt.Sprint(scale), func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "artifacts")
			res, code := harness.Run(context.Background(), harness.Options{Size: "800x600", Scale: "1", Layout: "us", Background: background, Out: root, Timeout: 60 * time.Second, ReadyTimeout: 10 * time.Second, Client: []string{bin, "--static", "--styles", "--scale", fmt.Sprint(scale), "--frames", "24"}})
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
			bg := color.NRGBA{17, 17, 17, 255}
			probes := []struct {
				name string
				x, y int
				want color.NRGBA
			}{
				{"top border", 200 * scale, 123 * scale, color.NRGBA{255, 0, 0, 255}},
				{"right border", 276 * scale, 180 * scale, color.NRGBA{0, 255, 0, 255}},
				{"bottom border", 200 * scale, 236 * scale, color.NRGBA{0, 0, 255, 255}},
				{"left border", 123 * scale, 180 * scale, color.NRGBA{255, 255, 0, 255}},
				{"interior", 200 * scale, 180 * scale, color.NRGBA{255, 255, 255, 255}},
				{"rounded corner", 113 * scale, 113 * scale, bg},
				{"outline", 200 * scale, 114 * scale, color.NRGBA{255, 0, 255, 255}},
				{"far", 200 * scale, 285 * scale, bg},
				{"single-side left corner", 300 * scale, 43 * scale, color.NRGBA{255, 0, 0, 255}},
				{"single-side right corner", 379 * scale, 43 * scale, color.NRGBA{255, 0, 0, 255}},
				{"unequal corner top", 301 * scale, 242 * scale, color.NRGBA{255, 0, 0, 255}},
				{"unequal corner left", 300 * scale, 246 * scale, color.NRGBA{0, 0, 255, 255}},
			}
			for _, p := range probes {
				got := color.NRGBAModel.Convert(img.At(p.x, p.y)).(color.NRGBA)
				t.Logf("scale=%d %s (%d,%d)=%v want=%v", scale, p.name, p.x, p.y, got, p.want)
				if err := harness.PixelProbe(img, p.x, p.y, p.want, 3); err != nil {
					t.Error(err)
				}
			}
			x, y := 200*scale, 252*scale
			shadow := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			t.Logf("scale=%d shadow (%d,%d)=%v background=%v", scale, x, y, shadow, bg)
			if shadow.R > bg.R-3 || shadow.G > bg.G-3 || shadow.B > bg.B-3 {
				t.Errorf("shadow not darker: %v", shadow)
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
}
