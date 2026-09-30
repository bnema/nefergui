package render

import (
	"testing"

	"github.com/bnema/nefergui/internal/css"
	"github.com/bnema/nefergui/internal/layout"
	"github.com/bnema/nefergui/internal/text"
)

// BenchmarkPrepare exercises the product example's text and box display-list mix.
func BenchmarkPrepare(b *testing.B) {
	catalog, err := text.Load(text.DirectorySource("../../testdata/fonts"))
	if err != nil {
		b.Fatal(err)
	}
	engine := text.NewEngine(catalog)
	var commands []layout.Command
	labels := []string{"Demo", "Accueil", "Paramètres", "Thème sombre", "Bienvenue", "Une interface native écrite entièrement en Go.", "Votre nom", "Continuer", "Bonjour Ada"}
	for i, label := range labels {
		measured, err := engine.Measure(label, text.Request{Families: []string{"Noto Sans"}, Size: 16}, 0)
		if err != nil {
			b.Fatal(err)
		}
		commands = append(commands, layout.Command{Op: "rect", Rect: layout.Rect{X: 10, Y: float64(i * 45), W: 320, H: 35}, Color: css.Color{A: 1}, Opacity: 1})
		cmd := layout.Command{Op: "text", Rect: layout.Rect{X: 20, Y: float64(i * 45), W: 300, H: 35}, Color: css.Color{A: 1}, Opacity: 1}
		for _, line := range measured.Lines {
			for _, run := range line.Runs {
				r := layout.Run{Face: run.Face, FaceID: run.Face.ID, Size: 16}
				for _, glyph := range run.Glyphs {
					r.Glyphs = append(r.Glyphs, layout.Glyph{ID: uint32(glyph.ID), X: 20 + glyph.X, Y: float64(i*45+20) + glyph.Y})
				}
				cmd.Runs = append(cmd.Runs, r)
			}
		}
		commands = append(commands, cmd)
	}
	p, err := NewPreparer(1024, 4)
	if err != nil {
		b.Fatal(err)
	}
	if _, err = p.Prepare(commands, 1, 960, 640); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		frame, err := p.Prepare(commands, 1, 960, 640)
		if err != nil || len(frame.Quads) == 0 {
			b.Fatalf("prepare: %v", err)
		}
	}
}
