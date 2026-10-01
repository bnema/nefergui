// Package buffers models buffer ownership between the renderer and the
// compositor without GPU dependencies. The caller serializes all events.
package buffers

import (
	"errors"
	"fmt"
)

var ErrNoBuffer = errors.New("no available buffer")
var ErrTransition = errors.New("invalid buffer transition")

type State string

const (
	Available       State = "available"
	Rendering       State = "rendering"
	Acquired        State = "acquire_exported"
	Committed       State = "committed"
	CompositorOwned State = "compositor_owned"
	Released        State = "release_point_signaled"
)

// Buffer is an immutable identity and mutable ownership record. On an explicit
// sync surface wl_buffer.release is optional; only the latest release point of
// this buffer's own timeline authorizes reuse.
type Buffer struct {
	ID, Generation             uint64
	State                      State
	AcquirePoint, ReleasePoint uint64
	WlReleased, PointSignaled  bool
}

type Pool struct {
	Generation uint64
	NextPoint  uint64
	Buffers    []*Buffer
}

func New(count int) (*Pool, error) {
	if count < 1 {
		return nil, fmt.Errorf("buffer count must be positive")
	}
	p := &Pool{Generation: 1}
	for i := 0; i < count; i++ {
		p.Buffers = append(p.Buffers, &Buffer{ID: uint64(i + 1), Generation: 1, State: Available})
	}
	return p, nil
}

// Begin skips rendering when every buffer is busy. Retired generations are
// never selected, even if their release events complete later.
func (p *Pool) Begin() (*Buffer, error) {
	for _, b := range p.Buffers {
		if b.Generation == p.Generation && b.State == Available {
			b.State = Rendering
			b.WlReleased, b.PointSignaled = false, false
			return b, nil
		}
	}
	return nil, ErrNoBuffer
}

func (p *Pool) Acquire(b *Buffer) (uint64, error) {
	if !p.owns(b) || b.State != Rendering || b.Generation != p.Generation {
		return 0, ErrTransition
	}
	p.NextPoint++
	b.AcquirePoint = p.NextPoint
	b.State = Acquired
	return b.AcquirePoint, nil
}

func (p *Pool) Commit(b *Buffer) (uint64, error) {
	if !p.owns(b) || b.State != Acquired || b.Generation != p.Generation {
		return 0, ErrTransition
	}
	p.NextPoint++
	b.ReleasePoint = p.NextPoint
	b.PointSignaled = false
	b.WlReleased = false
	b.State = Committed
	return b.ReleasePoint, nil
}

func (p *Pool) Own(b *Buffer) error {
	if !p.owns(b) || b.State != Committed {
		return ErrTransition
	}
	b.State = CompositorOwned
	return nil
}

// Release records an optional wl_buffer.release event. It never authorizes
// reuse, and a late event from a prior commit must not modify the new commit.
func (p *Pool) Release(b *Buffer) error {
	if !p.owns(b) {
		return ErrTransition
	}
	if b.State == Committed || b.State == CompositorOwned {
		b.WlReleased = true
	}
	return nil
}

func (p *Pool) Signal(b *Buffer, point uint64) error {
	if !p.owns(b) || (b.State != Committed && b.State != CompositorOwned) || b.PointSignaled || point != b.ReleasePoint {
		return ErrTransition
	}
	b.PointSignaled = true
	b.State = Released
	return nil
}

// Reuse requires the caller to have arranged a GPU wait on the exported
// release-point sync_file before submitting new writes to this image.
func (p *Pool) Reuse(b *Buffer, gpuWaitInstalled bool) error {
	if !p.owns(b) || b.Generation != p.Generation || b.State != Released || !gpuWaitInstalled {
		return ErrTransition
	}
	b.State = Available
	return nil
}

// Resize installs an entirely new generation. Old images remain tracked until
// their latest release point signals; even Available old images are retired.
func (p *Pool) Resize(count int) error {
	if count < 1 {
		return fmt.Errorf("buffer count must be positive")
	}
	p.Generation++
	var next uint64
	for _, b := range p.Buffers {
		if b.ID > next {
			next = b.ID
		}
	}
	for i := 0; i < count; i++ {
		next++
		p.Buffers = append(p.Buffers, &Buffer{ID: next, Generation: p.Generation, State: Available})
	}
	return nil
}

// Retirable reports when the compositor and GPU no longer own a retired image.
// An abandoned Rendering or Acquired image still needs the caller to finish or
// wait for its Vulkan submission before destroying the GPU allocation.
func (p *Pool) Retirable(b *Buffer) bool {
	return p.owns(b) && b.Generation < p.Generation && (b.State == Available || b.State == Released)
}

func (p *Pool) Remove(b *Buffer) error {
	if !p.Retirable(b) {
		return ErrTransition
	}
	for i, other := range p.Buffers {
		if other == b {
			p.Buffers = append(p.Buffers[:i], p.Buffers[i+1:]...)
			return nil
		}
	}
	return ErrTransition
}

func (p *Pool) owns(b *Buffer) bool {
	if b == nil {
		return false
	}
	for _, other := range p.Buffers {
		if other == b {
			return true
		}
	}
	return false
}
