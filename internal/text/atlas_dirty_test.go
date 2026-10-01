package text

import (
	"bytes"
	"image"
	"testing"

	"github.com/go-text/typesetting/font"
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

// TestAtlasCachesEmptyGlyphs keeps spaces out of the per-frame raster path and
// bounds their entries, which no page eviction removes.
func TestAtlasCachesEmptyGlyphs(t *testing.T) {
	a, err := NewAtlas(16, 1)
	if err != nil {
		t.Fatal(err)
	}
	space := Mask{Alpha: image.NewAlpha(image.Rect(0, 0, 0, 0)), Origin: image.Pt(0, -3)}
	if p, err := a.Insert(Key{Glyph: 3}, space); err != nil || p.Page != -1 {
		t.Fatalf("insert: %+v %v", p, err)
	}
	a.BeginFrame()
	if p, ok := a.Lookup(Key{Glyph: 3}); !ok || p.Page != -1 || p.Origin != space.Origin {
		t.Fatalf("lookup: %+v %v", p, ok)
	}
	// Evicting the only page for a new glyph must keep the empty entry.
	ink := Mask{Alpha: image.NewAlpha(image.Rect(0, 0, 10, 10))}
	for g := range 3 {
		a.BeginFrame()
		if _, err := a.Insert(Key{Glyph: font.GID(10 + g)}, ink); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := a.Lookup(Key{Glyph: 3}); !ok {
		t.Fatal("page eviction dropped an empty glyph")
	}
	for g := range maxEmptyGlyphs + 10 {
		if _, err := a.Insert(Key{Glyph: font.GID(100 + g)}, space); err != nil {
			t.Fatal(err)
		}
	}
	if a.empty > maxEmptyGlyphs || len(a.entries) > maxEmptyGlyphs+1 {
		t.Fatalf("empty glyphs unbounded: %d counted, %d entries", a.empty, len(a.entries))
	}
}
