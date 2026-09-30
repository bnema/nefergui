//go:build linux

package wayland

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"github.com/bnema/nefergui/internal/presentation/buffers"
	"github.com/bnema/wlturbo"
	"github.com/bnema/wlturbo/protocol/core"
	"github.com/bnema/wlturbo/protocol/drmsyncobj"
	"github.com/bnema/wlturbo/wl"
)

// A socket peer records real wire requests (not a mocked Present implementation).
// Every frame must carry both sync points and a buffer on the same commit.
func TestPresentWireSequence(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	// ConnectFromConn sends get_registry before it returns: drain the pipe first.
	type request struct {
		id     uint32
		opcode uint16
		args   []byte
	}
	requests := make(chan request, 64)
	errs := make(chan error, 1)
	go func() {
		for {
			header := make([]byte, 8)
			if _, e := io.ReadFull(server, header); e != nil {
				errs <- e
				return
			}
			n := int(binary.LittleEndian.Uint32(header[4:]) >> 16)
			data := make([]byte, n-8)
			if _, e := io.ReadFull(server, data); e != nil {
				errs <- e
				return
			}
			requests <- request{binary.LittleEndian.Uint32(header), binary.LittleEndian.Uint16(header[4:]), data}
		}
	}()
	d, err := wlturbo.ConnectFromConn(client)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := d.Context()
	surface := core.NewSurface(ctx)
	syncSurface := drmsyncobj.NewWpLinuxDrmSyncobjSurface(ctx)
	acquire := drmsyncobj.NewWpLinuxDrmSyncobjTimeline(ctx)
	release := drmsyncobj.NewWpLinuxDrmSyncobjTimeline(ctx)
	buffer := core.NewBuffer(ctx)
	for i, p := range []wl.Proxy{surface, syncSurface, acquire, release, buffer} {
		p.SetID(uint32(20 + i))
		ctx.Register(p)
	}
	w := &Window{Surface: surface, SyncSurface: syncSurface, Width: 40, Height: 30, Scale: 1, Configured: true, FeedbackDone: true, FrameReady: true, Transparent: true}
	p, _ := buffers.New(1)
	// The fake server receives a finite number of requests; no dispatch loop/GPU.
	for frame := 0; frame < 2; frame++ {
		b, e := p.Begin()
		if e != nil {
			t.Fatal(e)
		}
		a, e := p.Acquire(b)
		if e != nil {
			t.Fatal(e)
		}
		z, e := p.Commit(b)
		if e != nil {
			t.Fatal(e)
		}
		if e = w.Present(buffer, acquire, release, a, z); e != nil {
			t.Fatal(e)
		}
		if e = p.Own(b); e != nil {
			t.Fatal(e)
		}
		// Observe the generated protocol sequence up to commit. Ignore ancillary
		// viewport/opaque/damage/frame requests, but ensure they do not replace the buffer.
		var events []string
		var attached, acquired, released bool
		deadline := time.After(3 * time.Second)
	loop:
		for {
			select {
			case q := <-requests:
				switch {
				case q.id == syncSurface.ID() && q.opcode == 1:
					acquired = true
					events = append(events, "acquire")
				case q.id == syncSurface.ID() && q.opcode == 2:
					released = true
					events = append(events, "release")
				case q.id == surface.ID() && q.opcode == 1:
					attached = binary.LittleEndian.Uint32(q.args) == buffer.ID()
					events = append(events, "attach")
				case q.id == surface.ID() && q.opcode == 6:
					events = append(events, "commit")
					break loop
				}
			case e := <-errs:
				t.Fatal(e)
			case <-deadline:
				t.Fatal("missing commit")
			}
		}
		if !attached || !acquired || !released || len(events) != 4 || events[0] != "acquire" || events[1] != "release" || events[2] != "attach" || events[3] != "commit" {
			t.Fatalf("frame %d wire sequence %v attached=%v", frame, events, attached)
		}
		if e = p.Release(b); e != nil {
			t.Fatal(e)
		}
		if _, e = p.Begin(); e != buffers.ErrNoBuffer {
			t.Fatalf("wl_buffer.release reused buffer: %v", e)
		}
		if e = p.Signal(b, z); e != nil {
			t.Fatal(e)
		}
		if e = p.Reuse(b, true); e != nil {
			t.Fatal(e)
		}
		w.FrameReady = true // synthetic frame callback
	}
}
