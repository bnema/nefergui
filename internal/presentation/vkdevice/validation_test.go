//go:build linux

package vkdevice

import (
	"os"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// Opt-in validation runs with a real render node. A missing layer is reported
// as an explicit skip, never confused with a positive validation result.
func TestValidationLayer(t *testing.T) {
	path := os.Getenv("NEFERGUI_RENDER_NODE")
	if path == "" || os.Getenv("NEFERGUI_VULKAN_PROBE") != "1" {
		t.Skip("set NEFERGUI_VULKAN_PROBE=1 and NEFERGUI_RENDER_NODE=/dev/dri/renderD...")
	}
	t.Setenv("NEFERGUI_VULKAN_VALIDATION", "1")
	var stat unix.Stat_t
	if err := unix.Stat(path, &stat); err != nil {
		t.Fatal(err)
	}
	d, err := Open(DeviceIdentity(uint64(stat.Rdev)))
	if err != nil {
		if strings.Contains(err.Error(), "validation layer VK_LAYER_KHRONOS_validation unavailable") {
			t.Skip(err)
		}
		t.Fatal(err)
	}
	pipeline, err := d.NewRectPipeline(64, 64)
	if err != nil {
		d.Close()
		t.Fatal(err)
	}
	pipeline.Close()
	d.Close()
	if messages := d.Validation.Messages(); len(messages) > 0 {
		t.Fatalf("Vulkan validation errors: %v", messages)
	}
}
