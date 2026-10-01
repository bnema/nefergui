//go:build linux

package vkdevice

import (
	"encoding/binary"
	"fmt"
	"image"
	"unsafe"

	"github.com/bnema/nefergui/internal/presentation/shaders"
	"github.com/bnema/purego-vulkan/vulkan"
)

// Instance is a physical-pixel list quad. Color is straight sRGB; the shader
// premultiplies it. Clip uses x/y/width/height, radii are TL/TR/BR/BL.
// KindLayer.x = 6 samples set 1 binding 0 as a 2D image texture.
type Instance struct {
	Bounds [4]float32
	Color  [4]float32
	Clip   [4]float32
	Radii  [4]float32
	// UV stores normalized atlas min/max. KindLayer.x is 1 for glyphs;
	// KindLayer.y selects the array layer. Rectangles keep both zero.
	UV        [4]float32
	KindLayer [4]float32
	Widths    [4]float32    // top, right, bottom, left
	Sides     [4][4]float32 // top, right, bottom, left straight RGBA
	Shape     [4]float32    // original box for outline/shadow
	Shadow    [4]float32    // offset x/y, blur, spread
}

// Batch preserves the painter's order. Non-image runs use set 0 (atlas);
// image draws bind Texture at set 1. Source is borrowed until cache resolution.
// The image's interface value must be comparable and its pixels immutable.
// First indexes the shared instance buffer; Count is the instance count.
type Batch struct {
	First, Count uint32
	Source       image.Image
	Texture      *ImageTexture
}

// ImageTexture owns a GPU image and the descriptor slot assigned by the cache.
// Its lifetime is gated by the last frame fence that sampled it.
type ImageTexture struct {
	Set vulkan.DescriptorSet
}

type Pipeline struct {
	device *Device
	Handle vulkan.Pipeline
	Layout vulkan.PipelineLayout
}

// NewListPipeline creates an instanced, premultiplied-alpha quad pipeline. It
// fixes the Vulkan viewport and scissor to the image size: the pinned Vulkan
// binding does not expose vkCmdSetViewport/Scissor, so resize builds a new
// pipeline rather than recording unsupported dynamic-state calls.
func (d *Device) NewListPipeline(width, height int32) (p *Pipeline, err error) {
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid render area %dx%d", width, height)
	}
	p = &Pipeline{device: d}
	defer func() {
		if err != nil {
			p.Close()
		}
	}()
	if d.atlas == nil {
		d.atlas, err = d.newGlyphAtlas()
		if err != nil {
			return nil, err
		}
	}
	if d.images == nil {
		d.images, err = d.newImageCache()
		if err != nil {
			return nil, err
		}
	}
	rangeInfo := vulkan.PushConstantRange{StageFlags: vulkan.ShaderStageVertexBit | vulkan.ShaderStageFragmentBit, Size: 8}
	// Set 0 samples the atlas; set 1 samples the image descriptor bound for
	// the current image batch.
	layouts := [2]vulkan.DescriptorSetLayout{d.atlas.Layout, d.images.Layout}
	layoutInfo := vulkan.PipelineLayoutCreateInfo{SType: vulkan.StructureTypePipelineLayoutCreateInfo, SetLayoutCount: uint32(len(layouts)), SetLayouts: &layouts[0], PushConstantRangeCount: 1, PushConstantRanges: &rangeInfo}
	if err = vulkan.Check(d.Dispatch.CreatePipelineLayout(d.Logical, &layoutInfo, nil, &p.Layout)); err != nil {
		return nil, fmt.Errorf("create list pipeline layout: %w", err)
	}
	vertex, err := d.shaderModule(shaders.ListVertex)
	if err != nil {
		return nil, err
	}
	defer d.Dispatch.DestroyShaderModule(d.Logical, vertex, nil)
	fragment, err := d.shaderModule(shaders.ListFragment)
	if err != nil {
		return nil, err
	}
	defer d.Dispatch.DestroyShaderModule(d.Logical, fragment, nil)
	main := []byte("main\x00")
	stages := [2]vulkan.PipelineShaderStageCreateInfo{
		{SType: vulkan.StructureTypePipelineShaderStageCreateInfo, Stage: vulkan.ShaderStageVertexBit, Module: vertex, Name: &main[0]},
		{SType: vulkan.StructureTypePipelineShaderStageCreateInfo, Stage: vulkan.ShaderStageFragmentBit, Module: fragment, Name: &main[0]},
	}
	vertexInput := vulkan.PipelineVertexInputStateCreateInfo{SType: vulkan.StructureTypePipelineVertexInputStateCreateInfo}
	var attrs [13]vulkan.VertexInputAttributeDescription
	binding := vulkan.VertexInputBindingDescription{Stride: uint32(unsafe.Sizeof(Instance{})), InputRate: vulkan.VertexInputRateInstance}
	offsets := [13]uint32{uint32(unsafe.Offsetof(Instance{}.Bounds)), uint32(unsafe.Offsetof(Instance{}.Color)), uint32(unsafe.Offsetof(Instance{}.Clip)), uint32(unsafe.Offsetof(Instance{}.Radii)), uint32(unsafe.Offsetof(Instance{}.UV)), uint32(unsafe.Offsetof(Instance{}.KindLayer)), uint32(unsafe.Offsetof(Instance{}.Widths)), uint32(unsafe.Offsetof(Instance{}.Sides)), uint32(unsafe.Offsetof(Instance{}.Sides)) + 16, uint32(unsafe.Offsetof(Instance{}.Sides)) + 32, uint32(unsafe.Offsetof(Instance{}.Sides)) + 48, uint32(unsafe.Offsetof(Instance{}.Shape)), uint32(unsafe.Offsetof(Instance{}.Shadow))}
	for i := range attrs {
		attrs[i] = vulkan.VertexInputAttributeDescription{Location: uint32(i), Format: vulkan.FormatR32g32b32a32Sfloat, Offset: offsets[i]}
	}
	vertexInput.VertexBindingDescriptionCount = 1
	vertexInput.VertexBindingDescriptions = &binding
	vertexInput.VertexAttributeDescriptionCount = uint32(len(attrs))
	vertexInput.VertexAttributeDescriptions = &attrs[0]
	assembly := vulkan.PipelineInputAssemblyStateCreateInfo{SType: vulkan.StructureTypePipelineInputAssemblyStateCreateInfo, Topology: vulkan.PrimitiveTopologyTriangleList}
	viewport := vulkan.Viewport{Width: float32(width), Height: float32(height), MaxDepth: 1}
	scissor := vulkan.Rect2D{Extent: vulkan.Extent2D{Width: uint32(width), Height: uint32(height)}}
	vp := vulkan.PipelineViewportStateCreateInfo{SType: vulkan.StructureTypePipelineViewportStateCreateInfo, ViewportCount: 1, Viewports: &viewport, ScissorCount: 1, Scissors: &scissor}
	raster := vulkan.PipelineRasterizationStateCreateInfo{SType: vulkan.StructureTypePipelineRasterizationStateCreateInfo, PolygonMode: vulkan.PolygonModeFill, CullMode: vulkan.CullModeNone, FrontFace: vulkan.FrontFaceCounterClockwise, LineWidth: 1}
	ms := vulkan.PipelineMultisampleStateCreateInfo{SType: vulkan.StructureTypePipelineMultisampleStateCreateInfo, RasterizationSamples: vulkan.SampleCount1Bit}
	blendAttachment := vulkan.PipelineColorBlendAttachmentState{BlendEnable: 1, SrcColorBlendFactor: vulkan.BlendFactorOne, DstColorBlendFactor: vulkan.BlendFactorOneMinusSrcAlpha, ColorBlendOp: vulkan.BlendOpAdd, SrcAlphaBlendFactor: vulkan.BlendFactorOne, DstAlphaBlendFactor: vulkan.BlendFactorOneMinusSrcAlpha, AlphaBlendOp: vulkan.BlendOpAdd, ColorWriteMask: vulkan.ColorComponentRBit | vulkan.ColorComponentGBit | vulkan.ColorComponentBBit | vulkan.ColorComponentABit}
	blend := vulkan.PipelineColorBlendStateCreateInfo{SType: vulkan.StructureTypePipelineColorBlendStateCreateInfo, AttachmentCount: 1, Attachments: &blendAttachment}
	format := vulkan.Format(vulkan.FormatB8g8r8a8Unorm)
	rendering := vulkan.PipelineRenderingCreateInfo{SType: vulkan.StructureTypePipelineRenderingCreateInfo, ColorAttachmentCount: 1, ColorAttachmentFormats: &format}
	ci := vulkan.GraphicsPipelineCreateInfo{SType: vulkan.StructureTypeGraphicsPipelineCreateInfo, Next: unsafe.Pointer(&rendering), StageCount: 2, Stages: &stages[0], VertexInputState: &vertexInput, InputAssemblyState: &assembly, ViewportState: &vp, RasterizationState: &raster, MultisampleState: &ms, ColorBlendState: &blend, Layout: p.Layout}
	if err = vulkan.Check(d.Dispatch.CreateGraphicsPipelines(d.Logical, 0, 1, &ci, nil, &p.Handle)); err != nil {
		return nil, fmt.Errorf("create list pipeline with dynamic rendering: %w", err)
	}
	return p, nil
}

func (d *Device) shaderModule(data []byte) (vulkan.ShaderModule, error) {
	if len(data) < 20 || len(data)%4 != 0 || binary.LittleEndian.Uint32(data) != 0x07230203 {
		return 0, fmt.Errorf("invalid embedded SPIR-V")
	}
	words := make([]uint32, len(data)/4)
	for i := range words {
		words[i] = binary.LittleEndian.Uint32(data[i*4:])
	}
	ci := vulkan.ShaderModuleCreateInfo{SType: vulkan.StructureTypeShaderModuleCreateInfo, CodeSize: uintptr(len(data)), Code: &words[0]}
	var module vulkan.ShaderModule
	if err := vulkan.Check(d.Dispatch.CreateShaderModule(d.Logical, &ci, nil, &module)); err != nil {
		return 0, err
	}
	return module, nil
}

func (p *Pipeline) Close() {
	if p == nil || p.device == nil {
		return
	}
	if p.Handle != 0 {
		p.device.Dispatch.DestroyPipeline(p.device.Logical, p.Handle, nil)
		p.Handle = 0
	}
	if p.Layout != 0 {
		p.device.Dispatch.DestroyPipelineLayout(p.device.Logical, p.Layout, nil)
		p.Layout = 0
	}
}
