//go:build linux

package wayland

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"github.com/bnema/wlturbo"
	"github.com/bnema/wlturbo/protocol/core"
	"github.com/bnema/wlturbo/protocol/cursorshape"
)

// readRequest returns the object ID, opcode and body of the next request.
// Requests can share one socket read, so each is framed by its size field.
func readRequest(t *testing.T, p *net.UnixConn) (uint32, uint16, []byte) {
	t.Helper()
	_ = p.SetReadDeadline(time.Now().Add(time.Second))
	header := make([]byte, 8)
	if _, err := io.ReadFull(p, header); err != nil {
		t.Fatalf("read request header: %v", err)
	}
	size := int(binary.LittleEndian.Uint32(header[4:]) >> 16)
	body := make([]byte, size-8)
	if _, err := io.ReadFull(p, body); err != nil {
		t.Fatalf("read request body: %v", err)
	}
	return binary.LittleEndian.Uint32(header), uint16(binary.LittleEndian.Uint32(header[4:])), body
}

// noRequest asserts the client sent nothing.
func noRequest(t *testing.T, p *net.UnixConn) {
	t.Helper()
	_ = p.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
	b := make([]byte, 64)
	if n, err := p.Read(b); err == nil {
		t.Fatalf("unexpected request %x", b[:n])
	}
}

func TestCursorShapeWire(t *testing.T) {
	ca, server := inputPair(t)
	d, err := wlturbo.ConnectFromConn(ca)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	inputRequest(t, server)
	ctx := d.Context()
	pointer := core.NewPointer(ctx)
	pointer.SetID(d.AllocateID())
	ctx.Register(pointer)
	shapes := cursorshape.NewWpCursorShapeManager(ctx)
	if err = d.Registry().Bind(1, cursorshape.WpCursorShapeManagerInterface, 1, shapes); err != nil {
		t.Fatal(err)
	}
	inputRequest(t, server)
	w := &Window{Pointer: pointer, CursorShapes: shapes, pointerGen: 1}

	// Outside the surface nothing is sent.
	if err = w.SetCursor(CursorPointer); err != nil {
		t.Fatal(err)
	}
	noRequest(t, server)

	// First shape after enter creates the device, then sets the shape at the
	// enter serial.
	w.PointerEntered(7, 1)
	if err = w.SetCursor(CursorPointer); err != nil {
		t.Fatal(err)
	}
	id, op, body := readRequest(t, server)
	if id != shapes.ID() || op != 1 || binary.LittleEndian.Uint32(body[4:]) != pointer.ID() {
		t.Fatalf("get_pointer id=%d op=%d %x", id, op, body)
	}
	device := binary.LittleEndian.Uint32(body)
	id, op, body = readRequest(t, server)
	if id != device || op != 1 || binary.LittleEndian.Uint32(body) != 7 || binary.LittleEndian.Uint32(body[4:]) != CursorPointer {
		t.Fatalf("set_shape id=%d op=%d %x", id, op, body)
	}

	// An unchanged shape is not re-sent; a new one reuses the device.
	if err = w.SetCursor(CursorPointer); err != nil {
		t.Fatal(err)
	}
	noRequest(t, server)
	if err = w.SetCursor(CursorText); err != nil {
		t.Fatal(err)
	}
	if id, _, body = readRequest(t, server); id != device || binary.LittleEndian.Uint32(body[4:]) != CursorText {
		t.Fatalf("text shape id=%d %x", id, body)
	}

	// After leave nothing is sent; re-enter re-sends even the same shape,
	// at the new serial.
	w.PointerLeft()
	if err = w.SetCursor(CursorDefault); err != nil {
		t.Fatal(err)
	}
	noRequest(t, server)
	w.PointerEntered(9, 1)
	if err = w.SetCursor(CursorText); err != nil {
		t.Fatal(err)
	}
	if id, _, body = readRequest(t, server); id != device || binary.LittleEndian.Uint32(body) != 9 || binary.LittleEndian.Uint32(body[4:]) != CursorText {
		t.Fatalf("re-enter shape id=%d %x", id, body)
	}

	// Dropping the device (pointer capability loss) destroys it.
	w.dropCursorDevice()
	if id, op, _ = readRequest(t, server); id != device || op != 0 {
		t.Fatalf("destroy id=%d op=%d", id, op)
	}
	// The old enter serial went with the pointer: nothing is sent until the
	// replacement pointer's own enter, and a late enter from the released
	// pointer (older generation) is ignored.
	if err = w.SetCursor(CursorPointer); err != nil {
		t.Fatal(err)
	}
	noRequest(t, server)
	w.pointerGen = 2
	w.PointerEntered(11, 1)
	if err = w.SetCursor(CursorPointer); err != nil {
		t.Fatal(err)
	}
	noRequest(t, server)
}

// Without the global, SetCursor is a silent no-op.
func TestCursorShapeUnsupported(t *testing.T) {
	w := &Window{Pointer: &core.Pointer{}}
	w.PointerEntered(1, 0)
	if err := w.SetCursor(CursorPointer); err != nil {
		t.Fatal(err)
	}
}
