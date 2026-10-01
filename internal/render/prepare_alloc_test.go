package render

import (
	"testing"

	"github.com/bnema/nefergui/internal/css"
	"github.com/bnema/nefergui/internal/layout"
)

func rectCommands(n int) []layout.Command {
	cmds := make([]layout.Command, n)
	for i := range cmds {
		cmds[i] = layout.Command{Op: "rect", Rect: layout.Rect{X: 1, Y: float64(i), W: 10, H: 1}, Color: css.Color{A: 1}, Opacity: 1}
	}
	return cmds
}

func TestQuadCapacityIsSufficientAndExactWithoutCulling(t *testing.T) {
	glyphs := glyphCommand(t, "office")
	n := 0
	for _, r := range glyphs.Runs {
		n += len(r.Glyphs)
	}
	cmds := []layout.Command{{Op: "clip-push", Rect: layout.Rect{W: 200, H: 100}}, rectCommands(1)[0], glyphs, {Op: "clip-pop"}}
	if got := quadCapacity(cmds); got != n+1 {
		t.Fatalf("capacity %d, want %d", got, n+1)
	}
	p, err := NewPreparer(256, 2)
	if err != nil {
		t.Fatal(err)
	}
	f, err := p.Prepare(cmds, 1, 200, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Quads) == 0 || len(f.Quads) > n+1 || cap(f.Quads) != n+1 {
		t.Fatalf("len=%d cap=%d, want cap %d", len(f.Quads), cap(f.Quads), n+1)
	}
}

// Frames are detached: a later Prepare must not alias or overwrite an earlier
// frame that is still queued, blocked or superseded.
func TestSuccessiveFramesAreDetached(t *testing.T) {
	p, err := NewPreparer(256, 2)
	if err != nil {
		t.Fatal(err)
	}
	a, err := p.Prepare(rectCommands(4), 1, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	want := append([]Quad(nil), a.Quads...)
	b, err := p.Prepare(rectCommands(3), 2, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Quads) != 4 || len(b.Quads) != 3 || &a.Quads[0] == &b.Quads[0] {
		t.Fatalf("frames alias: %d %d", len(a.Quads), len(b.Quads))
	}
	for i := range want {
		if a.Quads[i] != want[i] {
			t.Fatalf("earlier frame quad %d overwritten", i)
		}
	}
	// Appending to a returned frame must not write into another frame.
	b.Quads = append(b.Quads[:len(b.Quads):len(b.Quads)], Quad{Op: "x"})
	if a.Quads[0] != want[0] {
		t.Fatal("append leaked into earlier frame")
	}
}

func TestFailedPrepareReturnsNoPartialFrame(t *testing.T) {
	p, err := NewPreparer(256, 2)
	if err != nil {
		t.Fatal(err)
	}
	cmds := append(rectCommands(8), layout.Command{Op: "bogus"})
	f, err := p.Prepare(cmds, 1, 100, 100)
	if err == nil || f.Quads != nil || f.Uploads != nil {
		t.Fatalf("partial frame on failure: %v %+v", err, f)
	}
	if f, err = p.Prepare(nil, 1, 100, 100); err != nil || f.Quads != nil {
		t.Fatalf("empty frame: %v %+v", err, f)
	}
}

// Preallocation must not disturb owed-upload accounting across frames whose
// quads were sized differently.
func TestPreallocatedFramesKeepUploadDebt(t *testing.T) {
	cmd := glyphCommand(t, "office")
	p, err := NewPreparer(256, 2)
	if err != nil {
		t.Fatal(err)
	}
	first, err := p.Prepare([]layout.Command{cmd}, 1, 200, 100)
	if err != nil || len(first.Uploads) == 0 {
		t.Fatalf("first: %v %d", err, len(first.Uploads))
	}
	// Superseding frame with no glyphs still resends the owed pages.
	second, err := p.Prepare(rectCommands(2), 1, 200, 100)
	if err != nil || len(second.Uploads) == 0 {
		t.Fatalf("debt lost: %v %d", err, len(second.Uploads))
	}
	p.Submitted(first.Uploads)
	if f, err := p.Prepare(rectCommands(2), 1, 200, 100); err != nil || len(f.Uploads) == 0 {
		t.Fatalf("stale ack cleared debt: %v %d", err, len(f.Uploads))
	}
}

// Box-only frames must cost one Quads allocation regardless of command count.
func TestPrepareQuadAllocationsDoNotScaleWithCommands(t *testing.T) {
	p, err := NewPreparer(256, 2)
	if err != nil {
		t.Fatal(err)
	}
	cmds := rectCommands(512)
	allocs := testing.AllocsPerRun(20, func() {
		if _, err := p.Prepare(cmds, 1, 100, 1000); err != nil {
			t.Fatal(err)
		}
	})
	if allocs > 2 {
		t.Fatalf("%v allocs for 512 rects, want a small constant", allocs)
	}
}

// PrepareInto reuses the caller's quads, so a steady frame allocates nothing,
// and stale references from a longer frame are cleared.
func TestPrepareIntoReusesQuads(t *testing.T) {
	p, err := NewPreparer(256, 2)
	if err != nil {
		t.Fatal(err)
	}
	long, err := p.PrepareInto(nil, rectCommands(8), 1, 100, 1000)
	if err != nil {
		t.Fatal(err)
	}
	quads := long.Quads
	short, err := p.PrepareInto(quads, rectCommands(3), 1, 100, 1000)
	if err != nil || len(short.Quads) != 3 || &short.Quads[0] != &quads[0] {
		t.Fatalf("not reused: %v %d", err, len(short.Quads))
	}
	if tail := quads[3:8]; tail[0] != (Quad{}) || tail[4] != (Quad{}) {
		t.Fatal("stale quads kept past the new length")
	}
	cmds := rectCommands(64)
	if allocs := testing.AllocsPerRun(20, func() {
		f, err := p.PrepareInto(quads, cmds, 1, 100, 1000)
		if err != nil {
			t.Fatal(err)
		}
		quads = f.Quads
	}); allocs != 0 {
		t.Fatalf("PrepareInto allocs=%v, want 0", allocs)
	}
	// Mostly culled: the reservation stays large while few quads are visible.
	culled := rectCommands(400)
	if allocs := testing.AllocsPerRun(20, func() {
		f, err := p.PrepareInto(quads, culled, 1, 100, 10)
		if err != nil || len(f.Quads) > 10 {
			t.Fatalf("culled frame: %v %d", err, len(f.Quads))
		}
		quads = f.Quads
	}); allocs != 0 {
		t.Fatalf("culled PrepareInto allocs=%v, want 0", allocs)
	}
	// A much smaller frame drops the past peak buffer.
	f, err := p.PrepareInto(quads, rectCommands(2), 1, 100, 10)
	if err != nil || cap(f.Quads) >= cap(quads) {
		t.Fatalf("peak buffer kept: %v cap %d, was %d", err, cap(f.Quads), cap(quads))
	}
}

func BenchmarkPrepareBoxes(b *testing.B) {
	p, err := NewPreparer(256, 2)
	if err != nil {
		b.Fatal(err)
	}
	cmds := rectCommands(512)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if f, err := p.Prepare(cmds, 1, 100, 1000); err != nil || len(f.Quads) != 512 {
			b.Fatal(err)
		}
	}
}
