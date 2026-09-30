package text

import (
	"errors"
	"image"
)

// Upload is an immutable tightly packed grayscale rectangle, for the future renderer.
// It contains the latest pixels for Rect, including zeroed areas after eviction.
type Upload struct {
	Page  int
	Rect  image.Rectangle
	Bytes []byte
}
type Placement struct {
	Page   int
	Rect   image.Rectangle
	Origin image.Point
}
type atlasEntry struct {
	placement  Placement
	generation uint64
}
type atlasPage struct {
	pixels     []byte
	x, y, row  int
	generation uint64
}

// Atlas owns fixed-size monochrome pages. One pixel padding isolates bilinear samples.
// It is not concurrent-safe; call BeginFrame before inserting a new frame and drain Uploads
// before displaying that frame. An active page is never evicted within the same frame.
// Placements expire when their page is evicted; the caller must only retain them
// for its current frame. Drain Uploads once per frame; undrained changes are
// coalesced, never discarded. At most one pending upload per page (bounding
// union of all dirty rects) uses at most MaxPages*Size*Size pending bytes.
type Atlas struct {
	Size, MaxPages int
	pages          []atlasPage
	entries        map[Key]atlasEntry
	frame          uint64
	dirty          map[int]image.Rectangle
}

func NewAtlas(size, maxPages int) (*Atlas, error) {
	if size < 4 || size > 4096 || maxPages < 1 || maxPages > 64 {
		return nil, errors.New("invalid atlas bounds")
	}
	return &Atlas{Size: size, MaxPages: maxPages, entries: map[Key]atlasEntry{}, dirty: map[int]image.Rectangle{}, frame: 1}, nil
}
func (a *Atlas) BeginFrame() { a.frame++ }

// PagesUsed reports the number of allocated atlas pages.
func (a *Atlas) PagesUsed() int { return len(a.pages) }

// MarkAllDirty restores the GPU mirror after a failed upload or atlas recreation.
// Existing pending changes are subsumed by the full-page rectangles.
func (a *Atlas) MarkAllDirty() {
	for page := range a.pages {
		a.dirty[page] = image.Rect(0, 0, a.Size, a.Size)
	}
}

// Uploads returns a snapshot of all pending page changes, then clears the pending
// set. Call once per frame before submitting the renderer uploads.
func (a *Atlas) Uploads() []Upload {
	out := make([]Upload, 0, len(a.dirty))
	for page := range a.pages {
		rect, ok := a.dirty[page]
		if !ok {
			continue
		}
		p := &a.pages[page]
		w, h := rect.Dx(), rect.Dy()
		buf := make([]byte, w*h)
		for y := 0; y < h; y++ {
			copy(buf[y*w:(y+1)*w], p.pixels[(rect.Min.Y+y)*a.Size+rect.Min.X:])
		}
		out = append(out, Upload{Page: page, Rect: rect, Bytes: buf})
	}
	clear(a.dirty)
	return out
}
func (a *Atlas) markDirty(page int, rect image.Rectangle) {
	if old, ok := a.dirty[page]; ok {
		rect = old.Union(rect)
	}
	a.dirty[page] = rect
}

// Lookup only updates the page generation; a hit neither rasterizes nor uploads.
func (a *Atlas) Lookup(k Key) (Placement, bool) {
	e, ok := a.entries[k]
	if !ok {
		return Placement{}, false
	}
	e.generation = a.frame
	a.entries[k] = e
	a.pages[e.placement.Page].generation = a.frame
	return e.placement, true
}
func (a *Atlas) Insert(k Key, m Mask) (Placement, error) {
	if m.Alpha == nil || m.Alpha.Rect.Min != image.Pt(0, 0) || m.Alpha.Stride < m.Alpha.Rect.Dx() || len(m.Alpha.Pix) < m.Alpha.Stride*m.Alpha.Rect.Dy() {
		return Placement{}, errors.New("invalid mask")
	}
	if p, ok := a.Lookup(k); ok {
		return p, nil
	}
	w, h := m.Alpha.Rect.Dx(), m.Alpha.Rect.Dy()
	if w < 0 || h < 0 || w+2 > a.Size || h+2 > a.Size {
		return Placement{}, errors.New("glyph too large")
	}
	if w == 0 || h == 0 {
		return Placement{Page: -1, Origin: m.Origin}, nil
	}
	chosen := -1
	var x, y int
	for i := range a.pages {
		if xx, yy, ok := a.position(&a.pages[i], w+2, h+2); ok {
			chosen = i
			x = xx
			y = yy
			break
		}
	}
	if chosen < 0 {
		if len(a.pages) < a.MaxPages {
			chosen = len(a.pages)
			a.pages = append(a.pages, atlasPage{pixels: make([]byte, a.Size*a.Size)})
		} else {
			oldest := uint64(^uint64(0))
			for i, p := range a.pages {
				if p.generation < a.frame && p.generation < oldest {
					oldest = p.generation
					chosen = i
				}
			}
			if chosen < 0 {
				return Placement{}, errors.New("atlas full: all pages used this frame")
			}
			for key, e := range a.entries {
				if e.placement.Page == chosen {
					delete(a.entries, key)
				}
			}
			a.pages[chosen] = atlasPage{pixels: make([]byte, a.Size*a.Size)}
			a.markDirty(chosen, image.Rect(0, 0, a.Size, a.Size))
		}
		x, y = 0, 0
	}
	p := &a.pages[chosen]
	if x == 0 && p.x != 0 {
		p.row = 0
	}
	p.x = x + w + 2
	p.y = y
	p.row = max(p.row, h+2)
	p.generation = a.frame
	rect := image.Rect(x+1, y+1, x+1+w, y+1+h)
	buf := make([]byte, w*h)
	for j := 0; j < h; j++ {
		copy(buf[j*w:(j+1)*w], m.Alpha.Pix[j*m.Alpha.Stride:j*m.Alpha.Stride+w])
		copy(p.pixels[(y+1+j)*a.Size+x+1:], buf[j*w:(j+1)*w])
	}
	place := Placement{Page: chosen, Rect: rect, Origin: m.Origin}
	a.entries[k] = atlasEntry{place, a.frame}
	a.markDirty(chosen, rect)
	return place, nil
}
func (a *Atlas) position(p *atlasPage, w, h int) (int, int, bool) {
	x, y := p.x, p.y
	if x+w > a.Size {
		x = 0
		y += p.row
	}
	return x, y, y+h <= a.Size
}
