//go:build linux

package wayland

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/bnema/nefergui/internal/edit"
	"github.com/bnema/wlturbo"
	"github.com/bnema/wlturbo/protocol/core"
	"golang.org/x/sys/unix"
)

func clipboardFixture(t *testing.T) (*Clipboard, *wlturbo.Display, *net.UnixConn) {
	t.Helper()
	ca, server := inputPair(t)
	d, err := wlturbo.ConnectFromConn(ca)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	inputRequest(t, server)
	ctx := d.Context()
	manager := core.NewDataDeviceManager(ctx)
	if err = d.Registry().Bind(1, core.DataDeviceManagerInterface, 3, manager); err != nil {
		t.Fatal(err)
	}
	inputRequest(t, server)
	seat := core.NewSeat(ctx)
	seat.SetID(20)
	ctx.Register(seat)
	device, err := manager.GetDataDevice(seat)
	if err != nil {
		t.Fatal(err)
	}
	inputRequest(t, server)
	c := &Clipboard{manager: manager, device: device, serial: func() uint32 { return 42 }, pending: make(map[*core.DataOffer]map[string]bool), offers: make(map[uint32]*core.DataOffer)}
	c.post = func(e ClipboardEvent) { c.ApplyClipboard(e) }
	return c, d, server
}
func TestClipboardReadOverSocketpair(t *testing.T) {
	c, d, server := clipboardFixture(t)
	offer := core.NewDataOffer(d.Context())
	offer.SetID(30)
	d.Context().Register(offer)
	c.ApplyClipboard(ClipboardEvent{kind: "offer", offer: offer})
	for _, m := range []string{mimePlain, mimeX11, mimeUTF8} {
		c.ApplyClipboard(ClipboardEvent{kind: "mime", offer: offer, mime: m})
	}
	c.ApplyClipboard(ClipboardEvent{kind: "selection", offer: offer})
	if c.bestMIME() != mimeUTF8 {
		t.Fatal("MIME priority")
	}
	results := make(chan ClipboardResult, 2)
	// The peer reads receive's SCM_RIGHTS and writes to the transferred pipe.
	go func() {
		b := make([]byte, 512)
		oob := make([]byte, 128)
		_ = server.SetReadDeadline(time.Now().Add(time.Second))
		_, n, _, _, err := server.ReadMsgUnix(b, oob)
		if err != nil {
			results <- ClipboardResult{Err: err}
			return
		}
		messages, err := unix.ParseSocketControlMessage(oob[:n])
		if err != nil {
			results <- ClipboardResult{Err: err}
			return
		}
		for _, m := range messages {
			fd, e := unix.ParseUnixRights(&m)
			if e != nil || len(fd) == 0 {
				continue
			}
			f := os.NewFile(uintptr(fd[0]), "offer-write")
			_, e = io.WriteString(f, "hello")
			_ = f.Close()
			if e != nil {
				results <- ClipboardResult{Err: e}
			}
			return
		}
		results <- ClipboardResult{Err: errors.New("missing pipe FD")}
	}()
	if err := c.ReadTextAsync(context.Background(), edit.MaxClipboardBytes, func(b []byte, e error) { results <- ClipboardResult{Data: b, Err: e} }); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-results:
		if result.Err != nil || string(result.Data) != "hello" {
			t.Fatalf("result: %+v", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("read blocked")
	}
}
func TestClipboardBoundAndCancel(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	results := make(chan ClipboardResult, 1)
	go func() { data, e := io.ReadAll(io.LimitReader(r, 5)); results <- ClipboardResult{data, e} }()
	_, _ = w.Write([]byte("123456"))
	select {
	case result := <-results:
		if len(result.Data) != 5 {
			t.Fatal(result)
		}
	case <-time.After(time.Second):
		t.Fatal("limit blocked")
	}
	state := edit.State{}
	if ok, _ := state.PasteAsync([]byte{0xff}, nil); ok {
		t.Fatal("accepted invalid UTF-8")
	}
	if ok, _ := state.PasteAsync(make([]byte, edit.MaxClipboardBytes+1), nil); ok {
		t.Fatal("accepted oversized paste")
	}
}
