//go:build linux

// Package session coordinates Wayland ownership, Vulkan submissions, DRM
// release points and the application frame loop behind nefergui.Run.
package session

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/bnema/nefergui/internal/platform/wayland"
	"github.com/bnema/nefergui/internal/presentation/buffers"
	"github.com/bnema/nefergui/internal/presentation/syncobj"
	"github.com/bnema/nefergui/internal/presentation/vkdevice"
	"github.com/bnema/nefergui/internal/render"
	"github.com/bnema/nefergui/internal/text"
	"github.com/bnema/wlturbo/protocol/drmsyncobj"
	"golang.org/x/sys/unix"
)

type Slot struct {
	Buffer          *buffers.Buffer
	Image           *vkdevice.Image
	Frame           *vkdevice.Frame
	First           bool
	Wait            *vkdevice.BinaryFence
	PendingWait     *vkdevice.BinaryFence
	WaitInFlight    bool
	ReadbackFrame   uint64
	ReadbackPending bool
	ReleaseHandle   uint32
	ReleaseTimeline *drmsyncobj.WpLinuxDrmSyncobjTimeline
}
type Session struct {
	Window           *wayland.Window
	Device           *vkdevice.Device
	Node             *syncobj.Node
	Pool             *buffers.Pool
	Pipeline         *vkdevice.Pipeline
	ListPipeline     *vkdevice.Pipeline
	Preparer         *render.Preparer
	Slots            map[uint64]*Slot
	retiredPipelines []retiredPipelines
	Acquire          uint32
	AcquireTimeline  *drmsyncobj.WpLinuxDrmSyncobjTimeline
	Width, Height    int32
	Transparent      bool
	trace            *os.File
	traceBuffer      *bufio.Writer
	debugDir         string
	timings          *os.File
	timingBuffer     *bufio.Writer
	frame            uint64
	debugFrames      uint64
	debugDropped     uint64
	readbacks        chan readbackJob
	readbackDone     chan struct{}
	debugWritten     chan uint64
	// Written only by readbackWorker; read after readbackDone.
	debugWriteErrors  uint64
	debugLastWriteErr string
	fdStart, fdPeak   int
	framePending      bool
	waitCtx           context.Context
	waitCancel        context.CancelFunc
	waiters           sync.WaitGroup
	releases          chan releaseEvent
}

type retiredPipelines struct {
	generation uint64
	rect, list *vkdevice.Pipeline
}

type readbackJob struct {
	frame uint64
	image *image.NRGBA
}

const maxDebugPNGs = 16
const readbackQueueSize = 2

func captureBudget(saved, dropped uint64, pending int, queued int, limit uint64) bool {
	return saved+dropped+uint64(pending+queued) < limit
}

// offerReadback never blocks the state owner. The worker owns the copied image.
func offerReadback(queue chan readbackJob, job readbackJob) bool {
	select {
	case queue <- job:
		return true
	default:
		return false
	}
}

// readbackWorker owns debugWriteErrors until readbackDone is closed.
func (s *Session) readbackWorker() {
	defer close(s.readbackDone)
	for job := range s.readbacks {
		path := filepath.Join(s.debugDir, fmt.Sprintf("readback-%06d.png", job.frame))
		vkdevice.SwizzleBGRA(job.image)
		if err := writePNG(path, job.image); err != nil {
			s.debugWriteErrors++
			s.debugLastWriteErr = err.Error()
			continue
		}
		s.debugWritten <- job.frame
	}
}

func writePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	err = png.Encode(f, img)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return err
}

type releaseEvent struct {
	id, point uint64
	err       error
}

// traceState is entirely disabled when NEFERGUI_DEBUG_DIR is unset.
func (s *Session) traceState(b *buffers.Buffer, event string) {
	if s.trace == nil || b == nil {
		return
	}
	_ = json.NewEncoder(s.traceBuffer).Encode(struct {
		Time                       time.Time `json:"time"`
		Frame, Buffer, Generation  uint64
		State                      buffers.State
		Event                      string
		AcquirePoint, ReleasePoint uint64
		WlReleased, PointSignaled  bool
	}{time.Now(), s.frame, b.ID, b.Generation, b.State, event, b.AcquirePoint, b.ReleasePoint, b.WlReleased, b.PointSignaled})
}

func Open(name string, width, height int32, transparent bool) (_ *Session, err error) {
	w, err := wayland.Connect(name, width, height, transparent)
	if err != nil {
		return nil, err
	}
	s := &Session{Window: w, Transparent: transparent, Slots: make(map[uint64]*Slot)}
	if dir := os.Getenv("NEFERGUI_DEBUG_DIR"); dir != "" {
		s.debugDir = dir
		if err = os.MkdirAll(dir, 0700); err != nil {
			_ = w.Close()
			return nil, err
		}
		s.trace, err = os.Create(filepath.Join(dir, "buffer-lifecycle.jsonl"))
		if err != nil {
			_ = w.Close()
			return nil, err
		}
		s.timings, err = os.Create(filepath.Join(dir, "frame-timings.jsonl"))
		if err != nil {
			_ = s.trace.Close()
			_ = w.Close()
			return nil, err
		}
		s.traceBuffer = bufio.NewWriterSize(s.trace, 64*1024)
		s.timingBuffer = bufio.NewWriterSize(s.timings, 64*1024)
		s.readbacks = make(chan readbackJob, readbackQueueSize)
		s.readbackDone = make(chan struct{})
		s.debugWritten = make(chan uint64, maxDebugPNGs)
		go s.readbackWorker()
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, s.Close())
		}
	}()
	s.Preparer, err = render.NewPreparer(vkdevice.AtlasSize, vkdevice.AtlasPages)
	if err != nil {
		return nil, err
	}
	s.Device, err = vkdevice.Open(vkdevice.DeviceIdentity(w.MainDevice))
	if err != nil {
		return nil, err
	}
	s.Node, err = syncobj.Open(filepath.Join("/dev/dri", fmt.Sprintf("renderD%d", s.Device.Render.Minor)))
	if err != nil {
		return nil, err
	}
	if s.Acquire, err = s.Node.Create(); err != nil {
		return nil, err
	}
	if s.AcquireTimeline, err = s.importTimeline(s.Acquire); err != nil {
		return nil, err
	}
	s.Pool, err = buffers.New(3)
	if err != nil {
		return nil, err
	}
	if err = s.newGeneration(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Session) importTimeline(handle uint32) (*drmsyncobj.WpLinuxDrmSyncobjTimeline, error) {
	fd, err := s.Node.ExportTimeline(handle)
	if err != nil {
		return nil, err
	}
	// WLTurbo closes the sent descriptor on successful ImportTimeline; on
	// transport failure its ownership remains with this caller.
	timeline, err := s.Window.SyncManager.ImportTimeline(fd)
	if err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	return timeline, nil
}

func (s *Session) newGeneration() error {
	width, height, err := s.Window.PhysicalSize()
	if err != nil {
		return err
	}
	mods, err := s.Device.ExportableModifiers()
	if err != nil {
		return err
	}
	selected, err := vkdevice.ChooseModifier(s.Window.Tranches, s.Transparent, mods)
	if err != nil {
		return err
	}
	pipeline, err := s.Device.NewRectPipeline(width, height)
	if err != nil {
		return err
	}
	listPipeline, err := s.Device.NewListPipeline(width, height)
	if err != nil {
		pipeline.Close()
		return err
	}
	// A pipeline may still be referenced by submissions for the previous
	// generation. retire closes it only after all old slots have retired.
	if s.Pipeline != nil {
		s.retiredPipelines = append(s.retiredPipelines, retiredPipelines{
			generation: s.Pool.Generation - 1, rect: s.Pipeline, list: s.ListPipeline,
		})
	}
	s.Pipeline = pipeline
	s.ListPipeline = listPipeline
	s.Width, s.Height = width, height
	for _, b := range s.Pool.Buffers {
		if b.Generation != s.Pool.Generation {
			continue
		}
		img, e := s.Device.AllocateImage(width, height, selected, s.Window.Dmabuf)
		if e != nil {
			return e
		}
		frame, e := s.Device.NewFrame(img)
		if e == nil && s.debugDir != "" {
			frame.Readback, e = s.Device.NewReadback(width, height)
		}
		if e != nil {
			if frame != nil {
				frame.Close()
			}
			img.Close()
			return e
		}
		slot := &Slot{Buffer: b, Image: img, Frame: frame, First: true}
		slot.ReleaseHandle, e = s.Node.Create()
		if e != nil {
			frame.Close()
			img.Close()
			return e
		}
		slot.ReleaseTimeline, e = s.importTimeline(slot.ReleaseHandle)
		if e != nil {
			_ = s.Node.Destroy(slot.ReleaseHandle)
			frame.Close()
			img.Close()
			return e
		}
		s.Slots[b.ID] = slot
		img.Buffer.OnRelease(func() { s.Window.BufferReleased(b.ID) })
	}
	return nil
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

// Tick presents at most one frame. The caller drives the Wayland dispatch
// loop; a frame callback only schedules Tick, never makes an image reusable.
func (s *Session) Tick(rect vkdevice.Rect) (bool, error) {
	return s.tick(&rect, nil, nil, nil)
}

// TickList presents ordered instanced quads with the same ownership gates as Tick.
func (s *Session) TickList(instances []vkdevice.Instance, uploads []text.Upload) (bool, error) {
	var batches []vkdevice.Batch
	if len(instances) > 0 {
		batches = []vkdevice.Batch{{Count: uint32(len(instances))}}
	}
	return s.tick(nil, instances, uploads, batches)
}

func (s *Session) tick(rect *vkdevice.Rect, instances []vkdevice.Instance, uploads []text.Upload, batches []vkdevice.Batch) (bool, error) {
	// Uploads were drained by Prepare. Restore them if this tick did not submit.
	submitted := false
	defer func() {
		if !submitted && len(uploads) > 0 {
			s.Preparer.Atlas.MarkAllDirty()
		}
	}()
	started := time.Now()
	if s.Window.Dirty {
		w, h, err := s.Window.PhysicalSize()
		if err != nil {
			return false, err
		}
		if w != s.Width || h != s.Height {
			if err = s.Pool.Resize(3); err != nil {
				return false, err
			}
			if err = s.newGeneration(); err != nil {
				return false, err
			}
		}
		s.Window.Dirty = false
	}
	if err := s.retire(); err != nil {
		return false, err
	}
	if !s.Window.FrameReady {
		return false, nil
	}
	b, slot, waiting, err := beginReady(s.Pool, s.Slots, func(slot *Slot) (bool, error) { return slot.Frame.Ready() })
	if err != nil {
		return false, err
	}
	if b == nil {
		s.framePending = waiting
		return false, nil
	}
	s.framePending = false
	s.traceState(b, "begin")
	if err := s.saveReadback(slot); err != nil {
		return false, err
	}
	if slot.Wait != nil && slot.WaitInFlight {
		slot.Wait.Close()
		slot.Wait = nil
		slot.WaitInFlight = false
	}
	capture := s.wantReadback()
	if rect != nil {
		err = slot.Frame.Record(slot.Image, s.Pipeline, s.Width, s.Height, *rect, slot.First, capture)
	} else {
		err = slot.Frame.RecordList(slot.Image, s.ListPipeline, s.Width, s.Height, instances, uploads, batches, slot.First, capture)
	}
	if err != nil {
		return false, err
	}
	if err = slot.Frame.Submit(slot.Wait); err != nil {
		return false, err
	}
	submitted = true
	if slot.Wait != nil {
		slot.WaitInFlight = true
	}
	// Keep the imported release-wait semaphore alive until GPU completion.
	slot.First = false
	fd, err := slot.Frame.Signal.Export()
	if err != nil {
		return false, err
	}
	acquire, err := s.Pool.Acquire(b)
	if err != nil {
		_ = unix.Close(fd)
		return false, err
	}
	if err = s.Node.AcquirePoint(fd, s.Acquire, acquire); err != nil {
		_ = unix.Close(fd)
		return false, err
	}
	s.traceState(b, "acquire_point_exported")
	release, err := s.Pool.Commit(b)
	if err != nil {
		return false, err
	}
	if err = s.Window.Present(slot.Image.Buffer, s.AcquireTimeline, slot.ReleaseTimeline, acquire, release); err != nil {
		return false, err
	}
	s.traceState(b, "committed")
	if err = s.Pool.Own(b); err != nil {
		return false, err
	}
	s.traceState(b, "compositor_owns")
	if capture {
		slot.ReadbackFrame = s.frame
		slot.ReadbackPending = true
	}
	if s.timings != nil {
		if len(s.Slots) > 6 {
			return false, fmt.Errorf("unbounded image generations: %d", len(s.Slots))
		}
		fds, _ := os.ReadDir("/proc/self/fd")
		if len(fds) > s.fdPeak {
			s.fdPeak = len(fds)
		}
		_ = json.NewEncoder(s.timingBuffer).Encode(map[string]any{"frame": s.frame, "buffer": b.ID, "generation": b.Generation, "submit_to_commit_ns": time.Since(started).Nanoseconds(), "fd_count": len(fds), "images": len(s.Slots), "atlas_pages": s.Preparer.Atlas.PagesUsed(), "pipelines": 2 * (1 + len(s.retiredPipelines)), "time": time.Now()})
	}
	s.startReleaseWait(slot, release)
	s.frame++
	return true, nil
}

func (s *Session) wantReadback() bool {
	if s.readbacks == nil {
		return false
	}
	pending := 0
	for _, slot := range s.Slots {
		if slot.ReadbackPending {
			pending++
		}
	}
	return captureBudget(s.debugFrames, s.debugDropped, pending, len(s.readbacks), maxDebugPNGs)
}

// saveReadback is only called for a completed GPU submission; the state owner
// copies into owned memory and hands the PNG encoding to the bounded worker.
func (s *Session) saveReadback(slot *Slot) error {
	if !slot.ReadbackPending || slot.Frame.Readback == nil {
		return nil
	}
	// Do not map a full-size image while the encoder cannot accept it.
	if len(s.readbacks) == cap(s.readbacks) {
		slot.ReadbackPending = false
		s.debugDropped++
		return nil
	}
	img, err := slot.Frame.Readback.Copy()
	if err != nil {
		return err
	}
	slot.ReadbackPending = false
	if offerReadback(s.readbacks, readbackJob{slot.ReadbackFrame, img}) {
		s.debugFrames++
	} else {
		s.debugDropped++
	}
	return nil
}

// startReleaseWait never mutates Pool or Vulkan state from the waiter.
func (s *Session) startReleaseWait(slot *Slot, point uint64) {
	if s.waitCancel == nil {
		return
	}
	ctx := s.waitCtx
	id, handle := slot.Buffer.ID, slot.ReleaseHandle
	s.waiters.Add(1)
	go func() {
		defer s.waiters.Done()
		for {
			if ctx.Err() != nil {
				return
			}
			ready, err := s.Node.WaitPoint(handle, point, 20*time.Millisecond)
			if err != nil || ready {
				select {
				case s.releases <- releaseEvent{id: id, point: point, err: err}:
				case <-ctx.Done():
				}
				return
			}
		}
	}()
}

func (s *Session) release(ev releaseEvent) error {
	if ev.err != nil {
		return ev.err
	}
	slot := s.Slots[ev.id]
	if slot == nil {
		return nil
	}
	b := slot.Buffer
	// An older completion cannot authorize a newer commit of the same image.
	if b.ReleasePoint != ev.point || b.State != buffers.CompositorOwned {
		return nil
	}
	if err := s.Pool.Signal(b, ev.point); err != nil {
		return err
	}
	s.traceState(b, "release_point_signaled")
	if b.Generation == s.Pool.Generation {
		fd, err := s.Node.ReleaseFence(slot.ReleaseHandle, ev.point)
		if err != nil {
			return err
		}
		wait, err := s.Device.NewBinaryFence(false)
		if err != nil {
			_ = unix.Close(fd)
			return err
		}
		if err = wait.ImportTemporary(fd); err != nil {
			_ = unix.Close(fd)
			wait.Close()
			return err
		}
		installed, e := installReleaseWait(slot, wait, slot.Frame.Ready)
		if e != nil {
			wait.Close()
			return e
		}
		if !installed {
			return nil
		}
		if err = s.Pool.Reuse(b, true); err != nil {
			return err
		}
		s.traceState(b, "available")
	}
	return s.retire()
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

// finishPending runs only on the state-owner loop; never waits on a GPU fence.
func (s *Session) finishPending() (bool, error) {
	pending := false
	for _, slot := range s.Slots {
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
		if err := s.Pool.Reuse(slot.Buffer, true); err != nil {
			return false, err
		}
		s.traceState(slot.Buffer, "available")
	}
	return pending, nil
}

func (s *Session) retire() error {
	for _, slot := range s.Slots {
		b := slot.Buffer
		if !s.Pool.Retirable(b) {
			continue
		}
		ready, err := slot.Frame.Ready()
		if err != nil {
			return err
		}
		if !ready {
			continue
		}
		if err := s.saveReadback(slot); err != nil {
			return err
		}
		slot.Frame.Close()
		if slot.PendingWait != nil {
			slot.PendingWait.Close()
		}
		if slot.Wait != nil {
			slot.Wait.Close()
		}
		slot.Image.Close()
		_ = slot.ReleaseTimeline.Destroy()
		_ = s.Node.Destroy(slot.ReleaseHandle)
		s.traceState(b, "retired")
		delete(s.Slots, b.ID)
		if err = s.Pool.Remove(b); err != nil {
			return err
		}
	}
	for i := 0; i < len(s.retiredPipelines); {
		old := s.retiredPipelines[i]
		live := false
		for _, slot := range s.Slots {
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
		s.retiredPipelines = append(s.retiredPipelines[:i], s.retiredPipelines[i+1:]...)
	}
	return nil
}

func (s *Session) Run(ctx context.Context, frames int, draw func(int) vkdevice.Rect) (int, error) {
	return s.RunWithCommit(ctx, frames, draw, nil)
}

// RunWithCommit calls committed after each successfully committed frame.
// The optional hook is for the example's capture request; it never gates reuse.
func (s *Session) RunWithCommit(ctx context.Context, frames int, draw func(int) vkdevice.Rect, committed func(int) error) (int, error) {
	if draw == nil {
		return 0, fmt.Errorf("nil draw function")
	}
	return s.runWithCommit(ctx, frames, func(i int) (bool, error) { return s.Tick(draw(i)) }, committed)
}

// RunPreparedListWithCommit prepares uploads on every attempt. A skipped
// submission restores dirty pages, so the next attempt resends them.
func (s *Session) RunPreparedListWithCommit(ctx context.Context, frames int, draw func(int) (render.Frame, error), committed func(int) error) (int, error) {
	if draw == nil {
		return 0, fmt.Errorf("nil draw function")
	}
	return s.runWithCommit(ctx, frames, func(i int) (bool, error) {
		frame, err := draw(i)
		if err != nil {
			return false, err
		}
		instances, batches, stats := render.ListBatches(frame)
		if len(stats.Skipped) != 0 {
			s.Preparer.Atlas.MarkAllDirty()
			return false, fmt.Errorf("unsupported list operations: %v", stats.Skipped)
		}
		return s.tick(nil, instances, frame.Uploads, batches)
	}, committed)
}

// RunListWithCommit drives the list renderer using the existing frame loop.
func (s *Session) RunListWithCommit(ctx context.Context, frames int, draw func(int) []vkdevice.Instance, committed func(int) error) (int, error) {
	if draw == nil {
		return 0, fmt.Errorf("nil draw function")
	}
	return s.runWithCommit(ctx, frames, func(i int) (bool, error) { return s.TickList(draw(i), nil) }, committed)
}

func (s *Session) runWithCommit(ctx context.Context, frames int, tick func(int) (bool, error), committed func(int) error) (int, error) {
	if frames < 0 {
		return 0, fmt.Errorf("invalid frame count")
	}
	if s.waitCancel != nil {
		return 0, fmt.Errorf("session already running")
	}
	s.waitCtx, s.waitCancel = context.WithCancel(ctx)
	s.releases = make(chan releaseEvent, 128)
	if s.debugDir != "" {
		fds, _ := os.ReadDir("/proc/self/fd")
		s.fdStart = len(fds)
		s.fdPeak = s.fdStart
	}
	wlEvents := s.Window.StartReader()
	count := 0
	defer func() { s.waitCancel(); s.waiters.Wait() }()
	for !s.Window.Closed && (frames == 0 || count < frames) {
		if err := ctx.Err(); err != nil {
			return count, err
		}
		pending, err := s.finishPending()
		if err != nil {
			return count, err
		}
		if ok, err := tick(count); err != nil {
			return count, err
		} else if ok {
			count++
			if committed != nil {
				if err := committed(count); err != nil {
					return count, err
				}
			}
			continue
		}
		var retry <-chan time.Time
		if pending || s.framePending {
			retry = time.After(5 * time.Millisecond)
		}
		select {
		case <-ctx.Done():
			return count, ctx.Err()
		case <-retry:
		case ev := <-wlEvents:
			if ev.Kind == wayland.BufferRelease {
				if slot := s.Slots[ev.BufferID]; slot != nil {
					if err := s.Pool.Release(slot.Buffer); err != nil {
						return count, err
					}
					s.traceState(slot.Buffer, "wl_buffer.release")
				}
			} else if err := s.Window.Apply(ev); err != nil {
				return count, err
			}
		case ev := <-s.releases:
			if err := s.release(ev); err != nil {
				return count, err
			}
		}
	}
	if s.Device != nil && s.Device.Validation != nil && len(s.Device.Validation.Messages()) > 0 {
		return count, fmt.Errorf("%d Vulkan validation errors: %v", len(s.Device.Validation.Messages()), s.Device.Validation.Messages())
	}
	return count, nil
}

// Close detaches and destroys the explicit-sync surface, traces release points
// for at most 200 ms, waits for our GPU, then frees every client allocation.
// Compositor imports keep independent DMA-BUF kernel references.
// It returns errors from writing requested debug artifacts; allocations are
// freed regardless.
func (s *Session) Close() (err error) {
	if s == nil {
		return nil
	}
	if s.trace != nil {
		defer func() { err = errors.Join(err, s.traceBuffer.Flush(), s.trace.Close()) }()
	}
	if s.timings != nil {
		defer func() { err = errors.Join(err, s.timingBuffer.Flush(), s.timings.Close()) }()
	}
	if s.readbacks != nil {
		defer func() {
			if s.readbacks != nil {
				close(s.readbacks)
				<-s.readbackDone
			}
		}()
	}
	if s.Window != nil && s.Window.Surface != nil {
		_ = s.Window.Surface.Attach(nil, 0, 0)
		_ = s.Window.Surface.Commit()
		_ = s.Window.DestroySyncSurface()
	}
	// Drain detach/release points for tracing, not for freeing allocations. The
	// reader and waiters continue posting events, but the loop owns all state.
	if s.waitCancel != nil && s.Pool != nil {
		deadline := time.NewTimer(200 * time.Millisecond)
		defer deadline.Stop()
		for {
			busy := false
			for _, slot := range s.Slots {
				if slot.Buffer.State != buffers.Available && slot.Buffer.State != buffers.Released {
					busy = true
					break
				}
			}
			if !busy {
				break
			}
			select {
			case ev := <-s.releases:
				_ = s.release(ev)
			case ev := <-s.Window.Events():
				if ev.Kind == wayland.BufferRelease {
					if slot := s.Slots[ev.BufferID]; slot != nil {
						_ = s.Pool.Release(slot.Buffer)
					}
				} else if ev.Kind != wayland.TransportError {
					_ = s.Window.Apply(ev)
				}
			case <-deadline.C:
				goto stopWaiters
			}
		}
	}
stopWaiters:
	if s.waitCancel != nil {
		s.waitCancel()
		s.waiters.Wait()
		s.waitCancel = nil
	}
	// The client GPU's completion is independent from compositor release.
	if s.Device != nil && s.Device.Logical != 0 && s.Device.Dispatch != nil {
		_ = s.Device.Dispatch.DeviceWaitIdle(s.Device.Logical)
	}
	if s.Pool != nil {
		_ = s.retire()
	}
	for _, slot := range s.Slots {
		// GPU work must finish before destroying a semaphore it waits on.
		if slot.ReadbackPending {
			_ = s.saveReadback(slot)
		}
		if slot.Frame != nil {
			slot.Frame.Close()
		}
		if slot.Wait != nil {
			slot.Wait.Close()
		}
		if slot.PendingWait != nil {
			slot.PendingWait.Close()
		}
		if slot.ReleaseTimeline != nil {
			_ = slot.ReleaseTimeline.Destroy()
		}
		// The exported DMA-BUF remains alive in the compositor independently.
		if slot.Image != nil {
			slot.Image.Close()
		}
		if s.Node != nil && slot.ReleaseHandle != 0 {
			_ = s.Node.Destroy(slot.ReleaseHandle)
		}
	}
	if s.AcquireTimeline != nil {
		_ = s.AcquireTimeline.Destroy()
	}
	if s.Window != nil {
		_ = s.Window.Close()
	}
	if s.Node != nil {
		if s.Acquire != 0 {
			_ = s.Node.Destroy(s.Acquire)
		}
		_ = s.Node.Close()
	}
	for _, old := range s.retiredPipelines {
		old.rect.Close()
		old.list.Close()
	}
	s.retiredPipelines = nil
	if s.Pipeline != nil {
		s.Pipeline.Close()
	}
	if s.ListPipeline != nil {
		s.ListPipeline.Close()
	}
	if s.Device != nil {
		s.Device.Close()
	}
	if s.debugDir != "" {
		// Wait for the worker before reporting completed PNGs.
		if s.readbacks != nil {
			close(s.readbacks)
			<-s.readbackDone
			s.readbacks = nil
		}
		runtime.GC()
		fds, _ := os.ReadDir("/proc/self/fd")
		data, e := json.MarshalIndent(map[string]any{"fd_start": s.fdStart, "fd_peak": s.fdPeak, "fd_after": len(fds), "frames": s.frame, "images_after": 0, "debug_png_count": len(s.debugWritten), "debug_png_dropped": s.debugDropped, "debug_png_write_errors": s.debugWriteErrors, "debug_png_last_error": s.debugLastWriteErr}, "", "  ")
		if e == nil {
			e = os.WriteFile(filepath.Join(s.debugDir, "resource-summary.json"), append(data, '\n'), 0600)
		}
		if e != nil {
			err = errors.Join(err, fmt.Errorf("session: resource summary: %w", e))
		}
	}
	return err
}
