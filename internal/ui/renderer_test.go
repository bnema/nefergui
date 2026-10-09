//go:build linux

package ui

import (
	"errors"
	"fmt"
	"math"
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
	// Nothing changed: no rebuild result, so no Draw (the mock has no further expectation).
	if ok, err := r.Render(&out, &m, testView); ok || err != nil {
		t.Fatalf("unchanged view: ok=%v err=%v", ok, err)
	}
}

func TestRendererKeepsFrameWhenNoBufferFree(t *testing.T) {
	r, tg := newTestRenderer(t)
	var m testModel
	var out Output
	tg.EXPECT().Resize(mock.Anything, mock.Anything).Return().Once()
	r.Resize(100, 50, 1)
	tg.EXPECT().Draw(mock.Anything, mock.Anything, mock.Anything).Return(false, nil).Once()
	tg.EXPECT().Waiting().Return(true).Once()
	if ok, err := r.Render(&out, &m, testView); ok || err != nil {
		t.Fatalf("no buffer: ok=%v err=%v", ok, err)
	}
	if !r.Pending() {
		t.Fatal("a built frame blocked on the GPU must report Pending")
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
	value := ""
	view := func(f *Frame, v *string) { f.Root().Input("name", v, Key("name"), Inline("width:100px;height:20px")) }
	r, settle := settledRenderer(t, &value, view)
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
	// After Close nothing reaches the target (the mock has no expectation).
	r.Resize(10, 10, 1)
	if r.Input(&Input{Kind: InputPointerMotion}) {
		t.Fatal("Input after Close requested a redraw")
	}
	if err := r.Released(3); err == nil {
		t.Fatal("Released after Close succeeded")
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

// settledRenderer returns a 200x100 renderer for view and a settle function
// that renders until queued input has been applied.
func settledRenderer[T any](t *testing.T, model *T, view func(*Frame, *T)) (*Renderer, func()) {
	t.Helper()
	r, tg := newTestRenderer(t)
	tg.EXPECT().Resize(mock.Anything, mock.Anything).Return().Once()
	r.Resize(200, 100, 1)
	drawOK(tg, nil).Maybe()
	var out Output
	settle := func() {
		for i := 0; i < 3; i++ {
			if _, err := r.Render(&out, model, view); err != nil {
				t.Fatal(err)
			}
		}
	}
	settle()
	return r, settle
}

func TestModifierBits(t *testing.T) {
	// The values match neferclient's, so applications copy the bits as is.
	if ModShift != 1 || ModCtrl != 2 || ModAlt != 4 || ModSuper != 8 || ModCapsLock != 16 || ModNumLock != 32 {
		t.Fatal("modifier bit values changed")
	}
}

// Caps Lock and Num Lock must not change shortcuts, selection or activation.
func TestRendererLockModifiersDoNotChangeInput(t *testing.T) {
	inputView := func(f *Frame, v *string) { f.Root().Input("name", v, Key("name"), Inline("width:100px;height:20px")) }
	key := func(r *Renderer, keysym uint32, text string, mods Modifiers) {
		in := &Input{Kind: InputKey, Keysym: keysym, Pressed: true, Modifiers: mods}
		if text != "" {
			in.Text = []byte(text)
		}
		r.Input(in)
	}
	click := func(r *Renderer, x float64, mods Modifiers) {
		r.Input(&Input{Kind: InputPointerPress, X: x, Y: 5, Button: 0x110, Pressed: true, Modifiers: mods})
		r.Input(&Input{Kind: InputPointerRelease, X: x, Y: 5, Button: 0x110, Modifiers: mods})
	}
	for _, lock := range []Modifiers{0, ModCapsLock, ModNumLock, ModCapsLock | ModNumLock} {
		t.Run(fmt.Sprintf("lock=%#x", uint8(lock)), func(t *testing.T) {
			t.Run("CtrlA and ShiftLeft", func(t *testing.T) {
				value := "hello"
				r, settle := settledRenderer(t, &value, inputView)
				click(r, 5, lock)
				settle()
				key(r, 'a', "a", ModCtrl|lock) // select all
				settle()
				key(r, 'X', "X", lock)
				settle()
				if value != "X" {
					t.Fatalf("Ctrl+A then text: value=%q, want X", value)
				}
				key(r, 0xff51, "", ModShift|lock) // extend selection left
				settle()
				key(r, 'y', "y", lock)
				settle()
				if value != "y" {
					t.Fatalf("Shift+Left then text: value=%q, want y", value)
				}
			})
			t.Run("ShiftClick", func(t *testing.T) {
				value := "hello"
				r, settle := settledRenderer(t, &value, inputView)
				click(r, 1, lock)
				settle()
				click(r, 95, ModShift|lock) // select to the end
				settle()
				key(r, 'Z', "Z", lock)
				settle()
				if value != "Z" {
					t.Fatalf("Shift+click then text: value=%q, want Z", value)
				}
			})
			t.Run("TabReturn", func(t *testing.T) {
				m := testModel{}
				r, settle := settledRenderer(t, &m, testView)
				key(r, 0xff09, "", lock)
				key(r, 0xff0d, "", lock)
				settle()
				if m.clicks != 1 {
					t.Fatalf("Tab+Return clicks=%d, want 1", m.clicks)
				}
			})
		})
	}
}

type measureModel struct{ title, body string }

func measureView(f *Frame, m *measureModel) {
	root := f.Root(Inline("padding:4px"))
	root.Text(m.title)
	root.Text(m.body)
}

func TestRendererMeasureNaturalSizeBeforeResize(t *testing.T) {
	r, _ := newTestRenderer(t) // the mock target has no expectations: any target call fails
	m := measureModel{title: "Title", body: "short"}
	w, h, err := r.Measure(&m, measureView, 400)
	if err != nil {
		t.Fatal(err)
	}
	if w <= 8 || w >= 400 || h <= 8 || w != float64(int(w)) || h != float64(int(h)) {
		t.Fatalf("size %g x %g", w, h)
	}
	single := h
	m.body = "alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron pi rho sigma tau"
	w, h, err = r.Measure(&m, measureView, 100)
	if err != nil {
		t.Fatal(err)
	}
	if w != 100 || h <= single+10 {
		t.Fatalf("wrapped size %g x %g, one-line height %g", w, h, single)
	}
}

func TestRendererMeasureValidation(t *testing.T) {
	r, _ := newTestRenderer(t)
	m := measureModel{}
	for _, bad := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		if _, _, err := r.Measure(&m, measureView, bad); err == nil {
			t.Errorf("maxWidth %v accepted", bad)
		}
	}
	if _, _, err := r.Measure[measureModel](nil, measureView, 100); err == nil {
		t.Error("nil model accepted")
	}
	if _, _, err := r.Measure(&m, nil, 100); err == nil {
		t.Error("nil view accepted")
	}
	if w, h, err := r.Measure(&m, func(*Frame, *measureModel) {}, 100); err != nil || w != 0 || h != 0 {
		t.Errorf("view without root: %g x %g %v", w, h, err)
	}
	tg := r.target.(*mocktarget)
	tg.EXPECT().Close().Return(nil).Once()
	_ = r.Close()
	if _, _, err := r.Measure(&m, measureView, 100); err == nil {
		t.Error("closed renderer accepted")
	}
}

func TestRendererMeasureChangesNoState(t *testing.T) {
	r, tg := newTestRenderer(t)
	type model struct {
		clicks int
		text   string
	}
	m := model{text: "edit me"}
	view := func(f *Frame, m *model) {
		root := f.Root()
		if root.Button("Go", Key("go"), Inline("width:60px;height:20px")).Activated() {
			m.clicks++
		}
		root.Input("field", &m.text, Key("field"))
	}
	var out Output
	tg.EXPECT().Resize(mock.Anything, mock.Anything).Return().Once()
	r.Resize(200, 100, 1)
	drawOK(tg, nil).Maybe()
	render := func() {
		t.Helper()
		for i := 0; i < 3; i++ {
			if _, err := r.Render(&out, &m, view); err != nil {
				t.Fatal(err)
			}
		}
	}
	render()
	r.Input(&Input{Kind: InputPointerMotion, X: 5, Y: 5})
	r.Input(&Input{Kind: InputKey, Keysym: 0xff09, Pressed: true}) // Tab: focus the button
	render()
	draws := len(tg.Calls)
	rt := r.rt
	if rt.state.hover == nil || rt.state.focus == nil {
		t.Fatalf("setup: hover=%v focus=%v", rt.state.hover, rt.state.focus)
	}
	// Input queued but not yet built must survive Measure untouched.
	r.Input(&Input{Kind: InputPointerPress, X: 5, Y: 5, Button: 0x110, Pressed: true})
	r.Input(&Input{Kind: InputPointerRelease, X: 5, Y: 5, Button: 0x110})
	before := struct {
		state                interactionState
		committed            *element
		generation           uint64
		edits, pending, wake int
		styles               any
		engine               any
	}{rt.state, rt.committed, rt.generation, len(rt.edits), len(rt.pending), len(rt.wake), rt.styles, rt.textEngine}
	if before.pending == 0 || before.wake == 0 {
		t.Fatalf("setup: pending=%d wake=%d", before.pending, before.wake)
	}

	if _, _, err := r.Measure(&m, view, 300); err != nil {
		t.Fatal(err)
	}

	if rt.state.hover != before.state.hover || rt.state.focus != before.state.focus || rt.state.x != before.state.x || rt.state.y != before.state.y || rt.state.inside != before.state.inside || rt.state.down != before.state.down {
		t.Fatalf("interaction state changed: %+v -> %+v", before.state, rt.state)
	}
	if rt.committed != before.committed || rt.generation != before.generation {
		t.Fatal("committed tree or generation changed")
	}
	if len(rt.edits) != before.edits || rt.styles != before.styles || rt.textEngine != before.engine {
		t.Fatal("runtime parts changed")
	}
	if len(rt.pending) != before.pending || len(rt.wake) != before.wake {
		t.Fatalf("pending %d -> %d, wake %d -> %d", before.pending, len(rt.pending), before.wake, len(rt.wake))
	}
	if m.clicks != 0 || m.text != "edit me" {
		t.Fatalf("model changed: %+v", m)
	}
	if len(tg.Calls) != draws {
		t.Fatalf("target called by Measure: %d -> %d calls", draws, len(tg.Calls))
	}
	render()
	if m.clicks != 1 {
		t.Fatalf("the click queued before Measure was lost: clicks=%d", m.clicks)
	}
	if rt.state.hover == nil || rt.state.focus == nil {
		t.Fatalf("hover/focus lost after Render: %+v", rt.state)
	}
}
