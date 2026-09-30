//go:build linux

package vkdevice

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func TestRectPipelineHardware(t *testing.T) {
	path := os.Getenv("NEFERGUI_RENDER_NODE")
	if path == "" {
		t.Skip("opt in with NEFERGUI_RENDER_NODE=/dev/dri/renderD...")
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
	p, err := d.NewRectPipeline(320, 240)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.Handle == 0 || p.Layout == 0 {
		t.Fatal("pipeline not created")
	}
	t.Logf("created 320x240 rectangle pipeline with dynamic rendering on %s", path)
}
