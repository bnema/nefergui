package render

import (
	"testing"

	"github.com/bnema/nefergui/internal/layout"
)

func TestPrepareBoundsReservationForClippedGlyphs(t *testing.T) {
	for _, op := range []string{"text", "preedit-text"} {
		t.Run(op, func(t *testing.T) {
			cmd := glyphCommand(t, "a")
			cmd.Op = op
			run := cmd.Runs[0]
			glyph := run.Glyphs[0]
			run.Glyphs = make([]layout.Glyph, 50000)
			for i := range run.Glyphs {
				run.Glyphs[i] = glyph
				run.Glyphs[i].Y = 10000
			}
			cmd.Runs = []layout.Run{run}
			p, err := NewPreparer(256, 2)
			if err != nil {
				t.Fatal(err)
			}
			f, err := p.Prepare([]layout.Command{cmd}, 1, 100, 100)
			if err != nil {
				t.Fatal(err)
			}
			if len(f.Quads) != 0 || cap(f.Quads) > maxQuadPrealloc {
				t.Fatalf("clipped glyphs: len=%d cap=%d", len(f.Quads), cap(f.Quads))
			}
		})
	}
}

func TestPrepareGrowsBeyondReservationForVisibleQuads(t *testing.T) {
	p, err := NewPreparer(256, 2)
	if err != nil {
		t.Fatal(err)
	}
	cmds := rectCommands(maxQuadPrealloc + 100)
	f, err := p.Prepare(cmds, 1, 100, len(cmds)+1)
	if err != nil || len(f.Quads) != len(cmds) {
		t.Fatalf("visible quads: got %d want %d, error %v", len(f.Quads), len(cmds), err)
	}
}
