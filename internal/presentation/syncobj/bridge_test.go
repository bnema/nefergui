//go:build linux

package syncobj_test

import (
	"os"
	"testing"

	"github.com/bnema/nefergui/internal/presentation/syncobj"
	"github.com/bnema/nefergui/internal/presentation/vkdevice"
	"golang.org/x/sys/unix"
)

// This test proves that the selected render node accepts an actual Vulkan
// SYNC_FD, transfers a point into a DRM timeline, and exports a release point
// as a SYNC_FD that Vulkan can temporarily import. It is not an end-to-end
// compositor acceptance test; the Wayland commit gate is separate.
func TestVulkanDRMBridge(t *testing.T) {
	path := os.Getenv("NEFERGUI_RENDER_NODE")
	if path == "" {
		t.Skip("set NEFERGUI_RENDER_NODE to the feedback-matched render node")
	}
	var stat unix.Stat_t
	if err := unix.Stat(path, &stat); err != nil {
		t.Fatal(err)
	}
	dev, err := vkdevice.Open(vkdevice.DeviceIdentity(uint64(stat.Rdev)))
	if err != nil {
		t.Fatal(err)
	}
	defer dev.Close()
	node, err := syncobj.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	acquire, err := node.Create()
	if err != nil {
		t.Fatal(err)
	}
	defer node.Destroy(acquire)
	release, err := node.Create()
	if err != nil {
		t.Fatal(err)
	}
	defer node.Destroy(release)
	out, err := dev.NewBinaryFence(true)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	if err = dev.SubmitEmpty(out, nil); err != nil {
		t.Fatal(err)
	}
	fd, err := out.Export()
	if err != nil {
		t.Fatal(err)
	}
	// Do not close fd on a successful AcquirePoint (the ioctl wrapper closes
	// after import). On an ioctl failure it remains ours.
	if err = node.AcquirePoint(fd, acquire, 1); err != nil {
		_ = unix.Close(fd)
		t.Fatalf("Vulkan SYNC_FD → FD_TO_HANDLE → TRANSFER acquire: %v", err)
	}
	ready, err := node.Signaled(acquire, 1)
	if err != nil || !ready {
		t.Fatalf("acquire TIMELINE_WAIT ready=%v err=%v", ready, err)
	}
	if err = node.Transfer(acquire, 1, release, 2); err != nil {
		t.Fatalf("transfer acquire to release: %v", err)
	}
	ready, err = node.Signaled(release, 2)
	if err != nil || !ready {
		t.Fatalf("release TIMELINE_WAIT ready=%v err=%v", ready, err)
	}
	value, err := node.Query(release)
	if err != nil || value < 2 {
		t.Fatalf("QUERY release point=%d err=%v", value, err)
	}
	back, err := node.ReleaseFence(release, 2)
	if err != nil {
		t.Fatalf("TRANSFER and HANDLE_TO_FD EXPORT_SYNC_FILE: %v", err)
	}
	wait, err := dev.NewBinaryFence(false)
	if err != nil {
		_ = unix.Close(back)
		t.Fatal(err)
	}
	defer wait.Close()
	if err = wait.ImportTemporary(back); err != nil {
		_ = unix.Close(back)
		t.Fatalf("Vulkan temporary SYNC_FD import: %v", err)
	}
	if err = dev.SubmitEmpty(nil, wait); err != nil {
		t.Fatalf("Vulkan release-fence wait: %v", err)
	}
	t.Logf("render node %s: CREATE acquire=%d release=%d; Vulkan export SYNC_FD → FD_TO_HANDLE IMPORT_SYNC_FILE → TRANSFER(1) → TIMELINE_WAIT → TRANSFER(2) → QUERY=%d → HANDLE_TO_FD EXPORT_SYNC_FILE → Vulkan temporary import/wait: accepted", path, acquire, release, value)
}
