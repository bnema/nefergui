//go:build linux

package wayland

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bnema/nefergui/internal/harness"
	"github.com/bnema/wlturbo/protocol/core"
	"github.com/bnema/wlturbo/protocol/xdgshell"
	"github.com/bnema/wlturbo/wl"
	"golang.org/x/sys/unix"
)

// The child uses a plain shm toplevel so the test does not depend on a GPU
// renderer. The parent runs the pinned compositor with its real input script.
func TestHarnessInput(t *testing.T) {
	if os.Getenv("NEFERGUI_HARNESS") != "1" {
		t.Skip("set NEFERGUI_HARNESS=1 for NeferWL input injection")
	}
	script := filepath.Join(t.TempDir(), "input.txt")
	if err := os.WriteFile(script, []byte("sleep 2s\nmove 100 100\nsleep 200ms\nclick left\nkey a\nsleep 500ms\n"), 0600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "artifacts")
	result, code := harness.Run(context.Background(), harness.Options{Size: "400x300", Scale: "1", Layout: "us", Background: "#111111", Out: root, Input: script, Timeout: 25 * time.Second, ReadyTimeout: 8 * time.Second, Client: []string{os.Args[0], "-test.run=^TestHarnessInputClient$", "-test.v"}})
	if code != harness.Pass {
		t.Fatalf("harness code=%d status=%s error=%s artifacts=%s", code, result.Status, result.Error, root)
	}
	data, err := os.ReadFile(result.Artifacts["client_stdout"])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"INPUT motion", "INPUT press", "INPUT release", "INPUT key a"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("missing %s in client output:\n%s", want, data)
		}
	}
}

func TestHarnessInputClient(t *testing.T) {
	if os.Getenv("NEFERGUI_DEBUG_DIR") == "" {
		t.Skip("harness child only")
	}
	d, err := wl.Connect("")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err = d.Roundtrip(); err != nil {
		t.Fatal(err)
	}
	ctx := d.Context()
	compositor := core.NewCompositor(ctx)
	if _, err = d.Registry().BindNegotiated(core.CompositorInterface, 6, compositor); err != nil {
		t.Fatal(err)
	}
	shell := xdgshell.NewXdgWmBase(ctx)
	if _, err = d.Registry().BindNegotiated(xdgshell.XdgWmBaseInterface, 6, shell); err != nil {
		t.Fatal(err)
	}
	shm := core.NewShm(ctx)
	if _, err = d.Registry().BindNegotiated(core.ShmInterface, 1, shm); err != nil {
		t.Fatal(err)
	}
	surface, err := compositor.CreateSurface()
	if err != nil {
		t.Fatal(err)
	}
	xdg, err := shell.GetXdgSurface(surface)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = xdg.GetToplevel(); err != nil {
		t.Fatal(err)
	}
	configured := false
	xdg.OnConfigure(func(serial uint32) {
		if e := xdg.AckConfigure(serial); e != nil {
			t.Error(e)
		}
		configured = true
	})
	if err = surface.Commit(); err != nil {
		t.Fatal(err)
	}
	for !configured {
		if err = d.Dispatch(); err != nil {
			t.Fatal(err)
		}
	}
	fd, err := unix.MemfdCreate("nefergui-harness-input", unix.MFD_CLOEXEC)
	if err != nil {
		t.Fatal(err)
	}
	if err = unix.Ftruncate(fd, 400*300*4); err != nil {
		t.Fatal(err)
	}
	pool, err := shm.CreatePool(fd, 400*300*4) // consumes fd on success
	if err != nil {
		_ = unix.Close(fd)
		t.Fatal(err)
	}
	buffer, err := pool.CreateBuffer(0, 400, 300, 400*4, 0) // wl_shm XRGB8888
	if err != nil {
		t.Fatal(err)
	}
	if err = surface.Attach(buffer, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err = surface.DamageBuffer(0, 0, 400, 300); err != nil {
		t.Fatal(err)
	}
	if err = surface.Commit(); err != nil {
		t.Fatal(err)
	}
	w := &Window{Display: d, Surface: surface, events: make(chan Event, 128), readerStop: make(chan struct{})}
	if err = w.BindSeat(); err != nil {
		t.Fatal(err)
	}
	adapter, err := NewInputAdapter("en_US.UTF-8")
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	deadline := time.After(6 * time.Second)
	seen := map[string]bool{}
	for !(seen["motion"] && seen["press"] && seen["release"] && seen["key a"]) {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for injected input: %v", seen)
		default:
		}
		if err = d.Dispatch(); err != nil {
			t.Fatal(err)
		}
		for len(w.events) > 0 {
			ev := <-w.events
			switch ev.Kind {
			case SeatCapabilities, SeatRemoved:
				if err = w.ApplySeat(ev); err != nil {
					t.Fatal(err)
				}
			case InputFrame, InputKeymap, InputFocusIn, InputFocusOut, InputKey, InputModifiers, InputRepeatInfo:
				inputs, e := adapter.ApplyInput(ev, time.Now())
				if e != nil {
					t.Fatal(e)
				}
				for _, in := range inputs {
					kind := in.Kind
					if kind == "key" && in.Key.Pressed && in.Key.Text == "a" {
						kind = "key a"
					}
					if kind == "motion" || kind == "press" || kind == "release" || kind == "key a" {
						seen[kind] = true
						fmt.Println("INPUT", kind)
					}
				}
			}
		}
	}
}
