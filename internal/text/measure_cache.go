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
	c.entries[requestKey(s, r, width)] = &measureEntry{families: slices.Clone(r.Families), variations: slices.Clone(r.Variations), layout: l, used: c.frame}
}

func (c *measureCache) endFrame() {
	for k, e := range c.entries {
		if c.frame-e.used >= 2 {
			delete(c.entries, k)
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
