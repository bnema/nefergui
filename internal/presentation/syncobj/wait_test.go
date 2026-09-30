//go:build linux

package syncobj

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestWaitResultMapping(t *testing.T) {
	if ok, err := waitResult(0); !ok || err != nil {
		t.Fatalf("success: got %v, %v", ok, err)
	}
	for _, e := range []unix.Errno{unix.ETIME, unix.EBUSY} {
		if ok, err := waitResult(e); ok || err != nil {
			t.Fatalf("%v: want (false,nil), got %v, %v", e, ok, err)
		}
	}
	for _, e := range []unix.Errno{unix.EINTR, unix.EBADF, unix.EINVAL, unix.ENOENT, unix.ENOTTY, unix.EFAULT} {
		ok, err := waitResult(e)
		if ok || err == nil {
			t.Fatalf("%v: want error, got %v, %v", e, ok, err)
		}
		if !errors.Is(err, e) {
			t.Errorf("%v: error does not wrap errno: %v", e, err)
		}
		if !strings.Contains(err.Error(), "DRM syncobj ioctl 0xc03064ca") {
			t.Errorf("%v: lost request context: %v", e, err)
		}
	}
}

func TestWaitResultIdleDoesNotAllocate(t *testing.T) {
	for _, e := range []unix.Errno{unix.ETIME, unix.EBUSY} {
		if n := testing.AllocsPerRun(1000, func() { _, _ = waitResult(e) }); n != 0 {
			t.Errorf("%v: %v allocs per idle result, want 0", e, n)
		}
	}
}

func TestWaitPointBadFDKeepsRealError(t *testing.T) {
	for _, fd := range []int{-1, 1 << 30} {
		n := &Node{fd: fd}
		ok, err := n.WaitPoint(1, 1, 20*time.Millisecond)
		if ok || !errors.Is(err, unix.EBADF) {
			t.Fatalf("fd %d: want (false, EBADF), got %v, %v", fd, ok, err)
		}
		if !strings.Contains(err.Error(), "DRM syncobj ioctl") {
			t.Errorf("fd %d: lost context: %v", fd, err)
		}
	}
	// Closed Node (fd -1 after Close) must also surface EBADF.
	n := &Node{fd: -1}
	if _, err := n.Signaled(1, 1); !errors.Is(err, unix.EBADF) {
		t.Fatalf("Signaled on closed node: %v", err)
	}
}

func TestWaitPointNonDRMFDIsNotSwallowed(t *testing.T) {
	fd, err := unix.Eventfd(0, unix.EFD_CLOEXEC)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)
	n := &Node{fd: fd}
	if ok, err := n.WaitPoint(1, 1, 0); ok || !errors.Is(err, unix.ENOTTY) {
		t.Fatalf("want (false, ENOTTY), got %v, %v", ok, err)
	}
}

func TestWaitPointTimeoutBounds(t *testing.T) {
	n := &Node{fd: -1}
	for _, d := range []time.Duration{-1, time.Second + 1} {
		if _, err := n.WaitPoint(1, 1, d); err == nil || errors.Is(err, unix.EBADF) {
			t.Errorf("timeout %s: want bounds error before ioctl, got %v", d, err)
		}
	}
	// Boundaries stay accepted (fail later at the ioctl with EBADF).
	for _, d := range []time.Duration{0, time.Second} {
		if _, err := n.WaitPoint(1, 1, d); !errors.Is(err, unix.EBADF) {
			t.Errorf("timeout %s: want EBADF, got %v", d, err)
		}
	}
}

// openBenchNode opts in to a real render node, like the other hardware tests.
func openBenchNode(tb testing.TB) *Node {
	path := os.Getenv("NEFERGUI_RENDER_NODE")
	if path == "" {
		tb.Skip("set NEFERGUI_RENDER_NODE=/dev/dri/renderD... for real idle-wait measurement")
	}
	n, err := Open(path)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = n.Close() })
	return n
}

func TestWaitPointIdleTimeoutRealNodeNoAllocs(t *testing.T) {
	n := openBenchNode(t)
	h, err := n.Create()
	if err != nil {
		t.Fatal(err)
	}
	defer n.Destroy(h)
	ready, err := n.WaitPoint(h, 1, 0)
	if ready || err != nil {
		t.Fatalf("idle unsignaled point: got %v, %v", ready, err)
	}
	if a := testing.AllocsPerRun(200, func() { _, _ = n.WaitPoint(h, 1, 0) }); a != 0 {
		t.Errorf("idle WaitPoint allocs/op = %v, want 0", a)
	}
}

func BenchmarkWaitResultIdle(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = waitResult(unix.ETIME)
	}
}

// BenchmarkWaitPointIdle measures the presentation waiter's idle path on a
// real render node: an unsignaled point that times out (ETIME/EBUSY).
func BenchmarkWaitPointIdle(b *testing.B) {
	n := openBenchNode(b)
	h, err := n.Create()
	if err != nil {
		b.Fatal(err)
	}
	defer n.Destroy(h)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if ready, err := n.WaitPoint(h, 1, 20*time.Millisecond); ready || err != nil {
			b.Fatalf("got %v, %v", ready, err)
		}
	}
}
