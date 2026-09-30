package render

import (
	"image"
	"math"
	"strings"
	"testing"

	"github.com/bnema/nefergui/internal/css"
	"github.com/bnema/nefergui/internal/layout"
	"github.com/bnema/nefergui/internal/text"
)

func TestClipStackAndPhysical(t *testing.T) {
	p, err := NewPreparer(128, 2)
	if err != nil {
		t.Fatal(err)
	}
	color := css.Color{R: 1, A: 1}
	cmds := []layout.Command{
		{Op: "clip-push", Rect: layout.Rect{X: 1, Y: 2, W: 8, H: 7}},
		{Op: "clip-push", Rect: layout.Rect{X: 4, Y: 1, W: 10, H: 4}},
		{Op: "selection", Rect: layout.Rect{W: 20, H: 20}, Color: color, Opacity: .5},
		{Op: "clip-pop"},
		{Op: "caret", Rect: layout.Rect{X: 2, Y: 3, W: 1, H: 1}, Color: color, Opacity: 1},
		{Op: "clip-pop"},
	}
	f, err := p.Prepare(cmds, 1.25, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Quads) != 2 {
		t.Fatalf("quads: %+v", f.Quads)
	}
	a, b := f.Quads[0], f.Quads[1]
	if a.Clip != (layout.Rect{X: 5, Y: 3, W: 6, H: 3}) || a.Color.A != .5 || b.Clip != (layout.Rect{X: 1, Y: 3, W: 10, H: 8}) {
		t.Fatalf("clip/opacity: %+v %+v", a, b)
	}
	for _, bad := range [][]layout.Command{{{Op: "clip-pop"}}, {{Op: "clip-push"}}, {{Op: "unrecognized"}}, {{Op: "image", ID: "missing"}}} {
		if _, err := p.Prepare(bad, 1, 100, 100); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}

func TestImageQuad(t *testing.T) {
	p, err := NewPreparer(128, 2)
	if err != nil {
		t.Fatal(err)
	}
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	f, err := p.Prepare([]layout.Command{{Op: "image", Rect: layout.Rect{X: 1, Y: 2, W: 20, H: 10}, Image: img, Opacity: .5, Radii: [4]float64{4, 3, 2, 1}}}, 2, 100, 100)
	if err != nil || len(f.Quads) != 1 {
		t.Fatalf("prepare: %+v, %v", f, err)
	}
	q := f.Quads[0]
	if q.Image != img || q.Op != "image" || q.Rect != (layout.Rect{X: 2, Y: 4, W: 40, H: 20}) || q.Clip != (layout.Rect{W: 100, H: 100}) || q.Color.A != .5 || q.Radii != ([4]float64{8, 6, 4, 2}) {
		t.Fatalf("image quad: %+v", q)
	}
}

func TestGlyphRasterAndAtlas(t *testing.T) {
	catalog, err := text.Load(text.DirectorySource("../../testdata/fonts"))
	if err != nil {
		t.Fatal(err)
	}
	measured, err := text.NewEngine(catalog).Measure("office سلام 日本語", text.Request{Families: []string{"Noto Sans"}, Size: 20}, 0)
	if err != nil {
		t.Fatal(err)
	}
	cmd := layout.Command{Op: "text", Color: css.Color{A: 1}, Opacity: 1}
	for _, line := range measured.Lines {
		for _, shaped := range line.Runs {
			r := layout.Run{Face: shaped.Face, FaceID: shaped.Face.ID, Size: 20}
			for _, g := range shaped.Glyphs {
				r.Glyphs = append(r.Glyphs, layout.Glyph{ID: uint32(g.ID), X: 6 + g.X, Y: 6 + g.Y})
			}
			cmd.Runs = append(cmd.Runs, r)
		}
	}
	p, err := NewPreparer(1024, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, scale := range []float64{1, 1.25, 1.5, 2} {
		f, err := p.Prepare([]layout.Command{cmd}, scale, int(math.Ceil((measured.Width+12)*scale)), 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(f.Quads) < 10 || len(f.Uploads) == 0 {
			t.Fatalf("scale %g: %d glyphs %d uploads", scale, len(f.Quads), len(f.Uploads))
		}
		f, err = p.Prepare([]layout.Command{cmd}, scale, int(math.Ceil((measured.Width+12)*scale)), 100)
		if err != nil || len(f.Uploads) != 0 {
			t.Fatalf("scale %g: second frame uploads=%d err=%v", scale, len(f.Uploads), err)
		}
	}
	cmd.Runs[0].FaceID = "wrong"
	if _, err := p.Prepare([]layout.Command{cmd}, 1, 400, 100); err == nil || !strings.Contains(err.Error(), "mismatched") {
		t.Fatalf("accepted mismatched face: %v", err)
	}
}
