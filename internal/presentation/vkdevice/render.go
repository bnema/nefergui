//go:build linux

package vkdevice

import (
	"fmt"
	"math"
	"unsafe"

	"github.com/bnema/nefergui/internal/text"
	"github.com/bnema/purego-vulkan/vulkan"
)

// Frame records into its own command buffer and signals a binary semaphore.
// Do not reset the command buffer or destroy the semaphore until its GPU fence
// completes. The compositor's release point is an independent ownership gate.
type Frame struct {
	device           *Device
	Pool             vulkan.CommandPool
	Commands         vulkan.CommandBuffer
	Done             vulkan.Fence
	Signal           *BinaryFence
	View             vulkan.ImageView
	submitted        bool
	Readback         *Readback
	instances        *instanceBuffer
	glyphStaging     *instanceBuffer
	imageStaging     *instanceBuffer
	imageCopies      []imageCopy
	imageSerial      uint64
	atlasRecorded    bool
	fallbackRecorded bool
	// signalExported is set once the acquire SYNC_FD was exported. Exporting
	// with copy transference resets the binary semaphore, so Signal may be
	// signaled again after the frame's fence completes.
	signalExported bool
	scratch        frameScratch
}

// frameScratch holds the Vulkan info structs and out-parameters of one
// recording and submission. The purego bindings force every pointer argument
// to the heap, so building these per call allocated each frame. A Frame is
// used by a single owner and its structs are only read by the driver during
// the call that receives them (the command buffer copies recorded state), so
// they are rebuilt and reused on every call.
type frameScratch struct {
	begin      vulkan.CommandBufferBeginInfo
	barrier    vulkan.ImageMemoryBarrier2
	dependency vulkan.DependencyInfo
	attachment vulkan.RenderingAttachmentInfo
	rendering  vulkan.RenderingInfo
	sets       [2]vulkan.DescriptorSet
	screen     [2]float32
	offset     vulkan.DeviceSize
	cmd        vulkan.CommandBufferSubmitInfo
	signal     vulkan.SemaphoreSubmitInfo
	waiting    vulkan.SemaphoreSubmitInfo
	submit     vulkan.SubmitInfo2
	mapped     unsafe.Pointer
}

func (d *Device) NewFrame(img *Image) (f *Frame, err error) {
	f = &Frame{device: d}
	defer func() {
		if err != nil {
			f.Close()
		}
	}()
	pool := vulkan.CommandPoolCreateInfo{SType: vulkan.StructureTypeCommandPoolCreateInfo, QueueFamilyIndex: d.QueueFamily, Flags: vulkan.CommandPoolCreateResetCommandBufferBit}
	if err = vulkan.Check(d.Dispatch.CreateCommandPool(d.Logical, &pool, nil, &f.Pool)); err != nil {
		return nil, err
	}
	allocate := vulkan.CommandBufferAllocateInfo{SType: vulkan.StructureTypeCommandBufferAllocateInfo, CommandPool: f.Pool, Level: vulkan.CommandBufferLevelPrimary, CommandBufferCount: 1}
	if err = vulkan.Check(d.Dispatch.AllocateCommandBuffers(d.Logical, &allocate, &f.Commands)); err != nil {
		return nil, err
	}
	view := vulkan.ImageViewCreateInfo{SType: vulkan.StructureTypeImageViewCreateInfo, Image: img.Image, ViewType: vulkan.ImageViewType2d, Format: vulkan.FormatB8g8r8a8Unorm, SubresourceRange: vulkan.ImageSubresourceRange{AspectMask: vulkan.ImageAspectColorBit, LevelCount: 1, LayerCount: 1}}
	if err = vulkan.Check(d.Dispatch.CreateImageView(d.Logical, &view, nil, &f.View)); err != nil {
		return nil, err
	}
	fence := vulkan.FenceCreateInfo{SType: vulkan.StructureTypeFenceCreateInfo}
	if err = vulkan.Check(d.Dispatch.CreateFence(d.Logical, &fence, nil, &f.Done)); err != nil {
		return nil, err
	}
	f.Signal, err = d.NewBinaryFence(true)
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (f *Frame) Ready() (bool, error) {
	if !f.submitted {
		return true, nil
	}
	r := f.device.Dispatch.WaitForFences(f.device.Logical, 1, &f.Done, 1, 0)
	if r == vulkan.Timeout {
		return false, nil
	}
	if err := vulkan.Check(r); err != nil {
		return false, err
	}
	return true, nil
}

// RecordList records an ordered list of physical-pixel quads. Reuse is gated
// by Ready, including any instance-buffer growth.
func (f *Frame) RecordList(img *Image, pipe *Pipeline, width, height int32, instances []Instance, uploads []text.Upload, batches []Batch, initial, capture bool) error {
	return f.record(img, pipe, width, height, initial, capture, instances, uploads, batches)
}

func (f *Frame) record(img *Image, pipe *Pipeline, width, height int32, initial, capture bool, instances []Instance, uploads []text.Upload, batches []Batch) (err error) {
	if width <= 0 || height <= 0 || pipe == nil {
		return fmt.Errorf("invalid render dimensions or pipeline")
	}
	ready, err := f.Ready()
	if err != nil || !ready {
		return fmt.Errorf("command buffer still in use: ready=%v err=%v", ready, err)
	}
	f.settleImages()
	if f.submitted {
		if err = vulkan.Check(f.device.Dispatch.ResetFences(f.device.Logical, 1, &f.Done)); err != nil {
			return err
		}
		if err = vulkan.Check(f.device.Dispatch.ResetCommandBuffer(f.Commands, 0)); err != nil {
			return err
		}
		// The fence is signaled, so the previous submission finished. Its
		// semaphore is reusable only if the SYNC_FD export consumed the signal;
		// otherwise it is still signaled and must be replaced.
		if !f.signalExported {
			f.Signal.Close()
			f.Signal, err = f.device.NewBinaryFence(true)
			if err != nil {
				return err
			}
		}
		f.submitted = false
	}
	if f.device.atlas == nil {
		return fmt.Errorf("list atlas not initialized")
	}
	if err := f.stageGlyphs(uploads); err != nil {
		return err
	}
	if f.device.images != nil {
		defer func() {
			if err != nil {
				f.device.images.abort(f)
			}
		}()
		batches, err = f.device.images.resolve(f, batches)
		if err != nil {
			return err
		}
	}
	if len(instances) > 0 {
		if err := f.uploadInstances(instances); err != nil {
			return err
		}
	}
	sc := &f.scratch
	sc.begin = vulkan.CommandBufferBeginInfo{SType: vulkan.StructureTypeCommandBufferBeginInfo, Flags: vulkan.CommandBufferUsageOneTimeSubmitBit}
	d := f.device.Dispatch
	if err = vulkan.Check(d.BeginCommandBuffer(f.Commands, &sc.begin)); err != nil {
		return err
	}
	f.atlasRecorded = !f.device.atlas.initialized
	// Glyph uploads, a new image texture or the first frame each need
	// transfers before rendering: an image added without new glyphs must
	// still be uploaded.
	if len(uploads) != 0 || len(f.imageCopies) != 0 || !f.device.atlas.initialized || !f.device.images.fallbackReady {
		var staging vulkan.Buffer
		if len(uploads) != 0 {
			staging = f.glyphStaging.Buffer
		}
		f.device.atlas.recordUploads(f.Commands, staging, uploads)
		if !f.device.images.fallbackReady {
			f.recordFallback()
		}
		f.recordImages()
	}
	rangeInfo := vulkan.ImageSubresourceRange{AspectMask: vulkan.ImageAspectColorBit, LevelCount: 1, LayerCount: 1}
	old := vulkan.ImageLayout(vulkan.ImageLayoutGeneral)
	if initial {
		old = vulkan.ImageLayoutUndefined
	}
	barrier, dependency := &sc.barrier, &sc.dependency
	*barrier = vulkan.ImageMemoryBarrier2{SType: vulkan.StructureTypeImageMemoryBarrier2, SrcStageMask: vulkan.PipelineStage2None, DstStageMask: vulkan.PipelineStage2ColorAttachmentOutputBit, DstAccessMask: vulkan.Access2ColorAttachmentWriteBit, OldLayout: old, NewLayout: vulkan.ImageLayoutColorAttachmentOptimal, SrcQueueFamilyIndex: ^uint32(0), DstQueueFamilyIndex: ^uint32(0), Image: img.Image, SubresourceRange: rangeInfo}
	*dependency = vulkan.DependencyInfo{SType: vulkan.StructureTypeDependencyInfo, ImageMemoryBarrierCount: 1, ImageMemoryBarriers: barrier}
	d.CmdPipelineBarrier2(f.Commands, dependency)
	clear := vulkan.ClearValue{}
	sc.attachment = vulkan.RenderingAttachmentInfo{SType: vulkan.StructureTypeRenderingAttachmentInfo, ImageView: f.View, ImageLayout: vulkan.ImageLayoutColorAttachmentOptimal, LoadOp: vulkan.AttachmentLoadOpClear, StoreOp: vulkan.AttachmentStoreOpStore, ClearValue: clear}
	area := vulkan.Rect2D{Extent: vulkan.Extent2D{Width: uint32(width), Height: uint32(height)}}
	sc.rendering = vulkan.RenderingInfo{SType: vulkan.StructureTypeRenderingInfo, RenderArea: area, LayerCount: 1, ColorAttachmentCount: 1, ColorAttachments: &sc.attachment}
	d.CmdBeginRendering(f.Commands, &sc.rendering)
	d.CmdBindPipeline(f.Commands, vulkan.PipelineBindPointGraphics, pipe.Handle)
	// The fragment shader statically references both descriptor sets even
	// for non-image draws. Bind a valid initialized image set before any
	// draw; each image batch then replaces set 1 in painter's order.
	sc.sets = [2]vulkan.DescriptorSet{f.device.atlas.Set, f.device.images.fallback.Texture.Set}
	d.CmdBindDescriptorSets(f.Commands, vulkan.PipelineBindPointGraphics, pipe.Layout, 0, 2, &sc.sets[0], 0, nil)
	sc.screen = [2]float32{float32(width), float32(height)}
	d.CmdPushConstants(f.Commands, pipe.Layout, vulkan.ShaderStageVertexBit|vulkan.ShaderStageFragmentBit, 0, 8, unsafe.Pointer(&sc.screen))
	if len(instances) > 0 {
		sc.offset = 0
		d.CmdBindVertexBuffers(f.Commands, 0, 1, &f.instances.Buffer, &sc.offset)
		for _, batch := range batches {
			if batch.Count == 0 || uint64(batch.First)+uint64(batch.Count) > uint64(len(instances)) {
				return fmt.Errorf("invalid list batch range")
			}
			if batch.Source != nil {
				if batch.Texture == nil {
					return fmt.Errorf("image batch has no GPU texture")
				}
				d.CmdBindDescriptorSets(f.Commands, vulkan.PipelineBindPointGraphics, pipe.Layout, 1, 1, &batch.Texture.Set, 0, nil)
			}
			d.CmdDraw(f.Commands, 6, batch.Count, 0, batch.First)
		}
	}
	d.CmdEndRendering(f.Commands)
	if capture && f.Readback != nil {
		barrier.SrcStageMask = vulkan.PipelineStage2ColorAttachmentOutputBit
		barrier.SrcAccessMask = vulkan.Access2ColorAttachmentWriteBit
		barrier.DstStageMask = vulkan.PipelineStage2TransferBit
		barrier.DstAccessMask = vulkan.Access2TransferReadBit
		barrier.OldLayout = vulkan.ImageLayoutColorAttachmentOptimal
		barrier.NewLayout = vulkan.ImageLayoutTransferSrcOptimal
		d.CmdPipelineBarrier2(f.Commands, dependency)
		f.Readback.record(f.Commands, img.Image)
		barrier.SrcStageMask = vulkan.PipelineStage2TransferBit
		barrier.SrcAccessMask = vulkan.Access2TransferReadBit
		barrier.OldLayout = vulkan.ImageLayoutTransferSrcOptimal
	} else {
		barrier.SrcStageMask = vulkan.PipelineStage2ColorAttachmentOutputBit
		barrier.SrcAccessMask = vulkan.Access2ColorAttachmentWriteBit
		barrier.OldLayout = vulkan.ImageLayoutColorAttachmentOptimal
	}
	barrier.DstStageMask = vulkan.PipelineStage2AllCommandsBit
	barrier.DstAccessMask = vulkan.Access2MemoryReadBit
	barrier.NewLayout = vulkan.ImageLayoutGeneral
	d.CmdPipelineBarrier2(f.Commands, dependency)
	return vulkan.Check(d.EndCommandBuffer(f.Commands))
}

// Submit has no CPU wait: the next render waits on a temporarily imported
// release SYNC_FD, and the compositor waits on the exported acquire SYNC_FD.
func (f *Frame) Submit(wait *BinaryFence) error {
	sc := &f.scratch
	sc.cmd = vulkan.CommandBufferSubmitInfo{SType: vulkan.StructureTypeCommandBufferSubmitInfo, CommandBuffer: f.Commands}
	sc.signal = vulkan.SemaphoreSubmitInfo{SType: vulkan.StructureTypeSemaphoreSubmitInfo, Semaphore: f.Signal.Semaphore, StageMask: vulkan.PipelineStage2AllCommandsBit}
	sc.submit = vulkan.SubmitInfo2{SType: vulkan.StructureTypeSubmitInfo2, CommandBufferInfoCount: 1, CommandBufferInfos: &sc.cmd, SignalSemaphoreInfoCount: 1, SignalSemaphoreInfos: &sc.signal}
	if wait != nil {
		sc.waiting = vulkan.SemaphoreSubmitInfo{SType: vulkan.StructureTypeSemaphoreSubmitInfo, Semaphore: wait.Semaphore, StageMask: vulkan.PipelineStage2AllCommandsBit}
		sc.submit.WaitSemaphoreInfoCount = 1
		sc.submit.WaitSemaphoreInfos = &sc.waiting
	}
	if err := vulkan.Check(f.device.Dispatch.QueueSubmit2(f.device.Queue, 1, &sc.submit, f.Done)); err != nil {
		// Only this recording's unsubmitted image state is released; a
		// previous submission's serial was completed by record.
		if f.device.images != nil && f.imageSerial != 0 {
			f.device.images.abort(f)
		}
		return err
	}
	f.submitted = true
	f.signalExported = false
	if f.device.images != nil && f.imageSerial != 0 {
		f.device.images.inflight[f] = struct{}{}
	}
	if f.atlasRecorded {
		f.device.atlas.initialized = true
	}
	if f.fallbackRecorded {
		f.device.images.fallbackReady = true
	}
	return nil
}

// ExportSignal exports the submitted frame's acquire SYNC_FD. The export
// resets the semaphore, which lets the next recording of this frame reuse it.
func (f *Frame) ExportSignal() (int, error) {
	fd, err := f.Signal.Export()
	if err == nil {
		f.signalExported = true
	}
	return fd, err
}

// settleImages runs once the frame is Ready. A submitted frame's serial is
// completed and its upload list forgotten, so a later failed submission can
// never abort textures that submitted work used. A recorded but unsubmitted
// frame is aborted: nothing on the GPU references its new textures.
func (f *Frame) settleImages() {
	if f.device.images == nil || f.imageSerial == 0 {
		return
	}
	if f.submitted {
		delete(f.device.images.inflight, f)
		f.device.images.complete(f.imageSerial)
		f.imageSerial = 0
		f.imageCopies = f.imageCopies[:0]
		return
	}
	f.device.images.abort(f)
}

func (f *Frame) Wait() error {
	if !f.submitted {
		return nil
	}
	return vulkan.Check(f.device.Dispatch.WaitForFences(f.device.Logical, 1, &f.Done, 1, math.MaxUint64))
}
func (f *Frame) Close() {
	if f == nil || f.device == nil {
		return
	}
	_ = f.Wait()
	d := f.device.Dispatch
	if f.Signal != nil {
		f.Signal.Close()
		f.Signal = nil
	}
	if f.Done != 0 {
		d.DestroyFence(f.device.Logical, f.Done, nil)
		f.Done = 0
	}
	if f.View != 0 {
		d.DestroyImageView(f.device.Logical, f.View, nil)
		f.View = 0
	}
	if f.Pool != 0 {
		d.DestroyCommandPool(f.device.Logical, f.Pool, nil)
		f.Pool = 0
	}
	if f.instances != nil {
		f.instances.Close()
		f.instances = nil
	}
	if f.glyphStaging != nil {
		f.glyphStaging.Close()
		f.glyphStaging = nil
	}
	if f.imageStaging != nil {
		f.imageStaging.Close()
		f.imageStaging = nil
	}
	// After Wait: complete submitted work, abort a recorded-but-unsubmitted list.
	f.settleImages()
	if f.Readback != nil {
		f.Readback.Close()
		f.Readback = nil
	}
}
