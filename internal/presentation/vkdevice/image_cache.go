//go:build linux

package vkdevice

import (
	"fmt"
	"image"
	"image/draw"
	"math"
	"reflect"
	"unsafe"

	"github.com/bnema/purego-vulkan/vulkan"
)

type gpuImage struct {
	Image   vulkan.Image
	Memory  vulkan.DeviceMemory
	View    vulkan.ImageView
	Texture ImageTexture
}

type imageCache struct {
	device        *Device
	policy        *imageCachePolicy
	Layout        vulkan.DescriptorSetLayout
	Pool          vulkan.DescriptorPool
	Sampler       vulkan.Sampler
	fallback      *gpuImage
	fallbackReady bool
	sets          [imageDescriptorSlots]vulkan.DescriptorSet
	textures      map[*imageEntry]*gpuImage
	serial        uint64
	done          map[uint64]bool
	// inflight holds submitted frames whose image serial is not settled yet.
	// resolve polls them so an idle frame slot cannot stall completion.
	inflight map[*Frame]struct{}
}

func (c *imageCache) Close() {
	if c == nil || c.device == nil {
		return
	}
	for _, t := range c.textures {
		c.destroy(t)
	}
	if c.fallback != nil {
		c.destroy(c.fallback)
		c.fallback = nil
	}
	d := c.device.Dispatch
	if c.Pool != 0 {
		d.DestroyDescriptorPool(c.device.Logical, c.Pool, nil)
		c.Pool = 0
	}
	if c.Layout != 0 {
		d.DestroyDescriptorSetLayout(c.device.Logical, c.Layout, nil)
		c.Layout = 0
	}
	if c.Sampler != 0 {
		d.DestroySampler(c.device.Logical, c.Sampler, nil)
		c.Sampler = 0
	}
}
func (c *imageCache) destroy(t *gpuImage) {
	d := c.device.Dispatch
	if t.View != 0 {
		d.DestroyImageView(c.device.Logical, t.View, nil)
	}
	if t.Image != 0 {
		d.DestroyImage(c.device.Logical, t.Image, nil)
	}
	if t.Memory != 0 {
		d.FreeMemory(c.device.Logical, t.Memory, nil)
	}
}
func (c *imageCache) dispose(entries []*imageEntry) {
	for _, e := range entries {
		if t := c.textures[e]; t != nil {
			c.destroy(t)
			delete(c.textures, e)
		}
	}
}
func (d *Device) newImageCache() (c *imageCache, err error) {
	c = &imageCache{device: d, policy: newImageCachePolicy(), textures: make(map[*imageEntry]*gpuImage), done: make(map[uint64]bool), inflight: make(map[*Frame]struct{})}
	defer func() {
		if err != nil {
			c.Close()
		}
	}()
	binding := vulkan.DescriptorSetLayoutBinding{DescriptorType: vulkan.DescriptorTypeCombinedImageSampler, DescriptorCount: 1, StageFlags: vulkan.ShaderStageFragmentBit}
	li := vulkan.DescriptorSetLayoutCreateInfo{SType: vulkan.StructureTypeDescriptorSetLayoutCreateInfo, BindingCount: 1, Bindings: &binding}
	if err = vulkan.Check(d.Dispatch.CreateDescriptorSetLayout(d.Logical, &li, nil, &c.Layout)); err != nil {
		return nil, err
	}
	// Allocate the full cache budget once. No FreeDescriptorSets binding is needed.
	size := vulkan.DescriptorPoolSize{Type: vulkan.DescriptorTypeCombinedImageSampler, DescriptorCount: imageDescriptorSlots}
	pi := vulkan.DescriptorPoolCreateInfo{SType: vulkan.StructureTypeDescriptorPoolCreateInfo, MaxSets: imageDescriptorSlots, PoolSizeCount: 1, PoolSizes: &size}
	if err = vulkan.Check(d.Dispatch.CreateDescriptorPool(d.Logical, &pi, nil, &c.Pool)); err != nil {
		return nil, err
	}
	for i := range c.sets {
		ai := vulkan.DescriptorSetAllocateInfo{SType: vulkan.StructureTypeDescriptorSetAllocateInfo, DescriptorPool: c.Pool, DescriptorSetCount: 1, SetLayouts: &c.Layout}
		if err = vulkan.Check(d.Dispatch.AllocateDescriptorSets(d.Logical, &ai, &c.sets[i])); err != nil {
			return nil, err
		}
	}
	sampler := vulkan.SamplerCreateInfo{SType: vulkan.StructureTypeSamplerCreateInfo, MagFilter: vulkan.FilterLinear, MinFilter: vulkan.FilterLinear, MipmapMode: vulkan.SamplerMipmapModeNearest, AddressModeU: vulkan.SamplerAddressModeClampToEdge, AddressModeV: vulkan.SamplerAddressModeClampToEdge, AddressModeW: vulkan.SamplerAddressModeClampToEdge}
	if err = vulkan.Check(d.Dispatch.CreateSampler(d.Logical, &sampler, nil, &c.Sampler)); err != nil {
		return nil, err
	}
	c.fallback, err = c.create(image.NewNRGBA(image.Rect(0, 0, 1, 1)), 0)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// complete releases resources only after their final sampling fence is signaled.
func (c *imageCache) complete(serial uint64) {
	if serial == 0 {
		return
	}
	c.done[serial] = true
	for c.done[c.policy.completed+1] {
		delete(c.done, c.policy.completed+1)
		c.policy.completed++
	}
	c.dispose(c.policy.collect())
}

// resolve is invoked after a reusable frame slot has been selected. Upload
// bytes live in that slot's staging buffer until its fence signals.
// On error the frame's serial and new entries are released by abort (the
// caller invokes it for every failure before a successful Submit).
func (c *imageCache) resolve(f *Frame, batches []Batch) ([]Batch, error) {
	// Completion is otherwise observed only when a slot is recorded again; a
	// slot the compositor keeps idle would block the contiguous completed
	// serial and pin every later texture. Poll without waiting.
	if err := c.pollInflight((*Frame).Ready); err != nil {
		return nil, err
	}
	c.serial++
	f.imageSerial = c.serial
	f.imageCopies = f.imageCopies[:0]
	c.dispose(c.policy.begin(c.serial, c.policy.completed))
	result := append([]Batch(nil), batches...)
	var pixels []byte
	// Validate the entire frame before creating or changing any GPU resources.
	// In particular, do not partially record an over-budget frame.
	seen := make(map[image.Image]bool)
	count, bytes := 0, 0
	for _, batch := range result {
		key := batch.Source
		if key == nil {
			continue
		}
		if !reflect.TypeOf(key).Comparable() {
			return nil, fmt.Errorf("image cache: image identity must be comparable")
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		count++
		b := key.Bounds()
		if b.Dx() <= 0 || b.Dy() <= 0 || b.Dx() > maxImageDimension || b.Dy() > maxImageDimension {
			return nil, fmt.Errorf("image cache: invalid dimensions %dx%d (maximum %d)", b.Dx(), b.Dy(), maxImageDimension)
		}
		if e := c.policy.entries[key]; e != nil {
			bytes += e.bytes
		} else {
			bytes += b.Dx() * b.Dy() * 4
		}
	}
	if count > maxImageTextures || bytes > maxImageBytes {
		return nil, fmt.Errorf("image cache: frame exceeds 64 textures/64 MiB budget")
	}
	for i := range result {
		if result[i].Source == nil {
			continue
		}
		key := result[i].Source
		e, retired, err := c.policy.reserve(key)
		c.dispose(retired)
		if err != nil {
			return nil, err
		}
		t := c.textures[e]
		if t == nil {
			// Track the entry before creating GPU objects so abort can drop it.
			f.imageCopies = append(f.imageCopies, imageCopy{entry: e})
			t, err = c.create(key, e.slot)
			if err != nil {
				return nil, err
			}
			c.textures[e] = t
			n := image.NewNRGBA(image.Rect(0, 0, key.Bounds().Dx(), key.Bounds().Dy()))
			draw.Draw(n, n.Bounds(), key, key.Bounds().Min, draw.Src)
			f.imageCopies[len(f.imageCopies)-1] = imageCopy{entry: e, texture: t, offset: vulkan.DeviceSize(len(pixels)), width: uint32(n.Rect.Dx()), height: uint32(n.Rect.Dy())}
			pixels = append(pixels, n.Pix...)
		}
		result[i].Texture = &t.Texture
	}
	if len(pixels) > 0 {
		if err := f.stageImages(pixels); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (c *imageCache) pollInflight(readyFn func(*Frame) (bool, error)) error {
	for g := range c.inflight {
		ready, err := readyFn(g)
		if err != nil {
			return err
		}
		if ready {
			g.settleImages()
		}
	}
	return nil
}

// abort undoes resolve for a frame that will not be submitted: textures
// created for it were never uploaded, so they are destroyed and their slots
// freed at once (no GPU work references them), and its serial is completed so
// later fences can still release retired textures.
func (c *imageCache) abort(f *Frame) {
	for _, cp := range f.imageCopies {
		if t := c.textures[cp.entry]; t != nil {
			c.destroy(t)
			delete(c.textures, cp.entry)
		}
		c.policy.drop(cp.entry)
	}
	f.imageCopies = f.imageCopies[:0]
	c.complete(f.imageSerial)
	f.imageSerial = 0
}

type imageCopy struct {
	entry         *imageEntry
	texture       *gpuImage
	offset        vulkan.DeviceSize
	width, height uint32
}

func (c *imageCache) create(source image.Image, slot int) (t *gpuImage, err error) {
	d := c.device
	t = &gpuImage{Texture: ImageTexture{Set: c.sets[slot]}}
	defer func() {
		if err != nil {
			c.destroy(t)
		}
	}()
	b := source.Bounds()
	ci := vulkan.ImageCreateInfo{SType: vulkan.StructureTypeImageCreateInfo, ImageType: vulkan.ImageType2d, Format: vulkan.FormatR8g8b8a8Unorm, Extent: vulkan.Extent3D{Width: uint32(b.Dx()), Height: uint32(b.Dy()), Depth: 1}, MipLevels: 1, ArrayLayers: 1, Samples: vulkan.SampleCount1Bit, Tiling: vulkan.ImageTilingOptimal, Usage: vulkan.ImageUsageSampledBit | vulkan.ImageUsageTransferDstBit, SharingMode: vulkan.SharingModeExclusive, InitialLayout: vulkan.ImageLayoutUndefined}
	if err = vulkan.Check(d.Dispatch.CreateImage(d.Logical, &ci, nil, &t.Image)); err != nil {
		return nil, err
	}
	var req vulkan.MemoryRequirements
	d.Dispatch.GetImageMemoryRequirements(d.Logical, t.Image, &req)
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
		return nil, fmt.Errorf("no device-local image memory")
	}
	alloc := vulkan.MemoryAllocateInfo{SType: vulkan.StructureTypeMemoryAllocateInfo, AllocationSize: req.Size, MemoryTypeIndex: index}
	if err = vulkan.Check(d.Dispatch.AllocateMemory(d.Logical, &alloc, nil, &t.Memory)); err != nil {
		return nil, err
	}
	if err = vulkan.Check(d.Dispatch.BindImageMemory(d.Logical, t.Image, t.Memory, 0)); err != nil {
		return nil, err
	}
	view := vulkan.ImageViewCreateInfo{SType: vulkan.StructureTypeImageViewCreateInfo, Image: t.Image, ViewType: vulkan.ImageViewType2d, Format: vulkan.FormatR8g8b8a8Unorm, SubresourceRange: vulkan.ImageSubresourceRange{AspectMask: vulkan.ImageAspectColorBit, LevelCount: 1, LayerCount: 1}}
	if err = vulkan.Check(d.Dispatch.CreateImageView(d.Logical, &view, nil, &t.View)); err != nil {
		return nil, err
	}
	info := vulkan.DescriptorImageInfo{Sampler: c.Sampler, ImageView: t.View, ImageLayout: vulkan.ImageLayoutShaderReadOnlyOptimal}
	write := vulkan.WriteDescriptorSet{SType: vulkan.StructureTypeWriteDescriptorSet, DstSet: t.Texture.Set, DescriptorCount: 1, DescriptorType: vulkan.DescriptorTypeCombinedImageSampler, ImageInfo: &info}
	d.Dispatch.UpdateDescriptorSets(d.Logical, 1, &write, 0, nil)
	return t, nil
}

func (f *Frame) stageImages(pixels []byte) error {
	if f.imageStaging == nil || len(pixels) > f.imageStaging.capacity {
		if f.imageStaging != nil {
			f.imageStaging.Close()
		}
		f.imageStaging = &instanceBuffer{device: f.device}
		b := f.imageStaging
		ci := vulkan.BufferCreateInfo{SType: vulkan.StructureTypeBufferCreateInfo, Size: vulkan.DeviceSize(len(pixels)), Usage: vulkan.BufferUsageTransferSrcBit, SharingMode: vulkan.SharingModeExclusive}
		if err := vulkan.Check(f.device.Dispatch.CreateBuffer(f.device.Logical, &ci, nil, &b.Buffer)); err != nil {
			return err
		}
		var req vulkan.MemoryRequirements
		f.device.Dispatch.GetBufferMemoryRequirements(f.device.Logical, b.Buffer, &req)
		var mem vulkan.PhysicalDeviceMemoryProperties
		f.device.InstanceDispatch.GetPhysicalDeviceMemoryProperties(f.device.Physical, &mem)
		want := vulkan.MemoryPropertyFlags(vulkan.MemoryPropertyHostVisibleBit | vulkan.MemoryPropertyHostCoherentBit)
		index := uint32(math.MaxUint32)
		for i := uint32(0); i < mem.MemoryTypeCount; i++ {
			if req.MemoryTypeBits&(1<<i) != 0 && mem.MemoryTypes[i].PropertyFlags&want == want {
				index = i
				break
			}
		}
		if index == math.MaxUint32 {
			return fmt.Errorf("no host-coherent image staging memory")
		}
		ai := vulkan.MemoryAllocateInfo{SType: vulkan.StructureTypeMemoryAllocateInfo, AllocationSize: req.Size, MemoryTypeIndex: index}
		if err := vulkan.Check(f.device.Dispatch.AllocateMemory(f.device.Logical, &ai, nil, &b.Memory)); err != nil {
			return err
		}
		if err := vulkan.Check(f.device.Dispatch.BindBufferMemory(f.device.Logical, b.Buffer, b.Memory, 0)); err != nil {
			return err
		}
		b.capacity = len(pixels)
	}
	var ptr unsafe.Pointer
	if err := vulkan.Check(f.device.Dispatch.MapMemory(f.device.Logical, f.imageStaging.Memory, 0, vulkan.DeviceSize(len(pixels)), 0, &ptr)); err != nil {
		return err
	}
	copy(unsafe.Slice((*byte)(ptr), len(pixels)), pixels)
	f.device.Dispatch.UnmapMemory(f.device.Logical, f.imageStaging.Memory)
	return nil
}

// recordFallback transitions a dedicated, never sampled fallback until the
// first list submission. The shader statically requires set 1 on every draw.
func (f *Frame) recordFallback() {
	c := f.device.images
	f.fallbackRecorded = true
	barrier := vulkan.ImageMemoryBarrier2{SType: vulkan.StructureTypeImageMemoryBarrier2, SrcStageMask: vulkan.PipelineStage2None, DstStageMask: vulkan.PipelineStage2FragmentShaderBit, DstAccessMask: vulkan.Access2ShaderSampledReadBit, OldLayout: vulkan.ImageLayoutUndefined, NewLayout: vulkan.ImageLayoutShaderReadOnlyOptimal, SrcQueueFamilyIndex: ^uint32(0), DstQueueFamilyIndex: ^uint32(0), Image: c.fallback.Image, SubresourceRange: vulkan.ImageSubresourceRange{AspectMask: vulkan.ImageAspectColorBit, LevelCount: 1, LayerCount: 1}}
	dep := vulkan.DependencyInfo{SType: vulkan.StructureTypeDependencyInfo, ImageMemoryBarrierCount: 1, ImageMemoryBarriers: &barrier}
	f.device.Dispatch.CmdPipelineBarrier2(f.Commands, &dep)
}

func (f *Frame) recordImages() {
	d := f.device.Dispatch
	for _, copyInfo := range f.imageCopies {
		t := copyInfo.texture
		barrier := vulkan.ImageMemoryBarrier2{SType: vulkan.StructureTypeImageMemoryBarrier2, SrcStageMask: vulkan.PipelineStage2None, DstStageMask: vulkan.PipelineStage2TransferBit, DstAccessMask: vulkan.Access2TransferWriteBit, OldLayout: vulkan.ImageLayoutUndefined, NewLayout: vulkan.ImageLayoutTransferDstOptimal, SrcQueueFamilyIndex: ^uint32(0), DstQueueFamilyIndex: ^uint32(0), Image: t.Image, SubresourceRange: vulkan.ImageSubresourceRange{AspectMask: vulkan.ImageAspectColorBit, LevelCount: 1, LayerCount: 1}}
		dep := vulkan.DependencyInfo{SType: vulkan.StructureTypeDependencyInfo, ImageMemoryBarrierCount: 1, ImageMemoryBarriers: &barrier}
		d.CmdPipelineBarrier2(f.Commands, &dep)
		region := vulkan.BufferImageCopy{BufferOffset: copyInfo.offset, ImageSubresource: vulkan.ImageSubresourceLayers{AspectMask: vulkan.ImageAspectColorBit, LayerCount: 1}, ImageExtent: vulkan.Extent3D{Width: copyInfo.width, Height: copyInfo.height, Depth: 1}}
		d.CmdCopyBufferToImage(f.Commands, f.imageStaging.Buffer, t.Image, vulkan.ImageLayoutTransferDstOptimal, 1, &region)
		barrier.OldLayout = vulkan.ImageLayoutTransferDstOptimal
		barrier.NewLayout = vulkan.ImageLayoutShaderReadOnlyOptimal
		barrier.SrcStageMask = vulkan.PipelineStage2TransferBit
		barrier.SrcAccessMask = vulkan.Access2TransferWriteBit
		barrier.DstStageMask = vulkan.PipelineStage2FragmentShaderBit
		barrier.DstAccessMask = vulkan.Access2ShaderSampledReadBit
		d.CmdPipelineBarrier2(f.Commands, &dep)
	}
}
