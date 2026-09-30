//go:build linux

package vkdevice

import (
	"fmt"
	"unsafe"

	"github.com/bnema/purego-vulkan/vulkan"
	"github.com/bnema/wlturbo/protocol/linuxdmabuf"
)

// Modifier is the one-plane GPU/compositor common image layout chosen in
// compositor tranche order. XRGB and ARGB both use B8G8R8A8_UNORM on Vulkan.
type Modifier struct {
	DRMFormat uint32
	Value     uint64
}

const (
	XRGB8888 uint32 = 0x34325258
	ARGB8888 uint32 = 0x34325241
)

func ChooseModifier(tranches [][]linuxdmabuf.FormatEntry, transparent bool, supported map[uint64]bool) (Modifier, error) {
	format := XRGB8888
	if transparent {
		format = ARGB8888
	}
	for _, tranche := range tranches {
		for _, entry := range tranche {
			if entry.Format == format && supported[entry.Modifier] {
				return Modifier{format, entry.Modifier}, nil
			}
		}
	}
	return Modifier{}, fmt.Errorf("no one-plane exportable B8G8R8A8 modifier shared with compositor for DRM format %#x", format)
}

// ExportableModifiers checks the exact usage required by drawing and readback;
// the presence of a modifier alone is not sufficient to allocate a DMA-BUF.
func (d *Device) ExportableModifiers() (map[uint64]bool, error) {
	props := vulkan.FormatProperties2{SType: vulkan.StructureTypeFormatProperties2}
	mods := vulkan.DrmFormatModifierPropertiesList2EXT{SType: vulkan.StructureTypeDRMFormatModifierPropertiesList2EXT}
	props.Next = unsafe.Pointer(&mods)
	d.InstanceDispatch.GetPhysicalDeviceFormatProperties2(d.Physical, vulkan.FormatB8g8r8a8Unorm, &props)
	out := make(map[uint64]bool)
	if mods.DrmFormatModifierCount == 0 {
		return out, nil
	}
	list := make([]vulkan.DrmFormatModifierProperties2EXT, mods.DrmFormatModifierCount)
	mods.DrmFormatModifierProperties = &list[0]
	d.InstanceDispatch.GetPhysicalDeviceFormatProperties2(d.Physical, vulkan.FormatB8g8r8a8Unorm, &props)
	for _, m := range list[:mods.DrmFormatModifierCount] {
		features := m.DrmFormatModifierTilingFeatures
		if m.DrmFormatModifierPlaneCount != 1 || features&vulkan.FormatFeature2ColorAttachmentBit == 0 || features&vulkan.FormatFeature2TransferSrcBit == 0 {
			continue
		}
		modifierInfo := vulkan.PhysicalDeviceImageDrmFormatModifierInfoEXT{SType: vulkan.StructureTypePhysicalDeviceImageDRMFormatModifierInfoEXT, DrmFormatModifier: m.DrmFormatModifier, SharingMode: vulkan.SharingModeExclusive}
		externalInfo := vulkan.PhysicalDeviceExternalImageFormatInfo{SType: vulkan.StructureTypePhysicalDeviceExternalImageFormatInfo, Next: unsafe.Pointer(&modifierInfo), HandleType: vulkan.ExternalMemoryHandleTypeDMABUFBitEXT}
		info := vulkan.PhysicalDeviceImageFormatInfo2{SType: vulkan.StructureTypePhysicalDeviceImageFormatInfo2, Next: unsafe.Pointer(&externalInfo), Format: vulkan.FormatB8g8r8a8Unorm, Type: vulkan.ImageType2d, Tiling: vulkan.ImageTilingDRMFormatModifierEXT, Usage: vulkan.ImageUsageColorAttachmentBit | vulkan.ImageUsageTransferSrcBit}
		externalProps := vulkan.ExternalImageFormatProperties{SType: vulkan.StructureTypeExternalImageFormatProperties}
		imageProps := vulkan.ImageFormatProperties2{SType: vulkan.StructureTypeImageFormatProperties2, Next: unsafe.Pointer(&externalProps)}
		r := d.InstanceDispatch.GetPhysicalDeviceImageFormatProperties2(d.Physical, &info, &imageProps)
		if r == vulkan.ErrorFormatNotSupported {
			continue
		}
		if err := vulkan.Check(r); err != nil {
			return nil, fmt.Errorf("query DRM modifier %#x: %w", m.DrmFormatModifier, err)
		}
		if externalProps.ExternalMemoryProperties.ExternalMemoryFeatures&vulkan.ExternalMemoryFeatureExportableBit != 0 {
			out[m.DrmFormatModifier] = true
		}
	}
	return out, nil
}
