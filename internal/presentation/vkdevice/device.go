//go:build linux

// Package vkdevice selects a Vulkan graphics device by compositor-advertised
// DRM dev_t. It rejects cross-GPU fallback rather than silently copying images.
package vkdevice

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"github.com/bnema/purego-vulkan/vulkan"
)

// Identity uses the Linux gnu_dev_major/minor dev_t encoding. A Wayland
// main_device is an 8-byte native-endian dev_t, not a Vulkan device index.
type Identity struct{ Major, Minor uint32 }

func DecodeDevice(device []byte) (Identity, error) {
	if len(device) != 8 {
		return Identity{}, fmt.Errorf("invalid main_device dev_t length %d", len(device))
	}
	d := binary.NativeEndian.Uint64(device)
	return Identity{Major: uint32((d >> 8) & 0xfff), Minor: uint32((d & 0xff) | ((d >> 12) & 0xffffff00))}, nil
}
func DeviceIdentity(device uint64) Identity {
	return Identity{Major: uint32((device >> 8) & 0xfff), Minor: uint32((device & 0xff) | ((device >> 12) & 0xffffff00))}
}
func Matches(main Identity, primary, render Identity, hasPrimary, hasRender bool) bool {
	return (hasPrimary && main == primary) || (hasRender && main == render)
}

// RequiredExtensions are mandatory on the matched device. Vulkan 1.3
// supplies dynamic rendering and synchronization2 as core features.
var RequiredExtensions = []string{
	"VK_EXT_physical_device_drm",
	"VK_KHR_external_memory_fd",
	"VK_EXT_external_memory_dma_buf",
	"VK_EXT_image_drm_format_modifier",
	"VK_KHR_external_semaphore_fd",
}

type Device struct {
	Instance         vulkan.Instance
	InstanceDispatch *vulkan.InstanceDispatch
	Physical         vulkan.PhysicalDevice
	Logical          vulkan.Device
	Dispatch         *vulkan.DeviceDispatch
	Queue            vulkan.Queue
	QueueFamily      uint32
	Render           Identity
	Validation       *Validation
	atlas            *glyphAtlas
	images           *imageCache
}

func Open(main Identity) (_ *Device, err error) {
	if err = vulkan.Init(); err != nil {
		return nil, err
	}
	app := append([]byte("NeferGUI"), 0)
	ai := vulkan.ApplicationInfo{SType: vulkan.StructureTypeApplicationInfo, ApplicationName: &app[0], ApiVersion: (1 << 22) | (3 << 12)}
	ci := vulkan.InstanceCreateInfo{SType: vulkan.StructureTypeInstanceCreateInfo, ApplicationInfo: &ai}
	var v *Validation
	var layer, extension []byte
	var layerPtr, extensionPtr *byte
	if os.Getenv("NEFERGUI_VULKAN_VALIDATION") == "1" {
		v = &Validation{}
		if _, _, err = v.enabled(); err != nil {
			return nil, err
		}
		layer = append([]byte(validationLayer), 0)
		extension = append([]byte(debugExtension), 0)
		layerPtr, extensionPtr = &layer[0], &extension[0]
		ci.EnabledLayerCount = 1
		ci.PpEnabledLayerNames = &layerPtr
		ci.EnabledExtensionCount = 1
		ci.PpEnabledExtensionNames = &extensionPtr
	}
	var instance vulkan.Instance
	if err = vulkan.Check(vulkan.Global().CreateInstance(&ci, nil, &instance)); err != nil {
		return nil, fmt.Errorf("create Vulkan 1.3 instance: %w", err)
	}
	id, e := vulkan.LoadInstanceDispatch(instance)
	if e != nil {
		vulkan.VkDestroyInstance(instance, nil)
		return nil, e
	}
	d := &Device{Instance: instance, InstanceDispatch: id, Validation: v}
	if v != nil {
		if e = v.create(id, instance); e != nil {
			d.Close()
			return nil, e
		}
	}
	defer func() {
		if err != nil {
			d.Close()
		}
	}()
	var count uint32
	if err = vulkan.Check(id.EnumeratePhysicalDevices(instance, &count, nil)); err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, fmt.Errorf("no Vulkan physical devices; need DRM main_device %d:%d", main.Major, main.Minor)
	}
	physical := make([]vulkan.PhysicalDevice, count)
	if err = vulkan.Check(id.EnumeratePhysicalDevices(instance, &count, &physical[0])); err != nil {
		return nil, err
	}
	for _, candidate := range physical[:count] {
		exts, e := extensions(id, candidate)
		if e != nil {
			return nil, e
		}
		if !exts["VK_EXT_physical_device_drm"] {
			continue
		}
		props := vulkan.PhysicalDeviceProperties2{SType: vulkan.StructureTypePhysicalDeviceProperties2}
		drm := vulkan.PhysicalDeviceDrmPropertiesEXT{SType: vulkan.StructureTypePhysicalDeviceDRMPropertiesEXT}
		props.Next = unsafe.Pointer(&drm)
		id.GetPhysicalDeviceProperties2(candidate, &props)
		if !Matches(main, Identity{uint32(drm.PrimaryMajor), uint32(drm.PrimaryMinor)}, Identity{uint32(drm.RenderMajor), uint32(drm.RenderMinor)}, drm.HasPrimary != 0, drm.HasRender != 0) {
			continue
		}
		if drm.HasRender == 0 {
			return nil, fmt.Errorf("vulkan device matching main_device %d:%d has no render node", main.Major, main.Minor)
		}
		for _, required := range RequiredExtensions {
			if !exts[required] {
				return nil, fmt.Errorf("matching Vulkan device missing %s", required)
			}
		}
		d.Physical = candidate
		d.Render = Identity{uint32(drm.RenderMajor), uint32(drm.RenderMinor)}
		break
	}
	if d.Physical == 0 {
		return nil, fmt.Errorf("no Vulkan DRM device matches linux-dmabuf main_device %d:%d (no cross-GPU fallback)", main.Major, main.Minor)
	}
	features := vulkan.PhysicalDeviceFeatures2{SType: vulkan.StructureTypePhysicalDeviceFeatures2}
	timeline := vulkan.PhysicalDeviceTimelineSemaphoreFeatures{SType: vulkan.StructureTypePhysicalDeviceTimelineSemaphoreFeatures}
	sync2 := vulkan.PhysicalDeviceSynchronization2Features{SType: vulkan.StructureTypePhysicalDeviceSynchronization2Features}
	dynamic := vulkan.PhysicalDeviceDynamicRenderingFeatures{SType: vulkan.StructureTypePhysicalDeviceDynamicRenderingFeatures}
	features.Next = unsafe.Pointer(&timeline)
	timeline.Next = unsafe.Pointer(&sync2)
	sync2.Next = unsafe.Pointer(&dynamic)
	id.GetPhysicalDeviceFeatures2(d.Physical, &features)
	if timeline.TimelineSemaphore == 0 || sync2.Synchronization2 == 0 || dynamic.DynamicRendering == 0 {
		return nil, fmt.Errorf("matching Vulkan device missing timeline/synchronization2/dynamic rendering feature")
	}
	var families uint32
	id.GetPhysicalDeviceQueueFamilyProperties(d.Physical, &families, nil)
	if families == 0 {
		return nil, fmt.Errorf("matching Vulkan device has no graphics queue")
	}
	queues := make([]vulkan.QueueFamilyProperties, families)
	id.GetPhysicalDeviceQueueFamilyProperties(d.Physical, &families, &queues[0])
	found := false
	for i, q := range queues[:families] {
		if q.QueueCount != 0 && q.QueueFlags&vulkan.QueueGraphicsBit != 0 {
			d.QueueFamily = uint32(i)
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("matching Vulkan device has no graphics queue")
	}
	priority := float32(1)
	qci := vulkan.DeviceQueueCreateInfo{SType: vulkan.StructureTypeDeviceQueueCreateInfo, QueueFamilyIndex: d.QueueFamily, QueueCount: 1, QueuePriorities: &priority}
	// Enable only the extensions actually used; core 1.3 features still need
	// their feature-chain Boolean set explicitly at vkCreateDevice.
	names := make([][]byte, 0, len(RequiredExtensions)-1)
	ptrs := make([]*byte, 0, len(RequiredExtensions)-1)
	for _, name := range RequiredExtensions {
		if name == "VK_EXT_physical_device_drm" {
			continue
		}
		names = append(names, append([]byte(name), 0))
		ptrs = append(ptrs, &names[len(names)-1][0])
	}
	dci := vulkan.DeviceCreateInfo{SType: vulkan.StructureTypeDeviceCreateInfo, Next: unsafe.Pointer(&timeline), QueueCreateInfoCount: 1, QueueCreateInfos: &qci, EnabledExtensionCount: uint32(len(ptrs)), PpEnabledExtensionNames: &ptrs[0]}
	if err = vulkan.Check(id.CreateDevice(d.Physical, &dci, nil, &d.Logical)); err != nil {
		return nil, fmt.Errorf("create matched Vulkan device: %w", err)
	}
	d.Dispatch, err = vulkan.LoadDeviceDispatch(id, d.Logical)
	if err != nil {
		vulkan.VkDestroyDevice(d.Logical, nil)
		d.Logical = 0
		return nil, err
	}
	d.Dispatch.GetDeviceQueue(d.Logical, d.QueueFamily, 0, &d.Queue)
	runtime.KeepAlive(app)
	runtime.KeepAlive(layer)
	runtime.KeepAlive(extension)
	runtime.KeepAlive(names)
	runtime.KeepAlive(ptrs)
	return d, nil
}

func (d *Device) Close() {
	if d == nil {
		return
	}
	if d.Logical != 0 {
		if d.Dispatch != nil {
			_ = vulkan.Check(d.Dispatch.DeviceWaitIdle(d.Logical))
			if d.atlas != nil {
				d.atlas.Close()
				d.atlas = nil
			}
			if d.images != nil {
				d.images.Close()
				d.images = nil
			}
			d.Dispatch.DestroyDevice(d.Logical, nil)
		}
		d.Logical = 0
	}
	if d.Instance != 0 {
		if d.Validation != nil {
			d.Validation.destroy(d.InstanceDispatch, d.Instance)
		}
		d.InstanceDispatch.DestroyInstance(d.Instance, nil)
		d.Instance = 0
	}
}

func extensions(id *vulkan.InstanceDispatch, physical vulkan.PhysicalDevice) (map[string]bool, error) {
	var count uint32
	if err := vulkan.Check(id.EnumerateDeviceExtensionProperties(physical, nil, &count, nil)); err != nil {
		return nil, err
	}
	found := map[string]bool{}
	if count == 0 {
		return found, nil
	}
	props := make([]vulkan.ExtensionProperties, count)
	if err := vulkan.Check(id.EnumerateDeviceExtensionProperties(physical, nil, &count, &props[0])); err != nil {
		return nil, err
	}
	for _, p := range props[:count] {
		found[string(bytes.TrimRight(p.ExtensionName[:], "\x00"))] = true
	}
	return found, nil
}
