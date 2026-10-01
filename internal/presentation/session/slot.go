//go:build linux

// Package session is the Wayland-free presentation core: Vulkan rendering into
// DMA-BUF buffers, DRM syncobj timelines and buffer ownership.
package session

import (
	"fmt"

	"github.com/bnema/nefergui/internal/presentation/buffers"
	"github.com/bnema/nefergui/internal/presentation/vkdevice"
)

// Slot is the GPU side of one pool buffer. Descriptors are -1 when unset.
type Slot struct {
	Buffer        *buffers.Buffer
	Image         *vkdevice.Image
	Frame         *vkdevice.Frame
	First         bool
	Wait          *vkdevice.BinaryFence
	PendingWait   *vkdevice.BinaryFence
	WaitInFlight  bool
	ReleaseHandle uint32
	ReleaseFD     int  // eventfd armed on each commit's release point
	ReleaseExport int  // exported syncobj fd of the release timeline
	Presented     bool // the caller has been told to import this buffer
}

func (slot *Slot) closeImage() { slot.Image.Close() }

// beginReady does not change Pool ownership until the previous GPU submission
// has completed. An unready available buffer is retried on a bounded wakeup.
func beginReady(pool *buffers.Pool, slots map[uint64]*Slot, ready func(*Slot) (bool, error)) (*buffers.Buffer, *Slot, bool, error) {
	waiting := false
	for _, b := range pool.Buffers {
		if b.Generation != pool.Generation || b.State != buffers.Available {
			continue
		}
		slot := slots[b.ID]
		if slot == nil {
			return nil, nil, false, fmt.Errorf("missing slot %d", b.ID)
		}
		ok, err := ready(slot)
		if err != nil {
			return nil, nil, false, err
		}
		if !ok {
			waiting = true
			continue
		}
		b.State = buffers.Rendering
		b.WlReleased, b.PointSignaled = false, false
		return b, slot, false, nil
	}
	return nil, nil, waiting, nil
}

// installReleaseWait holds the new semaphore until the old submission is done.
// Only the state owner calls it. On an unready fence it leaves Pool Released.
func installReleaseWait(slot *Slot, next *vkdevice.BinaryFence, ready func() (bool, error)) (bool, error) {
	if slot.Wait != nil && slot.WaitInFlight {
		ok, err := ready()
		if err != nil {
			return false, err
		}
		if !ok {
			slot.PendingWait = next
			return false, nil
		}
	}
	if slot.Wait != nil {
		slot.Wait.Close()
	}
	slot.Wait, slot.PendingWait, slot.WaitInFlight = next, nil, false
	return true, nil
}

type retiredPipelines struct {
	generation uint64
	list       *vkdevice.Pipeline
}
