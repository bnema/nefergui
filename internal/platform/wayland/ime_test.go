//go:build linux

package wayland

import (
	"encoding/binary"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/bnema/wlturbo"
	"github.com/bnema/wlturbo/protocol/core"
	"github.com/bnema/wlturbo/protocol/textinput"
)

func TestSurroundingUTF8Window(t *testing.T) {
	s := strings.Repeat("é", 3000)
	for _, pos := range []int{0, 1, 2000, 3999, len(s)} {
		text, cursor, anchor := surroundingWindow(s, pos, pos)
		if len(text) > 4000 || !strings.Contains(s, text) || cursor < 0 || cursor > len(text) || anchor != cursor {
			t.Fatalf("window at %d: len=%d cursor=%d", pos, len(text), cursor)
		}
	}
}
func imeStringFrame(id uint32, opcode uint16, s string, tail ...uint32) []byte {
	data := append([]byte(s), 0)
	padded := make([]byte, (len(data)+3)&^3)
	copy(padded, data)
	frame := inputFrame(id, opcode, uint32(len(data)))
	frame = append(frame, padded...)
	for _, v := range tail {
		b := make([]byte, 4)
		binary.LittleEndian.PutUint32(b, v)
		frame = append(frame, b...)
	}
	binary.LittleEndian.PutUint32(frame[4:], uint32(len(frame))<<16|uint32(opcode))
	return frame
}
func TestIMEBatchSerialOverSocketpair(t *testing.T) {
	ca, server := inputPair(t)
	d, err := wlturbo.ConnectFromConn(ca)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	inputRequest(t, server)
	manager := textinput.NewTextInputManagerV3(d.Context())
	if err = d.Registry().Bind(1, textinput.TextInputManagerV3Interface, 2, manager); err != nil {
		t.Fatal(err)
	}
	inputRequest(t, server)
	seat := core.NewSeat(d.Context())
	seat.SetID(20)
	d.Context().Register(seat)
	surface := core.NewSurface(d.Context())
	surface.SetID(30)
	d.Context().Register(surface)
	input, err := manager.GetTextInput(seat)
	if err != nil {
		t.Fatal(err)
	}
	inputRequest(t, server)
	events := make(chan IMEEvent, 10)
	m := &IME{input: input, surface: surface.ID(), post: func(e IMEEvent) { events <- e }}
	input.OnEnter(func(id uint32) { m.post(IMEEvent{Kind: "enter", Surface: id}) })
	input.OnPreeditString(func(s string, b, e int32) { m.post(IMEEvent{Kind: "preedit", Text: s, Begin: b, End: e}) })
	input.OnCommitString(func(s string) { m.post(IMEEvent{Kind: "commit", Text: s}) })
	input.OnDeleteSurroundingText(func(b, a uint32) { m.post(IMEEvent{Kind: "delete", Before: b, After: a}) })
	input.OnDone(func(serial uint32) { m.post(IMEEvent{Kind: "done", Serial: serial}) })
	m.Enable() // requests before surface enter are remembered
	inputSend(t, server, inputFrame(input.ID(), 0, surface.ID()))
	if err = d.Dispatch(); err != nil {
		t.Fatal(err)
	}
	m.ApplyIME(<-events)
	if m.commits != 1 || !m.enabled {
		t.Fatalf("enable %d %v", m.commits, m.enabled)
	}
	inputRequest(t, server) // enable; commit might be in the same read
	for _, msg := range [][]byte{
		imeStringFrame(input.ID(), 2, "é", 0, 2),
		imeStringFrame(input.ID(), 3, "x"),
		inputFrame(input.ID(), 4, 1, 0),
		inputFrame(input.ID(), 5, 0), // mismatched serial MUST still apply edit
	} {
		inputSend(t, server, msg)
		if err = d.Dispatch(); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 3; i++ {
		if _, ok := m.ApplyIME(<-events); ok {
			t.Fatal("partial batch delivered")
		}
	}
	batch, ok := m.ApplyIME(<-events)
	if !ok || batch.Preedit != "é" || batch.Commit != "x" || batch.DeleteBefore != 1 || m.synced {
		t.Fatalf("batch %+v ok=%v synced=%v", batch, ok, m.synced)
	}
	m.ContentType(true, false)
	if !m.pendingContent {
		t.Fatal("mismatched done changed protocol state")
	}
	m.ApplyIME(IMEEvent{Kind: "done", Serial: 1})
	if !m.synced || m.commits <= 1 {
		t.Fatal("pending state not flushed after matching done")
	}
}

func TestSurroundingWindowRuneBoundaries(t *testing.T) {
	s := strings.Repeat("é", 3500)
	for _, pos := range []int{0, 1, 3999, len(s) - 1, len(s)} {
		window, cursor, anchor := surroundingWindow(s, pos, len(s))
		if len(window) > 4000 || !utf8.ValidString(window) || !utf8.ValidString(window[:cursor]) || !utf8.ValidString(window[:anchor]) {
			t.Fatalf("invalid window at %d: len=%d cursor=%d anchor=%d", pos, len(window), cursor, anchor)
		}
	}
}

func TestIMEPasswordContentWire(t *testing.T) {
	ca, server := inputPair(t)
	d, err := wlturbo.ConnectFromConn(ca)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	inputRequest(t, server)
	input := textinput.NewTextInputV3(d.Context())
	input.SetID(20)
	d.Context().Register(input)
	m := &IME{input: input, entered: true, enabled: true, synced: true}
	m.ContentType(true, true)
	buf := make([]byte, 128)
	_ = server.SetReadDeadline(time.Now().Add(time.Second))
	n, err := server.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if n < 16 || binary.LittleEndian.Uint32(buf[:4]) != input.ID() || binary.LittleEndian.Uint16(buf[4:]) != 5 {
		t.Fatalf("unexpected content request: %x", buf[:n])
	}
	hint := binary.LittleEndian.Uint32(buf[8:12])
	purpose := binary.LittleEndian.Uint32(buf[12:16])
	if hint != textinput.CONTENT_HINT_SENSITIVE_DATA|textinput.CONTENT_HINT_HIDDEN_TEXT || purpose != textinput.CONTENT_PURPOSE_PASSWORD {
		t.Fatalf("password hint=%d purpose=%d", hint, purpose)
	}
}

func TestIMEInitialStateOneCommit(t *testing.T) {
	ca, server := inputPair(t)
	d, err := wlturbo.ConnectFromConn(ca)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	inputRequest(t, server)
	input := textinput.NewTextInputV3(d.Context())
	input.SetID(20)
	d.Context().Register(input)
	m := &IME{input: input, entered: true}
	m.Surrounding("hello", 5, 5)
	m.ContentType(true, false)
	m.CursorRect(1, 2, 3, 4)
	m.Enable()
	if m.Err != nil || m.commits != 1 {
		t.Fatalf("enable: %v commits=%d", m.Err, m.commits)
	}
	buf := make([]byte, 256)
	_ = server.SetReadDeadline(time.Now().Add(time.Second))
	n, err := server.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	var ops []uint16
	for offset := 0; offset < n; {
		size := int(binary.LittleEndian.Uint16(buf[offset+6:]))
		if size < 8 || offset+size > n {
			t.Fatalf("invalid frame: %x", buf[:n])
		}
		ops = append(ops, binary.LittleEndian.Uint16(buf[offset+4:]))
		offset += size
	}
	if len(ops) != 5 || ops[0] != 1 || ops[1] != 5 || ops[2] != 3 || ops[3] != 6 || ops[4] != 7 {
		t.Fatalf("request order %v", ops)
	}
}
