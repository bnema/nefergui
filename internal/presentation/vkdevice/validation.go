//go:build linux

package vkdevice

import (
	"bytes"
	"fmt"
	"os"
	"sync"
	"unsafe"

	"github.com/bnema/purego"
	"github.com/bnema/purego-vulkan/vulkan"
)

const validationLayer = "VK_LAYER_KHRONOS_validation"
const debugExtension = "VK_EXT_debug_utils"

type Validation struct {
	mu        sync.Mutex
	errors    []string
	callback  uintptr
	messenger vulkan.DebugUtilsMessengerEXT
}

func (v *Validation) Messages() []string {
	if v == nil {
		return nil
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return append([]string(nil), v.errors...)
}
func cBytes(b []byte) string { return string(bytes.TrimRight(b, "\x00")) }
func (v *Validation) enabled() (*byte, *byte, error) {
	g := vulkan.Global()
	var n uint32
	if err := vulkan.Check(g.EnumerateInstanceLayerProperties(&n, nil)); err != nil {
		return nil, nil, err
	}
	props := make([]vulkan.LayerProperties, n)
	if n > 0 {
		if err := vulkan.Check(g.EnumerateInstanceLayerProperties(&n, &props[0])); err != nil {
			return nil, nil, err
		}
	}
	found := false
	for _, p := range props {
		if cBytes(p.LayerName[:]) == validationLayer {
			found = true
			break
		}
	}
	if !found {
		return nil, nil, fmt.Errorf("validation layer %s unavailable", validationLayer)
	}
	if err := vulkan.Check(g.EnumerateInstanceExtensionProperties(nil, &n, nil)); err != nil {
		return nil, nil, err
	}
	exts := make([]vulkan.ExtensionProperties, n)
	if n > 0 {
		if err := vulkan.Check(g.EnumerateInstanceExtensionProperties(nil, &n, &exts[0])); err != nil {
			return nil, nil, err
		}
	}
	found = false
	for _, p := range exts {
		if cBytes(p.ExtensionName[:]) == debugExtension {
			found = true
			break
		}
	}
	if !found {
		return nil, nil, fmt.Errorf("instance extension %s unavailable", debugExtension)
	}
	// The backing bytes live until CreateInstance returns.
	return nil, nil, nil
}

func (v *Validation) create(id *vulkan.InstanceDispatch, instance vulkan.Instance) error {
	if !id.HasCreateDebugUtilsMessengerEXT() {
		return fmt.Errorf("VK_EXT_debug_utils messenger entrypoint unavailable")
	}
	v.callback = purego.NewCallback(func(severity vulkan.DebugUtilsMessageSeverityFlagsEXT, _ vulkan.DebugUtilsMessageTypeFlagsEXT, data *vulkan.DebugUtilsMessengerCallbackDataEXT, _ unsafe.Pointer) vulkan.Bool32 {
		if data != nil && data.Message != nil {
			msg := copyCString(data.Message)
			level := "WARNING"
			if severity&vulkan.DebugUtilsMessageSeverityFlagsEXT(vulkan.DebugUtilsMessageSeverityErrorBitEXT) != 0 {
				level = "ERROR"
				v.mu.Lock()
				v.errors = append(v.errors, msg)
				v.mu.Unlock()
			}
			_, _ = fmt.Fprintln(os.Stderr, "NEFERGUI_VULKAN_VALIDATION_"+level+":", msg)
		}
		return 0
	})
	ci := vulkan.DebugUtilsMessengerCreateInfoEXT{SType: vulkan.StructureTypeDebugUtilsMessengerCreateInfoEXT, MessageSeverity: vulkan.DebugUtilsMessageSeverityFlagsEXT(vulkan.DebugUtilsMessageSeverityErrorBitEXT | vulkan.DebugUtilsMessageSeverityWarningBitEXT), MessageType: vulkan.DebugUtilsMessageTypeGeneralBitEXT | vulkan.DebugUtilsMessageTypeValidationBitEXT | vulkan.DebugUtilsMessageTypePerformanceBitEXT, PfnUserCallback: vulkan.PFN_vkDebugUtilsMessengerCallbackEXT(v.callback)}
	if err := vulkan.Check(id.CreateDebugUtilsMessengerEXT(instance, &ci, nil, &v.messenger)); err != nil {
		purego.UnrefCallback(v.callback)
		v.callback = 0
		return err
	}
	return nil
}
func (v *Validation) destroy(id *vulkan.InstanceDispatch, instance vulkan.Instance) {
	if v == nil {
		return
	}
	if v.messenger != 0 {
		id.DestroyDebugUtilsMessengerEXT(instance, v.messenger, nil)
		v.messenger = 0
	}
	if v.callback != 0 {
		purego.UnrefCallback(v.callback)
		v.callback = 0
	}
}
func copyCString(p *byte) string {
	if p == nil {
		return ""
	}
	const max = 4096
	b := make([]byte, 0, 128)
	for i := 0; i < max; i++ {
		c := *(*byte)(unsafe.Add(unsafe.Pointer(p), i))
		if c == 0 {
			break
		}
		b = append(b, c)
	}
	return string(b)
}
