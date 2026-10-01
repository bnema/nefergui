//go:build linux

package ui

import (
	"errors"
	"testing"

	"github.com/bnema/nefergui/internal/layout"
	"github.com/bnema/nefergui/internal/presentation/session"
	"github.com/bnema/nefergui/internal/text"
	"github.com/stretchr/testify/mock"
)

type testModel struct {
	clicks int
	rects  []Rect
}

func testView(f *Frame, m *testModel) {
	root := f.Root()
	if root.Button("Go", Key("go"), Inline("width:60px;height:20px")).Activated() {
		m.clicks++
	}
	if m.rects != nil {
		_ = f.SetInputRects(m.rects)
	}
}

func newTestRenderer(t *testing.T) (*Renderer, *mocktarget) {
	t.Helper()
	tg := newMocktarget(t)
	r, err := newRenderer(RendererConfig{}, tg, text.DirectorySource("../../testdata/fonts"))
	if err != nil {
		t.Fatal(err)
	}
	return r, tg
}

func drawOK(tg *mocktarget, fill func(*session.Output)) *mocktarget_Draw_Call {
	return tg.EXPECT().Draw(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ []layout.Command, _ float64, out *session.Output) (bool, error) {
			*out = session.Output{Buffer: 1, Width: 100, Height: 50}
			if fill != nil {
				fill(out)
			}
			return true, nil
		})
}

func TestKeysymNames(t *testing.T) {
	for sym, want := range map[uint32]string{
		0x20: "space", 0xff09: "tab", 0xfe20: "iso_left_tab", 0xff51: "left", 0xff53: "right",
		0xff52: "up", 0xff54: "down", 0xff50: "home", 0xff57: "end", 0xff55: "page_up",
		0xff56: "page_down", 0xff08: "backspace", 0xffff: "delete", 'a': "a", 'A': "a",
		'c': "c", 'x': "x", 'v': "v", 0xff0d: "return", 0xff8d: "kp_enter", 0xff1b: "escape",
		'z': "", 0xffe1: "",
	} {
		if got := keysymName(sym); got != want {
			t.Errorf("keysym %#x: got %q want %q", sym, got, want)
		}
	}
}

func TestRendererRenderBeforeResizeAndWhenUnchanged(t *testing.T) {
	r, tg := newTestRenderer(t)
	var m testModel
	var out Output
	if ok, err := r.Render(&out, &m, testView); ok || err != nil {
		t.Fatalf("before Resize: ok=%v err=%v", ok, err)
	}
	tg.EXPECT().Resize(mock.Anything, mock.Anything).Return().Once()
	r.Resize(100, 50, 2)
	drawOK(tg, nil).Once()
	if ok, err := r.Render(&out, &m, testView); !ok || err != nil {
		t.Fatalf("first render: ok=%v err=%v", ok, err)
	}
	if out.Buffer != 1 || out.Width != 100 || len(out.Damage) != 1 || out.Damage[0] != (Rect{Width: 100, Height: 50}) {
		t.Fatalf("output: %+v", out)
	}
	// The follow-up frame a Build requests after settling still draws once; after that nothing changes.
	for i := 0; i < 3; i++ {
		ok, err := r.Render(&out, &m, testView)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			drawOK(tg, nil).Maybe()
		}
	}
	// tg asserts no Draw beyond the allowed ones when unchanged.
	if ok, _ := r.Render(&out, &m, testView); ok {
		t.Fatal("unchanged view drew")
	}
}

func TestRendererKeepsFrameWhenNoBufferFree(t *testing.T) {
	r, tg := newTestRenderer(t)
	var m testModel
	var out Output
	tg.EXPECT().Resize(mock.Anything, mock.Anything).Return().Once()
	r.Resize(100, 50, 1)
	tg.EXPECT().Draw(mock.Anything, mock.Anything, mock.Anything).Return(false, nil).Once()
	if ok, err := r.Render(&out, &m, testView); ok || err != nil {
		t.Fatalf("no buffer: ok=%v err=%v", ok, err)
	}
	drawOK(tg, nil).Once()
	if ok, err := r.Render(&out, &m, testView); !ok || err != nil {
		t.Fatalf("retry: ok=%v err=%v", ok, err)
	}
}

func TestRendererDrawError(t *testing.T) {
	r, tg := newTestRenderer(t)
	var m testModel
	var out Output
	tg.EXPECT().Resize(mock.Anything, mock.Anything).Return().Once()
	r.Resize(100, 50, 1)
	boom := errors.New("boom")
	tg.EXPECT().Draw(mock.Anything, mock.Anything, mock.Anything).Return(false, boom).Once()
	if ok, err := r.Render(&out, &m, testView); ok || !errors.Is(err, boom) {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestRendererResizeReachesTargetInPhysicalPixels(t *testing.T) {
	r, tg := newTestRenderer(t)
	tg.EXPECT().Resize(int32(150), int32(75)).Return().Once()
	r.Resize(100, 50, 1.5)
	r.Resize(100, 50, 1.5) // unchanged: no second call
	r.Resize(0, 50, 1)     // invalid: ignored
	r.Resize(10, 10, 0)    // invalid: ignored
}

func TestRendererInputActivatesButtonAndRequestsRedraw(t *testing.T) {
	r, tg := newTestRenderer(t)
	var m testModel
	var out Output
	tg.EXPECT().Resize(mock.Anything, mock.Anything).Return().Once()
	r.Resize(200, 100, 1)
	drawOK(tg, nil).Maybe()
	for i := 0; i < 3; i++ {
		if _, err := r.Render(&out, &m, testView); err != nil {
			t.Fatal(err)
		}
	}
	if r.Input(&Input{Kind: InputPointerMotion, X: 5, Y: 5}) != true {
		t.Fatal("hover over a button needs a redraw")
	}
	r.Input(&Input{Kind: InputPointerPress, X: 5, Y: 5, Button: 0x110, Pressed: true})
	if !r.Input(&Input{Kind: InputPointerRelease, X: 5, Y: 5, Button: 0x110}) {
		t.Fatal("release did not request redraw")
	}
	for i := 0; i < 3; i++ {
		if _, err := r.Render(&out, &m, testView); err != nil {
			t.Fatal(err)
		}
	}
	if m.clicks != 1 {
		t.Fatalf("clicks=%d", m.clicks)
	}
	// Keyboard: Tab focuses, Return activates, text comes from Text.
	r.Input(&Input{Kind: InputPointerLeave})
	r.Input(&Input{Kind: InputKey, Keysym: 0xff09, Pressed: true})
	r.Input(&Input{Kind: InputKey, Keysym: 0xff0d, Pressed: true})
	for i := 0; i < 3; i++ {
		if _, err := r.Render(&out, &m, testView); err != nil {
			t.Fatal(err)
		}
	}
	if m.clicks != 2 {
		t.Fatalf("Return did not activate the focused button: clicks=%d", m.clicks)
	}
	if r.Input(&Input{Kind: 99}) || r.Input(nil) {
		t.Fatal("unknown input requested a redraw")
	}
	if r.Input(&Input{Kind: InputPointerAxis}) {
		t.Fatal("zero scroll requested a redraw")
	}
}

func TestRendererTextInputUsesTextBytes(t *testing.T) {
	r, tg := newTestRenderer(t)
	value := ""
	var out Output
	view := func(f *Frame, v *string) { f.Root().Input("name", v, Key("name"), Inline("width:100px;height:20px")) }
	tg.EXPECT().Resize(mock.Anything, mock.Anything).Return().Once()
	r.Resize(200, 100, 1)
	drawOK(tg, nil).Maybe()
	settle := func() {
		for i := 0; i < 3; i++ {
			if _, err := r.Render(&out, &value, view); err != nil {
				t.Fatal(err)
			}
		}
	}
	settle()
	r.Input(&Input{Kind: InputPointerPress, X: 5, Y: 5, Button: 0x110, Pressed: true})
	r.Input(&Input{Kind: InputPointerRelease, X: 5, Y: 5, Button: 0x110})
	settle()
	text := []byte("é")
	r.Input(&Input{Kind: InputKey, Keysym: 0xe9, Pressed: true, Text: text})
	text[0] = 0 // the caller may reuse the buffer after the call
	settle()
	if value != "é" {
		t.Fatalf("value=%q", value)
	}
}

func TestRendererOutputCarriesCursorAndInputRects(t *testing.T) {
	r, tg := newTestRenderer(t)
	m := testModel{rects: []Rect{{1, 2, 30, 20}}}
	var out Output
	tg.EXPECT().Resize(mock.Anything, mock.Anything).Return().Once()
	r.Resize(200, 100, 1)
	drawOK(tg, nil).Maybe()
	r.Input(&Input{Kind: InputPointerMotion, X: 5, Y: 5}) // over the button: pointer cursor
	if ok, err := r.Render(&out, &m, testView); !ok || err != nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if out.Cursor != CursorPointer || !out.InputRectsChanged || len(out.InputRects) != 1 || out.InputRects[0] != (Rect{1, 2, 30, 20}) {
		t.Fatalf("output: cursor=%v changed=%v rects=%v", out.Cursor, out.InputRectsChanged, out.InputRects)
	}
	// An unchanged region is not reported again.
	r.Invalidate()
	ok, err := r.Render(&out, &m, testView)
	if err != nil {
		t.Fatal(err)
	}
	if ok && out.InputRectsChanged && len(out.InputRects) != 0 {
		t.Fatalf("region reported twice: %+v", out)
	}
	// nil restores the whole surface: changed with no rects.
	m.rects = nil
	var nilView = func(f *Frame, _ *testModel) { f.Root(); _ = f.SetInputRects(nil) }
	r.Invalidate()
	if ok, err = r.Render(&out, &m, nilView); err != nil || !ok || !out.InputRectsChanged || out.InputRects != nil {
		t.Fatalf("nil region: ok=%v err=%v %+v", ok, err, out)
	}
}

func TestRendererForwardsReleasedAndClose(t *testing.T) {
	r, tg := newTestRenderer(t)
	tg.EXPECT().Released(uint64(3)).Return(nil).Once()
	if err := r.Released(3); err != nil {
		t.Fatal(err)
	}
	tg.EXPECT().Close().Return(nil).Once()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil { // second Close is a no-op
		t.Fatal(err)
	}
	var out Output
	var m testModel
	if _, err := r.Render(&out, &m, testView); err == nil {
		t.Fatal("Render after Close succeeded")
	}
}

func TestTextIntAndMasked(t *testing.T) {
	r := newRuntime()
	r.Build(func(f *Frame) {
		root := f.Root()
		root.TextInt("Count: ", -42)
		root.Masked(3)
		root.Masked(0)
		root.Masked(300)
	})
	c := r.committed.children
	if len(c) != 4 || c[0].text != "Count: -42" || c[1].text != "•••" || c[2].text != "" || len([]rune(c[3].text)) != 300 {
		t.Fatalf("texts: %q %q %q", c[0].text, c[1].text, c[2].text)
	}
}
