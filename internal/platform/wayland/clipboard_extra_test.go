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

	"github.com/bnema/wlturbo/protocol/core"
	"golang.org/x/sys/unix"
)

func TestClipboardOfferReplacement(t *testing.T) {
	c, d, server := clipboardFixture(t)
	first := core.NewDataOffer(d.Context())
	first.SetID(30)
	d.Context().Register(first)
	second := core.NewDataOffer(d.Context())
	second.SetID(31)
	d.Context().Register(second)
	for _, o := range []*core.DataOffer{first, second} {
		c.ApplyClipboard(ClipboardEvent{kind: "offer", offer: o})
	}
	c.ApplyClipboard(ClipboardEvent{kind: "mime", offer: first, mime: mimePlain})
	c.ApplyClipboard(ClipboardEvent{kind: "mime", offer: second, mime: mimeUTF8})
	c.ApplyClipboard(ClipboardEvent{kind: "selection", offer: second})
	if c.offer != second || c.bestMIME() != mimeUTF8 {
		t.Fatal("latest selection not used")
	}
	inputRequest(t, server) // old offer destroyed
}
func TestClipboardWriteSendOverSocketpair(t *testing.T) {
	c, d, server := clipboardFixture(t)
	// WriteText sends create_data_source, three offers and set_selection.
	if err := c.WriteText([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	// Client writes can coalesce into a single socket read.
	_ = server.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	_, _ = server.Read(make([]byte, 4096))
	source := c.source
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	// Wayland event string uses byte length with trailing NUL, padded to 4.
	mime := append([]byte(mimeUTF8), 0)
	padded := make([]byte, (len(mime)+3)&^3)
	copy(padded, mime)
	body := make([]byte, 4+len(padded))
	body[0] = byte(len(mime))
	copy(body[4:], padded)
	msg := inputFrame(source.ID(), 1)
	msg = append(msg, body...)
	msg[6] = byte(len(msg))
	msg[7] = byte(len(msg) >> 8)
	inputSend(t, server, msg, int(w.Fd()))
	if err = d.Dispatch(); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 5)
	_ = r.SetReadDeadline(time.Now().Add(time.Second))
	if _, err = r.Read(data); err != nil || string(data) != "hello" {
		t.Fatalf("send %q: %v", data, err)
	}
}
func TestClipboardReadLimitAndTimeout(t *testing.T) {
	fd := make([]int, 2)
	if err := unix.Pipe2(fd, unix.O_CLOEXEC|unix.O_NONBLOCK); err != nil {
		t.Fatal(err)
	}
	r := os.NewFile(uintptr(fd[0]), "read")
	defer r.Close()
	defer unix.Close(fd[1])
	if _, err := unix.Write(fd[1], []byte("123456")); err != nil {
		t.Fatal(err)
	}
	if _, err := readClipboard(context.Background(), r, 5); err == nil {
		t.Fatal("oversize accepted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := readClipboard(ctx, r, 5); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout: %v", err)
	}
}

func TestClipboardSendOwnershipAndCancellation(t *testing.T) {
	c, d, server := clipboardFixture(t)
	if err := c.WriteText([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	inputRequest(t, server)
	source := c.source
	for _, mime := range []string{mimeUTF8, "application/octet-stream"} {
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		data := append([]byte(mime), 0)
		padded := make([]byte, (len(data)+3)&^3)
		copy(padded, data)
		msg := inputFrame(source.ID(), 1, uint32(len(data)))
		msg = append(msg, padded...)
		msg[6], msg[7] = byte(len(msg)), byte(len(msg)>>8)
		inputSend(t, server, msg, int(w.Fd()))
		if err = d.Dispatch(); err != nil {
			t.Fatal(err)
		}
		_ = w.Close()
		_ = r.SetReadDeadline(time.Now().Add(time.Second))
		buf := make([]byte, 8)
		n, err := r.Read(buf)
		if err != nil && err != io.EOF {
			t.Fatal(err)
		}
		if mime == mimeUTF8 && string(buf[:n]) != "hello" || mime != mimeUTF8 && n != 0 {
			t.Fatalf("send %s: %q %v", mime, buf[:n], err)
		}
		_ = r.Close()
	}
	inputSend(t, server, inputFrame(source.ID(), 2))
	if err := d.Dispatch(); err != nil {
		t.Fatal(err)
	}
	if c.source != nil {
		t.Fatal("cancelled source retained")
	}
	inputRequest(t, server) // source.destroy
}

func TestClipboardSelectionKeepsDnD(t *testing.T) {
	c, d, server := clipboardFixture(t)
	drag := core.NewDataOffer(d.Context())
	drag.SetID(70)
	d.Context().Register(drag)
	selected := core.NewDataOffer(d.Context())
	selected.SetID(71)
	d.Context().Register(selected)
	c.ApplyClipboard(ClipboardEvent{kind: "offer", offer: drag})
	c.ApplyClipboard(ClipboardEvent{kind: "enter", offer: drag})
	c.ApplyClipboard(ClipboardEvent{kind: "offer", offer: selected})
	c.ApplyClipboard(ClipboardEvent{kind: "selection", offer: selected})
	if c.dnd != drag || c.offer != selected {
		t.Fatal("lost active drag or selection")
	}
	_ = server.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	if _, err := server.Read(make([]byte, 128)); err == nil {
		t.Fatal("active drag destroyed")
	}
	c.ApplyClipboard(ClipboardEvent{kind: "drop"})
	if c.dnd != nil {
		t.Fatal("drop retained drag")
	}
	inputRequest(t, server)
}

func TestClipboardCloseCancelsReadWithoutDelivery(t *testing.T) {
	c, d, server := clipboardFixture(t)
	offer := core.NewDataOffer(d.Context())
	offer.SetID(80)
	d.Context().Register(offer)
	c.ApplyClipboard(ClipboardEvent{kind: "offer", offer: offer})
	c.ApplyClipboard(ClipboardEvent{kind: "mime", offer: offer, mime: mimePlain})
	c.ApplyClipboard(ClipboardEvent{kind: "selection", offer: offer})
	delivered := make(chan struct{}, 1)
	if err := c.ReadTextAsync(context.Background(), 32, func([]byte, error) { delivered <- struct{}{} }); err != nil {
		t.Fatal(err)
	}
	// Keep the compositor's write end open so the read cannot finish before Close.
	buf, oob := make([]byte, 256), make([]byte, 128)
	_ = server.SetReadDeadline(time.Now().Add(time.Second))
	_, n, _, _, err := server.ReadMsgUnix(buf, oob)
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := unix.ParseSocketControlMessage(oob[:n])
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range msgs {
		fds, err := unix.ParseUnixRights(&msg)
		if err != nil {
			t.Fatal(err)
		}
		for _, fd := range fds {
			defer unix.Close(fd)
		}
	}
	done := make(chan struct{})
	go func() { c.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close did not wait for cancelled read")
	}
	select {
	case <-delivered:
		t.Fatal("delivered after Close")
	default:
	}
}

func TestClipboardCloseInsideDelivery(t *testing.T) {
	c, d, server := clipboardFixture(t)
	offer := core.NewDataOffer(d.Context())
	offer.SetID(81)
	d.Context().Register(offer)
	c.ApplyClipboard(ClipboardEvent{kind: "offer", offer: offer})
	c.ApplyClipboard(ClipboardEvent{kind: "mime", offer: offer, mime: mimePlain})
	c.ApplyClipboard(ClipboardEvent{kind: "selection", offer: offer})
	done := make(chan struct{})
	if err := c.ReadTextAsync(context.Background(), 32, func([]byte, error) { c.Close(); close(done) }); err != nil {
		t.Fatal(err)
	}
	clipboardFinishRead(t, server)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close deadlocked in delivery")
	}
}

func TestClipboardCloseWhileDeliveryBlocks(t *testing.T) {
	c, d, server := clipboardFixture(t)
	offer := core.NewDataOffer(d.Context())
	offer.SetID(82)
	d.Context().Register(offer)
	c.ApplyClipboard(ClipboardEvent{kind: "offer", offer: offer})
	c.ApplyClipboard(ClipboardEvent{kind: "mime", offer: offer, mime: mimePlain})
	c.ApplyClipboard(ClipboardEvent{kind: "selection", offer: offer})
	entered := make(chan struct{})
	unblock := make(chan struct{})
	callbackDone := make(chan struct{})
	if err := c.ReadTextAsync(context.Background(), 32, func([]byte, error) {
		close(entered)
		<-unblock
		close(callbackDone)
	}); err != nil {
		t.Fatal(err)
	}
	clipboardFinishRead(t, server)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("delivery not called")
	}
	done := make(chan struct{})
	go func() { c.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close blocked on delivery")
	}
	close(unblock)
	select {
	case <-callbackDone:
	case <-time.After(time.Second):
		t.Fatal("callback did not finish")
	}
}

// Consume receive and close the transferred write FD to complete the read.
func clipboardFinishRead(t *testing.T, server *net.UnixConn) {
	t.Helper()
	buf, oob := make([]byte, 256), make([]byte, 128)
	_ = server.SetReadDeadline(time.Now().Add(time.Second))
	_, n, _, _, err := server.ReadMsgUnix(buf, oob)
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := unix.ParseSocketControlMessage(oob[:n])
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		fds, err := unix.ParseUnixRights(&m)
		if err != nil {
			t.Fatal(err)
		}
		for _, fd := range fds {
			_, _ = unix.Write(fd, []byte("ok"))
			_ = unix.Close(fd)
		}
	}
}
