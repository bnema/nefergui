//go:build linux

package ui

import (
	"image"
	"image/color"
	"testing"
	"time"
)

// An image added in a frame that uploads no glyph is still copied to the GPU:
// before the fix it sampled an uninitialized texture and drew nothing. A first
// text-only frame caches the glyphs and initializes the atlas, so the second
// frame has nothing to upload but the image.
func TestRendererGPUImageWithoutNewGlyphs(t *testing.T) {
	r, tg := gpuRenderer(t)
	src := image.NewNRGBA(image.Rect(0, 0, 40, 30))
	for y := range 30 {
		for x := range 40 {
			src.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 6), G: uint8(y * 8), B: 0x80, A: 0xff})
		}
	}
	show := false
	view := func(f *Frame, _ *struct{}) {
		st := f.Root().Stack()
		if show {
			st.Box(Inline("overflow:hidden")).Rect(0, 0, 40, 30).Image(src, Inline("width:100%;height:100%"))
		}
		st.Box().Rect(0, 0, 1, 1).Text("x")
	}
	r.Resize(40, 30, 1)
	var out Output
	var m struct{}
	ok, err := r.Render(&out, &m, view)
	if err != nil || !ok {
		t.Fatalf("text frame: ok=%v err=%v", ok, err)
	}
	signalRelease(t, tg, &out)
	if !readable(t, out.ReleaseFD, time.Second) {
		t.Fatal("text frame not released")
	}
	if err = r.Released(out.Buffer); err != nil {
		t.Fatal(err)
	}
	show = true
	r.Invalidate()
	if ok, err = r.Render(&out, &m, view); err != nil || !ok {
		t.Fatalf("image frame: ok=%v err=%v", ok, err)
	}
	img, err := tg.Readback(out.Buffer)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []image.Point{{5, 5}, {20, 15}, {39, 29}} {
		want, got := src.NRGBAAt(p.X, p.Y), img.NRGBAAt(p.X, p.Y)
		if got.R != want.R || got.G != want.G || got.B != want.B {
			t.Fatalf("pixel %v = %+v, want %+v", p, got, want)
		}
	}
}
