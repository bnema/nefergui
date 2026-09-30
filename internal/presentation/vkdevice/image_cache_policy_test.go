package vkdevice

import (
	"image"
	"image/color"
	"strings"
	"testing"
)

type nonComparableImage struct{ data []byte }

func (n nonComparableImage) ColorModel() color.Model { return color.NRGBAModel }
func (n nonComparableImage) Bounds() image.Rectangle { return image.Rect(0, 0, 1, 1) }
func (n nonComparableImage) At(x, y int) color.Color { return color.NRGBA{} }

func TestImageCacheBudgetAndFenceReuse(t *testing.T) {
	p := newImageCachePolicy()
	p.begin(1, 0)
	images := make([]*image.RGBA, 64)
	for i := range images {
		images[i] = image.NewRGBA(image.Rect(0, 0, 1, 1))
		if _, _, err := p.reserve(images[i]); err != nil {
			t.Fatal(err)
		}
	}
	fresh := image.NewRGBA(image.Rect(0, 0, 1, 1))
	if _, _, err := p.reserve(fresh); err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("expected pinned budget: %v", err)
	}
	p.begin(2, 0)
	if _, _, err := p.reserve(fresh); err == nil || !strings.Contains(err.Error(), "budget") {
		t.Fatalf("expected in-flight budget: %v", err)
	}
	if len(p.free) != 0 || len(p.retired) != 0 {
		t.Fatalf("slot retired before fence: %+v", p)
	}
	p.begin(3, 1)
	entry, destroyed, err := p.reserve(fresh)
	if err != nil || entry == nil || len(destroyed) != 1 || entry.slot != destroyed[0].slot {
		t.Fatalf("slot reuse after fence: %+v %v", entry, err)
	}
	if _, _, err := p.reserve(nonComparableImage{data: []byte{1}}); err == nil || !strings.Contains(err.Error(), "comparable") {
		t.Fatalf("expected identity error: %v", err)
	}
	if _, _, err := p.reserve(image.NewRGBA(image.Rect(0, 0, 4097, 1))); err == nil || !strings.Contains(err.Error(), "maximum") {
		t.Fatalf("expected size error: %v", err)
	}
}

func TestImageCacheLRUAndBytes(t *testing.T) {
	p := newImageCachePolicy()
	old := image.NewRGBA(image.Rect(0, 0, 2048, 2048))
	other := image.NewRGBA(image.Rect(0, 0, 2048, 2048))
	p.begin(1, 0)
	p.reserve(old)
	p.begin(2, 1)
	p.reserve(other)
	p.begin(3, 2)
	p.reserve(other)
	newest := image.NewRGBA(image.Rect(0, 0, 4096, 3072))
	entry, destroyed, err := p.reserve(newest)
	if err != nil || entry == nil || len(destroyed) == 0 || destroyed[0].key != old || p.bytes > maxImageBytes {
		t.Fatalf("LRU: entry=%v evicted=%v err=%v bytes=%d", entry, destroyed, err, p.bytes)
	}
}

// A frame whose resolution fails must not stall later fence completion or
// leak the slots it reserved.
func TestImageCacheAbortCompletesSerialAndFreesSlots(t *testing.T) {
	c := &imageCache{policy: newImageCachePolicy(), textures: make(map[*imageEntry]*gpuImage), done: make(map[uint64]bool), inflight: make(map[*Frame]struct{})}
	free := len(c.policy.free)
	failed := &Frame{}
	c.serial++
	failed.imageSerial = c.serial
	c.policy.begin(c.serial, c.policy.completed)
	e, _, err := c.policy.reserve(image.NewRGBA(image.Rect(0, 0, 1, 1)))
	if err != nil {
		t.Fatal(err)
	}
	failed.imageCopies = append(failed.imageCopies, imageCopy{entry: e})
	c.abort(failed)
	if len(c.policy.free) != free || c.policy.bytes != 0 || len(c.policy.entries) != 0 {
		t.Fatalf("abort leaked reservation: free=%d bytes=%d entries=%d", len(c.policy.free), c.policy.bytes, len(c.policy.entries))
	}
	c.serial++
	c.policy.begin(c.serial, c.policy.completed)
	c.complete(c.serial)
	if c.policy.completed != c.serial {
		t.Fatalf("completion stalled at %d, want %d", c.policy.completed, c.serial)
	}
}

// After a submitted list frame settles, a failure on the same slot (e.g. a
// rect submission) must not destroy textures that submitted work used.
func TestSettledFrameDoesNotAbortSubmittedTextures(t *testing.T) {
	c := &imageCache{policy: newImageCachePolicy(), textures: make(map[*imageEntry]*gpuImage), done: make(map[uint64]bool), inflight: make(map[*Frame]struct{})}
	f := &Frame{device: &Device{images: c}}
	c.serial++
	f.imageSerial = c.serial
	c.policy.begin(c.serial, c.policy.completed)
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	e, _, err := c.policy.reserve(img)
	if err != nil {
		t.Fatal(err)
	}
	f.imageCopies = append(f.imageCopies, imageCopy{entry: e})
	f.submitted = true
	f.settleImages()
	if f.imageSerial != 0 || len(f.imageCopies) != 0 || c.policy.completed != 1 {
		t.Fatalf("settle: serial=%d copies=%d completed=%d", f.imageSerial, len(f.imageCopies), c.policy.completed)
	}
	// Close before submit (settle on an unsubmitted frame) frees new entries.
	c.serial++
	unsent := &Frame{device: f.device, imageSerial: c.serial}
	c.policy.begin(c.serial, c.policy.completed)
	fresh := image.NewRGBA(image.Rect(0, 0, 1, 1))
	fe, _, err := c.policy.reserve(fresh)
	if err != nil {
		t.Fatal(err)
	}
	unsent.imageCopies = append(unsent.imageCopies, imageCopy{entry: fe})
	unsent.settleImages()
	if c.policy.entries[fresh] != nil || c.policy.completed != 2 {
		t.Fatalf("unsubmitted settle: entry kept=%v completed=%d", c.policy.entries[fresh] != nil, c.policy.completed)
	}
	// Settling again (as a later failed submission would) must keep the entry.
	f.submitted = false
	f.settleImages()
	if c.policy.entries[img] != e {
		t.Fatal("submitted texture entry dropped")
	}
}

// A frame slot that stays idle (not re-recorded) after its fence signals must
// not stall the contiguous completed serial: later frames would otherwise pin
// retired textures until the 64-slot budget is exhausted.
func TestIdleSlotDoesNotPinTextures(t *testing.T) {
	c := &imageCache{policy: newImageCachePolicy(), textures: make(map[*imageEntry]*gpuImage), done: make(map[uint64]bool), inflight: make(map[*Frame]struct{})}
	d := &Device{images: c}
	idle := &Frame{device: d}
	busy := &Frame{device: d}
	signalled := map[*Frame]bool{}
	ready := func(f *Frame) (bool, error) { return signalled[f], nil }
	submit := func(f *Frame) {
		if err := c.pollInflight(ready); err != nil {
			t.Fatal(err)
		}
		f.settleImages()
		c.serial++
		f.imageSerial = c.serial
		c.policy.begin(c.serial, c.policy.completed)
		if _, _, err := c.policy.reserve(image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
			t.Fatalf("frame %d: %v", c.serial, err)
		}
		f.submitted = true
		c.inflight[f] = struct{}{}
	}
	submit(idle)
	signalled[idle] = true // fence done, but the slot is never recorded again
	for i := 0; i < 3*maxImageTextures; i++ {
		submit(busy)
		signalled[busy] = true
	}
	if c.policy.completed+1 < c.serial {
		t.Fatalf("completion stalled at %d of %d", c.policy.completed, c.serial)
	}
}
