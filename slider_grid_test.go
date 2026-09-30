package nefergui

import (
	"math"
	"testing"

	"github.com/bnema/nefergui/internal/layout"
)

func TestSliderGridDecimalAndExtremeRanges(t *testing.T) {
	for _, tc := range []struct {
		name              string
		v, min, max, step float64
		want              float64
	}{
		{"decimal last stop", 0.3, 0, 0.3, 0.1, 0.30000000000000004},
		{"decimal end", 1, 0, 1, 0.1, 1},
		{"partial final step", 10, 0, 10, 3, 9},
		{"overflowing span clamps only", 1e308, -math.MaxFloat64, math.MaxFloat64, 1e-300, 1e308},
		{"sub-precision grid clamps only", 0.5, 0, 1, 1e-20, 0.5},
	} {
		if got := sliderGrid(tc.v, tc.min, tc.max, tc.step); math.Abs(got-tc.want) > 1e-12*math.Max(1, math.Abs(tc.want)) {
			t.Errorf("%s: sliderGrid = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestSliderGridAndNonfiniteValues(t *testing.T) {
	r := newRuntime()
	v := math.NaN()
	changed := false
	view := func(f *Frame) { changed = f.Root().Slider("level", &v, 1, 8, 2).Changed() }
	r.Build(view)
	if !changed || v != 1 {
		t.Fatalf("NaN normalization: changed=%v v=%v", changed, v)
	}
	r.Redraw()
	r.Build(view)
	if changed {
		t.Fatal("unchanged slider reported change")
	}
	v = math.Inf(1)
	r.Redraw()
	r.Build(view)
	if !changed || v != 1 {
		t.Fatalf("Inf normalization: %v %v", changed, v)
	}
	v = 4.1
	r.Redraw()
	r.Build(view)
	if !changed || v != 5 {
		t.Fatalf("off-grid normalization: %v %v", changed, v)
	}
	r.Queue(inputEvent{Target: r.Target(0), Kind: "slider-key", Text: "right"})
	r.Build(view)
	if !changed || v != 7 {
		t.Fatalf("key step: %v %v", changed, v)
	}
	r.Queue(inputEvent{Target: r.Target(0), Kind: "slider-key", Text: "end"})
	r.Build(view)
	if changed || v != 7 {
		t.Fatalf("end must remain on last grid stop: %v %v", changed, v)
	}
	r.Queue(inputEvent{Target: r.Target(0), Kind: "slider-pointer", X: 80, Track: layout.Rect{X: 0, W: 100}})
	r.Build(view)
	if changed || v != 7 {
		t.Fatalf("drag grid: %v %v", changed, v)
	}
	r.Queue(inputEvent{Target: r.Target(0), Kind: "slider-pointer", X: 35, Track: layout.Rect{X: 0, W: 100}})
	r.Build(view)
	if !changed || v != 3 {
		t.Fatalf("drag quantization: %v %v", changed, v)
	}
}
