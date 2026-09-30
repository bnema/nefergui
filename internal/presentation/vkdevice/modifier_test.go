//go:build linux

package vkdevice

import (
	"github.com/bnema/wlturbo/protocol/linuxdmabuf"
	"golang.org/x/sys/unix"
	"os"
	"testing"
)

func TestModifierIntersection(t *testing.T) {
	tranches := [][]linuxdmabuf.FormatEntry{{{Format: XRGB8888, Modifier: 11}, {Format: ARGB8888, Modifier: 22}}, {{Format: XRGB8888, Modifier: 33}, {Format: ARGB8888, Modifier: 44}}}
	for _, tc := range []struct {
		alpha     bool
		available map[uint64]bool
		want      uint64
	}{
		{false, map[uint64]bool{11: true, 33: true}, 11},
		{false, map[uint64]bool{33: true}, 33},
		{true, map[uint64]bool{11: true, 22: true, 44: true}, 22},
		{true, map[uint64]bool{11: true, 44: true}, 44},
	} {
		got, err := ChooseModifier(tranches, tc.alpha, tc.available)
		if err != nil || got.Value != tc.want {
			t.Fatalf("alpha=%v want=%d got=%+v err=%v", tc.alpha, tc.want, got, err)
		}
	}
	if _, err := ChooseModifier(tranches, true, map[uint64]bool{11: true, 33: true}); err == nil {
		t.Fatal("transparent accepted XRGB")
	}
}

func TestExportableModifiersHardware(t *testing.T) {
	path := os.Getenv("NEFERGUI_RENDER_NODE")
	if path == "" {
		t.Skip("set NEFERGUI_RENDER_NODE to matching DRM render node")
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
	mods, err := d.ExportableModifiers()
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) == 0 {
		t.Fatal("matched Vulkan device has no one-plane exportable B8G8R8A8 color+readback modifiers")
	}
	for mod := range mods {
		t.Logf("render node %s exportable DRM modifier %#x", path, mod)
	}
}
