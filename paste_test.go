package nefergui

import (
	"context"
	"errors"
	"github.com/bnema/nefergui/internal/edit"
	"testing"
)

type asyncClip struct {
	contexts  []context.Context
	callbacks []func([]byte, error)
}

func (*asyncClip) ReadText(int) ([]byte, error) { panic("sync clipboard read on loop") }
func (*asyncClip) WriteText([]byte) error       { return nil }
func (c *asyncClip) ReadTextAsync(ctx context.Context, limit int, deliver func([]byte, error)) error {
	if limit != edit.MaxClipboardBytes {
		panic("wrong limit")
	}
	c.contexts = append(c.contexts, ctx)
	c.callbacks = append(c.callbacks, deliver)
	return nil
}
func TestAsyncPasteFrameAndCancellation(t *testing.T) {
	r := newRuntime()
	c := &asyncClip{}
	r.clipboard = c
	name := ""
	var changed bool
	var diagnostics []string
	view := func(f *Frame) {
		changed = f.Root().Input("name", &name, Key("name")).Changed()
		diagnostics = f.Diagnostics()
	}
	r.Build(view)
	paste := func() {
		r.Queue(inputEvent{Target: r.Target(0), Kind: "edit-key", Text: "v", Ctrl: true})
		if !r.Build(view) {
			t.Fatal("no frame")
		}
		if changed {
			t.Fatal("paste changed before delivery")
		}
	}
	paste()
	paste()
	if c.contexts[0].Err() != context.Canceled {
		t.Fatal("old read not cancelled")
	}
	c.callbacks[0]([]byte("old"), nil)
	c.callbacks[1]([]byte("Ada"), nil)
	if !r.Build(view) || !changed || name != "Ada" {
		t.Fatalf("paste: changed=%v value=%q", changed, name)
	}
	paste()
	c.callbacks[2]([]byte{0xff}, nil)
	r.Build(view)
	if name != "Ada" || changed {
		t.Fatal("invalid UTF-8 accepted")
	}
	paste()
	c.callbacks[3](make([]byte, edit.MaxClipboardBytes+1), nil)
	r.Build(view)
	if name != "Ada" || changed {
		t.Fatal("oversize accepted")
	}
	paste()
	c.callbacks[4](nil, errors.New("transfer failed"))
	r.Build(view)
	if debugDiagnostics && len(diagnostics) != 1 {
		t.Fatalf("missing error diagnostic: %v", diagnostics)
	}
}
