package nefergui

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bnema/nefergui/internal/css"
	"github.com/bnema/nefergui/internal/keyboard"
	"github.com/bnema/nefergui/internal/platform/wayland"
	"github.com/bnema/nefergui/internal/text"
)

func key(name string) platformInput {
	return platformInput{Kind: "key", Key: keyboard.Key{Name: name, Pressed: true}}
}
func shell(r *runtime, activated *int) func(*Frame) {
	return func(f *Frame) {
		root := f.Root()
		root.Text("Hello")
		if root.Button("Go", Key("go")).Activated() {
			*activated++
		}
		root.Button("Disabled", Key("off"), Disabled(true))
		root.Button("Next", Key("next"))
	}
}
func TestShellStyleAndFocus(t *testing.T) {
	r := newRuntime()
	catalog, err := text.Load(text.DirectorySource("testdata/fonts"))
	if err != nil {
		t.Fatal(err)
	}
	r.setTextEngine(text.NewEngine(catalog))
	r.styles = css.Compile(css.UA(), css.Parse(`text {height:12px;color:red} button {height:20px;background:blue} button:hover {background:green} button:focus-visible {outline:2px solid red}`))
	var activated int
	view := shell(r, &activated)
	if !r.Build(view) {
		t.Fatal("first frame")
	}
	if len(r.output.Tree.Children) != 4 || len(r.output.Tree.Children[0].Lines) == 0 {
		t.Fatal("text shell was not shaped and laid out", r.output.Tree)
	}
	button := r.output.Tree.Children[1]
	r.route(platformInput{Kind: "motion", X: 2, Y: button.Border.Y + 2})
	r.Build(view)
	data, err := json.Marshal(r.output.Display)
	if err != nil || !strings.Contains(string(data), `"G":0.5019607843137255`) {
		t.Fatalf("hover snapshot: %s %v", data, err)
	}
	r.route(key("Tab"))
	r.Build(view)
	data, err = json.Marshal(r.output.Display)
	if err != nil || !strings.Contains(string(data), `"Op":"outline"`) {
		t.Fatalf("focus-visible snapshot: %s %v", data, err)
	}
	if !r.state.focus.same(r.Target(1)) || !r.state.visible {
		t.Fatal("tab focus")
	}
	r.route(key("Return"))
	r.Build(view)
	if activated != 1 {
		t.Fatal("keyboard activation", activated)
	}
	r.route(key("Tab"))
	r.Build(view)
	if !r.state.focus.same(r.Target(3)) {
		t.Fatal("disabled included in tab order")
	}
	r.route(platformInput{Kind: "key", Shift: true, Key: keyboard.Key{Name: "Tab", Pressed: true}})
	r.Build(view)
	if !r.state.focus.same(r.Target(1)) {
		t.Fatal("reverse tab")
	}
	r.route(platformInput{Kind: "press", Button: pointerPrimary, X: 2, Y: button.Border.Y + 2})
	r.Build(view)
	if r.state.visible {
		t.Fatal("pointer focus visible")
	}
	r.route(platformInput{Kind: "release", Button: pointerPrimary, X: 2, Y: button.Border.Y + 2})
	r.Build(view)
	if activated != 2 {
		t.Fatal("pointer activation", activated)
	}
}
func TestOverlapCaptureRemovalAndScale(t *testing.T) {
	r := newRuntime()
	r.styles = css.Compile(css.UA(), css.Parse(`stack {height:30px} button {width:40px;height:20px}`))
	front := true
	var back, top int
	view := func(f *Frame) {
		s := f.Root().Stack()
		if s.Button("back", Key("back")).Activated() {
			back++
		}
		if front {
			if s.Button("front", Key("front")).Activated() {
				top++
			}
		}
	}
	r.Build(view)
	for _, scale := range []float64{1.25, 1.5, 2} {
		r.route(platformInput{Kind: "resize", Width: 80, Height: 60, Scale: scale})
		r.Build(view)
		if got := r.hit(20, 10); got == nil || got.identity.key != "front" {
			t.Fatalf("scale %v hit %+v", scale, got)
		}
		r.route(platformInput{Kind: "press", Button: pointerPrimary, X: 20, Y: 10})
		r.Build(view)
		if !r.state.capture.same(r.Target(0, 1)) {
			t.Fatal("capture")
		}
		r.route(platformInput{Kind: "motion", X: 70, Y: 50})
		r.Build(view)
		if !r.state.capture.same(r.Target(0, 1)) {
			t.Fatal("capture lost outside")
		}
		r.route(platformInput{Kind: "release", Button: pointerPrimary, X: 70, Y: 50})
		r.Build(view)
		if top != 0 || back != 0 {
			t.Fatal("outside release activated")
		}
	}
	r.route(platformInput{Kind: "press", Button: pointerPrimary, X: 20, Y: 10})
	r.Build(view)
	front = false
	r.Redraw()
	r.Build(view)
	if r.state.capture != nil || r.state.down {
		t.Fatal("removed capture retained")
	}
	r.route(platformInput{Kind: "release", Button: pointerPrimary, X: 20, Y: 10})
	r.Build(view)
	if top != 0 || back != 0 {
		t.Fatal("removed target activated")
	}
}
func TestFocusRemovalAndScrollClamp(t *testing.T) {
	r := newRuntime()
	r.styles = css.Compile(css.UA(), css.Parse(`scroll {width:40px;height:20px} button {height:30px}`))
	show := true
	view := func(f *Frame) {
		root := f.Root()
		s := root.Scroll(Key("sc"))
		if show {
			s.Button("one", Key("one"))
		}
		s.Button("two", Key("two"))
	}
	r.Build(view)
	r.route(key("Tab"))
	r.Build(view)
	if !r.state.focus.same(r.Target(0, 0)) {
		t.Fatal("initial focus")
	}
	show = false
	r.Redraw()
	r.Build(view)
	if r.state.focus != nil {
		t.Fatal("focus should fall back to none")
	}
	r.route(key("Tab"))
	r.Build(view)
	if !r.state.focus.same(r.Target(0, 0)) {
		t.Fatal("tab after removal")
	}
	r.route(platformInput{Kind: "axis", X: 1, Y: 1, DY: 1000})
	r.Build(view)
	n := resultByID(r.output.Tree, idKey(r.Target(0)))
	if n == nil || n.ScrollY != 10 {
		t.Fatalf("scroll clamp: %+v", n)
	}
	if r.Build(view) {
		t.Fatal("idle redraw")
	}
	r.route(platformInput{Kind: "axis", X: 1, Y: 1, DY: 1000})
	if r.Build(view) {
		t.Fatal("clamped wheel redraw")
	}
	r.route(platformInput{Kind: "axis", X: 1, Y: 1, DY: -1000})
	r.Build(view)
	if r.output.Tree.Children[0].ScrollY != 0 {
		t.Fatal("negative scroll clamp")
	}
}

func TestPointerButtonOwnership(t *testing.T) {
	r := newRuntime()
	r.styles = css.Compile(css.UA(), css.Parse(`button {height:20px}`))
	a, b := 0, 0
	view := func(f *Frame) {
		root := f.Root()
		if root.Button("a", Key("a")).Activated() {
			a++
		}
		if root.Button("b", Key("b")).Activated() {
			b++
		}
	}
	r.Build(view)
	pointer := func(kind string, button uint32, y float64) {
		r.route(platformInput{Kind: kind, Button: button, X: 2, Y: y})
	}
	const secondary uint32 = 0x111
	pointer("press", pointerPrimary, 2)
	r.Build(view)
	pointer("press", secondary, 22)
	if !r.state.capture.same(r.Target(0)) || !r.state.focus.same(r.Target(0)) {
		t.Fatal("second press stole capture/focus")
	}
	pointer("release", secondary, 22)
	if !r.state.down {
		t.Fatal("second release ended capture")
	}
	pointer("release", pointerPrimary, 2)
	r.Build(view)
	if a != 1 || b != 0 || r.state.down {
		t.Fatalf("primary completion: %d %d", a, b)
	}
	pointer("press", secondary, 22)
	r.Build(view)
	if !r.state.capture.same(r.Target(1)) || r.state.focus == nil || !r.state.focus.same(r.Target(0)) {
		t.Fatal("secondary capture changed focus")
	}
	pointer("release", pointerPrimary, 22)
	if !r.state.down {
		t.Fatal("wrong release ended secondary capture")
	}
	pointer("release", secondary, 22)
	r.Build(view)
	if b != 0 || r.state.down {
		t.Fatal("secondary activated or remained captured")
	}
}

// The cursor follows the hovered element's CSS cursor, stays on the captured
// element while a primary button is held, and resets outside the surface.
func TestCursorFollowsHoverAndCapture(t *testing.T) {
	r := newRuntime()
	r.styles = css.Compile(css.UA(), css.Parse(`button,input{height:20px;width:100px}`))
	name := ""
	view := func(f *Frame) {
		root := f.Root()
		root.Button("go", Key("go"))
		root.Input("name", &name, Key("name"))
		root.Button("off", Key("off"), Disabled(true))
		root.Text("plain", Key("plain"))
	}
	r.Build(view)
	at := func(kind string, y float64) css.Keyword {
		r.route(platformInput{Kind: kind, Button: pointerPrimary, X: 2, Y: y})
		return r.cursor()
	}
	if c := r.cursor(); c != css.KeywordDefault {
		t.Fatalf("outside: %v", c)
	}
	for _, c := range []struct {
		y    float64
		want uint32
	}{{2, wayland.CursorPointer}, {22, wayland.CursorText}, {42, wayland.CursorNotAllowed}, {62, wayland.CursorDefault}} {
		if got := cursorShape(at("motion", c.y)); got != c.want {
			t.Errorf("y=%v shape %v, want %v", c.y, got, c.want)
		}
	}
	if cursorShape(css.KeywordAuto) != cursorShape(css.KeywordDefault) {
		t.Error("auto must map to the default shape")
	}
	// Press on the input, drag over the button: the input keeps its cursor.
	at("press", 22)
	r.Build(view)
	if got := at("motion", 2); got != css.KeywordText {
		t.Errorf("captured drag cursor %v", got)
	}
	at("release", 2)
	r.Build(view)
	if got := r.cursor(); got != css.KeywordPointer {
		t.Errorf("after release cursor %v", got)
	}
	r.route(platformInput{Kind: "leave"})
	if got := r.cursor(); got != css.KeywordDefault {
		t.Errorf("after leave cursor %v", got)
	}
}

// A control declared before the one whose event changes the model painted the
// old value during the event frame. The settle frame must show the new value
// without further input (the demo menu kept its old active item until the
// pointer moved).
func TestEventSettlesEarlierControls(t *testing.T) {
	r := newRuntime()
	r.styles = css.Compile(css.UA(), css.Parse(`button{height:20px} button.active{background:#ff0000}`))
	page := "a"
	view := func(f *Frame) {
		root := f.Root()
		for _, p := range []string{"a", "b"} {
			opts := []ButtonOption{Key(p)}
			if page == p {
				opts = append(opts, Class("active"))
			}
			if root.Button(p, opts...).Activated() {
				page = p
			}
		}
	}
	active := func() string {
		for _, c := range r.output.Display {
			if c.Op == "rect" && c.Color.R == 1 && c.Color.G == 0 {
				return c.ID
			}
		}
		return ""
	}
	r.Build(view)
	r.route(platformInput{Kind: "press", Button: pointerPrimary, X: 2, Y: 22})
	r.Build(view)
	r.route(platformInput{Kind: "release", Button: pointerPrimary, X: 2, Y: 22})
	r.Build(view)
	if page != "b" {
		t.Fatalf("page %q", page)
	}
	if !r.Build(view) {
		t.Fatal("no settle frame after activation")
	}
	if got := active(); got != "root/k1:b" {
		t.Fatalf("active item painted as %q after settle", got)
	}
	if r.Build(view) {
		t.Fatal("settle frame scheduled another frame")
	}
}

func TestKeyboardActivationLifecycle(t *testing.T) {
	r := newRuntime()
	r.styles = css.Compile(css.UA(), css.Parse(`button {height:20px} button:active {background:red}`))
	count := 0
	view := func(f *Frame) {
		root := f.Root()
		if root.Button("go", Key("go")).Activated() {
			count++
		}
		root.Button("other", Key("other"))
	}
	r.Build(view)
	r.route(key("Tab"))
	r.Build(view)
	repeat := key("Return")
	repeat.Key.Repeat = true
	r.route(key("Return"))
	r.route(repeat)
	r.Build(view)
	if count != 1 {
		t.Fatal("Enter repeat activated", count)
	}
	space := key("space")
	spaceRepeat := space
	spaceRepeat.Key.Repeat = true
	r.route(space)
	r.route(spaceRepeat)
	r.Build(view)
	if count != 1 || r.state.space == nil || r.state.flags(r.committed.children[0])&css.Active == 0 {
		t.Fatal("space press should only set active")
	}
	r.route(platformInput{Kind: "key", Key: keyboard.Key{Name: "space"}})
	r.Build(view)
	if count != 2 || r.state.space != nil {
		t.Fatal("space release should activate once", count)
	}
	r.route(space)
	r.route(key("Escape"))
	r.Build(view)
	r.route(platformInput{Kind: "key", Key: keyboard.Key{Name: "space"}})
	if r.Build(view) {
		t.Fatal("cancelled release redrew")
	}
	if count != 2 {
		t.Fatal("Escape failed to cancel")
	}
	r.route(space)
	r.route(platformInput{Kind: "focus-out"})
	r.Build(view)
	r.route(platformInput{Kind: "key", Key: keyboard.Key{Name: "space"}})
	if r.Build(view) || count != 2 {
		t.Fatal("focus loss failed to cancel")
	}
	r.route(key("Tab"))
	r.Build(view)
	r.route(space)
	r.route(key("Tab"))
	r.Build(view)
	r.route(platformInput{Kind: "key", Key: keyboard.Key{Name: "space"}})
	if r.Build(view) || count != 2 {
		t.Fatal("focus change failed to cancel")
	}
}

func TestScrollIdentityStopsBeingScrollable(t *testing.T) {
	r := newRuntime()
	r.styles = css.Compile(css.UA(), css.Parse(`scroll, box {width:40px;height:20px} text {height:40px}`))
	scroll := true
	view := func(f *Frame) {
		root := f.Root()
		var n Node
		if scroll {
			n = root.Scroll(Key("same"))
		} else {
			n = root.Box(Key("same"))
		}
		n.Text("content")
	}
	r.Build(view)
	r.route(platformInput{Kind: "axis", X: 1, Y: 1, DY: 15})
	r.Build(view)
	id := idKey(r.Target(0))
	if r.state.scroll[id].H != 15 {
		t.Fatal("scroll not stored")
	}
	scroll = false
	r.Redraw()
	r.Build(view)
	if _, ok := r.state.scroll[id]; ok {
		t.Fatal("non-scroll container retained offset")
	}
	scroll = true
	r.Redraw()
	r.Build(view)
	if r.output.Tree.Children[0].ScrollY != 0 {
		t.Fatal("stale offset restored")
	}
}

func TestKeyedReorderKeepsInteractionState(t *testing.T) {
	r := newRuntime()
	r.styles = css.Compile(css.UA(), css.Parse(`scroll {height:20px} button {height:30px}`))
	order := []string{"a", "b"}
	view := func(f *Frame) {
		root := f.Root()
		for _, k := range order {
			s := root.Scroll(Key(k))
			s.Button(k, Key("control"))
		}
	}
	r.Build(view)
	r.route(platformInput{Kind: "motion", X: 2, Y: 2})
	r.route(platformInput{Kind: "axis", X: 2, Y: 2, DY: 10})
	r.route(key("Tab"))
	r.Build(view)
	hover, focus := r.state.hover, r.state.focus
	scrollID := idKey(r.Target(0))
	if r.state.scroll[scrollID].H != 10 {
		t.Fatal("scroll state")
	}
	order = []string{"b", "a"}
	r.Redraw()
	r.Build(view)
	if !r.state.focus.same(focus) || r.state.scroll[scrollID].H != 10 {
		t.Fatal("keyed focus/scroll lost")
	}
	if r.state.hover.same(hover) || !r.state.hover.same(r.Target(0, 0)) {
		t.Fatal("hover should track pointer over reordered layout")
	}
	r.route(platformInput{Kind: "motion", X: 2, Y: 32})
	r.Build(view)
	if !r.state.hover.same(hover) {
		t.Fatal("hover failed to follow keyed element at new position")
	}
}

func TestInputDuringBuildTargetsLastCommit(t *testing.T) {
	r := newRuntime()
	r.styles = css.Compile(css.UA(), css.Parse(`button {height:20px}`))
	r.Build(func(f *Frame) { f.Root().Button("old", Key("old")) })
	r.Redraw()
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan bool, 1)
	go func() {
		done <- r.Build(func(f *Frame) { f.Root().Button("new", Key("new")); close(entered); <-release })
	}()
	waitEntered(t, entered)
	r.route(platformInput{Kind: "press", Button: pointerPrimary, X: 2, Y: 2})
	r.route(platformInput{Kind: "release", Button: pointerPrimary, X: 2, Y: 2})
	if len(r.pending) != 1 || !r.pending[0].Target.same(r.Target(0)) {
		t.Fatal("in-flight input missed last committed target")
	}
	close(release)
	if !<-done {
		t.Fatal("build failed")
	}
	received := false
	r.Build(func(f *Frame) { received = f.Root().Button("old", Key("old")).Activated() })
	if !received {
		t.Fatal("event lost during build")
	}
}

func TestPhysicalToLogicalBoundary(t *testing.T) {
	for _, scale := range []float64{1.25, 1.5, 2} {
		x, y, ok := physicalToLogical(20*scale, 10*scale, scale)
		if !ok || x != 20 || y != 10 {
			t.Fatalf("scale %v: %v %v %v", scale, x, y, ok)
		}
	}
	if _, _, ok := physicalToLogical(2, 2, 0); ok {
		t.Fatal("zero scale")
	}
}

func TestPointerLeaveClearsHoverKeepsCapture(t *testing.T) {
	r := newRuntime()
	r.styles = css.Compile(css.UA(), css.Parse(`button {height:20px;background:blue} button:hover {background:red}`))
	activations := 0
	view := func(f *Frame) {
		if f.Root().Button("go", Key("go")).Activated() {
			activations++
		}
	}
	r.Build(view)
	r.route(platformInput{Kind: "motion", X: 2, Y: 2})
	r.Build(view)
	if !r.state.inside || !r.state.hover.same(r.Target(0)) || r.committed.children[0].computed.Style.BackgroundColor.R != 1 {
		t.Fatal("hover not applied")
	}
	r.route(platformInput{Kind: "leave"})
	r.Build(view)
	if r.state.inside || r.state.hover != nil || r.committed.children[0].computed.Style.BackgroundColor.B != 1 {
		t.Fatal("leave did not clear hover style")
	}
	r.route(platformInput{Kind: "leave"})
	if r.Build(view) {
		t.Fatal("duplicate leave invalidated")
	}
	r.route(platformInput{Kind: "press", Button: pointerPrimary, X: 2, Y: 2})
	r.Build(view)
	if !r.state.down || !r.state.capture.same(r.Target(0)) {
		t.Fatal("no capture")
	}
	r.route(platformInput{Kind: "leave"})
	r.Build(view)
	if r.state.inside || r.state.hover != nil || !r.state.down || !r.state.capture.same(r.Target(0)) {
		t.Fatal("leave cancelled capture or kept hover")
	}
	r.route(platformInput{Kind: "release", Button: pointerPrimary, X: 2, Y: 2})
	r.Build(view)
	if activations != 0 || r.state.down || r.state.capture != nil || r.state.inside || r.state.hover != nil {
		t.Fatal("outside release activated or kept capture/hover")
	}
	if r.Build(view) {
		t.Fatal("idle redraw after leave/release")
	}
}
