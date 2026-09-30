//go:build linux

package vkdevice

import (
	"encoding/binary"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDeviceIdentity(t *testing.T) {
	for _, tc := range []struct{ major, minor uint32 }{{0, 0}, {226, 128}, {226, 0}, {4095, 1 << 20}, {2, 255}} {
		// Linux makedev as used by x/sys/unix and stat on /dev/dri.
		raw := unix.Mkdev(tc.major, tc.minor)
		b := make([]byte, 8)
		binary.NativeEndian.PutUint64(b, raw)
		got, err := DecodeDevice(b)
		if err != nil || got != (Identity{tc.major, tc.minor}) || got != DeviceIdentity(raw) {
			t.Fatalf("dev_t %d:%d -> %+v: %v", tc.major, tc.minor, got, err)
		}
	}
	if _, err := DecodeDevice([]byte{1}); err == nil {
		t.Fatal("accepted truncated dev_t")
	}
}

func TestNoCrossGPUFallback(t *testing.T) {
	main := Identity{226, 128}
	if Matches(main, Identity{226, 0}, Identity{226, 129}, true, true) {
		t.Fatal("matched different GPU")
	}
	if !Matches(main, main, Identity{226, 129}, true, true) {
		t.Fatal("did not match primary")
	}
	if !Matches(main, Identity{226, 0}, main, true, true) {
		t.Fatal("did not match render")
	}
	if Matches(main, main, main, false, false) {
		t.Fatal("matched device without DRM capability")
	}
}

func TestVulkanDeviceHardware(t *testing.T) {
	path := os.Getenv("NEFERGUI_RENDER_NODE")
	if path == "" {
		t.Skip("opt in with NEFERGUI_RENDER_NODE=/dev/dri/renderD... (matching compositor main_device)")
	}
	var stat unix.Stat_t
	if err := unix.Stat(path, &stat); err != nil {
		t.Fatal(err)
	}
	d, err := Open(DeviceIdentity(uint64(stat.Rdev)))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	t.Logf("selected main_device %d:%d -> Vulkan render node %d:%d queue family %d", unix.Major(uint64(stat.Rdev)), unix.Minor(uint64(stat.Rdev)), d.Render.Major, d.Render.Minor, d.QueueFamily)
	if d.Render != DeviceIdentity(uint64(stat.Rdev)) {
		t.Fatalf("render node mismatch: %+v", d.Render)
	}
}
