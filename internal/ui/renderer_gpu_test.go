//go:build linux

package ui

import (
	"cmp"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/bnema/nefergui/internal/presentation/session"
	"github.com/bnema/nefergui/internal/presentation/vkdevice"
	"github.com/bnema/nefergui/internal/text"
	"golang.org/x/sys/unix"
)

// gpuRenderer builds a real Renderer on the render node named by
// NEFERGUI_RENDER_NODE (skips without it) with readback enabled.
func gpuRenderer(t testing.TB) (*Renderer, *session.Target) {
	t.Helper()
	path := os.Getenv("NEFERGUI_RENDER_NODE")
	if path == "" {
		t.Skip("opt in with NEFERGUI_RENDER_NODE=/dev/dri/renderD...")
	}
	var stat unix.Stat_t
	if err := unix.Stat(path, &stat); err != nil {
		t.Fatal(err)
	}
	dev, err := vkdevice.Open(vkdevice.DeviceIdentity(uint64(stat.Rdev)))
	if err != nil {
		t.Fatal(err)
	}
	mods, err := dev.ExportableModifiers()
	dev.Close()
	if err != nil {
		t.Fatal(err)
	}
	var formats []Format
	for m := range mods {
		formats = append(formats, Format{FourCC: vkdevice.XRGB8888, Modifier: m})
	}
	slices.SortFunc(formats, func(a, b Format) int { return cmp.Compare(a.Modifier, b.Modifier) })
	vf := make([]vkdevice.Format, len(formats))
	for i, f := range formats {
		vf[i] = vkdevice.Format{FourCC: f.FourCC, Modifier: f.Modifier}
	}
	tg, err := session.NewTarget(session.TargetConfig{MainDevice: uint64(stat.Rdev), Formats: vf})
	if err != nil {
		t.Fatal(err)
	}
	tg.EnableReadback()
	r, err := newRenderer(RendererConfig{}, tg, text.DirectorySource("../../testdata/fonts"))
	if err != nil {
		_ = tg.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r, tg
}

func readable(t testing.TB, fd int, timeout time.Duration) bool {
	t.Helper()
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	n, err := unix.Poll(fds, int(timeout.Milliseconds()))
	if err != nil {
		t.Fatal(err)
	}
	return n > 0
}

// signalRelease plays the compositor: it signals the buffer's release point.
func signalRelease(t testing.TB, tg *session.Target, out *Output) {
	t.Helper()
	slot := tg.Slots[out.Buffer]
	if err := tg.Node.Signal(slot.ReleaseHandle, out.ReleasePoint); err != nil {
		t.Fatal(err)
	}
}

func TestRendererGPUPixelAndRelease(t *testing.T) {
	r, tg := gpuRenderer(t)
	var out Output
	var m struct{}
	view := func(f *Frame, _ *struct{}) {
		f.Root().Box(Inline("width:50px;height:50px;background:#336699"))
	}
	r.Resize(100, 80, 1)
	ok, err := r.Render(&out, &m, view)
	if err != nil || !ok {
		t.Fatalf("render: ok=%v err=%v", ok, err)
	}
	if !out.NewBuffer || !out.NewTimelines || out.Width != 100 || out.Height != 80 || out.PlaneCount != 1 ||
		out.Planes[0].FD < 0 || out.Planes[0].Stride < 400 || out.FourCC != vkdevice.XRGB8888 ||
		out.Acquire.FD < 0 || out.Release.FD < 0 || out.ReleaseFD < 0 || out.AcquirePoint == 0 || out.ReleasePoint <= out.AcquirePoint {
		t.Fatalf("output: %+v", out)
	}
	img, err := tg.Readback(out.Buffer)
	if err != nil {
		t.Fatal(err)
	}
	near := func(a, b uint8) bool { return int(a)-int(b) <= 1 && int(b)-int(a) <= 1 }
	px := img.NRGBAAt(10, 10)
	if !near(px.R, 0x33) || !near(px.G, 0x66) || !near(px.B, 0x99) {
		t.Fatalf("pixel inside box = %+v", px)
	}
	if px = img.NRGBAAt(90, 70); px.R != 0 || px.G != 0 || px.B != 0 {
		t.Fatalf("pixel outside box = %+v", px)
	}
	// Nothing is released until the compositor signals the point.
	if readable(t, out.ReleaseFD, 20*time.Millisecond) {
		t.Fatal("release eventfd readable before the point signaled")
	}
	signalRelease(t, tg, &out)
	if !readable(t, out.ReleaseFD, time.Second) {
		t.Fatal("release eventfd not readable after the release point signaled")
	}
	if err = r.Released(out.Buffer); err != nil {
		t.Fatal(err)
	}
	if readable(t, out.ReleaseFD, 0) {
		t.Fatal("release eventfd still readable after Released")
	}
	// The same buffer comes back with stable descriptors and no re-import.
	first := out.Buffer
	r.Invalidate()
	seen := map[uint64]bool{first: true}
	for i := 0; i < 3 && len(seen) < 3; i++ {
		r.Invalidate()
		ok, err = r.Render(&out, &m, view)
		if err != nil || !ok {
			t.Fatalf("render %d: ok=%v err=%v", i, ok, err)
		}
		seen[out.Buffer] = true
		if !out.NewBuffer && out.NewTimelines {
			t.Fatal("timelines announced for a known buffer")
		}
	}
	if len(seen) != 3 {
		t.Fatalf("expected a pool of 3 distinct buffers, saw %v", seen)
	}
	// All three are compositor owned: no buffer is free, Render must not draw.
	r.Invalidate()
	if ok, err = r.Render(&out, &m, view); ok || err != nil {
		t.Fatalf("render with no free buffer: ok=%v err=%v", ok, err)
	}
}

func TestRendererGPUResizeRetiresBuffers(t *testing.T) {
	r, tg := gpuRenderer(t)
	var out Output
	var m struct{}
	view := func(f *Frame, _ *struct{}) { f.Root().Box(Inline("width:10px;height:10px;background:#fff")) }
	r.Resize(64, 64, 1)
	if ok, err := r.Render(&out, &m, view); !ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	old := out.Buffer
	signalRelease(t, tg, &out)
	if err := r.Released(old); err != nil {
		t.Fatal(err)
	}
	// Retirement waits for the old frame's GPU work; a late one is reported by
	// a later Render, so let it finish to keep the test deterministic.
	if err := tg.Slots[old].Frame.Wait(); err != nil {
		t.Fatal(err)
	}
	r.Resize(96, 64, 1)
	if ok, err := r.Render(&out, &m, view); !ok || err != nil {
		t.Fatalf("after resize: ok=%v err=%v", ok, err)
	}
	if out.Width != 96 || !out.NewBuffer || out.Buffer == old {
		t.Fatalf("resize output: %+v", out)
	}
	found := false
	for _, rt := range out.Retired {
		found = found || rt.Buffer == old
	}
	if !found {
		t.Fatalf("released old-generation buffer %d not retired: %+v", old, out.Retired)
	}
}

// allocBaselineRenderer is the measured allocation count of one steady
// Render+Released cycle (radv); it is lowered with each optimization until it
// reaches the target of 0.
const allocBaselineRenderer = 48

// TestAllocRendererSteadyFrame measures Render with a view whose number
// changes on every frame and an unchanged structure. It runs on the real GPU
// path because testify mocks would allocate.
func TestAllocRendererSteadyFrame(t *testing.T) {
	if raceEnabled {
		t.Skip("race instrumentation changes allocation counts")
	}
	step := steadyFrame(t)
	got := testing.AllocsPerRun(50, step)
	t.Logf("allocs per steady Render+Released cycle: %v", got)
	if got > allocBaselineRenderer {
		t.Fatalf("allocs per steady frame = %v, baseline %v", got, allocBaselineRenderer)
	}
}

// BenchmarkRendererSteadyFrame profiles the TestAllocRendererSteadyFrame
// cycle; it needs NEFERGUI_RENDER_NODE.
func BenchmarkRendererSteadyFrame(b *testing.B) {
	step := steadyFrame(b)
	b.ReportAllocs()
	for b.Loop() {
		step()
	}
}

// steadyFrame returns one warmed Render+Released cycle for a counter view.
func steadyFrame(t testing.TB) func() {
	t.Helper()
	r, tg := gpuRenderer(t)
	var out Output
	n := 0
	view := func(f *Frame, n *int) {
		root := f.Root()
		root.Heading("Counter")
		root.TextInt("Value: ", int64(*n))
		root.Button("Go", Key("go"))
	}
	r.Resize(160, 120, 1)
	step := func() {
		n++
		r.Invalidate()
		ok, err := r.Render(&out, &n, view)
		for tries := 0; err == nil && !ok && r.Pending() && tries < 5000; tries++ {
			time.Sleep(200 * time.Microsecond)
			ok, err = r.Render(&out, &n, view)
		}
		if err != nil || !ok {
			t.Fatalf("render: ok=%v err=%v pending=%v", ok, err, r.Pending())
		}
		slot := tg.Slots[out.Buffer]
		if err = tg.Node.Signal(slot.ReleaseHandle, out.ReleasePoint); err != nil {
			t.Fatal(err)
		}
		if err = r.Released(out.Buffer); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 20; i++ { // warm up caches, atlas, buffers
		step()
	}
	return step
}

// A readable eventfd whose point is not signaled (a stale notification) must be
// drained without spinning, and the live registration must still work.
func TestRendererGPUStaleReleaseNotification(t *testing.T) {
	r, tg := gpuRenderer(t)
	var out Output
	var m struct{}
	view := func(f *Frame, _ *struct{}) { f.Root().Box(Inline("width:10px;height:10px;background:#fff")) }
	r.Resize(64, 64, 1)
	if ok, err := r.Render(&out, &m, view); !ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	one := [8]byte{1}
	if _, err := unix.Write(out.ReleaseFD, one[:]); err != nil {
		t.Fatal(err)
	}
	if !readable(t, out.ReleaseFD, time.Second) {
		t.Fatal("test setup: eventfd not readable")
	}
	if err := r.Released(out.Buffer); err != nil {
		t.Fatal(err)
	}
	if readable(t, out.ReleaseFD, 0) {
		t.Fatal("stale notification left the eventfd readable: the caller would spin")
	}
	if tg.Slots[out.Buffer].Buffer.State == "available" {
		t.Fatal("buffer reused before the compositor released it")
	}
	signalRelease(t, tg, &out)
	if !readable(t, out.ReleaseFD, time.Second) {
		t.Fatal("live registration lost after a stale notification")
	}
	if err := r.Released(out.Buffer); err != nil {
		t.Fatal(err)
	}
	// Released for an unknown or idle buffer is a no-op that still drains.
	if err := r.Released(999); err != nil {
		t.Fatal(err)
	}
}
