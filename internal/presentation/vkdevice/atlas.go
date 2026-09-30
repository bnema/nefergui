//go:build linux

package vkdevice

import (
	"fmt"
	"math"
	"unsafe"

	"github.com/bnema/nefergui/internal/text"
	"github.com/bnema/purego-vulkan/vulkan"
)

// AtlasSize and AtlasPages are shared with the CPU preparer at the session boundary.
const AtlasSize = 1024
const AtlasPages = 4

type glyphAtlas struct {
	device      *Device
	Image       vulkan.Image
	Memory      vulkan.DeviceMemory
	View        vulkan.ImageView
	Sampler     vulkan.Sampler
	Layout      vulkan.DescriptorSetLayout
	Pool        vulkan.DescriptorPool
	Set         vulkan.DescriptorSet
	initialized bool
}

func (a *glyphAtlas) Close() {
	if a == nil || a.device == nil {
		return
	}
	d := a.device.Dispatch
	if a.Pool != 0 {
		d.DestroyDescriptorPool(a.device.Logical, a.Pool, nil)
		a.Pool = 0
	}
	if a.Layout != 0 {
		d.DestroyDescriptorSetLayout(a.device.Logical, a.Layout, nil)
		a.Layout = 0
	}
	if a.Sampler != 0 {
		d.DestroySampler(a.device.Logical, a.Sampler, nil)
		a.Sampler = 0
	}
	if a.View != 0 {
		d.DestroyImageView(a.device.Logical, a.View, nil)
		a.View = 0
	}
	if a.Image != 0 {
		d.DestroyImage(a.device.Logical, a.Image, nil)
		a.Image = 0
	}
	if a.Memory != 0 {
		d.FreeMemory(a.device.Logical, a.Memory, nil)
		a.Memory = 0
	}
}

func (d *Device) newGlyphAtlas() (a *glyphAtlas, err error) {
	a = &glyphAtlas{device: d}
	defer func() {
		if err != nil {
			a.Close()
		}
	}()
	ci := vulkan.ImageCreateInfo{SType: vulkan.StructureTypeImageCreateInfo, ImageType: vulkan.ImageType2d, Format: vulkan.FormatR8Unorm, Extent: vulkan.Extent3D{Width: AtlasSize, Height: AtlasSize, Depth: 1}, MipLevels: 1, ArrayLayers: AtlasPages, Samples: vulkan.SampleCount1Bit, Tiling: vulkan.ImageTilingOptimal, Usage: vulkan.ImageUsageSampledBit | vulkan.ImageUsageTransferDstBit, SharingMode: vulkan.SharingModeExclusive, InitialLayout: vulkan.ImageLayoutUndefined}
	if err = vulkan.Check(d.Dispatch.CreateImage(d.Logical, &ci, nil, &a.Image)); err != nil {
		return nil, fmt.Errorf("create glyph atlas: %w", err)
	}
	var req vulkan.MemoryRequirements
	d.Dispatch.GetImageMemoryRequirements(d.Logical, a.Image, &req)
	var mem vulkan.PhysicalDeviceMemoryProperties
	d.InstanceDispatch.GetPhysicalDeviceMemoryProperties(d.Physical, &mem)
	index := uint32(math.MaxUint32)
	for i := uint32(0); i < mem.MemoryTypeCount; i++ {
		if req.MemoryTypeBits&(1<<i) != 0 && mem.MemoryTypes[i].PropertyFlags&vulkan.MemoryPropertyDeviceLocalBit != 0 {
			index = i
			break
		}
	}
	if index == math.MaxUint32 {
		return nil, fmt.Errorf("no device-local atlas memory")
	}
	alloc := vulkan.MemoryAllocateInfo{SType: vulkan.StructureTypeMemoryAllocateInfo, AllocationSize: req.Size, MemoryTypeIndex: index}
	if err = vulkan.Check(d.Dispatch.AllocateMemory(d.Logical, &alloc, nil, &a.Memory)); err != nil {
		return nil, err
	}
	if err = vulkan.Check(d.Dispatch.BindImageMemory(d.Logical, a.Image, a.Memory, 0)); err != nil {
		return nil, err
	}
	view := vulkan.ImageViewCreateInfo{SType: vulkan.StructureTypeImageViewCreateInfo, Image: a.Image, ViewType: vulkan.ImageViewType2dArray, Format: vulkan.FormatR8Unorm, SubresourceRange: vulkan.ImageSubresourceRange{AspectMask: vulkan.ImageAspectColorBit, LevelCount: 1, LayerCount: AtlasPages}}
	if err = vulkan.Check(d.Dispatch.CreateImageView(d.Logical, &view, nil, &a.View)); err != nil {
		return nil, err
	}
	sampler := vulkan.SamplerCreateInfo{SType: vulkan.StructureTypeSamplerCreateInfo, MagFilter: vulkan.FilterNearest, MinFilter: vulkan.FilterNearest, MipmapMode: vulkan.SamplerMipmapModeNearest, AddressModeU: vulkan.SamplerAddressModeClampToEdge, AddressModeV: vulkan.SamplerAddressModeClampToEdge, AddressModeW: vulkan.SamplerAddressModeClampToEdge}
	if err = vulkan.Check(d.Dispatch.CreateSampler(d.Logical, &sampler, nil, &a.Sampler)); err != nil {
		return nil, err
	}
	binding := vulkan.DescriptorSetLayoutBinding{DescriptorType: vulkan.DescriptorTypeCombinedImageSampler, DescriptorCount: 1, StageFlags: vulkan.ShaderStageFragmentBit}
	li := vulkan.DescriptorSetLayoutCreateInfo{SType: vulkan.StructureTypeDescriptorSetLayoutCreateInfo, BindingCount: 1, Bindings: &binding}
	if err = vulkan.Check(d.Dispatch.CreateDescriptorSetLayout(d.Logical, &li, nil, &a.Layout)); err != nil {
		return nil, err
	}
	poolSize := vulkan.DescriptorPoolSize{Type: vulkan.DescriptorTypeCombinedImageSampler, DescriptorCount: 1}
	pi := vulkan.DescriptorPoolCreateInfo{SType: vulkan.StructureTypeDescriptorPoolCreateInfo, MaxSets: 1, PoolSizeCount: 1, PoolSizes: &poolSize}
	if err = vulkan.Check(d.Dispatch.CreateDescriptorPool(d.Logical, &pi, nil, &a.Pool)); err != nil {
		return nil, err
	}
	ai := vulkan.DescriptorSetAllocateInfo{SType: vulkan.StructureTypeDescriptorSetAllocateInfo, DescriptorPool: a.Pool, DescriptorSetCount: 1, SetLayouts: &a.Layout}
	if err = vulkan.Check(d.Dispatch.AllocateDescriptorSets(d.Logical, &ai, &a.Set)); err != nil {
		return nil, err
	}
	info := vulkan.DescriptorImageInfo{Sampler: a.Sampler, ImageView: a.View, ImageLayout: vulkan.ImageLayoutShaderReadOnlyOptimal}
	write := vulkan.WriteDescriptorSet{SType: vulkan.StructureTypeWriteDescriptorSet, DstSet: a.Set, DescriptorCount: 1, DescriptorType: vulkan.DescriptorTypeCombinedImageSampler, ImageInfo: &info}
	d.Dispatch.UpdateDescriptorSets(d.Logical, 1, &write, 0, nil)
	return a, nil
}

// recordUploads uses one queue for all frame slots. The FRAGMENT read -> TRANSFER
// write dependency waits for prior submissions sampling even evicted pages; the
// reverse dependency makes new pixels visible to this frame's fragment shader.
func (a *glyphAtlas) recordUploads(cmd vulkan.CommandBuffer, staging vulkan.Buffer, uploads []text.Upload) {
	d := a.device.Dispatch
	sub := vulkan.ImageSubresourceRange{AspectMask: vulkan.ImageAspectColorBit, LevelCount: 1, LayerCount: AtlasPages}
	barrier := vulkan.ImageMemoryBarrier2{SType: vulkan.StructureTypeImageMemoryBarrier2, SrcQueueFamilyIndex: ^uint32(0), DstQueueFamilyIndex: ^uint32(0), Image: a.Image, SubresourceRange: sub, OldLayout: vulkan.ImageLayoutShaderReadOnlyOptimal, NewLayout: vulkan.ImageLayoutTransferDstOptimal, SrcStageMask: vulkan.PipelineStage2FragmentShaderBit, SrcAccessMask: vulkan.Access2ShaderSampledReadBit, DstStageMask: vulkan.PipelineStage2TransferBit, DstAccessMask: vulkan.Access2TransferWriteBit}
	dep := vulkan.DependencyInfo{SType: vulkan.StructureTypeDependencyInfo, ImageMemoryBarrierCount: 1, ImageMemoryBarriers: &barrier}
	if !a.initialized {
		barrier.OldLayout = vulkan.ImageLayoutUndefined
		barrier.SrcStageMask = vulkan.PipelineStage2None
		barrier.SrcAccessMask = 0
	}
	if !a.initialized || len(uploads) > 0 {
		d.CmdPipelineBarrier2(cmd, &dep)
	}
	if !a.initialized {
		clear := vulkan.ClearColorValue{}
		d.CmdClearColorImage(cmd, a.Image, vulkan.ImageLayoutTransferDstOptimal, &clear, 1, &sub)
		if len(uploads) > 0 {
			// Clear and copy are both transfer writes; order overlapping regions.
			barrier.OldLayout = vulkan.ImageLayoutTransferDstOptimal
			barrier.NewLayout = vulkan.ImageLayoutTransferDstOptimal
			barrier.SrcStageMask = vulkan.PipelineStage2TransferBit
			barrier.SrcAccessMask = vulkan.Access2TransferWriteBit
			barrier.DstStageMask = vulkan.PipelineStage2TransferBit
			barrier.DstAccessMask = vulkan.Access2TransferWriteBit
			d.CmdPipelineBarrier2(cmd, &dep)
		}
	}
	offset := vulkan.DeviceSize(0)
	for _, u := range uploads {
		region := vulkan.BufferImageCopy{BufferOffset: offset, ImageSubresource: vulkan.ImageSubresourceLayers{AspectMask: vulkan.ImageAspectColorBit, BaseArrayLayer: uint32(u.Page), LayerCount: 1}, ImageOffset: vulkan.Offset3D{X: int32(u.Rect.Min.X), Y: int32(u.Rect.Min.Y)}, ImageExtent: vulkan.Extent3D{Width: uint32(u.Rect.Dx()), Height: uint32(u.Rect.Dy()), Depth: 1}}
		d.CmdCopyBufferToImage(cmd, staging, a.Image, vulkan.ImageLayoutTransferDstOptimal, 1, &region)
		offset += vulkan.DeviceSize(len(u.Bytes))
	}
	if !a.initialized || len(uploads) > 0 {
		barrier.OldLayout = vulkan.ImageLayoutTransferDstOptimal
		barrier.NewLayout = vulkan.ImageLayoutShaderReadOnlyOptimal
		barrier.SrcStageMask = vulkan.PipelineStage2TransferBit
		barrier.SrcAccessMask = vulkan.Access2TransferWriteBit
		barrier.DstStageMask = vulkan.PipelineStage2FragmentShaderBit
		barrier.DstAccessMask = vulkan.Access2ShaderSampledReadBit
		d.CmdPipelineBarrier2(cmd, &dep)
	}
}

func (f *Frame) stageGlyphs(uploads []text.Upload) error {
	if len(uploads) == 0 {
		return nil
	}
	total := 0
	for _, u := range uploads {
		if u.Page < 0 || u.Page >= AtlasPages || u.Rect.Min.X < 0 || u.Rect.Min.Y < 0 || u.Rect.Max.X > AtlasSize || u.Rect.Max.Y > AtlasSize || u.Rect.Empty() || len(u.Bytes) != u.Rect.Dx()*u.Rect.Dy() || len(u.Bytes) > math.MaxInt-total {
			return fmt.Errorf("invalid atlas upload")
		}
		total += len(u.Bytes)
	}
	if f.glyphStaging == nil || total > f.glyphStaging.capacity {
		if f.glyphStaging != nil {
			f.glyphStaging.Close()
		}
		f.glyphStaging = &instanceBuffer{device: f.device}
		b := f.glyphStaging
		ci := vulkan.BufferCreateInfo{SType: vulkan.StructureTypeBufferCreateInfo, Size: vulkan.DeviceSize(total), Usage: vulkan.BufferUsageTransferSrcBit, SharingMode: vulkan.SharingModeExclusive}
		if err := vulkan.Check(f.device.Dispatch.CreateBuffer(f.device.Logical, &ci, nil, &b.Buffer)); err != nil {
			return err
		}
		var req vulkan.MemoryRequirements
		f.device.Dispatch.GetBufferMemoryRequirements(f.device.Logical, b.Buffer, &req)
		var mem vulkan.PhysicalDeviceMemoryProperties
		f.device.InstanceDispatch.GetPhysicalDeviceMemoryProperties(f.device.Physical, &mem)
		index := uint32(math.MaxUint32)
		want := vulkan.MemoryPropertyFlags(vulkan.MemoryPropertyHostVisibleBit | vulkan.MemoryPropertyHostCoherentBit)
		for i := uint32(0); i < mem.MemoryTypeCount; i++ {
			if req.MemoryTypeBits&(1<<i) != 0 && mem.MemoryTypes[i].PropertyFlags&want == want {
				index = i
				break
			}
		}
		if index == math.MaxUint32 {
			return fmt.Errorf("no host-coherent atlas staging memory")
		}
		ai := vulkan.MemoryAllocateInfo{SType: vulkan.StructureTypeMemoryAllocateInfo, AllocationSize: req.Size, MemoryTypeIndex: index}
		if err := vulkan.Check(f.device.Dispatch.AllocateMemory(f.device.Logical, &ai, nil, &b.Memory)); err != nil {
			return err
		}
		if err := vulkan.Check(f.device.Dispatch.BindBufferMemory(f.device.Logical, b.Buffer, b.Memory, 0)); err != nil {
			return err
		}
		b.capacity = total
	}
	var ptr unsafe.Pointer
	if err := vulkan.Check(f.device.Dispatch.MapMemory(f.device.Logical, f.glyphStaging.Memory, 0, vulkan.DeviceSize(total), 0, &ptr)); err != nil {
		return err
	}
	buf := unsafe.Slice((*byte)(ptr), total)
	offset := 0
	for _, u := range uploads {
		copy(buf[offset:], u.Bytes)
		offset += len(u.Bytes)
	}
	f.device.Dispatch.UnmapMemory(f.device.Logical, f.glyphStaging.Memory)
	return nil
}
