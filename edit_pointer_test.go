package nefergui

import (
	"testing"
	"time"

	"github.com/bnema/nefergui/internal/css"
	"github.com/bnema/nefergui/internal/text"
)

func TestPointerTextSelection(t *testing.T) {
	catalog, err := text.Load(text.DirectorySource("testdata/fonts"))
	if err != nil {
		t.Fatal(err)
	}
	r := newRuntime()
	r.setTextEngine(text.NewEngine(catalog))
	r.styles = css.Compile(css.UA(), css.Parse(`input, textarea { width: 240px; height: 32px; font-size: 16px; }`))
	value := "aé word"
	view := func(f *Frame) { f.Root().Input("edit", &value, Key("edit")) }
	r.Build(view)
	n := r.output.Tree.Children[0]
	id := n.ID
	bs := textBoundaries(value, n, r.output.Display)
	if len(bs) == 0 || len(bs[0]) < 3 {
		t.Fatalf("no text clusters: %+v", bs)
	}
	x := func(b int) float64 {
		for _, v := range bs[0] {
			if v.byteOffset == b {
				return v.x
			}
		}
		t.Fatalf("no boundary %d: %+v", b, bs)
		return 0
	}
	y := n.Content.Y + 1
	at := time.Unix(20, 0)
	press := func(px float64, when time.Time, shift bool) {
		r.route(platformInput{Kind: "press", Button: pointerPrimary, X: px, Y: y, Time: when, Shift: shift})
		r.Build(view)
	}
	release := func() { r.route(platformInput{Kind: "release", Button: pointerPrimary, X: 0, Y: y}); r.Build(view) }
	press(x(3), at, false)
	if got := r.edits[id].Cursor; got != 3 {
		t.Fatalf("UTF-8 boundary got %d want 3: %+v content=%+v", got, bs, n.Content)
	}
	r.route(platformInput{Kind: "motion", X: x(0), Y: y})
	r.Build(view)
	if a, b := r.edits[id].Range(); a != 0 || b != 3 {
		t.Fatalf("drag selection %d:%d", a, b)
	}
	release()
	press(x(8)-0.01, at.Add(time.Second), true)
	if a, b := r.edits[id].Range(); a != 3 || b != 8 {
		t.Fatalf("shift selection %d:%d", a, b)
	}
	release()
	press(x(5), at.Add(2*time.Second), false)
	release()
	press(x(5), at.Add(2*time.Second+300*time.Millisecond), false)
	if a, b := r.edits[id].Range(); a != 4 || b != 8 {
		t.Fatalf("double word selection %d:%d", a, b)
	}
	release()
	// A word start used to include the previous segment too.
	press(x(4), at.Add(3*time.Second), false)
	release()
	press(x(4), at.Add(3*time.Second+200*time.Millisecond), false)
	if a, b := r.edits[id].Range(); a != 4 || b != 8 {
		t.Fatalf("boundary word selection %d:%d", a, b)
	}
}
