//go:build linux

package session

import (
	"errors"
	"testing"

	"github.com/bnema/nefergui/internal/presentation/buffers"
	"github.com/bnema/nefergui/internal/presentation/vkdevice"
)

// No GPU needed: the old wait must remain owned until a later readiness check.
func TestInstallReleaseWaitDefersUnreadySubmission(t *testing.T) {
	old := &vkdevice.BinaryFence{}
	next := &vkdevice.BinaryFence{}
	slot := &Slot{Wait: old, WaitInFlight: true}
	checked := 0
	installed, err := installReleaseWait(slot, next, func() (bool, error) { checked++; return false, nil })
	if err != nil || installed || checked != 1 || slot.Wait != old || slot.PendingWait != next || !slot.WaitInFlight {
		t.Fatalf("premature replacement: installed=%v err=%v slot=%+v", installed, err, slot)
	}
	// Once ready, replacement clears the old (zero-handle) fake without waiting.
	installed, err = installReleaseWait(slot, next, func() (bool, error) { checked++; return true, nil })
	if err != nil || !installed || checked != 2 || slot.Wait != next || slot.PendingWait != nil || slot.WaitInFlight {
		t.Fatalf("failed replacement: installed=%v err=%v slot=%+v", installed, err, slot)
	}
}

// A resize while a release wait is outstanding leaves old-generation slots to
// retire: finishPending must not touch them, even if their frame is not ready.
func TestTargetFinishPendingSkipsOldGenerations(t *testing.T) {
	pool, _ := buffers.New(1)
	old := pool.Buffers[0]
	if err := pool.Resize(1); err != nil {
		t.Fatal(err)
	}
	// A nil Frame would panic if the old slot were examined.
	tg := &Target{Pool: pool, Slots: map[uint64]*Slot{old.ID: {Buffer: old, PendingWait: &vkdevice.BinaryFence{}}}}
	pending, err := tg.finishPending()
	if err != nil || pending {
		t.Fatalf("pending=%v err=%v", pending, err)
	}
	if old.State != buffers.Available || tg.Slots[old.ID].PendingWait == nil {
		t.Fatal("old-generation slot was modified")
	}
}

func TestTargetBrokenIsSticky(t *testing.T) {
	tg := &Target{wantW: 1, wantH: 1}
	boom := errors.New("boom")
	first := tg.fail(boom)
	if _, err := tg.Draw(nil, 1, &Output{}); !errors.Is(err, boom) || err != first {
		t.Fatalf("Draw after failure: %v", err)
	}
}
