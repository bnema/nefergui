//go:build linux

package session

import (
	"testing"

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
