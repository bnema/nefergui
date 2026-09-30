package render

import (
	"testing"

	"github.com/bnema/nefergui/internal/layout"
	"github.com/bnema/nefergui/internal/text"
	"github.com/go-text/typesetting/font"
)

func TestUncoveredSpaceRemainsInvisible(t *testing.T) {
	catalog, err := text.Load(text.DirectorySource("../../testdata/fonts"))
	if err != nil {
		t.Fatal(err)
	}
	measured, err := text.NewEngine(catalog).Measure("a\u1680b", text.Request{Families: []string{"Noto Sans"}, Size: 20}, 0)
	if err != nil {
		t.Fatal(err)
	}
	cmd := layout.Command{Op: "text", Opacity: 1}
	spaces := 0
	for _, line := range measured.Lines {
		for _, shaped := range line.Runs {
			r := layout.Run{Face: shaped.Face, FaceID: shaped.Face.ID, Size: 20}
			for _, g := range shaped.Glyphs {
				if g.ID == font.EmptyGlyph {
					spaces++
					if g.Missing || g.Advance <= 0 {
						t.Fatalf("empty space glyph: %+v", g)
					}
				}
				r.Glyphs = append(r.Glyphs, layout.Glyph{ID: uint32(g.ID), X: 6 + g.X, Y: 30 + g.Y})
			}
			cmd.Runs = append(cmd.Runs, r)
		}
	}
	if spaces != 1 {
		t.Fatalf("want one uncovered space, got %d", spaces)
	}
	p, err := NewPreparer(256, 2)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := p.Prepare([]layout.Command{cmd}, 1, 200, 100)
	if err != nil || len(frame.Quads) != 2 {
		t.Fatalf("space must emit no quad: %d quads, error %v", len(frame.Quads), err)
	}
}
