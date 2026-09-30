package ui

import (
	"context"
	"errors"
	"math"
	"net"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/bnema/wlturbo"
	"github.com/bnema/wlturbo/wl"

	"github.com/bnema/nefergui/internal/css"
	"github.com/bnema/nefergui/internal/keyboard"
	"github.com/bnema/nefergui/internal/layout"
	"github.com/bnema/nefergui/internal/platform/wayland"
	"github.com/bnema/nefergui/internal/text"
)

func TestLayerConfigSurfaceOptions(t *testing.T) {
	c := LayerConfig{Output: "DP-1", Namespace: "ns", Level: LayerOverlay, Anchors: AnchorTop | AnchorLeft | AnchorRight | AnchorBottom,
		Keyboard: KeyboardExclusive, ExclusiveZone: -1, Margin: [4]int32{1, 2, 3, 4}, InputRects: []Rect{{1, 2, 3, 4}}}
	got, err := c.surfaceOptions()
	if err != nil {
		t.Fatal(err)
	}
	l := got.Layer
	if l == nil || l.Output != "DP-1" || l.Namespace != "ns" || l.Layer != wayland.LayerOverlay || l.Anchor != 15 ||
		l.Keyboard != wayland.KeyboardExclusive || l.ExclusiveZone != -1 || l.Margin != [4]int32{1, 2, 3, 4} {
		t.Fatalf("layer options: %+v", l)
	}
	if len(got.InputRects) != 1 || got.InputRects[0] != (wayland.Rect{X: 1, Y: 2, W: 3, H: 4}) {
		t.Fatalf("input rects: %+v", got.InputRects)
	}
	for level, want := range map[LayerLevel]uint32{LayerBackground: 0, LayerBottom: 1, LayerTop: 2, LayerOverlay: 3} {
		o, err := LayerConfig{Level: level}.surfaceOptions()
		if err != nil || o.Layer.Layer != want {
			t.Fatalf("level %d: %+v %v", level, o.Layer, err)
		}
	}
	// nil InputRects keeps the full surface; empty non-nil means click-through.
	if o, _ := (LayerConfig{Level: LayerTop}).surfaceOptions(); o.InputRects != nil {
		t.Fatal("nil rects became non-nil")
	}
	if o, _ := (LayerConfig{Level: LayerTop, InputRects: []Rect{}}).surfaceOptions(); o.InputRects == nil {
		t.Fatal("empty rects became nil")
	}
}

func TestLayerConfigRejectsInvalid(t *testing.T) {
	for name, c := range map[string]LayerConfig{
		"zero level":   {},
		"bad level":    {Level: 9},
		"bad anchor":   {Level: LayerTop, Anchors: 1 << 5},
		"bad keyboard": {Level: LayerTop, Keyboard: 7},
		"empty rect":   {Level: LayerTop, InputRects: []Rect{{Width: 0, Height: 5}}},
		"nul output":   {Level: LayerTop, Output: "a\x00"},
	} {
		if _, err := c.surfaceOptions(); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestLayerCopiesInputRects(t *testing.T) {
	rects := []Rect{{0, 0, 1, 1}}
	var cfg windowConfig
	Layer(LayerConfig{Level: LayerTop, InputRects: rects}).window(&cfg)
	rects[0].Width = 99
	if cfg.layer.InputRects[0].Width != 1 {
		t.Fatal("option aliases caller slice")
	}
}

func TestInputEventMapping(t *testing.T) {
	ev, ok := toInputEvent(wayland.Input{Kind: "press", X: 1.5, Y: 2.5, Button: 0x110})
	if !ok || ev.Kind != InputPointerPress || !ev.Pressed || ev.X != 1.5 || ev.Y != 2.5 || ev.Button != 0x110 {
		t.Fatalf("press: %+v", ev)
	}
	ev, _ = toInputEvent(wayland.Input{Kind: "release", Button: 0x110})
	if ev.Kind != InputPointerRelease || ev.Pressed {
		t.Fatalf("release: %+v", ev)
	}
	ev, ok = toInputEvent(wayland.Input{Kind: "key", Key: keyboard.Key{Name: "Escape", Text: "x", Pressed: true, Repeat: true, Shift: true, Ctrl: true}})
	if !ok || ev.Kind != InputKey || ev.KeyName != "Escape" || ev.Text != "x" || !ev.Pressed || !ev.Repeat || ev.Modifiers != ModShift|ModCtrl {
		t.Fatalf("key: %+v", ev)
	}
	ev, _ = toInputEvent(wayland.Input{Kind: "axis", DX: 1, DY: -2})
	if ev.Kind != InputPointerAxis || ev.DX != 1 || ev.DY != -2 {
		t.Fatalf("axis: %+v", ev)
	}
	for kind, want := range map[string]InputKind{"motion": InputPointerMotion, "leave": InputPointerLeave, "focus-in": InputFocusIn, "focus-out": InputFocusOut, wayland.InputReset: InputReset} {
		if ev, ok = toInputEvent(wayland.Input{Kind: kind}); !ok || ev.Kind != want {
			t.Fatalf("%s: %+v", kind, ev)
		}
	}
	if _, ok = toInputEvent(wayland.Input{Kind: "unknown"}); ok {
		t.Fatal("unknown kind delivered")
	}
}

func TestInputHookDelivery(t *testing.T) {
	var got []InputEvent
	h := &inputHooks{onInput: func(ev InputEvent) bool { got = append(got, ev); return ev.Kind == InputPointerPress }}
	if h.input(wayland.Input{Kind: "motion", X: 3, Y: 4}) {
		t.Fatal("motion reported a change")
	}
	if !h.input(wayland.Input{Kind: "press", Button: 1}) {
		t.Fatal("press change not reported")
	}
	if len(got) != 2 || got[0].Kind != InputPointerMotion {
		t.Fatalf("got %+v", got)
	}
	// Absent callbacks and steady-state delivery do not allocate.
	idle := &inputHooks{}
	in := wayland.Input{Kind: "key", Key: keyboard.Key{Name: "Escape", Pressed: true}}
	if n := testing.AllocsPerRun(100, func() { idle.input(in) }); n != 0 {
		t.Fatalf("idle input allocs %v", n)
	}
	count := 0
	live := &inputHooks{onInput: func(InputEvent) bool { count++; return false }}
	if n := testing.AllocsPerRun(100, func() { live.input(in) }); n != 0 {
		t.Fatalf("hooked input allocs %v", n)
	}
	if n := testing.AllocsPerRun(100, func() { idle.resize(10, 10, 1) }); n != 0 {
		t.Fatalf("idle resize allocs %v", n)
	}
}

func TestResizeReportedOnlyOnChange(t *testing.T) {
	type size struct {
		w, h int
		s    float64
	}
	var got []size
	h := &inputHooks{onResize: func(w, hh int, s float64) { got = append(got, size{w, hh, s}) }}
	var changed []bool
	for _, s := range []size{{100, 50, 1}, {100, 50, 1}, {100, 60, 1}, {100, 60, 1}, {100, 60, 2}, {100, 60, 2}} {
		changed = append(changed, h.resize(s.w, s.h, s.s))
	}
	want := []size{{100, 50, 1}, {100, 60, 1}, {100, 60, 2}}
	if len(got) != len(want) {
		t.Fatalf("resize reports %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("report %d: %v", i, got[i])
		}
	}
	if !changed[0] || changed[1] || !changed[2] || changed[3] || !changed[4] || changed[5] {
		t.Fatalf("changed flags %v", changed)
	}
}

func TestExternalWakeForcesBuild(t *testing.T) {
	r := newRuntime()
	builds := 0
	view := func(f *Frame) { builds++; f.Root() }
	r.Build(view)
	if r.Build(view) {
		t.Fatal("idle runtime rebuilt")
	}
	ch := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { r.forwardWake(ctx, ch); close(done) }()
	ch <- struct{}{}
	waitCtx, waitCancel := context.WithTimeout(ctx, time.Second)
	defer waitCancel()
	if err := r.Wait(waitCtx); err != nil {
		t.Fatal("wake not forwarded:", err)
	}
	if !r.Build(view) || builds != 2 {
		t.Fatalf("wake did not rebuild: builds=%d", builds)
	}
	close(ch)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("forwarder did not stop on close")
	}
}

func TestNodeRectAndFrameSize(t *testing.T) {
	r := newRuntime()
	r.width, r.height = 400, 300
	var w, h float64
	r.Build(func(f *Frame) {
		w, h = f.Size()
		stack := f.Root().Stack()
		stack.Box(Key("a")).Rect(10, 20, 30, 40)
		stack.Box(Key("b")).Rect(50.5, 60.25, 70, 80)
	})
	if w != 400 || h != 300 {
		t.Fatalf("size %v x %v", w, h)
	}
	kids := r.output.Tree.Children[0].Children
	if len(kids) != 2 {
		t.Fatalf("children %d", len(kids))
	}
	if kids[0].Border != (layout.Rect{X: 10, Y: 20, W: 30, H: 40}) || kids[1].Border != (layout.Rect{X: 50.5, Y: 60.25, W: 70, H: 80}) {
		t.Fatalf("rects %+v %+v", kids[0].Border, kids[1].Border)
	}
	// Hit testing follows the rect geometry.
	if id := layout.Hit(r.output.Tree, 12, 22); id != kids[0].ID {
		t.Fatalf("hit %q want %q", id, kids[0].ID)
	}
}

// Raw input observation must not replace normal control routing.
func TestRawHookDoesNotBlockControls(t *testing.T) {
	r := newRuntime()
	catalog, err := text.Load(text.DirectorySource("../../testdata/fonts"))
	if err != nil {
		t.Fatal(err)
	}
	r.setTextEngine(text.NewEngine(catalog))
	r.styles = css.Compile(css.UA(), css.Parse(`text {height:12px} button {height:20px}`))
	activated := 0
	view := shell(r, &activated)
	r.Build(view)
	button := r.output.Tree.Children[1]
	seen := 0
	h := &inputHooks{onInput: func(ev InputEvent) bool {
		if ev.Kind == InputPointerPress || ev.Kind == InputPointerRelease {
			seen++
		}
		return false
	}}
	for _, in := range []wayland.Input{
		{Kind: "motion", X: 1, Y: button.Border.Y + 1},
		{Kind: "press", X: 1, Y: button.Border.Y + 1, Button: pointerPrimary},
		{Kind: "release", X: 1, Y: button.Border.Y + 1, Button: pointerPrimary},
	} {
		h.input(in)
		r.routeInput(in)
	}
	r.Build(view)
	if seen != 2 || activated != 1 {
		t.Fatalf("seen %d activated %d", seen, activated)
	}
}

func TestSurfaceHookOptionIsStored(t *testing.T) {
	var cfg windowConfig
	OnSurface(nil).window(&cfg)
	if cfg.onSurface != nil {
		t.Fatal("nil hook stored")
	}
	called := false
	OnSurface(func(context.Context, WaylandSurface) error { called = true; return nil }).window(&cfg)
	if cfg.onSurface == nil || cfg.onSurface(context.Background(), WaylandSurface{}) != nil || !called {
		t.Fatal("hook not stored")
	}
}

// testDisplay is a real wlturbo display over a unix socket with no compositor.
func testDisplay(t *testing.T) *wl.Display {
	t.Helper()
	l, err := net.Listen("unix", filepath.Join(t.TempDir(), "s"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, _ := l.Accept()
		accepted <- c
	}()
	c, err := net.Dial("unix", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	server := <-accepted
	t.Cleanup(func() { server.Close() })
	d, err := wlturbo.ConnectFromConn(c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestStartupHookErrorsAndCancellation(t *testing.T) {
	boom := errors.New("boom")
	// A plain error passes through and does not need a display.
	if err := startupHook(context.Background(), func(context.Context, WaylandSurface) error { return boom }, WaylandSurface{}); !errors.Is(err, boom) {
		t.Fatalf("error: %v", err)
	}
	if err := startupHook(context.Background(), func(context.Context, WaylandSurface) error { return nil }, WaylandSurface{}); err != nil {
		t.Fatalf("nil: %v", err)
	}
	// A hook that observes cancellation reports ctx.Err joined with its own error.
	display := testDisplay(t)
	ctx, cancel := context.WithCancel(context.Background())
	err := startupHook(ctx, func(ctx context.Context, _ WaylandSurface) error {
		cancel()
		<-ctx.Done()
		return boom
	}, WaylandSurface{Display: display})
	if !errors.Is(err, context.Canceled) || !errors.Is(err, boom) || !display.Closed() {
		t.Fatalf("cancel: %v", err)
	}
	// Cancelling without a hook error still reports the cancellation.
	ctx, cancel = context.WithCancel(context.Background())
	err = startupHook(ctx, func(context.Context, WaylandSurface) error { cancel(); return nil }, WaylandSurface{Display: testDisplay(t)})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel without error: %v", err)
	}
}

func TestFrameSetInputRects(t *testing.T) {
	r := newRuntime()
	var setErr error
	rects := []Rect{{1, 2, 3, 4}, {5, 6, 7, 8}}
	r.Build(func(f *Frame) { f.Root(); setErr = f.SetInputRects(rects) })
	if setErr != nil {
		t.Fatal(setErr)
	}
	rects[0].Width = 99 // caller reuse must not affect the staged copy
	var got [][]wayland.Rect
	set := func(rs []wayland.Rect) error { got = append(got, append([]wayland.Rect{}, rs...)); return nil }
	if err := r.applyInputRects(set); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0]) != 2 || got[0][0] != (wayland.Rect{X: 1, Y: 2, W: 3, H: 4}) {
		t.Fatalf("applied %+v", got)
	}
	// Nothing staged: the platform is not called again.
	if err := r.applyInputRects(set); err != nil || len(got) != 1 {
		t.Fatalf("restaged: %v %+v", err, got)
	}
	// nil restores the full surface; empty non-nil is click-through.
	var nilSeen, emptySeen bool
	r.Redraw()
	r.Build(func(f *Frame) { f.Root(); _ = f.SetInputRects(nil) })
	_ = r.applyInputRects(func(rs []wayland.Rect) error { nilSeen = rs == nil; return nil })
	r.Redraw()
	r.Build(func(f *Frame) { f.Root(); _ = f.SetInputRects([]Rect{}) })
	_ = r.applyInputRects(func(rs []wayland.Rect) error { emptySeen = rs != nil && len(rs) == 0; return nil })
	if !nilSeen || !emptySeen {
		t.Fatalf("nil %v empty %v", nilSeen, emptySeen)
	}
	// Invalid rects are rejected in the frame call and stage nothing.
	r.Redraw()
	for _, bad := range []Rect{{0, 0, 0, 5}, {0, 0, 5, -1}, {math.MaxInt32, 0, 5, 5}} {
		r.Build(func(f *Frame) { f.Root(); setErr = f.SetInputRects([]Rect{bad}) })
		if setErr == nil || r.inputStaged {
			t.Fatalf("accepted %+v", bad)
		}
		r.Redraw()
	}
	// Steady-state staging and apply do not allocate once buffers exist.
	r.Build(func(f *Frame) {
		f.Root()
		if n := testing.AllocsPerRun(50, func() { _ = f.SetInputRects(rects) }); n != 0 {
			t.Fatalf("SetInputRects allocs %v", n)
		}
	})
	noop := func([]wayland.Rect) error { return nil }
	r.inputStaged = true
	if n := testing.AllocsPerRun(50, func() { r.inputStaged = true; _ = r.applyInputRects(noop) }); n != 0 {
		t.Fatalf("applyInputRects allocs %v", n)
	}
}

// A failed stage keeps the region pending so the next frame retries it.
func TestStagedInputRectsRetainedOnFailure(t *testing.T) {
	r := newRuntime()
	r.Build(func(f *Frame) { f.Root(); _ = f.SetInputRects([]Rect{{1, 2, 3, 4}}) })
	boom := errors.New("stage failed")
	if err := r.applyInputRects(func([]wayland.Rect) error { return boom }); !errors.Is(err, boom) || !r.inputStaged {
		t.Fatalf("failure: %v staged=%v", err, r.inputStaged)
	}
	var got []wayland.Rect
	if err := r.applyInputRects(func(rs []wayland.Rect) error { got = append(got, rs...); return nil }); err != nil || r.inputStaged {
		t.Fatalf("retry: %v staged=%v", err, r.inputStaged)
	}
	if len(got) != 1 || got[0] != (wayland.Rect{X: 1, Y: 2, W: 3, H: 4}) {
		t.Fatalf("retried region %+v", got)
	}
	// A later frame that does not call SetInputRects stages nothing new.
	r.Redraw()
	r.Build(func(f *Frame) { f.Root() })
	called := false
	_ = r.applyInputRects(func([]wayland.Rect) error { called = true; return nil })
	if called {
		t.Fatal("unchanged frame restaged the region")
	}
}

func TestNodeRectOutsideStackIsIgnored(t *testing.T) {
	r := newRuntime()
	r.width, r.height = 200, 100
	r.Build(func(f *Frame) {
		col := f.Root().Column()
		col.Box(Key("a"), Inline("height:10px")).Rect(50, 50, 5, 5)
		col.Box(Key("b"), Inline("height:10px"))
	})
	kids := r.output.Tree.Children[0].Children
	if kids[0].Border.X != 0 || kids[0].Border.H != 10 || kids[1].Border.Y != 10 {
		t.Fatalf("Rect changed normal flow: %+v %+v", kids[0].Border, kids[1].Border)
	}
}

// The absolute geometry path adds no allocations over the same tree without it.
func TestNodeRectAddsNoAllocations(t *testing.T) {
	build := func(withRect bool) func() {
		r := newRuntime()
		r.width, r.height = 640, 480
		view := func(f *Frame) {
			st := f.Root().Stack()
			for i := 0; i < 32; i++ {
				b := st.Box(Key(strconv.Itoa(i)), Inline("background:#fff"))
				if withRect {
					b.Rect(float64(i), float64(i), 20, 20)
				}
			}
		}
		r.Build(view)
		return func() { r.Redraw(); r.Build(view) }
	}
	plain := testing.AllocsPerRun(20, build(false))
	rect := testing.AllocsPerRun(20, build(true))
	if rect > plain {
		t.Fatalf("Rect added allocations: %v vs %v", rect, plain)
	}
}
