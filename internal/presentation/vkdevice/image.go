//go:build linux

package vkdevice

import (
	"fmt"
	"math"
	"unsafe"

	"github.com/bnema/purego-vulkan/vulkan"
	"github.com/bnema/wlturbo/protocol/core"
	"github.com/bnema/wlturbo/protocol/linuxdmabuf"
	"golang.org/x/sys/unix"
)

// Image owns a Vulkan image, dedicated exportable memory and its Wayland buffer.
// Close must only be called after both wl_buffer.release and the compositor's
// release timeline point, or for an image never attached to a surface.
type Image struct {
	Image          vulkan.Image
	Memory         vulkan.DeviceMemory
	Buffer         *core.Buffer
	Modifier       uint64
	Offset, Stride uint32
	device         *Device
}

func (d *Device) AllocateImage(width, height int32, selected Modifier, dmabuf *linuxdmabuf.LinuxDmabuf) (img *Image, err error) {
	if width <= 0 || height <= 0 || dmabuf == nil {
		return nil, fmt.Errorf("invalid DMA-BUF image dimensions or manager")
	}
	img = &Image{device: d}
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
	// WLTurbo consumes fd only when its complete Add request succeeds. On a
	// failed send Vulkan still owns the image and we still own the exported FD.
	owned := true
	defer func() {
		if owned {
			_ = unix.Close(int(fd))
		}
	}()
	params, e := dmabuf.CreateParams()
	if e != nil {
		return nil, e
	}
	defer params.Destroy()
	if err = params.Add(int(fd), 0, img.Offset, img.Stride, uint32(img.Modifier>>32), uint32(img.Modifier)); err != nil {
		return nil, err
	}
	owned = false
	img.Buffer, err = params.CreateImmed(width, height, selected.DRMFormat, 0)
	if err != nil {
		return nil, err
	}
	return img, nil
}

func (img *Image) Close() {
	if img == nil || img.device == nil {
		return
	}
	if img.Buffer != nil {
		_ = img.Buffer.Destroy()
		img.Buffer = nil
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
