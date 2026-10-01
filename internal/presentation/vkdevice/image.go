//go:build linux

package vkdevice

import (
	"fmt"
	"math"
	"unsafe"

	"github.com/bnema/purego-vulkan/vulkan"
	"golang.org/x/sys/unix"
)

// Image owns a Vulkan image, dedicated exportable memory and the exported
// DMA-BUF file descriptor. FD stays owned by the Image and is closed by Close;
// a transport that consumes descriptors must be handed a dup. The compositor's
// import keeps its own kernel reference, so Close may run while the compositor
// still holds the buffer, but the GPU must be done with the image first.
type Image struct {
	Image          vulkan.Image
	Memory         vulkan.DeviceMemory
	FD             int // exported DMA-BUF, -1 when none
	Modifier       uint64
	Offset, Stride uint32
	device         *Device
}

func (d *Device) AllocateImage(width, height int32, selected Modifier) (img *Image, err error) {
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid DMA-BUF image dimensions")
	}
	img = &Image{device: d, FD: -1}
	defer func() {
		if err != nil {
			img.Close()
			img = nil
		}
	}()
	modifier := selected.Value
	list := vulkan.ImageDrmFormatModifierListCreateInfoEXT{SType: vulkan.StructureTypeImageDRMFormatModifierListCreateInfoEXT, DrmFormatModifierCount: 1, DrmFormatModifiers: &modifier}
	external := vulkan.ExternalMemoryImageCreateInfo{SType: vulkan.StructureTypeExternalMemoryImageCreateInfo, Next: unsafe.Pointer(&list), HandleTypes: vulkan.ExternalMemoryHandleTypeDMABUFBitEXT}
	ci := vulkan.ImageCreateInfo{SType: vulkan.StructureTypeImageCreateInfo, Next: unsafe.Pointer(&external), ImageType: vulkan.ImageType2d, Format: vulkan.FormatB8g8r8a8Unorm, Extent: vulkan.Extent3D{Width: uint32(width), Height: uint32(height), Depth: 1}, MipLevels: 1, ArrayLayers: 1, Samples: vulkan.SampleCount1Bit, Tiling: vulkan.ImageTilingDRMFormatModifierEXT, Usage: vulkan.ImageUsageColorAttachmentBit | vulkan.ImageUsageTransferSrcBit, SharingMode: vulkan.SharingModeExclusive, InitialLayout: vulkan.ImageLayoutUndefined}
	if err = vulkan.Check(d.Dispatch.CreateImage(d.Logical, &ci, nil, &img.Image)); err != nil {
		return nil, fmt.Errorf("create DRM modifier image: %w", err)
	}
	props := vulkan.ImageDrmFormatModifierPropertiesEXT{SType: vulkan.StructureTypeImageDRMFormatModifierPropertiesEXT}
	if err = vulkan.Check(d.Dispatch.GetImageDrmFormatModifierPropertiesEXT(d.Logical, img.Image, &props)); err != nil {
		return nil, err
	}
	if props.DrmFormatModifier != selected.Value {
		return nil, fmt.Errorf("selected DRM modifier %#x but Vulkan chose %#x", selected.Value, props.DrmFormatModifier)
	}
	img.Modifier = props.DrmFormatModifier
	sub := vulkan.ImageSubresource{AspectMask: vulkan.ImageAspectMemoryPlane0BitEXT}
	var layout vulkan.SubresourceLayout
	d.Dispatch.GetImageSubresourceLayout(d.Logical, img.Image, &sub, &layout)
	if layout.RowPitch > math.MaxUint32 || layout.Offset > math.MaxUint32 || layout.RowPitch == 0 {
		return nil, fmt.Errorf("DMA-BUF plane layout not representable: offset %d stride %d", layout.Offset, layout.RowPitch)
	}
	img.Offset, img.Stride = uint32(layout.Offset), uint32(layout.RowPitch)
	reqInfo := vulkan.ImageMemoryRequirementsInfo2{SType: vulkan.StructureTypeImageMemoryRequirementsInfo2, Image: img.Image}
	reqs := vulkan.MemoryRequirements2{SType: vulkan.StructureTypeMemoryRequirements2}
	d.Dispatch.GetImageMemoryRequirements2(d.Logical, &reqInfo, &reqs)
	var mem vulkan.PhysicalDeviceMemoryProperties
	d.InstanceDispatch.GetPhysicalDeviceMemoryProperties(d.Physical, &mem)
	memoryIndex := uint32(math.MaxUint32)
	for i := uint32(0); i < mem.MemoryTypeCount; i++ {
		if reqs.MemoryRequirements.MemoryTypeBits&(1<<i) != 0 && mem.MemoryTypes[i].PropertyFlags&vulkan.MemoryPropertyDeviceLocalBit != 0 {
			memoryIndex = i
			break
		}
	}
	if memoryIndex == math.MaxUint32 {
		return nil, fmt.Errorf("no device-local DMA-BUF memory type")
	}
	dedicated := vulkan.MemoryDedicatedAllocateInfo{SType: vulkan.StructureTypeMemoryDedicatedAllocateInfo, Image: img.Image}
	export := vulkan.ExportMemoryAllocateInfo{SType: vulkan.StructureTypeExportMemoryAllocateInfo, Next: unsafe.Pointer(&dedicated), HandleTypes: vulkan.ExternalMemoryHandleTypeDMABUFBitEXT}
	allocate := vulkan.MemoryAllocateInfo{SType: vulkan.StructureTypeMemoryAllocateInfo, Next: unsafe.Pointer(&export), AllocationSize: reqs.MemoryRequirements.Size, MemoryTypeIndex: memoryIndex}
	if err = vulkan.Check(d.Dispatch.AllocateMemory(d.Logical, &allocate, nil, &img.Memory)); err != nil {
		return nil, fmt.Errorf("allocate dedicated DMA-BUF memory: %w", err)
	}
	bind := vulkan.BindImageMemoryInfo{SType: vulkan.StructureTypeBindImageMemoryInfo, Image: img.Image, Memory: img.Memory}
	if err = vulkan.Check(d.Dispatch.BindImageMemory2(d.Logical, 1, &bind)); err != nil {
		return nil, fmt.Errorf("bind exportable image: %w", err)
	}
	fdInfo := vulkan.MemoryGetFdInfoKHR{SType: vulkan.StructureTypeMemoryGetFDInfoKHR, Memory: img.Memory, HandleType: vulkan.ExternalMemoryHandleTypeDMABUFBitEXT}
	fd := int32(-1)
	if err = vulkan.Check(d.Dispatch.GetMemoryFdKHR(d.Logical, &fdInfo, &fd)); err != nil {
		return nil, fmt.Errorf("export DMA-BUF: %w", err)
	}
	img.FD = int(fd)
	return img, nil
}

func (img *Image) Close() {
	if img == nil || img.device == nil {
		return
	}
	if img.FD >= 0 {
		_ = unix.Close(img.FD)
		img.FD = -1
	}
	if img.Image != 0 {
		img.device.Dispatch.DestroyImage(img.device.Logical, img.Image, nil)
		img.Image = 0
	}
	if img.Memory != 0 {
		img.device.Dispatch.FreeMemory(img.device.Logical, img.Memory, nil)
		img.Memory = 0
	}
}
