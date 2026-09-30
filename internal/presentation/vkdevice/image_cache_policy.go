package vkdevice

import (
	"fmt"
	"image"
	"reflect"
)

const maxImageTextures = 64
const imageDescriptorSlots = maxImageTextures + 1 // one fallback plus the 64-texture budget
const maxImageBytes = 64 << 20
const maxImageDimension = 4096

type imageEntry struct {
	key              image.Image
	slot             int
	bytes            int
	used, lastSerial uint64
	retiring         bool
}

// imageCachePolicy tracks frame use separately from completed fence serials.
// The 64 image descriptor sets plus one fallback set are allocated once,
// without FREE_DESCRIPTOR_SET. A
// retired texture keeps its slot until its last sampling fence completes;
// only then may UpdateDescriptorSets overwrite the recycled set. The pool is
// destroyed once at shutdown. This avoids requiring FreeDescriptorSets.
type imageCachePolicy struct {
	entries          map[image.Image]*imageEntry
	free             []int
	retired          []*imageEntry
	bytes            int
	frame, completed uint64
}

func newImageCachePolicy() *imageCachePolicy {
	p := &imageCachePolicy{entries: make(map[image.Image]*imageEntry)}
	// Slot 0 is permanently reserved for the valid set 1 fallback.
	for i := imageDescriptorSlots - 1; i >= 1; i-- {
		p.free = append(p.free, i)
	}
	return p
}

func (p *imageCachePolicy) begin(serial, completed uint64) []*imageEntry {
	p.frame = serial
	if completed > p.completed {
		p.completed = completed
	}
	return p.collect()
}

func (p *imageCachePolicy) collect() []*imageEntry {
	var destroyed []*imageEntry
	keep := p.retired[:0]
	for _, e := range p.retired {
		if e.lastSerial > p.completed {
			keep = append(keep, e)
			continue
		}
		p.free = append(p.free, e.slot)
		p.bytes -= e.bytes
		destroyed = append(destroyed, e)
	}
	p.retired = keep
	return destroyed
}

func (p *imageCachePolicy) reserve(key image.Image) (*imageEntry, []*imageEntry, error) {
	if key == nil || !reflect.TypeOf(key).Comparable() {
		return nil, nil, fmt.Errorf("image cache: image identity must be a comparable non-nil image.Image value")
	}
	bounds := key.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= 0 || h <= 0 || w > maxImageDimension || h > maxImageDimension {
		return nil, nil, fmt.Errorf("image cache: invalid dimensions %dx%d (maximum %d)", w, h, maxImageDimension)
	}
	if e := p.entries[key]; e != nil {
		e.used, e.lastSerial = p.frame, p.frame
		return e, nil, nil
	}
	size := w * h * 4
	if size > maxImageBytes {
		return nil, nil, fmt.Errorf("image cache: %d bytes exceeds %d-byte budget", size, maxImageBytes)
	}
	var destroyed []*imageEntry
	for len(p.free) == 0 || p.bytes+size > maxImageBytes {
		var victim *imageEntry
		for _, e := range p.entries {
			if e.used == p.frame || e.lastSerial > p.completed || (victim != nil && e.used >= victim.used) {
				continue
			}
			victim = e
		}
		if victim == nil {
			return nil, destroyed, fmt.Errorf("image cache: 64 textures/64 MiB budget exhausted by images used this frame or awaiting fences")
		}
		delete(p.entries, victim.key)
		victim.retiring = true
		p.retired = append(p.retired, victim)
		destroyed = append(destroyed, p.collect()...)
	}
	slot := p.free[len(p.free)-1]
	p.free = p.free[:len(p.free)-1]
	e := &imageEntry{key: key, slot: slot, bytes: size, used: p.frame, lastSerial: p.frame}
	p.entries[key] = e
	p.bytes += size
	return e, destroyed, nil
}

// drop forgets an entry that no submitted work has used and frees its slot.
func (p *imageCachePolicy) drop(e *imageEntry) {
	if p.entries[e.key] != e {
		return
	}
	delete(p.entries, e.key)
	p.free = append(p.free, e.slot)
	p.bytes -= e.bytes
}
