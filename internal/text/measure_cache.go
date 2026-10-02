package text

import (
	"encoding/binary"
	"hash/maphash"
	"math"
	"slices"

	"github.com/go-text/typesetting/font"
)

// maxMeasureEntries bounds the cache so a single frame measuring many unique
// strings cannot grow it without limit; beyond it results are not cached.
const maxMeasureEntries = 4096

var measureSeed = maphash.MakeSeed()

type measureKey struct {
	text     string
	families uint64 // hash of Request.Families and Variations
	weight   float32
	stretch  float32
	italic   bool
	dir      uint8
	size     float64
	spacing  float64
	width    float64
}

type measureEntry struct {
	families   []string // verified on hit, so a hash collision cannot alias
	variations []font.Variation
	layout     Layout
	used       uint64
}

type measureCache struct {
	entries map[measureKey]*measureEntry
	frame   uint64
	free    []*measureEntry // evicted entries, reused by put
}

func requestKey(s string, r Request, width float64) measureKey {
	var h maphash.Hash
	h.SetSeed(measureSeed)
	for _, f := range r.Families {
		h.WriteString(f)
		h.WriteByte(0)
	}
	h.WriteByte(1)
	for _, v := range r.Variations {
		var b [8]byte
		binary.LittleEndian.PutUint32(b[:4], uint32(v.Tag))
		binary.LittleEndian.PutUint32(b[4:], math.Float32bits(v.Value))
		h.Write(b[:])
	}
	return measureKey{text: s, families: h.Sum64(), weight: r.Weight, stretch: r.Stretch, italic: r.Italic, dir: uint8(r.Direction), size: r.Size, spacing: r.LetterSpacing, width: width}
}

func (c *measureCache) get(s string, r Request, width float64) (Layout, bool) {
	e := c.entries[requestKey(s, r, width)]
	if e == nil || !slices.Equal(e.families, r.Families) || !slices.Equal(e.variations, r.Variations) {
		return Layout{}, false
	}
	e.used = c.frame
	return e.layout, true
}

func (c *measureCache) put(s string, r Request, width float64, l Layout) {
	if c.entries == nil {
		c.entries = make(map[measureKey]*measureEntry)
	}
	if len(c.entries) >= maxMeasureEntries {
		return
	}
	var e *measureEntry
	if n := len(c.free); n > 0 {
		e, c.free = c.free[n-1], c.free[:n-1]
	} else {
		e = new(measureEntry)
	}
	// Reuse the evicted entry's slices; Clone of an empty request stays nil.
	e.families = append(e.families[:0], r.Families...)
	e.variations = append(e.variations[:0], r.Variations...)
	e.layout, e.used = l, c.frame
	c.entries[requestKey(s, r, width)] = e
}

func (c *measureCache) endFrame() {
	for k, e := range c.entries {
		if c.frame-e.used >= 2 {
			delete(c.entries, k)
			// Callers may still hold e.layout; only the entry is recycled.
			e.layout = Layout{}
			c.free = append(c.free, e)
		}
	}
	c.frame++
}

// Clone deep-copies lines, runs and glyphs so positions can be adjusted
// without changing a cached measurement.
func (l Layout) Clone() Layout {
	lines := make([]Line, len(l.Lines))
	for i, line := range l.Lines {
		runs := make([]Run, len(line.Runs))
		for j, run := range line.Runs {
			run.Glyphs = slices.Clone(run.Glyphs)
			runs[j] = run
		}
		line.Runs = runs
		lines[i] = line
	}
	l.Lines = lines
	return l
}
