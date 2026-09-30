package harness

import (
	"context"
	"image"
	"image/color"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestAssertions(t *testing.T) {
	img := image.NewNRGBA(image.Rect(4, 5, 6, 7))
	c := color.NRGBA{51, 102, 153, 255}
	for y := 5; y < 7; y++ {
		for x := 4; x < 6; x++ {
			img.Set(x, y, c)
		}
	}
	for _, tc := range []struct {
		name string
		err  error
		pass bool
	}{
		{"probe", PixelProbe(img, 4, 5, c, 0), true}, {"outside", PixelProbe(img, 0, 0, c, 0), false},
		{"tolerated", PixelProbe(img, 4, 5, color.NRGBA{52, 102, 153, 255}, 1), true},
		{"color", UniformRegion(img, Rect{4, 5, 2, 2}, c, 0), true},
		{"bounds", BoundsCheck(img, Rect{4, 5, 3, 2}), false}, {"empty", BoundsCheck(img, Rect{4, 5, 0, 2}), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if (tc.err == nil) != tc.pass {
				t.Fatalf("error %v, want pass %v", tc.err, tc.pass)
			}
		})
	}
	changed := image.NewNRGBA(img.Bounds())
	for y := 5; y < 7; y++ {
		for x := 4; x < 6; x++ {
			changed.Set(x, y, c)
		}
	}
	changed.Set(4, 5, color.NRGBA{54, 102, 153, 255})
	for _, tc := range []struct {
		tolerance uint8
		max       float64
		pass      bool
		ratio     float64
	}{{0, .24, false, .25}, {0, .25, true, .25}, {3, 0, true, 0}} {
		pass, ratio, diff, err := CompareGolden(changed, img, tc.tolerance, tc.max)
		if err != nil || pass != tc.pass || ratio != tc.ratio || diff == nil {
			t.Fatalf("%+v: %v %v %v", tc, pass, ratio, err)
		}
	}
	alpha := image.NewNRGBA(img.Bounds())
	for y := 5; y < 7; y++ {
		for x := 4; x < 6; x++ {
			alpha.SetNRGBA(x, y, c)
		}
	}
	alpha.SetNRGBA(4, 5, color.NRGBA{51, 102, 153, 254})
	pass, ratio, diff, err := CompareGolden(alpha, img, 0, 0)
	if err != nil || pass || ratio != .25 || diff == nil || diff.NRGBAAt(4, 5) == (color.NRGBA{0, 0, 0, 255}) {
		t.Fatalf("alpha diff: %v %v %v %v", pass, ratio, diff, err)
	}
	if _, _, _, err := CompareGolden(img, image.NewNRGBA(image.Rect(0, 0, 1, 1)), 0, 0); err == nil {
		t.Fatal("different bounds accepted")
	}
	if _, _, _, err := CompareGolden(img, img, 0, 1.1); err == nil {
		t.Fatal("invalid ratio accepted")
	}
	for _, s := range []string{"red", "#GG0000", "#12345"} {
		if _, err := ParseColor(s); err == nil {
			t.Fatalf("accepted %q", s)
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "img.png")
	if err := SavePNG(path, img); err != nil {
		t.Fatal(err)
	}
	decoded, err := LoadPNG(path)
	if err != nil || decoded.Bounds().Dx() != img.Bounds().Dx() || decoded.Bounds().Dy() != img.Bounds().Dy() {
		t.Fatalf("png: %v", err)
	}
}
func TestConfig(t *testing.T) {
	cfg, err := Config("800x600", "1.5", "fr", "#336699")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"output.HEADLESS-1 = 800x600", "output.HEADLESS-1.scale = 1.5", "keyboard.layout = fr", "background = #336699", "log.debug = wayland"} {
		if !strings.Contains(cfg, s) {
			t.Fatalf("missing %s", s)
		}
	}
	for _, tc := range [][4]string{{"0x600", "1", "fr", "#336699"}, {"800x600", "5", "fr", "#336699"}, {"800x600", "1", "fr\nstartup = bad", "#336699"}, {"800x600", "1", "fr", "red"}} {
		if _, err := Config(tc[0], tc[1], tc[2], tc[3]); err == nil {
			t.Fatalf("accepted %v", tc)
		}
	}
}
func TestDiscoverSocket(t *testing.T) {
	dir := t.TempDir()
	if name, err := DiscoverSocket(context.Background(), dir); err != nil || name != "" {
		t.Fatalf("empty: %q %v", name, err)
	}
	if err := os.Mkdir(filepath.Join(dir, "neferwl"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "neferwl", "wayland-1.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if name, _ := DiscoverSocket(context.Background(), dir); name != "" {
		t.Fatal("state without socket")
	}
	ln, err := net.Listen("unix", filepath.Join(dir, "wayland-1"))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if name, err := DiscoverSocket(context.Background(), dir); err != nil || name != "wayland-1" {
		t.Fatalf("socket: %q %v", name, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "neferwl", "wayland-2.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	ln2, err := net.Listen("unix", filepath.Join(dir, "wayland-2"))
	if err != nil {
		t.Fatal(err)
	}
	defer ln2.Close()
	if _, err := DiscoverSocket(context.Background(), dir); err == nil {
		t.Fatal("multiple sockets accepted")
	}
}
func TestHeadlessBackground(t *testing.T) {
	if os.Getenv("NEFERGUI_HARNESS") != "1" {
		t.Skip("set NEFERGUI_HARNESS=1")
	}
	out := filepath.Join(t.TempDir(), "run")
	res, code := Run(context.Background(), Options{Size: "128x96", Scale: "1.5", Layout: "fr", Background: "#336699", Out: out, Timeout: 60 * time.Second, ReadyTimeout: 20 * time.Second, Expect: Expectations{Probes: []Probe{{Name: "background", X: 64, Y: 48, Color: "#336699", Tolerance: 1}}}})
	t.Logf("code=%d result=%+v", code, res)
	if code != Pass {
		data, _ := os.ReadFile(filepath.Join(out, "logs", "compositor.log"))
		t.Fatalf("run: %s\ncompositor:\n%s", res.Error, data)
	}
	if len(res.Checks) != 1 || !res.Checks[0].Pass {
		t.Fatal(res.Checks)
	}
	if _, err := os.Stat(filepath.Join(out, "result.json")); err != nil {
		t.Fatal(err)
	}
}

func TestClientFailureAndCleanup(t *testing.T) {
	if os.Getenv("NEFERGUI_HARNESS") != "1" {
		t.Skip("set NEFERGUI_HARNESS=1")
	}
	isolateSessionTemp(t)
	out := filepath.Join(t.TempDir(), "failure")
	res, code := Run(context.Background(), Options{Size: "128x96", Scale: "1", Layout: "us", Background: "#336699", Out: out, Timeout: 10 * time.Second, ReadyTimeout: 5 * time.Second, Client: []string{"/bin/false"}})
	if code != ProcessFailure || res.Status != "error" {
		t.Fatalf("code=%d result=%+v", code, res)
	}
	if _, err := os.Stat(filepath.Join(out, "result.json")); err != nil {
		t.Fatal(err)
	}
	checkCleanup(t, res)
}

func checkCleanup(t *testing.T, res Result) {
	t.Helper()
	if _, ok := res.Artifacts["session"]; ok {
		t.Fatal("temporary session retained")
	}
	matches, err := filepath.Glob(filepath.Join(os.Getenv("TMPDIR"), "nefergui-harness-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary sessions remain: %v %v", matches, err)
	}
	if res.Compositor.PID <= 0 {
		t.Fatalf("missing compositor pid: %+v", res)
	}
	if err := syscall.Kill(res.Compositor.PID, 0); err != syscall.ESRCH {
		t.Fatalf("compositor pid %d not reaped: %v", res.Compositor.PID, err)
	}
}

func isolateSessionTemp(t *testing.T) { t.Helper(); t.Setenv("TMPDIR", t.TempDir()) }

func TestCancelAndCleanup(t *testing.T) {
	if os.Getenv("NEFERGUI_HARNESS") != "1" {
		t.Skip("set NEFERGUI_HARNESS=1")
	}
	isolateSessionTemp(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := filepath.Join(t.TempDir(), "cancel")
	ready := filepath.Join(t.TempDir(), "ready")
	result := make(chan struct {
		res  Result
		code int
	}, 1)
	go func() {
		res, code := Run(ctx, Options{Size: "128x96", Scale: "1", Layout: "us", Background: "#336699", Out: out, Timeout: 20 * time.Second, ReadyTimeout: 8 * time.Second, Client: []string{"sh", "-c", "touch \"$1\"; sleep 30", "sh", ready}})
		result <- struct {
			res  Result
			code int
		}{res, code}
	}()
	deadline := time.After(12 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		select {
		case <-deadline:
			cancel()
			t.Fatal("client never started")
		case <-time.After(25 * time.Millisecond):
		}
	}
	cancel()
	r := <-result
	if r.code != ProcessFailure || !strings.Contains(r.res.Error, "context canceled") {
		t.Fatalf("cancel: %d %+v", r.code, r.res)
	}
	checkCleanup(t, r.res)
	if _, err := os.Stat(filepath.Join(out, "result.json")); err != nil {
		t.Fatal(err)
	}
}

func TestClientEnvironment(t *testing.T) {
	if os.Getenv("NEFERGUI_HARNESS") != "1" {
		t.Skip("set NEFERGUI_HARNESS=1")
	}
	isolateSessionTemp(t)
	t.Setenv("DISPLAY", ":99")
	t.Setenv("WAYLAND_DISPLAY", "host-socket")
	t.Setenv("WAYLAND_SOCKET", "42")
	out := filepath.Join(t.TempDir(), "env")
	envFile := filepath.Join(t.TempDir(), "client.env")
	res, code := Run(context.Background(), Options{Size: "128x96", Scale: "1", Layout: "us", Background: "#336699", Out: out, Timeout: 20 * time.Second, ReadyTimeout: 8 * time.Second, Client: []string{"sh", "-c", "env > \"$1\"", "sh", envFile}})
	if code != Pass {
		t.Fatalf("run: %d %+v", code, res)
	}
	b, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok {
			env[k] = v
		}
	}
	for _, k := range []string{"DISPLAY", "WAYLAND_SOCKET"} {
		if _, ok := env[k]; ok {
			t.Fatalf("host %s leaked: %s", k, b)
		}
	}
	if env["WAYLAND_DISPLAY"] == "" || env["WAYLAND_DISPLAY"] == "host-socket" {
		t.Fatalf("wrong socket: %s", b)
	}
	if runtime := env["XDG_RUNTIME_DIR"]; !strings.HasPrefix(runtime, filepath.Join(os.TempDir(), "nefergui-harness-")) || filepath.Base(runtime) != "runtime" {
		t.Fatalf("wrong runtime: %s", b)
	}
	checkCleanup(t, res)
}
func TestUnpinnedOverride(t *testing.T) {
	t.Setenv("NEFERGUI_NEFERWL", "/bin/true")
	options := Options{Size: "128x96", Scale: "1", Layout: "us", Background: "#336699", Out: filepath.Join(t.TempDir(), "denied"), Timeout: time.Second, ReadyTimeout: 500 * time.Millisecond}
	res, code := Run(context.Background(), options)
	if code != Usage || res.Compositor.Pinned || res.Compositor.Binary != "/bin/true" {
		t.Fatalf("denied: %d %+v", code, res)
	}
	data, err := os.ReadFile(filepath.Join(options.Out, "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"pinned": false`) || !strings.Contains(string(data), `"binary": "/bin/true"`) {
		t.Fatalf("result: %s", data)
	}
	options.Out = filepath.Join(t.TempDir(), "allowed")
	options.AllowUnpinned = true
	res, code = Run(context.Background(), options)
	if code == Usage || res.Compositor.Pinned || res.Compositor.Binary != "/bin/true" {
		t.Fatalf("allowed: %d %+v", code, res)
	}
}

func TestResultWriteFailure(t *testing.T) {
	out := filepath.Join(t.TempDir(), "run")
	binary := filepath.Join(t.TempDir(), "fake-neferwl")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nmkdir \""+filepath.Join(out, "result.json")+"\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NEFERGUI_NEFERWL", binary)
	res, code := Run(context.Background(), Options{Size: "128x96", Scale: "1", Layout: "us", Background: "#336699", Out: out, AllowUnpinned: true, Timeout: time.Second, ReadyTimeout: 500 * time.Millisecond})
	if code != ProcessFailure || !strings.Contains(res.Error, "write result.json") {
		t.Fatalf("write failure: %d %+v", code, res)
	}
}

func TestCopyRunLog(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "out.log")
	// Older NeferWL writes no run log: no artifact, no error.
	if ok, err := copyRunLog(filepath.Join(dir, "missing.log"), dst); ok || err != nil {
		t.Fatalf("missing: %v %v", ok, err)
	}
	src := filepath.Join(dir, "latest.log")
	if err := os.WriteFile(src, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if ok, err := copyRunLog(src, dst); !ok || err != nil {
		t.Fatalf("copy: %v %v", ok, err)
	}
	if data, err := os.ReadFile(dst); err != nil || string(data) != "{}\n" {
		t.Fatalf("copied %q %v", data, err)
	}
	// A failed write is reported, not hidden.
	if ok, err := copyRunLog(src, filepath.Join(dir, "no-such-dir", "out.log")); ok || err == nil {
		t.Fatalf("write failure: %v %v", ok, err)
	}
}
