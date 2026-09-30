package text

import (
	"bytes"
	"image"
	"testing"
)

func TestMarkAllDirty(t *testing.T) {
	a, err := NewAtlas(16, 2)
	if err != nil {
		t.Fatal(err)
	}
	a.MarkAllDirty()
	if got := a.Uploads(); len(got) != 0 {
		t.Fatalf("empty atlas: %v", got)
	}
	m := Mask{Alpha: image.NewAlpha(image.Rect(0, 0, 2, 2))}
	m.Alpha.Pix[0] = 123
	if _, err := a.Insert(Key{Glyph: 1}, m); err != nil {
		t.Fatal(err)
	}
	a.Uploads()
	a.MarkAllDirty()
	a.MarkAllDirty()
	got := a.Uploads()
	if len(got) != 1 || got[0].Page != 0 || got[0].Rect != image.Rect(0, 0, 16, 16) || !bytes.Equal(got[0].Bytes, a.pages[0].pixels) {
		t.Fatalf("dirty snapshot: %v", got)
	}
	if got := a.Uploads(); len(got) != 0 {
		t.Fatalf("not drained: %v", got)
	}
}
