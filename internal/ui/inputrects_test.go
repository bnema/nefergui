//go:build linux

package ui

import (
	"math"
	"strconv"
	"testing"

	"github.com/bnema/nefergui/internal/layout"
	"github.com/stretchr/testify/mock"
)

func TestFrameSetInputRects(t *testing.T) {
	r, tg := newTestRenderer(t)
	tg.EXPECT().Resize(mock.Anything, mock.Anything).Return().Once()
	drawOK(tg, nil).Maybe()
	r.Resize(200, 100, 1)
	var out Output
	var setErr error
	rects := []Rect{{1, 2, 3, 4}, {5, 6, 7, 8}}
	render := func(view func(*Frame, *testModel)) bool {
		t.Helper()
		r.Invalidate()
		ok, err := r.Render(&out, &testModel{}, view)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if !render(func(f *Frame, _ *testModel) { f.Root(); setErr = f.SetInputRects(rects) }) || setErr != nil {
		t.Fatalf("render or set: %v", setErr)
	}
	rects[0].Width = 99 // caller reuse must not affect the staged copy
	if !out.InputRectsChanged || len(out.InputRects) != 2 || out.InputRects[0] != (Rect{1, 2, 3, 4}) {
		t.Fatalf("applied %+v", out.InputRects)
	}
	// An unchanged region is not reported again.
	if render(func(f *Frame, _ *testModel) { f.Root(); _ = f.SetInputRects([]Rect{{1, 2, 3, 4}, {5, 6, 7, 8}}) }) && out.InputRectsChanged {
		t.Fatalf("restaged %+v", out.InputRects)
	}
	// A frame that never calls SetInputRects stages nothing new.
	if render(func(f *Frame, _ *testModel) { f.Root() }) && out.InputRectsChanged {
		t.Fatal("unchanged frame restaged the region")
	}
	// nil restores the full surface; empty non-nil is click-through.
	if !render(func(f *Frame, _ *testModel) { f.Root(); _ = f.SetInputRects(nil) }) || !out.InputRectsChanged || out.InputRects != nil {
		t.Fatalf("nil region: %+v", out)
	}
	if !render(func(f *Frame, _ *testModel) { f.Root(); _ = f.SetInputRects([]Rect{}) }) || !out.InputRectsChanged || out.InputRects == nil || len(out.InputRects) != 0 {
		t.Fatalf("empty region: %+v", out)
	}
	// Invalid rects are rejected in the frame call and stage nothing.
	for _, bad := range []Rect{{0, 0, 0, 5}, {0, 0, 5, -1}, {math.MaxInt32, 0, 5, 5}} {
		render(func(f *Frame, _ *testModel) { f.Root(); setErr = f.SetInputRects([]Rect{bad}) })
		if setErr == nil || r.rt.inputStaged {
			t.Fatalf("accepted %+v", bad)
		}
	}
	// Steady-state staging does not allocate once buffers exist.
	rt := newRuntime()
	rt.Build(func(f *Frame) {
		f.Root()
		_ = f.SetInputRects(rects)
		if n := testing.AllocsPerRun(50, func() { _ = f.SetInputRects(rects) }); n != 0 {
			t.Fatalf("SetInputRects allocs %v", n)
		}
	})
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
