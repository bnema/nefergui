//go:build linux

package vkdevice

import (
	"fmt"
	"math"
	"unsafe"

	"github.com/bnema/purego-vulkan/vulkan"
)

type instanceBuffer struct {
	device   *Device
	Buffer   vulkan.Buffer
	Memory   vulkan.DeviceMemory
	capacity int
}

func (b *instanceBuffer) Close() {
	if b == nil {
		return
	}
	if b.Buffer != 0 {
		b.device.Dispatch.DestroyBuffer(b.device.Logical, b.Buffer, nil)
		b.Buffer = 0
	}
	if b.Memory != 0 {
		b.device.Dispatch.FreeMemory(b.device.Logical, b.Memory, nil)
		b.Memory = 0
	}
}

func (f *Frame) uploadInstances(instances []Instance) error {
	if len(instances) > math.MaxInt/int(unsafe.Sizeof(Instance{})) || len(instances) > math.MaxUint32 {
		return fmt.Errorf("too many instances")
	}
	size := vulkan.DeviceSize(len(instances)) * vulkan.DeviceSize(unsafe.Sizeof(Instance{}))
	if f.instances == nil || len(instances) > f.instances.capacity {
		// record calls this only after Ready; the old buffer cannot be in GPU use.
		if f.instances != nil {
			f.instances.Close()
		}
		f.instances = &instanceBuffer{device: f.device}
		b := f.instances
		ci := vulkan.BufferCreateInfo{SType: vulkan.StructureTypeBufferCreateInfo, Size: size, Usage: vulkan.BufferUsageVertexBufferBit, SharingMode: vulkan.SharingModeExclusive}
		if err := vulkan.Check(f.device.Dispatch.CreateBuffer(f.device.Logical, &ci, nil, &b.Buffer)); err != nil {
			return err
		}
		var req vulkan.MemoryRequirements
		f.device.Dispatch.GetBufferMemoryRequirements(f.device.Logical, b.Buffer, &req)
		var memory vulkan.PhysicalDeviceMemoryProperties
		f.device.InstanceDispatch.GetPhysicalDeviceMemoryProperties(f.device.Physical, &memory)
		want := vulkan.MemoryPropertyFlags(vulkan.MemoryPropertyHostVisibleBit | vulkan.MemoryPropertyHostCoherentBit)
		index := uint32(math.MaxUint32)
		for i := uint32(0); i < memory.MemoryTypeCount; i++ {
			if req.MemoryTypeBits&(1<<i) != 0 && memory.MemoryTypes[i].PropertyFlags&want == want {
				index = i
				break
			}
		}
		if index == math.MaxUint32 {
			return fmt.Errorf("no host-coherent instance memory type")
		}
		alloc := vulkan.MemoryAllocateInfo{SType: vulkan.StructureTypeMemoryAllocateInfo, AllocationSize: req.Size, MemoryTypeIndex: index}
		if err := vulkan.Check(f.device.Dispatch.AllocateMemory(f.device.Logical, &alloc, nil, &b.Memory)); err != nil {
			return err
		}
		if err := vulkan.Check(f.device.Dispatch.BindBufferMemory(f.device.Logical, b.Buffer, b.Memory, 0)); err != nil {
			return err
		}
		b.capacity = len(instances)
	}
	ptr := &f.scratch.mapped
	if err := vulkan.Check(f.device.Dispatch.MapMemory(f.device.Logical, f.instances.Memory, 0, size, 0, ptr)); err != nil {
		return err
	}
	copy(unsafe.Slice((*Instance)(*ptr), len(instances)), instances)
	f.device.Dispatch.UnmapMemory(f.device.Logical, f.instances.Memory)
	return nil
}
