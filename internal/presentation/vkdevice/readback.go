//go:build linux

package vkdevice

import (
	"fmt"
	"image"
	"math"
	"unsafe"

	"github.com/bnema/purego-vulkan/vulkan"
)

// Readback is only allocated for tests. Its transfer copy is
// recorded after drawing and before the acquire semaphore signals; mapping
// waits for the submission fence, never for compositor release.
type Readback struct {
	device        *Device
	Buffer        vulkan.Buffer
	Memory        vulkan.DeviceMemory
	width, height int32
	region        vulkan.BufferImageCopy // reused: bindings heap-allocate pointer arguments
}

func (d *Device) NewReadback(width, height int32) (r *Readback, err error) {
	if width <= 0 || height <= 0 || uint64(width)*uint64(height) > math.MaxInt/4 {
		return nil, fmt.Errorf("invalid readback dimensions %dx%d", width, height)
	}
	r = &Readback{device: d, width: width, height: height}
	defer func() {
		if err != nil {
			r.Close()
		}
	}()
	ci := vulkan.BufferCreateInfo{SType: vulkan.StructureTypeBufferCreateInfo, Size: vulkan.DeviceSize(width) * vulkan.DeviceSize(height) * 4, Usage: vulkan.BufferUsageTransferDstBit, SharingMode: vulkan.SharingModeExclusive}
	if err = vulkan.Check(d.Dispatch.CreateBuffer(d.Logical, &ci, nil, &r.Buffer)); err != nil {
		return nil, err
	}
	var req vulkan.MemoryRequirements
	d.Dispatch.GetBufferMemoryRequirements(d.Logical, r.Buffer, &req)
	var memory vulkan.PhysicalDeviceMemoryProperties
	d.InstanceDispatch.GetPhysicalDeviceMemoryProperties(d.Physical, &memory)
	// Prefer host-cached memory: CPU reads from uncached (write-combined)
	// memory are orders of magnitude slower for window-sized images.
	index := uint32(math.MaxUint32)
	coherent := vulkan.MemoryPropertyFlags(vulkan.MemoryPropertyHostVisibleBit | vulkan.MemoryPropertyHostCoherentBit)
	for _, want := range []vulkan.MemoryPropertyFlags{coherent | vulkan.MemoryPropertyHostCachedBit, coherent} {
		for i := uint32(0); i < memory.MemoryTypeCount && index == math.MaxUint32; i++ {
			if req.MemoryTypeBits&(1<<i) != 0 && memory.MemoryTypes[i].PropertyFlags&want == want {
				index = i
			}
		}
	}
	if index == math.MaxUint32 {
		return nil, fmt.Errorf("no host-coherent readback memory type")
	}
	allocate := vulkan.MemoryAllocateInfo{SType: vulkan.StructureTypeMemoryAllocateInfo, AllocationSize: req.Size, MemoryTypeIndex: index}
	if err = vulkan.Check(d.Dispatch.AllocateMemory(d.Logical, &allocate, nil, &r.Memory)); err != nil {
		return nil, err
	}
	if err = vulkan.Check(d.Dispatch.BindBufferMemory(d.Logical, r.Buffer, r.Memory, 0)); err != nil {
		return nil, err
	}
	return r, nil
}
func (r *Readback) record(cmd vulkan.CommandBuffer, img vulkan.Image) {
	r.region = vulkan.BufferImageCopy{ImageSubresource: vulkan.ImageSubresourceLayers{AspectMask: vulkan.ImageAspectColorBit, LayerCount: 1}, ImageExtent: vulkan.Extent3D{Width: uint32(r.width), Height: uint32(r.height), Depth: 1}}
	r.device.Dispatch.CmdCopyImageToBuffer(cmd, img, vulkan.ImageLayoutTransferSrcOptimal, r.Buffer, 1, &r.region)
}

// Copy returns the completed readback as an owned image whose pixels are still
// in B8G8R8A8 order. It performs one bulk copy; call SwizzleBGRA off the state
// owner before encoding.
func (r *Readback) Copy() (*image.NRGBA, error) {
	var ptr unsafe.Pointer
	size := vulkan.DeviceSize(r.width) * vulkan.DeviceSize(r.height) * 4
	if err := vulkan.Check(r.device.Dispatch.MapMemory(r.device.Logical, r.Memory, 0, size, 0, &ptr)); err != nil {
		return nil, err
	}
	defer r.device.Dispatch.UnmapMemory(r.device.Logical, r.Memory)
	out := image.NewNRGBA(image.Rect(0, 0, int(r.width), int(r.height)))
	copy(out.Pix, unsafe.Slice((*byte)(ptr), int(size)))
	return out, nil
}

// SwizzleBGRA converts B8G8R8A8 pixels to RGBA in place.
func SwizzleBGRA(img *image.NRGBA) {
	p := img.Pix
	for i := 0; i+3 < len(p); i += 4 {
		p[i], p[i+2] = p[i+2], p[i]
	}
}
func (r *Readback) Close() {
	if r == nil || r.device == nil {
		return
	}
	if r.Buffer != 0 {
		r.device.Dispatch.DestroyBuffer(r.device.Logical, r.Buffer, nil)
		r.Buffer = 0
	}
	if r.Memory != 0 {
		r.device.Dispatch.FreeMemory(r.device.Logical, r.Memory, nil)
		r.Memory = 0
	}
}
