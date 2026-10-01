//go:build linux

package session

import (
	"errors"
	"fmt"
	"image"
	"path/filepath"

	"github.com/bnema/nefergui/internal/layout"
	"github.com/bnema/nefergui/internal/presentation/buffers"
	"github.com/bnema/nefergui/internal/presentation/syncobj"
	"github.com/bnema/nefergui/internal/presentation/vkdevice"
	"github.com/bnema/nefergui/internal/render"
	"golang.org/x/sys/unix"
)

// Plane is one DMA-BUF plane. FD stays owned by the Target.
type Plane struct {
	FD     int
	Offset uint32
	Stride uint32
}

// Timeline is a DRM syncobj timeline the transport must import. FD stays owned
// by the Target and is valid only when the Output says the timeline is new.
type Timeline struct {
	ID uint64
	FD int
}

// Retired names a buffer the transport must stop watching and destroy before
// the next Draw. ReleaseFD closes at that Draw.
type Retired struct {
	Buffer    uint64
	ReleaseFD int
}

// Output describes one presented frame in physical pixels. Slices are reused;
// they are valid until the next Draw.
type Output struct {
	Buffer       uint64
	NewBuffer    bool // import Planes as a new wl_buffer
	Retired      []Retired
	Width        int32
	Height       int32
	FourCC       uint32
	Modifier     uint64
	Planes       [4]Plane
	PlaneCount   int
	Acquire      Timeline // ID 0; FD always valid (persistent)
	Release      Timeline // ID is the buffer; FD valid only with NewTimelines
	NewTimelines bool
	AcquirePoint uint64
	ReleasePoint uint64
	ReleaseFD    int // eventfd readable when ReleasePoint signals; stable per buffer
}

// TargetConfig selects the GPU and the compositor-acceptable formats.
type TargetConfig struct {
	MainDevice  uint64
	Formats     []vkdevice.Format
	Transparent bool
}

// Target is the Wayland-free presentation core: it renders prepared frames
// into DMA-BUF images, tracks buffer ownership with explicit-sync timelines
// and reports release through per-buffer eventfds. It starts no goroutines and
// is used from one owner goroutine.
type Target struct {
	Device   *vkdevice.Device
	Node     *syncobj.Node
	Pool     *buffers.Pool
	Preparer *render.Preparer
	Slots    map[uint64]*Slot

	list             render.ListBuffer
	pipeline         *vkdevice.Pipeline
	retiredPipelines []retiredPipelines
	acquire          uint32
	acquireFD        int
	formats          []vkdevice.Format
	transparent      bool
	selected         vkdevice.Modifier
	wantW, wantH     int32
	width, height    int32
	pending          []Retired // retired since the last Draw output
	reported         []Retired // given to the caller; fds close at the next Draw
	capture          bool
	closed           bool
}

// NewTarget opens the GPU and the render node. It allocates no images.
func NewTarget(cfg TargetConfig) (_ *Target, err error) {
	t := &Target{Slots: make(map[uint64]*Slot), acquireFD: -1, formats: append([]vkdevice.Format(nil), cfg.Formats...), transparent: cfg.Transparent}
	defer func() {
		if err != nil {
			err = errors.Join(err, t.Close())
		}
	}()
	if t.Preparer, err = render.NewPreparer(vkdevice.AtlasSize, vkdevice.AtlasPages); err != nil {
		return nil, err
	}
	if t.Device, err = vkdevice.Open(vkdevice.DeviceIdentity(cfg.MainDevice)); err != nil {
		return nil, err
	}
	if t.Node, err = syncobj.Open(filepath.Join("/dev/dri", fmt.Sprintf("renderD%d", t.Device.Render.Minor))); err != nil {
		return nil, err
	}
	if t.acquire, err = t.Node.Create(); err != nil {
		return nil, err
	}
	if t.acquireFD, err = t.Node.ExportTimeline(t.acquire); err != nil {
		return nil, err
	}
	if t.Pool, err = buffers.New(3); err != nil {
		return nil, err
	}
	// The pool starts with generation 1 buffers but no images yet; the first
	// Draw allocates them for the requested size.
	return t, nil
}

// EnableReadback makes later images capture their output for Readback. Tests only.
func (t *Target) EnableReadback() { t.capture = true }

// Resize sets the physical surface size; images follow lazily at the next Draw.
func (t *Target) Resize(width, height int32) { t.wantW, t.wantH = width, height }

// Size reports the physical size of the current image generation.
func (t *Target) Size() (int32, int32) { return t.width, t.height }

func (t *Target) newGeneration() error {
	mods, err := t.Device.ExportableModifiers()
	if err != nil {
		return err
	}
	if t.selected, err = vkdevice.ChooseModifier(t.formats, t.transparent, mods); err != nil {
		return err
	}
	pipeline, err := t.Device.NewListPipeline(t.wantW, t.wantH)
	if err != nil {
		return err
	}
	if t.pipeline != nil {
		t.retiredPipelines = append(t.retiredPipelines, retiredPipelines{generation: t.Pool.Generation - 1, list: t.pipeline})
	}
	t.pipeline = pipeline
	t.width, t.height = t.wantW, t.wantH
	for _, b := range t.Pool.Buffers {
		if b.Generation != t.Pool.Generation {
			continue
		}
		slot, err := t.newSlot(b)
		if err != nil {
			return err
		}
		t.Slots[b.ID] = slot
	}
	return nil
}

func (t *Target) newSlot(b *buffers.Buffer) (_ *Slot, err error) {
	slot := &Slot{Buffer: b, First: true, ReleaseFD: -1, ReleaseExport: -1}
	defer func() {
		if err != nil {
			t.closeSlot(slot)
		}
	}()
	if slot.Image, err = t.Device.AllocateImage(t.width, t.height, t.selected); err != nil {
		return nil, err
	}
	if slot.Frame, err = t.Device.NewFrame(slot.Image); err != nil {
		return nil, err
	}
	if t.capture {
		if slot.Frame.Readback, err = t.Device.NewReadback(t.width, t.height); err != nil {
			return nil, err
		}
	}
	if slot.ReleaseHandle, err = t.Node.Create(); err != nil {
		return nil, err
	}
	if slot.ReleaseExport, err = t.Node.ExportTimeline(slot.ReleaseHandle); err != nil {
		return nil, err
	}
	if slot.ReleaseFD, err = unix.Eventfd(0, unix.EFD_CLOEXEC|unix.EFD_NONBLOCK); err != nil {
		return nil, err
	}
	return slot, nil
}

// closeSlot frees a slot whose GPU work has completed (or never started).
func (t *Target) closeSlot(slot *Slot) {
	if slot.Frame != nil {
		slot.Frame.Close()
	}
	if slot.PendingWait != nil {
		slot.PendingWait.Close()
	}
	if slot.Wait != nil {
		slot.Wait.Close()
	}
	if slot.Image != nil {
		slot.Image.Close()
	}
	if slot.ReleaseHandle != 0 {
		_ = t.Node.Destroy(slot.ReleaseHandle)
	}
	closeFD(&slot.ReleaseExport)
}

func closeFD(fd *int) {
	if *fd >= 0 {
		_ = unix.Close(*fd)
		*fd = -1
	}
}

// Draw prepares and presents one frame. It returns false, touching no GPU
// state, when no image is ready; the caller retries after Released.
func (t *Target) Draw(display []layout.Command, scale float64, out *Output) (bool, error) {
	if t.closed {
		return false, errors.New("session: target closed")
	}
	if t.wantW <= 0 || t.wantH <= 0 {
		return false, nil
	}
	for _, r := range t.reported {
		_ = unix.Close(r.ReleaseFD)
	}
	t.reported = t.reported[:0]
	if t.width != t.wantW || t.height != t.wantH {
		if t.width != 0 {
			if err := t.Pool.Resize(3); err != nil {
				return false, err
			}
		}
		if err := t.newGeneration(); err != nil {
			return false, err
		}
	}
	if err := t.retire(); err != nil {
		return false, err
	}
	if _, err := t.finishPending(); err != nil {
		return false, err
	}
	b, slot, _, err := beginReady(t.Pool, t.Slots, func(s *Slot) (bool, error) { return s.Frame.Ready() })
	if err != nil || b == nil {
		return false, err
	}
	ok, err := t.submit(b, slot, display, scale, out)
	if !ok || err != nil {
		if b.State == buffers.Rendering {
			b.State = buffers.Available // nothing was submitted
		}
		return false, err
	}
	return true, nil
}

func (t *Target) submit(b *buffers.Buffer, slot *Slot, display []layout.Command, scale float64, out *Output) (bool, error) {
	frame, err := t.Preparer.Prepare(display, scale, int(t.width), int(t.height))
	if err != nil {
		return false, err
	}
	defer t.list.Release()
	instances, batches, stats := t.list.List(frame)
	if len(stats.Skipped) != 0 {
		return false, fmt.Errorf("unsupported list operations: %v", stats.Skipped)
	}
	if slot.Wait != nil && slot.WaitInFlight {
		slot.Wait.Close()
		slot.Wait = nil
		slot.WaitInFlight = false
	}
	if err = slot.Frame.RecordList(slot.Image, t.pipeline, t.width, t.height, instances, frame.Uploads, batches, slot.First, t.capture); err != nil {
		return false, err
	}
	if err = slot.Frame.Submit(slot.Wait); err != nil {
		return false, err
	}
	t.Preparer.Submitted(frame.Uploads)
	if slot.Wait != nil {
		slot.WaitInFlight = true
	}
	slot.First = false
	// From here the buffer is committed to the pipeline; failures are fatal.
	fd, err := slot.Frame.Signal.Export()
	if err != nil {
		return false, fatal(err)
	}
	acquire, err := t.Pool.Acquire(b)
	if err != nil {
		_ = unix.Close(fd)
		return false, fatal(err)
	}
	if err = t.Node.AcquirePoint(fd, t.acquire, acquire); err != nil {
		_ = unix.Close(fd)
		return false, fatal(err)
	}
	release, err := t.Pool.Commit(b)
	if err != nil {
		return false, fatal(err)
	}
	if err = t.Node.EventFD(slot.ReleaseHandle, release, slot.ReleaseFD); err != nil {
		return false, fatal(err)
	}
	if err = t.Pool.Own(b); err != nil {
		return false, fatal(err)
	}
	t.fill(out, slot, acquire, release)
	return true, nil
}

// fatal marks an error after which the buffer cannot be returned to Available.
func fatal(err error) error { return fmt.Errorf("session: presentation pipeline broken: %w", err) }

func (t *Target) fill(out *Output, slot *Slot, acquire, release uint64) {
	b := slot.Buffer
	retired := append(out.Retired[:0], t.pending...)
	t.reported = append(t.reported[:0], t.pending...)
	t.pending = t.pending[:0]
	*out = Output{
		Buffer: b.ID, NewBuffer: !slot.Presented, Retired: retired,
		Width: t.width, Height: t.height, FourCC: t.selected.DRMFormat, Modifier: slot.Image.Modifier,
		PlaneCount: 1, Acquire: Timeline{ID: 0, FD: t.acquireFD}, Release: Timeline{ID: b.ID, FD: -1},
		NewTimelines: !slot.Presented, AcquirePoint: acquire, ReleasePoint: release, ReleaseFD: slot.ReleaseFD,
	}
	out.Planes[0] = Plane{FD: slot.Image.FD, Offset: slot.Image.Offset, Stride: slot.Image.Stride}
	if !slot.Presented {
		out.Release.FD = slot.ReleaseExport
		slot.Presented = true
	}
}

// Released handles a readable release eventfd of the buffer: the compositor
// signaled its latest release point, so the image may be reused once the GPU
// wait is installed.
func (t *Target) Released(id uint64) error {
	slot := t.Slots[id]
	if slot == nil {
		return nil
	}
	b := slot.Buffer
	if b.State != buffers.CompositorOwned {
		return nil
	}
	signaled, err := t.Node.Signaled(slot.ReleaseHandle, b.ReleasePoint)
	if err != nil {
		return err
	}
	if !signaled {
		return nil // still armed; spurious call
	}
	var counter [8]byte
	if _, err = unix.Read(slot.ReleaseFD, counter[:]); err != nil && err != unix.EAGAIN {
		return fmt.Errorf("session: read release eventfd: %w", err)
	}
	return t.release(slot, b.ReleasePoint)
}

func (t *Target) release(slot *Slot, point uint64) error {
	b := slot.Buffer
	// An older completion cannot authorize a newer commit of the same image.
	if b.ReleasePoint != point || b.State != buffers.CompositorOwned {
		return nil
	}
	if err := t.Pool.Signal(b, point); err != nil {
		return err
	}
	if b.Generation == t.Pool.Generation {
		fd, err := t.Node.ReleaseFence(slot.ReleaseHandle, point)
		if err != nil {
			return err
		}
		wait, err := t.Device.NewBinaryFence(false)
		if err != nil {
			_ = unix.Close(fd)
			return err
		}
		if err = wait.ImportTemporary(fd); err != nil {
			_ = unix.Close(fd)
			wait.Close()
			return err
		}
		installed, err := installReleaseWait(slot, wait, slot.Frame.Ready)
		if err != nil {
			wait.Close()
			return err
		}
		if !installed {
			return nil
		}
		if err = t.Pool.Reuse(b, true); err != nil {
			return err
		}
	}
	return t.retire()
}

// finishPending installs release waits deferred because the old submission was
// still running. It never waits on the GPU.
func (t *Target) finishPending() (bool, error) {
	pending := false
	for _, slot := range t.Slots {
		if slot.PendingWait == nil {
			continue
		}
		ready, err := slot.Frame.Ready()
		if err != nil {
			return false, err
		}
		if !ready {
			pending = true
			continue
		}
		if slot.Wait != nil {
			slot.Wait.Close()
		}
		slot.Wait, slot.PendingWait, slot.WaitInFlight = slot.PendingWait, nil, false
		if err := t.Pool.Reuse(slot.Buffer, true); err != nil {
			return false, err
		}
	}
	return pending, nil
}

// retire frees images of old generations once nothing uses them, and queues
// the ones the transport imported for the next Output.
func (t *Target) retire() error {
	for id, slot := range t.Slots {
		b := slot.Buffer
		if !t.Pool.Retirable(b) {
			continue
		}
		ready, err := slot.Frame.Ready()
		if err != nil {
			return err
		}
		if !ready {
			continue
		}
		eventFD := slot.ReleaseFD
		slot.ReleaseFD = -1
		wasPresented := slot.Presented
		t.closeSlot(slot)
		if wasPresented {
			t.pending = append(t.pending, Retired{Buffer: id, ReleaseFD: eventFD})
		} else if eventFD >= 0 {
			_ = unix.Close(eventFD)
		}
		delete(t.Slots, id)
		if err = t.Pool.Remove(b); err != nil {
			return err
		}
	}
	for i := 0; i < len(t.retiredPipelines); {
		old := t.retiredPipelines[i]
		live := false
		for _, slot := range t.Slots {
			if slot.Buffer.Generation == old.generation {
				live = true
				break
			}
		}
		if live {
			i++
			continue
		}
		old.rect.Close()
		old.list.Close()
		t.retiredPipelines = append(t.retiredPipelines[:i], t.retiredPipelines[i+1:]...)
	}
	return nil
}

// Readback returns the last drawn pixels of a buffer in RGBA order. Tests only;
// call EnableReadback before the first Draw.
func (t *Target) Readback(id uint64) (*image.NRGBA, error) {
	slot := t.Slots[id]
	if slot == nil || slot.Frame.Readback == nil {
		return nil, errors.New("session: no readback for buffer")
	}
	if err := slot.Frame.Wait(); err != nil {
		return nil, err
	}
	img, err := slot.Frame.Readback.Copy()
	if err != nil {
		return nil, err
	}
	vkdevice.SwizzleBGRA(img)
	return img, nil
}

// Close waits for the GPU, then frees every descriptor and image. The
// compositor's imports keep their own kernel references.
func (t *Target) Close() error {
	if t == nil || t.closed {
		return nil
	}
	t.closed = true
	if t.Device != nil && t.Device.Logical != 0 && t.Device.Dispatch != nil {
		_ = t.Device.Dispatch.DeviceWaitIdle(t.Device.Logical)
	}
	for _, r := range t.reported {
		_ = unix.Close(r.ReleaseFD)
	}
	for _, r := range t.pending {
		_ = unix.Close(r.ReleaseFD)
	}
	t.reported, t.pending = nil, nil
	for id, slot := range t.Slots {
		closeFD(&slot.ReleaseFD)
		t.closeSlot(slot)
		delete(t.Slots, id)
	}
	for _, old := range t.retiredPipelines {
		old.rect.Close()
		old.list.Close()
	}
	t.retiredPipelines = nil
	t.pipeline.Close()
	closeFD(&t.acquireFD)
	if t.Node != nil {
		if t.acquire != 0 {
			_ = t.Node.Destroy(t.acquire)
		}
		_ = t.Node.Close()
	}
	t.Device.Close()
	return nil
}
