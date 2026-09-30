package buffers

import (
	"errors"
	"testing"
)

func committed(t *testing.T, p *Pool) *Buffer {
	t.Helper()
	b, e := p.Begin()
	if e != nil {
		t.Fatal(e)
	}
	acquire, e := p.Acquire(b)
	if e != nil {
		t.Fatal(e)
	}
	release, e := p.Commit(b)
	if e != nil || release <= acquire {
		t.Fatalf("points %d %d %v", acquire, release, e)
	}
	if e = p.Own(b); e != nil {
		t.Fatal(e)
	}
	return b
}
func TestLatestReleasePoint(t *testing.T) {
	p, e := New(1)
	if e != nil {
		t.Fatal(e)
	}
	b := committed(t, p)
	old := b.ReleasePoint
	if e = p.Release(b); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Begin(); !errors.Is(e, ErrNoBuffer) {
		t.Fatalf("wl release authorized reuse: %v", e)
	}
	if e = p.Signal(b, old+1); !errors.Is(e, ErrTransition) {
		t.Fatalf("future point: %v", e)
	}
	if e = p.Reuse(b, true); !errors.Is(e, ErrTransition) {
		t.Fatalf("premature reuse: %v", e)
	}
	if e = p.Signal(b, old); e != nil {
		t.Fatal(e)
	}
	if e = p.Reuse(b, false); !errors.Is(e, ErrTransition) {
		t.Fatalf("missing GPU wait: %v", e)
	}
	if e = p.Reuse(b, true); e != nil {
		t.Fatal(e)
	}
	b = committed(t, p)
	if b.ReleasePoint <= old {
		t.Fatal("release point not advanced")
	}
	if e = p.Signal(b, old); !errors.Is(e, ErrTransition) {
		t.Fatalf("old point accepted: %v", e)
	}
	if _, e = p.Begin(); !errors.Is(e, ErrNoBuffer) {
		t.Fatalf("old point allowed reuse: %v", e)
	}
	if e = p.Signal(b, b.ReleasePoint); e != nil {
		t.Fatal(e)
	}
	// A late wl_buffer.release is diagnostic and cannot re-lock a signaled image.
	if e = p.Release(b); e != nil {
		t.Fatal(e)
	}
	if e = p.Reuse(b, true); e != nil {
		t.Fatal(e)
	}
}
func TestThreeBuffersOutOfOrderAndResize(t *testing.T) {
	p, e := New(3)
	if e != nil {
		t.Fatal(e)
	}
	a, b, c := committed(t, p), committed(t, p), committed(t, p)
	if _, e = p.Begin(); !errors.Is(e, ErrNoBuffer) {
		t.Fatal(e)
	}
	if e = p.Signal(c, c.ReleasePoint); e != nil {
		t.Fatal(e)
	}
	if e = p.Reuse(c, true); e != nil {
		t.Fatal(e)
	}
	if got, e := p.Begin(); e != nil || got != c {
		t.Fatalf("out-of-order point: %v %v", got, e)
	}
	if e = p.Resize(3); e != nil {
		t.Fatal(e)
	}
	if e = p.Signal(b, b.ReleasePoint); e != nil {
		t.Fatal(e)
	}
	if !p.Retirable(b) || p.Retirable(a) || p.Retirable(c) {
		t.Fatal("retired generation ownership")
	}
	if e = p.Reuse(b, true); !errors.Is(e, ErrTransition) {
		t.Fatalf("reused retired: %v", e)
	}
	for range 3 {
		got, e := p.Begin()
		if e != nil || got.Generation != 2 {
			t.Fatalf("new generation: %v %v", got, e)
		}
	}
	if _, e = p.Begin(); !errors.Is(e, ErrNoBuffer) {
		t.Fatal(e)
	}
	if e = p.Remove(a); !errors.Is(e, ErrTransition) {
		t.Fatal(e)
	}
	if e = p.Remove(b); e != nil {
		t.Fatal(e)
	}
}
func TestInvalidTransitions(t *testing.T) {
	p, _ := New(1)
	b := p.Buffers[0]
	if _, e := p.Commit(b); !errors.Is(e, ErrTransition) {
		t.Fatal(e)
	}
	if e := p.Signal(b, 0); !errors.Is(e, ErrTransition) {
		t.Fatal(e)
	}
	if e := p.Release(&Buffer{}); !errors.Is(e, ErrTransition) {
		t.Fatal(e)
	}
	b = committed(t, p)
	if e := p.Signal(b, b.ReleasePoint); e != nil {
		t.Fatal(e)
	}
	if e := p.Signal(b, b.ReleasePoint); !errors.Is(e, ErrTransition) {
		t.Fatal(e)
	}
}
