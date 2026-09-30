//go:build linux

package wayland

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bnema/nefergui/internal/harness"
	"github.com/bnema/wlturbo"
	"github.com/bnema/wlturbo/protocol/core"
	"github.com/bnema/wlturbo/wl"
)

func TestLayerConstantsMatchProtocol(t *testing.T) {
	if LayerBackground != 0 || LayerBottom != 1 || LayerTop != 2 || LayerOverlay != 3 {
		t.Fatal("layer values")
	}
	if AnchorTop != 1 || AnchorBottom != 2 || AnchorLeft != 4 || AnchorRight != 8 {
		t.Fatal("anchor bits")
	}
	if KeyboardNone != 0 || KeyboardExclusive != 1 || KeyboardOnDemand != 2 {
		t.Fatal("keyboard modes")
	}
}

func TestSurfaceOptionsValidate(t *testing.T) {
	ok := LayerOptions{Layer: LayerOverlay, Anchor: AnchorTop | AnchorLeft | AnchorRight | AnchorBottom, Keyboard: KeyboardOnDemand}
	for name, tc := range map[string]struct {
		opts SurfaceOptions
		bad  bool
	}{
		"zero":            {},
		"valid layer":     {opts: SurfaceOptions{Layer: &ok}},
		"empty passthru":  {opts: SurfaceOptions{InputRects: []Rect{}}},
		"layer 4":         {opts: SurfaceOptions{Layer: &LayerOptions{Layer: 4}}, bad: true},
		"anchor 16":       {opts: SurfaceOptions{Layer: &LayerOptions{Anchor: 16}}, bad: true},
		"zone -2":         {opts: SurfaceOptions{Layer: &LayerOptions{ExclusiveZone: -2}}, bad: true},
		"zone -1":         {opts: SurfaceOptions{Layer: &LayerOptions{ExclusiveZone: -1}}},
		"keyboard 3":      {opts: SurfaceOptions{Layer: &LayerOptions{Keyboard: 3}}, bad: true},
		"namespace NUL":   {opts: SurfaceOptions{Layer: &LayerOptions{Namespace: "a\x00b"}}, bad: true},
		"zero-size rect":  {opts: SurfaceOptions{InputRects: []Rect{{0, 0, 0, 5}}}, bad: true},
		"overflow rect":   {opts: SurfaceOptions{InputRects: []Rect{{1<<31 - 2, 0, 5, 5}}}, bad: true},
		"negative origin": {opts: SurfaceOptions{InputRects: []Rect{{-5, -5, 10, 10}}}},
	} {
		if err := tc.opts.Validate(); (err != nil) != tc.bad {
			t.Errorf("%s: err=%v", name, err)
		}
	}
	if _, err := ConnectWithOptions(context.Background(), "", 10, 10, false, SurfaceOptions{Layer: &LayerOptions{Layer: 9}}); err == nil || !strings.Contains(err.Error(), "invalid layer") {
		t.Fatalf("connect must validate before dialing: %v", err)
	}
}

func TestLayerConfigureSizeBounds(t *testing.T) {
	if w, h, err := layerConfigureSize(0, 16384); err != nil || w != 0 || h != 16384 {
		t.Fatal(w, h, err)
	}
	if _, _, err := layerConfigureSize(16385, 1); err == nil {
		t.Fatal("oversize accepted")
	}
	w := &Window{Width: 100, Height: 50}
	w.applyLayerSize(0, 40)
	if w.Width != 100 || w.Height != 40 || !w.Dirty {
		t.Fatalf("%+v", w)
	}
}

// A post-startup configure must carry size and serial together, even when the
// previous frame is ready. Applying it must ack before the owner can commit.
func TestLayerConfigureAfterReaderStart(t *testing.T) {
	srv, accept := newWireServer(t)
	type dialed struct {
		d   *wlturbo.Display
		err error
	}
	ch := make(chan dialed, 1)
	go func() {
		d, err := wlturbo.Connect(srv.path)
		ch <- dialed{d, err}
	}()
	conn := accept()
	r := <-ch
	if r.err != nil {
		t.Fatal(r.err)
	}
	d := r.d
	defer d.Close()
	comp := core.NewCompositor(d.Context())
	if err := d.Registry().Bind(1, core.CompositorInterface, 6, comp); err != nil {
		t.Fatal(err)
	}
	surface, err := comp.CreateSurface()
	if err != nil {
		t.Fatal(err)
	}
	w := &Window{Display: d, Compositor: comp, Surface: surface, Width: 100, Height: 40,
		Configured: true, FrameReady: true, events: make(chan Event, 8), readerStop: make(chan struct{})}
	if err := w.bindLayerShell(func(iface string, max, min uint32, p wl.Proxy) error {
		return d.Registry().Bind(2, iface, max, p)
	}); err != nil {
		t.Fatal(err)
	}
	if err := w.createLayerSurface(LayerOptions{Layer: LayerTop}); err != nil {
		t.Fatal(err)
	}
	w.StartReader()
	defer func() {
		close(w.readerStop)
		_ = d.Close()
		<-w.readerDone
	}()
	for _, tc := range []struct {
		serial, width, height uint32
		wantW, wantH          int32
		ready                 bool
	}{{7, 200, 50, 200, 50, true}, {8, 0, 60, 200, 60, false}} {
		w.FrameReady, w.Dirty = tc.ready, false
		if _, err := conn.Write(frame(w.LayerSurface.ID(), 0, tc.serial, tc.width, tc.height)); err != nil {
			t.Fatal(err)
		}
		var ev Event
		select {
		case ev = <-w.Events():
		case <-time.After(5 * time.Second):
			t.Fatal("no configure event")
		}
		if ev.Kind != ConfigureLayer || ev.Width != int32(tc.width) || ev.Height != int32(tc.height) || ev.Serial != tc.serial {
			t.Fatalf("configure must be one size+serial event: %+v", ev)
		}
		if err := w.Apply(ev); err != nil {
			t.Fatal(err)
		}
		if w.Width != tc.wantW || w.Height != tc.wantH || !w.Dirty || !w.Configured || !w.FrameReady {
			t.Fatalf("configure state: size=%dx%d dirty=%v configured=%v ready=%v", w.Width, w.Height, w.Dirty, w.Configured, w.FrameReady)
		}
		if err := surface.Commit(); err != nil {
			t.Fatal(err)
		}
		// Ignore setup requests, but require exactly one ack of this configure
		// before the first subsequent surface commit.
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		acks := 0
		for {
			var hdr [8]byte
			if _, err := io.ReadFull(conn, hdr[:]); err != nil {
				t.Fatal(err)
			}
			obj, word := binary.NativeEndian.Uint32(hdr[:]), binary.NativeEndian.Uint32(hdr[4:])
			size, op := int(word>>16), uint16(word)
			if size < 8 {
				t.Fatalf("bad frame size %d", size)
			}
			args := make([]byte, size-8)
			if _, err := io.ReadFull(conn, args); err != nil {
				t.Fatal(err)
			}
			if obj == w.LayerSurface.ID() && op == 6 {
				if len(args) != 4 || binary.NativeEndian.Uint32(args) != tc.serial {
					t.Fatalf("wrong ack payload %x, want serial %d", args, tc.serial)
				}
				acks++
			}
			if obj == surface.ID() && op == 6 {
				if acks != 1 {
					t.Fatalf("commit preceded by %d configure acks, want 1", acks)
				}
				break
			}
		}
	}
}

// Harness acceptance: a real NeferWL compositor and the real client code.
func runLayerHarness(t *testing.T, child string) (string, string) {
	t.Helper()
	if os.Getenv("NEFERGUI_HARNESS") != "1" {
		t.Skip("set NEFERGUI_HARNESS=1 and NEFERGUI_NEFERWL for the layer-shell harness")
	}
	root := filepath.Join(t.TempDir(), "artifacts")
	result, code := harness.Run(context.Background(), harness.Options{Size: "400x300", Scale: "1", Layout: "us", Background: "#111111", Out: root, AllowUnpinned: os.Getenv("NEFERGUI_NEFERWL") != "", Timeout: 30 * time.Second, ReadyTimeout: 10 * time.Second, Client: []string{os.Args[0], "-test.run=^" + child + "$", "-test.v"}})
	out, _ := os.ReadFile(result.Artifacts["client_stdout"])
	errOut, _ := os.ReadFile(result.Artifacts["client_stderr"])
	if code != harness.Pass {
		t.Fatalf("code=%d status=%s error=%s\nstdout:\n%s\nstderr:\n%s", code, result.Status, result.Error, out, errOut)
	}
	return string(out), string(errOut)
}

func TestHarnessLayerSurface(t *testing.T) {
	out, _ := runLayerHarness(t, "TestHarnessLayerSurfaceClient")
	for _, want := range []string{"LAYER configured", "LAYER height=40", "LAYER input-passthrough", "LAYER input-default", "LAYER closed"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestHarnessLayerOutputSelection(t *testing.T) {
	out, _ := runLayerHarness(t, "TestHarnessLayerOutputClient")
	for _, want := range []string{"OUTPUT exact ok", "OUTPUT missing rejected"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestHarnessXDGDefaultUnchanged(t *testing.T) {
	out, _ := runLayerHarness(t, "TestHarnessXDGClient")
	if !strings.Contains(out, "XDG configured toplevel") {
		t.Fatal(out)
	}
}

func harnessChild(t *testing.T) {
	t.Helper()
	if os.Getenv("NEFERGUI_DEBUG_DIR") == "" {
		t.Skip("harness child only")
	}
}

func TestHarnessLayerSurfaceClient(t *testing.T) {
	harnessChild(t)
	w, err := ConnectWithOptions(context.Background(), "", 100, 40, true, SurfaceOptions{
		InputRects: []Rect{{0, 0, 10, 10}},
		Layer: &LayerOptions{
			Layer: LayerTop, Anchor: AnchorTop | AnchorLeft | AnchorRight, Keyboard: KeyboardOnDemand,
			ExclusiveZone: 40, Margin: [4]int32{2, 3, 4, 5}, Namespace: "nefergui-test",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !w.IsLayer() || w.Toplevel != nil || w.XdgSurface != nil || w.Shell != nil {
		t.Fatal("layer window must not create xdg objects")
	}
	if !w.Configured || w.Width < 100 {
		t.Fatalf("not configured: %+v", w)
	}
	fmt.Println("LAYER configured", w.Width, w.Height)
	if w.Height == 40 {
		fmt.Println("LAYER height=40")
	}
	if err = w.SetTitle("ignored"); err != nil {
		t.Fatal(err)
	}
	// Empty non-nil region: pass-through; nil: default full surface.
	if err = w.SetInputRects([]Rect{}); err != nil {
		t.Fatal(err)
	}
	if err = w.SetInputRects([]Rect{}); err != nil {
		t.Fatal(err)
	}
	fmt.Println("LAYER input-passthrough")
	if err = w.SetInputRects(nil); err != nil {
		t.Fatal(err)
	}
	fmt.Println("LAYER input-default")
	// A roundtrip proves the compositor processed and accepted every request
	// (a protocol error would fail it); only then does the reader take over.
	if err = w.Display.Roundtrip(); err != nil {
		t.Fatal(err)
	}
	// Hand dispatch to the reader and prove it works: a close from the
	// owner side must still tear down cleanly.
	events := w.StartReader()
	for drained := false; !drained; {
		select {
		case ev := <-events:
			if ev.Kind == TransportError || ev.Kind == CloseEvent {
				t.Fatalf("unexpected %+v", ev)
			}
			if err = w.Apply(ev); err != nil {
				t.Fatal(err)
			}
		default:
			drained = true
		}
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	fmt.Println("LAYER closed")
}

func TestHarnessLayerOutputClient(t *testing.T) {
	harnessChild(t)
	d, err := wl.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	if err = d.Roundtrip(); err != nil {
		t.Fatal(err)
	}
	var name string
	for _, g := range d.Registry().GetGlobals() {
		if g.Interface != core.OutputInterface {
			continue
		}
		if g.Version < outputNameVersion {
			t.Fatalf("wl_output v%d lacks names", g.Version)
		}
		o := core.NewOutput(d.Context())
		o.OnName(func(n string) { name = n })
		if err = d.Registry().Bind(g.Name, core.OutputInterface, outputNameVersion, o); err != nil {
			t.Fatal(err)
		}
		break
	}
	if err = d.Roundtrip(); err != nil {
		t.Fatal(err)
	}
	_ = d.Close()
	if name == "" {
		t.Fatal("no output name")
	}
	w, err := ConnectWithOptions(context.Background(), "", 100, 40, false, SurfaceOptions{Layer: &LayerOptions{Output: name, Layer: LayerBottom, Anchor: AnchorBottom | AnchorLeft | AnchorRight}})
	if err != nil {
		t.Fatalf("output %q: %v", name, err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	fmt.Println("OUTPUT exact ok", name)
	_, err = ConnectWithOptions(context.Background(), "", 100, 40, false, SurfaceOptions{Layer: &LayerOptions{Output: name + "-missing"}})
	var ce *CapabilityError
	if !errors.As(err, &ce) {
		t.Fatalf("missing output error: %v", err)
	}
	fmt.Println("OUTPUT missing rejected")
}

func TestHarnessXDGClient(t *testing.T) {
	harnessChild(t)
	w, err := Connect("", 100, 50, false)
	if err != nil {
		t.Fatal(err)
	}
	if w.IsLayer() || w.Toplevel == nil || w.XdgSurface == nil || !w.Configured {
		t.Fatal("default must be xdg toplevel")
	}
	if err = w.SetTitle("x"); err != nil {
		t.Fatal(err)
	}
	fmt.Println("XDG configured toplevel")
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
}

// wireServer is a real unix-socket Wayland peer; tests speak raw frames.
type wireServer struct {
	conn net.Conn
	path string
}

func newWireServer(t *testing.T) (*wireServer, func() net.Conn) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "wl-test")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	srv := &wireServer{path: path}
	accepted := make(chan net.Conn, 1)
	go func() {
		c, e := ln.Accept()
		if e == nil {
			accepted <- c
		}
	}()
	return srv, func() net.Conn {
		select {
		case c := <-accepted:
			srv.conn = c
			t.Cleanup(func() { _ = c.Close() })
			return c
		case <-time.After(5 * time.Second):
			t.Fatal("client never connected")
			return nil
		}
	}
}

func frame(object uint32, opcode uint16, args ...uint32) []byte {
	b := make([]byte, 8+4*len(args))
	binary.NativeEndian.PutUint32(b, object)
	binary.NativeEndian.PutUint32(b[4:], uint32(len(b))<<16|uint32(opcode))
	for i, a := range args {
		binary.NativeEndian.PutUint32(b[8+4*i:], a)
	}
	return b
}

// A compositor that never answers must not block startup past ctx; the
// connection is closed so the server sees EOF.
func TestConnectWithOptionsCancelsHangingServer(t *testing.T) {
	srv, accept := newWireServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	type result struct {
		w   *Window
		err error
	}
	done := make(chan result, 1)
	go func() {
		w, err := ConnectWithOptions(ctx, srv.path, 100, 40, false, SurfaceOptions{})
		done <- result{w, err}
	}()
	conn := accept()
	buf := make([]byte, 64)
	if _, err := conn.Read(buf); err != nil { // get_registry / sync requests: client is now blocked
		t.Fatal(err)
	}
	select {
	case <-done:
		t.Fatal("returned before cancel")
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	select {
	case r := <-done:
		if r.w != nil || !errors.Is(r.err, context.Canceled) {
			t.Fatalf("window=%v err=%v", r.w, r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ConnectWithOptions still blocked after cancel")
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		if _, err := conn.Read(buf); err != nil {
			if !errors.Is(err, io.EOF) {
				t.Fatalf("server expected EOF (client closed), got %v", err)
			}
			break
		}
	}
}

func TestConnectWithOptionsPrecancelledAndNil(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ConnectWithOptions(ctx, "/nonexistent", 1, 1, false, SurfaceOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	//lint:ignore SA1012 nil context is an explicit error case
	if _, err := ConnectWithOptions(nil, "/nonexistent", 1, 1, false, SurfaceOptions{}); err == nil {
		t.Fatal("nil context accepted")
	}
}

// Removal of the selected output's registry global closes the window, before
// and after the reader starts; other globals are ignored.
func TestSelectedOutputRemovalClosesWindow(t *testing.T) {
	for _, reader := range []bool{false, true} {
		t.Run(fmt.Sprintf("reader=%v", reader), func(t *testing.T) {
			srv, accept := newWireServer(t)
			type dialed struct {
				d   *wlturbo.Display
				err error
			}
			ch := make(chan dialed, 1)
			go func() {
				d, err := wlturbo.Connect(srv.path)
				ch <- dialed{d, err}
			}()
			conn := accept()
			r := <-ch
			if r.err != nil {
				t.Fatal(r.err)
			}
			d := r.d
			defer d.Close()
			w := &Window{Display: d, events: make(chan Event, 8), readerStop: make(chan struct{}), layerOutputSelected: true, layerOutputGlobal: 7}
			d.Registry().AddGlobalRemoveHandler(outputRemoved{w})
			if reader {
				w.StartReader()
			}
			regID := d.Registry().ID()
			if _, err := conn.Write(frame(regID, 1, 9)); err != nil { // other global
				t.Fatal(err)
			}
			if !reader {
				if err := d.Dispatch(); err != nil {
					t.Fatal(err)
				}
				if w.Closed {
					t.Fatal("unrelated removal closed the window")
				}
			} else {
				select {
				case ev := <-w.Events():
					t.Fatalf("unrelated removal posted %+v", ev)
				case <-time.After(100 * time.Millisecond):
				}
			}
			if _, err := conn.Write(frame(regID, 1, 7)); err != nil {
				t.Fatal(err)
			}
			if !reader {
				if err := d.Dispatch(); err != nil {
					t.Fatal(err)
				}
				if !w.Closed {
					t.Fatal("selected output removal did not close")
				}
				return
			}
			select {
			case ev := <-w.Events():
				if ev.Kind != CloseEvent {
					t.Fatalf("event %+v", ev)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("no CloseEvent")
			}
			close(w.readerStop)
			_ = d.Close()
			<-w.readerDone
		})
	}
}

// surfaceRequestCounts reads raw client frames until the sentinel (wl_surface.damage on
// surface) and returns "object/opcode" counts for the surface.
func surfaceRequestCounts(t *testing.T, conn net.Conn, surface uint32) map[uint16]int {
	t.Helper()
	counts := map[uint16]int{}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	hdr := make([]byte, 8)
	for {
		if _, err := io.ReadFull(conn, hdr); err != nil {
			t.Fatal(err)
		}
		obj := binary.NativeEndian.Uint32(hdr)
		word := binary.NativeEndian.Uint32(hdr[4:])
		size, op := int(word>>16), uint16(word)
		if size < 8 {
			t.Fatalf("bad frame size %d", size)
		}
		if _, err := io.CopyN(io.Discard, conn, int64(size-8)); err != nil {
			t.Fatal(err)
		}
		if obj != surface {
			continue
		}
		if op == 2 { // wl_surface.damage sentinel
			return counts
		}
		counts[op]++
	}
}

// StageInputRects never commits; SetInputRects commits only a real change on a
// configured window; an unchanged request sends nothing. Counted on the wire.
func TestInputRectRequestsOnWire(t *testing.T) {
	const opSetInputRegion, opCommit = 5, 6
	srv, accept := newWireServer(t)
	type dialed struct {
		d   *wlturbo.Display
		err error
	}
	ch := make(chan dialed, 1)
	go func() {
		d, err := wlturbo.Connect(srv.path)
		ch <- dialed{d, err}
	}()
	conn := accept()
	r := <-ch
	if r.err != nil {
		t.Fatal(r.err)
	}
	d := r.d
	defer d.Close()
	comp := core.NewCompositor(d.Context())
	if err := d.Registry().Bind(1, core.CompositorInterface, 6, comp); err != nil {
		t.Fatal(err)
	}
	surface, err := comp.CreateSurface()
	if err != nil {
		t.Fatal(err)
	}
	w := &Window{Display: d, Compositor: comp, Surface: surface}
	step := func(name string, run func() error, wantSet, wantCommit int) {
		t.Helper()
		if err := run(); err != nil {
			t.Fatal(name, err)
		}
		if err := surface.Damage(0, 0, 1, 1); err != nil {
			t.Fatal(err)
		}
		got := surfaceRequestCounts(t, conn, surface.ID())
		if got[opSetInputRegion] != wantSet || got[opCommit] != wantCommit {
			t.Fatalf("%s: set_input_region=%d commit=%d, want %d/%d", name, got[opSetInputRegion], got[opCommit], wantSet, wantCommit)
		}
	}
	rects := []Rect{{0, 0, 10, 10}, {20, 0, 5, 5}}
	step("stage", func() error { return w.StageInputRects(rects) }, 1, 0)
	step("stage identical", func() error { return w.StageInputRects(append([]Rect(nil), rects...)) }, 0, 0)
	step("set identical while configured", func() error { w.Configured = true; return w.SetInputRects(rects) }, 0, 0)
	step("set changed", func() error { return w.SetInputRects([]Rect{{1, 1, 2, 2}}) }, 1, 1)
	step("set passthrough", func() error { return w.SetInputRects([]Rect{}) }, 1, 1)
	step("set passthrough again", func() error { return w.SetInputRects([]Rect{}) }, 0, 0)
	step("set default", func() error { return w.SetInputRects(nil) }, 1, 1)
	step("set default again", func() error { return w.SetInputRects(nil) }, 0, 0)
	w.Configured = false
	step("unconfigured change stages only", func() error { return w.SetInputRects(rects) }, 1, 0)
	before := len(w.inputRects)
	step("invalid stays unsent", func() error {
		if err := w.StageInputRects([]Rect{{0, 0, 0, 1}}); err == nil {
			t.Fatal("invalid rect accepted")
		}
		return nil
	}, 0, 0)
	if len(w.inputRects) != before {
		t.Fatal("invalid request changed cached state")
	}
	if n := testing.AllocsPerRun(100, func() { _ = w.StageInputRects(rects) }); n != 0 {
		t.Fatalf("unchanged stage allocates %v", n)
	}
}
