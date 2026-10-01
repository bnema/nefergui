//go:build linux

package vkdevice

import (
	"fmt"
	"unsafe"

	"github.com/bnema/purego-vulkan/vulkan"
)

// BinaryFence exports a SYNC_FD after its queue submit, never an OPAQUE_FD.
// Vulkan consumes a successfully imported SYNC_FD; a failed import leaves it
// with the caller. Exported SYNC_FDs belong to the caller until DRM accepts.
type BinaryFence struct {
	Device    *Device
	Semaphore vulkan.Semaphore
	// Reused call arguments: the bindings heap-allocate pointer arguments, and
	// a fence has a single owner that calls these methods one at a time.
	getInfo    vulkan.SemaphoreGetFdInfoKHR
	importInfo vulkan.ImportSemaphoreFdInfoKHR
	fd         int32
}

func (d *Device) NewBinaryFence(export bool) (*BinaryFence, error) {
	ci := vulkan.SemaphoreCreateInfo{SType: vulkan.StructureTypeSemaphoreCreateInfo}
	if export {
		ex := vulkan.ExportSemaphoreCreateInfo{SType: vulkan.StructureTypeExportSemaphoreCreateInfo, HandleTypes: vulkan.ExternalSemaphoreHandleTypeSyncFDBit}
		ci.Next = unsafe.Pointer(&ex)
		var sem vulkan.Semaphore
		if err := vulkan.Check(d.Dispatch.CreateSemaphore(d.Logical, &ci, nil, &sem)); err != nil {
			return nil, fmt.Errorf("create SYNC_FD semaphore: %w", err)
		}
		return &BinaryFence{Device: d, Semaphore: sem}, nil
	}
	var sem vulkan.Semaphore
	if err := vulkan.Check(d.Dispatch.CreateSemaphore(d.Logical, &ci, nil, &sem)); err != nil {
		return nil, err
	}
	return &BinaryFence{Device: d, Semaphore: sem}, nil
}

func (s *BinaryFence) Export() (int, error) {
	s.getInfo = vulkan.SemaphoreGetFdInfoKHR{SType: vulkan.StructureTypeSemaphoreGetFDInfoKHR, Semaphore: s.Semaphore, HandleType: vulkan.ExternalSemaphoreHandleTypeSyncFDBit}
	s.fd = -1
	if err := vulkan.Check(s.Device.Dispatch.GetSemaphoreFdKHR(s.Device.Logical, &s.getInfo, &s.fd)); err != nil {
		return -1, fmt.Errorf("export Vulkan SYNC_FD: %w", err)
	}
	return int(s.fd), nil
}

func (s *BinaryFence) ImportTemporary(fd int) error {
	s.importInfo = vulkan.ImportSemaphoreFdInfoKHR{SType: vulkan.StructureTypeImportSemaphoreFDInfoKHR, Semaphore: s.Semaphore, Flags: vulkan.SemaphoreImportTemporaryBit, HandleType: vulkan.ExternalSemaphoreHandleTypeSyncFDBit, Fd: int32(fd)}
	return vulkan.Check(s.Device.Dispatch.ImportSemaphoreFdKHR(s.Device.Logical, &s.importInfo))
}

func (s *BinaryFence) Close() {
	if s == nil || s.Semaphore == 0 {
		return
	}
	s.Device.Dispatch.DestroySemaphore(s.Device.Logical, s.Semaphore, nil)
	s.Semaphore = 0
}

// SubmitEmpty is a hardware probe for the fence bridge. The production
// renderer must submit its draw command buffer instead of an empty submit.
// The queue is idle before exporting so this test isolates FD interop from
// in-flight rendering; it does not authorize a CPU-wait presentation fallback.
func (d *Device) SubmitEmpty(signal *BinaryFence, wait *BinaryFence) error {
	info := vulkan.SubmitInfo2{SType: vulkan.StructureTypeSubmitInfo2}
	if signal != nil {
		s := vulkan.SemaphoreSubmitInfo{SType: vulkan.StructureTypeSemaphoreSubmitInfo, Semaphore: signal.Semaphore, StageMask: vulkan.PipelineStage2AllCommandsBit}
		info.SignalSemaphoreInfoCount = 1
		info.SignalSemaphoreInfos = &s
	}
	if wait != nil {
		w := vulkan.SemaphoreSubmitInfo{SType: vulkan.StructureTypeSemaphoreSubmitInfo, Semaphore: wait.Semaphore, StageMask: vulkan.PipelineStage2AllCommandsBit}
		info.WaitSemaphoreInfoCount = 1
		info.WaitSemaphoreInfos = &w
	}
	if err := vulkan.Check(d.Dispatch.QueueSubmit2(d.Queue, 1, &info, 0)); err != nil {
		return fmt.Errorf("submit SYNC_FD semaphore: %w", err)
	}
	return vulkan.Check(d.Dispatch.QueueWaitIdle(d.Queue))
}
