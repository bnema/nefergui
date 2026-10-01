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
	spare         *vkdevice.BinaryFence // consumed, unsignaled wait semaphore kept for reuse
	ReleaseHandle uint32
	ReleaseFD     int  // eventfd armed on each commit's release point
	ReleaseExport int  // exported syncobj fd of the release timeline
	Presented     bool // the caller has been told to import this buffer
}

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
		if slot.WaitInFlight {
			slot.recycle(slot.Wait) // submission done: the wait was consumed
		} else {
			slot.Wait.Close() // never waited on: its imported payload is still signaled
		}
	}
	slot.Wait, slot.PendingWait, slot.WaitInFlight = next, nil, false
	return true, nil
}

// recycle keeps a release-wait semaphore for reuse. The caller guarantees the
// submission that waited on it has completed: a wait operation consumes the
// temporarily imported payload and leaves the semaphore unsignaled and idle.
func (s *Slot) recycle(w *vkdevice.BinaryFence) {
	if s.spare != nil {
		w.Close()
		return
	}
	s.spare = w
}

type retiredPipelines struct {
	generation uint64
	list       *vkdevice.Pipeline
}
