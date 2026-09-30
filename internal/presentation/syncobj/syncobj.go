//go:build linux

// Package syncobj bridges Vulkan SYNC_FD semaphores and Wayland DRM syncobj
// timelines on the compositor-selected render node. It never interprets a
// Vulkan OPAQUE_FD as a DRM syncobj FD.
package syncobj

import (
	"errors"
	"fmt"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// These ABI layouts and request numbers correspond to Linux drm.h syncobj
// ioctls (DRM_IOWR, type 'd'). They are intentionally fixed-width; keep the
// layout tests aligned with the pinned host UAPI when updating the bridge.
type createArg struct{ Handle, Flags uint32 }
type destroyArg struct{ Handle, Pad uint32 }
type handleArg struct {
	Handle, Flags uint32
	FD            int32
	Pad           uint32
	Point         uint64
}
type transferArg struct {
	Src, Dst           uint32
	SrcPoint, DstPoint uint64
	Flags, Pad         uint32
}
type timelineWaitArg struct {
	Handles, Points          uint64
	TimeoutNS                int64
	Count, Flags, First, Pad uint32
	DeadlineNS               uint64
}
type queryArg struct {
	Handles, Points uint64
	Count, Flags    uint32
}

const (
	ioctlCreate       = uintptr(0xc00864bf)
	ioctlDestroy      = uintptr(0xc00864c0)
	ioctlHandleToFD   = uintptr(0xc01864c1)
	ioctlFDToHandle   = uintptr(0xc01864c2)
	ioctlTimelineWait = uintptr(0xc03064ca)
	ioctlQuery        = uintptr(0xc01864cb)
	ioctlTransfer     = uintptr(0xc02064cc)
	importSyncFile    = 1
	exportSyncFile    = 1
)

// Node owns the DRM render fd. Only open the render node of the Vulkan device
// matched to linux-dmabuf main_device. Never substitute a different GPU.
type Node struct{ fd int }

func Open(path string) (*Node, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open matching render node %s: %w", path, err)
	}
	return &Node{fd: fd}, nil
}

func (n *Node) Close() error {
	if n.fd < 0 {
		return nil
	}
	err := unix.Close(n.fd)
	n.fd = -1
	return err
}
func (n *Node) ioctl(request uintptr, arg unsafe.Pointer) error {
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(n.fd), request, uintptr(arg))
	if errno != 0 {
		return fmt.Errorf("DRM syncobj ioctl %#x: %w", request, errno)
	}
	return nil
}

func (n *Node) Create() (uint32, error) {
	var a createArg
	if err := n.ioctl(ioctlCreate, unsafe.Pointer(&a)); err != nil {
		return 0, err
	}
	return a.Handle, nil
}
func (n *Node) Destroy(handle uint32) error {
	return n.ioctl(ioctlDestroy, unsafe.Pointer(&destroyArg{Handle: handle}))
}

// ExportTimeline returns a new owned syncobj FD (not a sync_file). The caller
// hands it to WLTurbo's ImportTimeline, which closes only after a successful
// request send; on send failure the caller must close the FD.
func (n *Node) ExportTimeline(handle uint32) (int, error) {
	a := handleArg{Handle: handle, FD: -1}
	if err := n.ioctl(ioctlHandleToFD, unsafe.Pointer(&a)); err != nil {
		return -1, err
	}
	return int(a.FD), nil
}

// ImportFence consumes syncFile only after FD_TO_HANDLE imports it into a
// pre-created destination syncobj. With IMPORT_SYNC_FILE, Handle is an input,
// not an output: asking the kernel to import into handle zero returns ENOENT.
// The caller retains syncFile if the ioctl rejects it.
func (n *Node) ImportFence(syncFile int) (uint32, error) {
	handle, err := n.Create()
	if err != nil {
		return 0, err
	}
	a := handleArg{Handle: handle, Flags: importSyncFile, FD: int32(syncFile)}
	if err = n.ioctl(ioctlFDToHandle, unsafe.Pointer(&a)); err != nil {
		_ = n.Destroy(handle)
		return 0, err
	}
	if err = unix.Close(syncFile); err != nil {
		_ = n.Destroy(handle)
		return 0, err
	}
	return handle, nil
}

func (n *Node) Transfer(src uint32, srcPoint uint64, dst uint32, dstPoint uint64) error {
	a := transferArg{Src: src, Dst: dst, SrcPoint: srcPoint, DstPoint: dstPoint}
	return n.ioctl(ioctlTransfer, unsafe.Pointer(&a))
}

// AcquirePoint transfers the Vulkan submission's exported SYNC_FD into the
// persistent acquire timeline. The caller retains syncFile on import error.
func (n *Node) AcquirePoint(syncFile int, timeline uint32, point uint64) error {
	tmp, err := n.ImportFence(syncFile)
	if err != nil {
		return err
	}
	defer n.Destroy(tmp)
	return n.Transfer(tmp, 0, timeline, point)
}

// ReleaseFence exports a compositor release point as a sync_file suitable for
// TEMPORARY Vulkan semaphore import. The caller owns the returned FD until
// Vulkan accepts the import; failed imports must close it.
func (n *Node) ReleaseFence(timeline uint32, point uint64) (int, error) {
	tmp, err := n.Create()
	if err != nil {
		return -1, err
	}
	defer n.Destroy(tmp)
	if err = n.Transfer(timeline, point, tmp, 0); err != nil {
		return -1, err
	}
	a := handleArg{Handle: tmp, Flags: exportSyncFile, FD: -1}
	if err = n.ioctl(ioctlHandleToFD, unsafe.Pointer(&a)); err != nil {
		return -1, err
	}
	return int(a.FD), nil
}

// WaitPoint waits for a submitted release point for at most timeout. Call it
// only from a waiter goroutine, never from the presentation loop. WAIT_FOR_SUBMIT
// handles the interval before the compositor attaches its fence to the point.
func (n *Node) WaitPoint(handle uint32, point uint64, timeout time.Duration) (bool, error) {
	if timeout < 0 || timeout > time.Second {
		return false, fmt.Errorf("invalid syncobj wait timeout %s", timeout)
	}
	h := handle
	p := point
	var now unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &now); err != nil {
		return false, err
	}
	a := timelineWaitArg{Handles: uint64(uintptr(unsafe.Pointer(&h))), Points: uint64(uintptr(unsafe.Pointer(&p))), TimeoutNS: now.Nano() + timeout.Nanoseconds(), Count: 1, Flags: 2}
	err := n.ioctl(ioctlTimelineWait, unsafe.Pointer(&a))
	runtime.KeepAlive(h)
	runtime.KeepAlive(p)
	if err != nil {
		if errors.Is(err, unix.ETIME) || errors.Is(err, unix.EBUSY) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Signaled performs a zero-time wait for tests and shutdown diagnostics.
func (n *Node) Signaled(handle uint32, point uint64) (bool, error) {
	return n.WaitPoint(handle, point, 0)
}

// Query returns the last signaled point. This is diagnostic only: timeline
// wait, not an optimistic query, authorizes release.
func (n *Node) Query(handle uint32) (uint64, error) {
	h := handle
	var point uint64
	a := queryArg{Handles: uint64(uintptr(unsafe.Pointer(&h))), Points: uint64(uintptr(unsafe.Pointer(&point))), Count: 1}
	err := n.ioctl(ioctlQuery, unsafe.Pointer(&a))
	runtime.KeepAlive(h)
	runtime.KeepAlive(point)
	if err != nil {
		return 0, err
	}
	return point, nil
}
