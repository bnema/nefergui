//go:build linux

package session

import (
	"image"
	"path/filepath"
	"testing"
	"time"
)

func TestReadbackBudgetAndBoundedWorker(t *testing.T) {
	s := &Session{readbacks: make(chan readbackJob, readbackQueueSize)}
	for i := 0; i < maxDebugPNGs; i++ {
		if !s.wantReadback() {
			t.Fatalf("budget ended at %d", i)
		}
		s.debugFrames++
	}
	if s.wantReadback() {
		t.Fatal("readback copy scheduled after limit")
	}
	queue := make(chan readbackJob, readbackQueueSize)
	for i := 0; i < readbackQueueSize; i++ {
		if !offerReadback(queue, readbackJob{uint64(i), image.NewNRGBA(image.Rect(0, 0, 1, 1))}) {
			t.Fatal("queue rejected before full")
		}
	}
	done := make(chan bool, 1)
	go func() { done <- offerReadback(queue, readbackJob{}) }()
	select {
	case accepted := <-done:
		if accepted {
			t.Fatal("full queue accepted job")
		}
	case <-time.After(time.Second):
		t.Fatal("full queue blocked owner")
	}
	if len(queue) != readbackQueueSize {
		t.Fatalf("queue grew: %d", len(queue))
	}
}

func TestReadbackWorkerCountsWriteErrors(t *testing.T) {
	s := &Session{
		debugDir:     filepath.Join(t.TempDir(), "missing"),
		readbacks:    make(chan readbackJob, readbackQueueSize),
		readbackDone: make(chan struct{}),
		debugWritten: make(chan uint64, maxDebugPNGs),
	}
	go s.readbackWorker()
	s.readbacks <- readbackJob{1, image.NewNRGBA(image.Rect(0, 0, 1, 1))}
	close(s.readbacks)
	<-s.readbackDone
	if s.debugWriteErrors != 1 || s.debugLastWriteErr == "" || len(s.debugWritten) != 0 {
		t.Fatalf("errors=%d last=%q written=%d", s.debugWriteErrors, s.debugLastWriteErr, len(s.debugWritten))
	}
}

// saveReadback is called only after beginReady/retire see Ready(); it has no
// Frame.Wait path, even if a PNG is pending. The budget prevents new copies.
func TestNoReadbackAfterLimit(t *testing.T) {
	s := &Session{readbacks: make(chan readbackJob, readbackQueueSize), debugFrames: maxDebugPNGs}
	if s.wantReadback() {
		t.Fatal("readback enabled at limit")
	}
	s.debugFrames = 0
	s.Slots = map[uint64]*Slot{1: {ReadbackPending: true}, 2: {ReadbackPending: true}}
	for i := uint64(0); i < maxDebugPNGs-2; i++ {
		s.debugFrames++
	}
	if s.wantReadback() {
		t.Fatal("pending readbacks ignored")
	}
}
