package text

import (
	"fmt"
	"testing"
)

func TestMeasureCacheHitsKeysAndEviction(t *testing.T) {
	e := NewEngine(fixture(t))
	r := Request{Families: []string{"Noto Sans"}, Size: 16}
	first, err := e.Measure("Bonjour Ada", r, 0)
	if err != nil {
		t.Fatal(err)
	}
	allocs := testing.AllocsPerRun(5, func() {
		if _, err := e.Measure("Bonjour Ada", r, 0); err != nil {
			panic(err)
		}
	})
	if allocs > 1 {
		t.Fatalf("cached Measure allocates %.0f times", allocs)
	}
	// Any request field, the text or the width must miss.
	for name, other := range map[string]func() (Layout, error){
		"size": func() (Layout, error) { return e.Measure("Bonjour Ada", Request{Families: r.Families, Size: 20}, 0) },
		"family": func() (Layout, error) {
			return e.Measure("Bonjour Ada", Request{Families: []string{"serif"}, Size: 16}, 0)
		},
		"weight": func() (Layout, error) {
			return e.Measure("Bonjour Ada", Request{Families: r.Families, Size: 16, Weight: 700}, 0)
		},
		"width": func() (Layout, error) { return e.Measure("Bonjour Ada", r, 40) },
		"text":  func() (Layout, error) { return e.Measure("Bonjour", r, 0) },
	} {
		l, err := other()
		if err != nil {
			t.Fatal(name, err)
		}
		if l.Width == first.Width && len(l.Lines) == len(first.Lines) && name != "family" && name != "weight" {
			t.Errorf("%s: expected a different measurement", name)
		}
	}
	if n := len(e.cache.entries); n != 6 {
		t.Fatalf("entries=%d, want 6", n)
	}
	// Entries unused for two frames are evicted; used ones stay.
	e.EndFrame()
	if _, err := e.Measure("Bonjour Ada", r, 0); err != nil {
		t.Fatal(err)
	}
	e.EndFrame()
	e.EndFrame()
	if n := len(e.cache.entries); n != 1 {
		t.Fatalf("after eviction entries=%d, want 1", n)
	}
}

func TestMeasureCacheBoundAndCloneIsolation(t *testing.T) {
	e := NewEngine(fixture(t))
	r := Request{Families: []string{"Noto Sans"}, Size: 16}
	for i := 0; i < maxMeasureEntries+10; i++ {
		if _, err := e.Measure(fmt.Sprint(i), r, 0); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(e.cache.entries); n != maxMeasureEntries {
		t.Fatalf("entries=%d, want bound %d", n, maxMeasureEntries)
	}
	cached, err := e.Measure("0", r, 0)
	if err != nil || len(cached.Lines) == 0 || len(cached.Lines[0].Runs) == 0 || len(cached.Lines[0].Runs[0].Glyphs) == 0 {
		t.Fatalf("measure: %+v %v", cached, err)
	}
	y := cached.Lines[0].Runs[0].Glyphs[0].Y
	c := cached.Clone()
	c.Lines[0].Baseline += 10
	c.Lines[0].Runs[0].Y += 10
	c.Lines[0].Runs[0].Glyphs[0].Y += 10
	again, _ := e.Measure("0", r, 0)
	if again.Lines[0].Runs[0].Glyphs[0].Y != y || again.Lines[0].Baseline != cached.Lines[0].Baseline {
		t.Fatal("Clone shares memory with the cache")
	}
}
