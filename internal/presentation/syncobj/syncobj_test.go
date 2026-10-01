//go:build linux

package syncobj

import (
	"errors"
	"os"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

func TestUAPILayouts(t *testing.T) {
	for _, tc := range []struct {
		name      string
		got, want uintptr
	}{
		{"create", unsafe.Sizeof(createArg{}), 8},
		{"destroy", unsafe.Sizeof(destroyArg{}), 8},
		{"handle", unsafe.Sizeof(handleArg{}), 24},
		{"transfer", unsafe.Sizeof(transferArg{}), 32},
		{"wait", unsafe.Sizeof(timelineWaitArg{}), 48},
		{"query", unsafe.Sizeof(queryArg{}), 24},
		{"eventfd", unsafe.Sizeof(eventFDArg{}), 24},
	} {
		if tc.got != tc.want {
			t.Errorf("%s size: got %d want %d", tc.name, tc.got, tc.want)
		}
	}
}

func TestRenderNodeIoctls(t *testing.T) {
	path := os.Getenv("NEFERGUI_RENDER_NODE")
	if path == "" {
		t.Skip("opt in with NEFERGUI_RENDER_NODE=/dev/dri/renderD... (matching feedback main_device)")
	}
	n, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	acquire, err := n.Create()
	if err != nil {
		t.Fatalf("SYNCOBJ_CREATE acquire: %v", err)
	}
	defer n.Destroy(acquire)
	release, err := n.Create()
	if err != nil {
		t.Fatalf("SYNCOBJ_CREATE release: %v", err)
	}
	defer n.Destroy(release)
	fd, err := n.ExportTimeline(acquire)
	if err != nil {
		t.Fatalf("HANDLE_TO_FD timeline: %v", err)
	}
	defer unix.Close(fd)
	// Import as another handle to prove this FD is a DRM syncobj, not a
	// Vulkan OPAQUE_FD. FD_TO_HANDLE without IMPORT_SYNC_FILE does not consume fd.
	a := handleArg{FD: int32(fd)}
	if err = n.ioctl(ioctlFDToHandle, unsafe.Pointer(&a)); err != nil {
		t.Fatalf("FD_TO_HANDLE timeline: %v", err)
	}
	defer n.Destroy(a.Handle)
	if a.Handle == 0 {
		t.Fatal("zero imported handle")
	}
	before, err := n.Query(release)
	if err != nil {
		t.Fatalf("QUERY release: %v", err)
	}
	t.Logf("render node %s: CREATE=%d,%d HANDLE_TO_FD=%d FD_TO_HANDLE=%d QUERY=%d", path, acquire, release, fd, a.Handle, before)
	// An unsignaled point cannot be used for image recycling. TIMELINE_WAIT
	// must time out without a CPU fence fallback.
	ready, err := n.Signaled(release, 1)
	if err != nil {
		t.Fatalf("TIMELINE_WAIT: %v", err)
	}
	if ready {
		t.Fatal("unsignaled point reported ready")
	}
	// TRANSFER may be rejected for a point not yet submitted. A valid
	// SYNC_FD import is exercised by an actual Vulkan submission test.
	if err = n.Transfer(acquire, 1, release, 2); err != nil {
		t.Logf("TRANSFER unsubmitted point (expected kernel-dependent rejection): %v", err)
	} else {
		t.Log("TRANSFER accepted unsubmitted point")
	}
}

func TestFailedImportRetainsFD(t *testing.T) {
	// No render node needed: EBADF leaves the caller's descriptor owned.
	fd, err := unix.Eventfd(0, unix.EFD_CLOEXEC)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	n := &Node{fd: -1}
	if _, err = n.ImportFence(fd); !errors.Is(err, unix.EBADF) {
		t.Fatalf("want EBADF, got %v", err)
	}
	if _, err = unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err != nil {
		t.Fatalf("lost caller FD after failed import: %v", err)
	}
}

// DRM_IOWR(type 'd', nr, size): direction 3<<30, size<<16, type<<8, nr.
func TestEventFDRequestNumber(t *testing.T) {
	want := uintptr(3<<30 | unsafe.Sizeof(eventFDArg{})<<16 | 'd'<<8 | 0xCF)
	if ioctlEventFD != want {
		t.Fatalf("DRM_IOCTL_SYNCOBJ_EVENTFD: got %#x want %#x", ioctlEventFD, want)
	}
	want = uintptr(3<<30 | unsafe.Sizeof(queryArg{})<<16 | 'd'<<8 | 0xCD)
	if ioctlTimelineSig != want {
		t.Fatalf("DRM_IOCTL_SYNCOBJ_TIMELINE_SIGNAL: got %#x want %#x", ioctlTimelineSig, want)
	}
}

func TestEventFDBadFDFailsWithErrno(t *testing.T) {
	n := &Node{fd: -1}
	if err := n.EventFD(1, 1, 0); !errors.Is(err, unix.EBADF) {
		t.Fatalf("want EBADF, got %v", err)
	}
}

// A point signaled from the CPU makes the registered eventfd readable without
// any waiter thread; the registration is one-shot.
func TestEventFDSignaledPointHardware(t *testing.T) {
	path := os.Getenv("NEFERGUI_RENDER_NODE")
	if path == "" {
		t.Skip("opt in with NEFERGUI_RENDER_NODE=/dev/dri/renderD...")
	}
	n, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	handle, err := n.Create()
	if err != nil {
		t.Fatal(err)
	}
	defer n.Destroy(handle)
	efd, err := unix.Eventfd(0, unix.EFD_CLOEXEC|unix.EFD_NONBLOCK)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(efd)
	if err = n.EventFD(handle, 7, efd); err != nil {
		t.Fatalf("EventFD: %v", err)
	}
	readable := func(timeout int) bool {
		fds := []unix.PollFd{{Fd: int32(efd), Events: unix.POLLIN}}
		r, err := unix.Poll(fds, timeout)
		if err != nil {
			t.Fatal(err)
		}
		return r > 0
	}
	if readable(0) {
		t.Fatal("eventfd readable before the point signaled")
	}
	if err = n.Signal(handle, 7); err != nil {
		t.Fatalf("Signal: %v", err)
	}
	if !readable(1000) {
		t.Fatal("eventfd not readable after the point signaled")
	}
	var buf [8]byte
	if _, err = unix.Read(efd, buf[:]); err != nil {
		t.Fatal(err)
	}
	if readable(0) {
		t.Fatal("eventfd still readable after read")
	}
	// Re-arming for an already signaled point fires immediately.
	if err = n.EventFD(handle, 7, efd); err != nil {
		t.Fatal(err)
	}
	if !readable(1000) {
		t.Fatal("re-armed eventfd for a signaled point stayed silent")
	}
}
