package text

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func goldenPNG(t *testing.T, scale float64) []byte {
	t.Helper()
	c := fixture(t)
	l, err := NewEngine(c).Measure("office fi سلام कि 日本語 ★", Request{Families: []string{"Noto Sans"}, Size: 20}, 0)
	if err != nil {
		t.Fatal(err)
	}
	dst := image.NewRGBA(image.Rect(0, 0, int(math.Ceil(l.Width*scale))+12, int(math.Ceil(l.Height*scale))+12))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	for _, line := range l.Lines {
		for _, run := range line.Runs {
			for _, g := range run.Glyphs {
				x, y := 6+g.X*scale, 6+g.Y*scale
				k, err := GlyphKey(run.Face, g.ID, 20*scale, x, run.Face.Variations)
				if err != nil {
					t.Fatal(err)
				}
				mask, err := Rasterize(run.Face, k)
				if err != nil {
					t.Fatal(err)
				}
				if mask.Alpha.Rect.Empty() {
					continue
				}
				// Phase is already encoded into the mask; the integer dot and glyph origin
				// determine the destination rectangle, without rounding advance positions.
				pt := image.Pt(int(math.Floor(x))+mask.Origin.X, int(math.Floor(y))+mask.Origin.Y)
				draw.DrawMask(dst, mask.Alpha.Bounds().Add(pt), image.NewUniform(color.Black), image.Point{}, mask.Alpha, image.Point{}, draw.Over)
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
func TestGoldenPending(t *testing.T) {
	for _, tc := range []struct {
		name  string
		scale float64
	}{{"1", 1}, {"1.25", 1.25}, {"1.5", 1.5}, {"2", 2}} {
		t.Run(tc.name, func(t *testing.T) {
			pending := filepath.Join("../../testdata/golden/pending", tc.name+".png")
			got := goldenPNG(t, tc.scale)
			want, err := os.ReadFile(pending)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("pending CPU image drift: %s", pending)
			}
			approved := filepath.Join("../../testdata/golden/approved", tc.name+".png")
			if ref, err := os.ReadFile(approved); err == nil && !bytes.Equal(got, ref) {
				t.Errorf("approved reference mismatch: %s", approved)
			}
		})
	}
}
