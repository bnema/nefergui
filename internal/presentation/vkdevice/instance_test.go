//go:build linux

package vkdevice

import (
	"testing"
	"unsafe"
)

func TestInstanceLayout(t *testing.T) {
	var v Instance
	if got := unsafe.Sizeof(v); got != 208 {
		t.Fatalf("size=%d", got)
	}
	for i, got := range []uintptr{unsafe.Offsetof(v.Bounds), unsafe.Offsetof(v.Color), unsafe.Offsetof(v.Clip), unsafe.Offsetof(v.Radii), unsafe.Offsetof(v.UV), unsafe.Offsetof(v.KindLayer), unsafe.Offsetof(v.Widths), unsafe.Offsetof(v.Sides), unsafe.Offsetof(v.Sides) + 16, unsafe.Offsetof(v.Sides) + 32, unsafe.Offsetof(v.Sides) + 48, unsafe.Offsetof(v.Shape), unsafe.Offsetof(v.Shadow)} {
		if want := uintptr(i * 16); got != want {
			t.Fatalf("attribute %d offset=%d want=%d", i, got, want)
		}
	}
}
