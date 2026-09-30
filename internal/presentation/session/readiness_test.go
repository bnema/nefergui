//go:build linux

package session

import (
	"github.com/bnema/nefergui/internal/presentation/buffers"
	"testing"
)

func TestBeginReadySkipsUnfinishedGPU(t *testing.T) {
	pool, _ := buffers.New(2)
	slots := map[uint64]*Slot{1: {}, 2: {}}
	calls := 0
	check := func(slot *Slot) (bool, error) { calls++; return slot == slots[2], nil }
	b, slot, waiting, err := beginReady(pool, slots, check)
	if err != nil || b != pool.Buffers[1] || slot != slots[2] || waiting || calls != 2 {
		t.Fatalf("selection b=%v slot=%v waiting=%v calls=%d err=%v", b, slot, waiting, calls, err)
	}
	if pool.Buffers[0].State != buffers.Available || pool.Buffers[1].State != buffers.Rendering {
		t.Fatalf("states: %v", pool.Buffers)
	}
	pool.Buffers[1].State = buffers.CompositorOwned
	b, _, waiting, err = beginReady(pool, slots, func(*Slot) (bool, error) { return false, nil })
	if err != nil || b != nil || !waiting || pool.Buffers[0].State != buffers.Available {
		t.Fatalf("unready mutated pool: %v %v %v", b, waiting, err)
	}
	b, _, waiting, err = beginReady(pool, slots, func(*Slot) (bool, error) { return true, nil })
	if err != nil || b != pool.Buffers[0] || waiting {
		t.Fatalf("retry: b=%v waiting=%v err=%v", b, waiting, err)
	}
}
