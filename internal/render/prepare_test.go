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
		p.Submitted(f.Uploads)
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

func glyphCommand(t *testing.T, str string) layout.Command {
	t.Helper()
	catalog, err := text.Load(text.DirectorySource("../../testdata/fonts"))
	if err != nil {
		t.Fatal(err)
	}
	measured, err := text.NewEngine(catalog).Measure(str, text.Request{Families: []string{"Noto Sans"}, Size: 20}, 0)
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
	return cmd
}

func uploadBytes(us []text.Upload) (n int) {
	for _, u := range us {
		n += len(u.Bytes)
	}
	return n
}

func TestUploadsStayOwedUntilSubmitted(t *testing.T) {
	cmd := glyphCommand(t, "office")
	p, err := NewPreparer(256, 2)
	if err != nil {
		t.Fatal(err)
	}
	prep := func() Frame {
		f, err := p.Prepare([]layout.Command{cmd}, 1, 200, 100)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	first := prep()
	if len(first.Uploads) == 0 {
		t.Fatal("no uploads")
	}
	// No submit (deferred/retried): the same placements are resent, as full pages.
	retry := prep()
	if len(retry.Uploads) == 0 || len(retry.Quads) != len(first.Quads) {
		t.Fatalf("retry uploads=%d quads=%d/%d", len(retry.Uploads), len(retry.Quads), len(first.Quads))
	}
	for i := range first.Quads {
		if first.Quads[i].Glyph != retry.Quads[i].Glyph {
			t.Fatal("placements moved on retry")
		}
	}
	if got := retry.Uploads[0].Rect; got.Dx() != 256 || got.Dy() != 256 {
		t.Fatalf("retry rect %v not full page", got)
	}
	// Acknowledging a stale frame's uploads is ignored.
	p.Submitted(first.Uploads)
	again := prep()
	if len(again.Uploads) == 0 {
		t.Fatal("stale acknowledgement discarded owed uploads")
	}
	// Successful submit clears the debt; an unchanged frame uploads nothing.
	p.Submitted(again.Uploads)
	if f := prep(); len(f.Uploads) != 0 {
		t.Fatalf("uploads after ack: %d", len(f.Uploads))
	}
	// Repeated acknowledgement is harmless.
	p.Submitted(again.Uploads)
	if f := prep(); len(f.Uploads) != 0 {
		t.Fatalf("uploads after repeated ack: %d", len(f.Uploads))
	}
}

func TestSupersededFrameKeepsAbandonedAtlasChanges(t *testing.T) {
	a, b := glyphCommand(t, "office"), glyphCommand(t, "wxyz日本語")
	p, err := NewPreparer(256, 2)
	if err != nil {
		t.Fatal(err)
	}
	// Frame 1 is prepared and acknowledged: the GPU mirror holds "office".
	f, err := p.Prepare([]layout.Command{a}, 1, 400, 100)
	if err != nil {
		t.Fatal(err)
	}
	p.Submitted(f.Uploads)
	// Frame 2 adds glyphs but is abandoned before submission.
	old, err := p.Prepare([]layout.Command{a, b}, 1, 400, 100)
	if err != nil || len(old.Uploads) == 0 {
		t.Fatalf("old frame: %v uploads=%d", err, len(old.Uploads))
	}
	// Frame 3 does not use frame 2's glyphs, yet must carry its atlas changes.
	latest, err := p.Prepare([]layout.Command{a}, 1, 400, 100)
	if err != nil {
		t.Fatal(err)
	}
	if uploadBytes(latest.Uploads) < uploadBytes(old.Uploads) {
		t.Fatalf("latest uploads %d < abandoned %d", uploadBytes(latest.Uploads), uploadBytes(old.Uploads))
	}
	// The abandoned frame's late acknowledgement must not clear the debt.
	p.Submitted(old.Uploads)
	if next, err := p.Prepare([]layout.Command{a}, 1, 400, 100); err != nil || len(next.Uploads) == 0 {
		t.Fatalf("debt lost: %v uploads=%d", err, len(next.Uploads))
	}
}

func TestFailedPrepareKeepsOwedUploads(t *testing.T) {
	cmd := glyphCommand(t, "office")
	p, err := NewPreparer(256, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Prepare([]layout.Command{cmd}, 1, 200, 100); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Prepare([]layout.Command{cmd, {Op: "clip-pop"}}, 1, 200, 100); err == nil {
		t.Fatal("expected failure")
	}
	f, err := p.Prepare([]layout.Command{cmd}, 1, 200, 100)
	if err != nil || len(f.Uploads) == 0 {
		t.Fatalf("uploads lost after failed prepare: %v %d", err, len(f.Uploads))
	}
}

func TestFirstPrepareFailureKeepsUndrainedAtlasChanges(t *testing.T) {
	cmd := glyphCommand(t, "office")
	p, err := NewPreparer(256, 2)
	if err != nil {
		t.Fatal(err)
	}
	// Glyphs are inserted, then the frame fails before uploads are drained.
	if _, err = p.Prepare([]layout.Command{cmd, {Op: "clip-pop"}}, 1, 200, 100); err == nil {
		t.Fatal("expected failure")
	}
	f, err := p.Prepare([]layout.Command{cmd}, 1, 200, 100)
	if err != nil || len(f.Uploads) == 0 || len(f.Quads) == 0 {
		t.Fatalf("undrained changes lost: %v uploads=%d quads=%d", err, len(f.Uploads), len(f.Quads))
	}
	p.Submitted(f.Uploads)
	if g, err := p.Prepare([]layout.Command{cmd}, 1, 200, 100); err != nil || len(g.Uploads) != 0 {
		t.Fatalf("uploads after ack: %v %d", err, len(g.Uploads))
	}
}
