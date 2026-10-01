package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bnema/nefergui/internal/harness"
)

// These tests drive the real Run loop against a headless NeferWL. They run only
// when NEFERGUI_NEFERWL points at a compositor build; the client is this test
// binary re-executed, since Run needs a compositor socket in its environment.

const headlessClientEnv = "NEFERGUI_UI_HEADLESS_SCENARIO"

func requireCompositor(t *testing.T) {
	t.Helper()
	if os.Getenv("NEFERGUI_NEFERWL") == "" {
		t.Skip("NEFERGUI_NEFERWL not set")
	}
}

func runHeadless(t *testing.T, scenario, script string, timeout time.Duration) []string {
	t.Helper()
	return runHeadlessSize(t, "640x480", scenario, script, timeout)
}

func runHeadlessSize(t *testing.T, size, scenario, script string, timeout time.Duration) []string {
	t.Helper()
	requireCompositor(t)
	dir := t.TempDir()
	events := filepath.Join(dir, "events.txt")
	input := ""
	if script != "" {
		input = filepath.Join(dir, "input")
		if err := os.WriteFile(input, []byte(script), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv(headlessClientEnv, scenario)
	t.Setenv("NEFERGUI_UI_HEADLESS_OUT", events)
	res, code := harness.Run(context.Background(), harness.Options{
		Size: size, Scale: "1", Layout: "us", Background: "#000000",
		Input: input, Out: filepath.Join(dir, "out"), AllowUnpinned: true,
		Timeout: timeout, ReadyTimeout: 5 * time.Second,
		Client: []string{os.Args[0], "-test.run=^TestHeadlessClientProcess$", "-test.count=1"},
	})
	data, _ := os.ReadFile(events)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if code != harness.Pass {
		stderr, _ := os.ReadFile(res.Artifacts["client_stderr"])
		t.Fatalf("harness code %d: %s\nevents:\n%s\nstderr:\n%s", code, res.Error, data, stderr)
	}
	return lines
}

func indexOf(lines []string, prefix string) int {
	for i, l := range lines {
		if strings.HasPrefix(l, prefix) {
			return i
		}
	}
	return -1
}

func TestHeadlessLayerHooks(t *testing.T) {
	lines := runHeadless(t, "layer", "sleep 3s\nmove 5 5\nsleep 200ms\nclick left\nkey a\nkey Escape\nsleep 500ms\n", 30*time.Second)
	surface, view := indexOf(lines, "surface"), indexOf(lines, "view ")
	if surface != 0 || view < 0 || surface > view {
		t.Fatalf("OnSurface must run once, before the first view:\n%s", strings.Join(lines, "\n"))
	}
	count := 0
	for _, l := range lines {
		if l == "surface" {
			count++
		}
	}
	joined := "\n" + strings.Join(lines, "\n") + "\n"
	if count != 1 || strings.Count(joined, "\nresize ") != 1 || !strings.Contains(joined, "\nresize 640 480 1\n") {
		t.Fatalf("surface %d, resize must fire once with 640x480:\n%s", count, joined)
	}
	if w := indexOf(lines, "wake-sent"); w < 0 || indexOf(lines[w:], "view ") < 0 || indexOf(lines[w:], "view ") > indexOf(lines[w:], "input press") {
		t.Errorf("wake must produce a view before input starts:\n%s", joined)
	}
	for _, want := range []string{"\nview size 640x480", "\ninput press", "\ninput key Escape", "\nclicks 1\n", "\nexit context canceled\n"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in:\n%s", want, joined)
		}
	}
}

// Every output gets a surface laid out at its own size; the wake redraws them all.
func TestHeadlessLayerAllOutputs(t *testing.T) {
	lines := runHeadlessSize(t, "640x480,320x200", "all-outputs", "", 30*time.Second)
	joined := "\n" + strings.Join(lines, "\n") + "\n"
	w := indexOf(lines, "wake-sent")
	if w < 0 {
		t.Fatalf("no wake:\n%s", joined)
	}
	after := "\n" + strings.Join(lines[w:], "\n") + "\n"
	for _, want := range []string{"\nview size 640x480\n", "\nview size 320x200\n"} {
		if !strings.Contains(after, want) {
			t.Errorf("missing %q after the wake in:\n%s", strings.TrimSpace(want), joined)
		}
	}
	if !strings.Contains(joined, "\nexit context deadline exceeded\n") {
		t.Errorf("Run must end only with its context:\n%s", joined)
	}
}

func TestHeadlessSurfaceHookErrorAborts(t *testing.T) {
	lines := runHeadless(t, "hook-error", "", 20*time.Second)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "exit nefergui: surface hook: hook failed") || strings.Contains(joined, "view") {
		t.Fatalf("hook error must abort before any view:\n%s", joined)
	}
}

func TestHeadlessSurfaceHookCancellation(t *testing.T) {
	lines := runHeadless(t, "hook-cancel", "", 20*time.Second)
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "exit context canceled") || strings.Contains(joined, "view") {
		t.Fatalf("cancellation during the hook must return ctx.Err before any view:\n%s", joined)
	}
}

// TestHeadlessClientProcess is the client half; it does nothing under go test.
func TestHeadlessClientProcess(t *testing.T) {
	scenario := os.Getenv(headlessClientEnv)
	if scenario == "" {
		t.Skip("client process only")
	}
	out, err := os.Create(os.Getenv("NEFERGUI_UI_HEADLESS_OUT"))
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	logf := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintf(out, format+"\n", args...)
	}
	type model struct{ clicks int }
	m := &model{}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	view := func(f *Frame, m *model) {
		w, h := f.Size()
		logf("view size %vx%v", w, h)
		st := f.Root().Stack()
		st.Box(Key("box")).Rect(10, 20, 50, 40)
		if st.Button("Go", Key("go"), Inline("width:60px;height:20px")).Activated() {
			m.clicks++
			logf("clicks %d", m.clicks)
		}
	}
	options := []WindowOption{Size(300, 200), Transparent()}
	switch scenario {
	case "layer":
		wake := make(chan struct{}, 1)
		// Nothing else redraws an idle window before input starts at 3s.
		go func() { time.Sleep(1500 * time.Millisecond); logf("wake-sent"); wake <- struct{}{} }()
		options = append(options,
			Layer(LayerConfig{Level: LayerOverlay, Anchors: AnchorTop | AnchorBottom | AnchorLeft | AnchorRight, Keyboard: KeyboardExclusive}),
			Wake(wake),
			OnSurface(func(context.Context, WaylandSurface) error { logf("surface"); return nil }),
			OnResize(func(w, h int, s float64) { logf("resize %d %d %v", w, h, s) }),
			OnInput(func(ev InputEvent) bool {
				switch {
				case ev.Kind == InputPointerPress:
					logf("input press")
					return true
				case ev.Kind == InputKey && ev.Pressed && ev.KeyName == "Escape":
					logf("input key Escape")
					cancel()
				}
				return false
			}),
		)
	case "all-outputs":
		ctx, cancel = context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		wake := make(chan struct{}, 1)
		go func() { time.Sleep(2 * time.Second); logf("wake-sent"); wake <- struct{}{} }()
		options = append(options, Wake(wake), Layer(LayerConfig{AllOutputs: true, Level: LayerOverlay,
			Anchors: AnchorTop | AnchorBottom | AnchorLeft | AnchorRight, ExclusiveZone: -1, InputRects: []Rect{}}))
	case "hook-error":
		options = append(options, Layer(LayerConfig{Level: LayerOverlay}),
			OnSurface(func(context.Context, WaylandSurface) error { return errors.New("hook failed") }))
	case "hook-cancel":
		options = append(options, Layer(LayerConfig{Level: LayerOverlay}),
			OnSurface(func(ctx context.Context, _ WaylandSurface) error {
				go func() { time.Sleep(200 * time.Millisecond); cancel() }()
				<-ctx.Done()
				return nil
			}))
	}
	err = Run(ctx, m, view, options...)
	logf("exit %v", err)
	out.Close()
	os.Exit(0)
}
