package ui

import (
	"context"
	"errors"
	"testing"

	"github.com/bnema/nefergui/internal/edit"
	"github.com/stretchr/testify/mock"
)

func TestAsyncPasteFrameAndCancellation(t *testing.T) {
	r := newRuntime()
	c := NewMockAsyncClipboard(t) // synchronous ReadText is not expected
	var contexts []context.Context
	var callbacks []func([]byte, error)
	c.EXPECT().ReadTextAsync(mock.Anything, edit.MaxClipboardBytes, mock.Anything).
		RunAndReturn(func(ctx context.Context, _ int, deliver func([]byte, error)) error {
			contexts = append(contexts, ctx)
			callbacks = append(callbacks, deliver)
			return nil
		})
	r.clipboard = asyncClipboard{c}
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
	if contexts[0].Err() != context.Canceled {
		t.Fatal("old read not cancelled")
	}
	callbacks[0]([]byte("old"), nil)
	callbacks[1]([]byte("Ada"), nil)
	if !r.Build(view) || !changed || name != "Ada" {
		t.Fatalf("paste: changed=%v value=%q", changed, name)
	}
	paste()
	callbacks[2]([]byte{0xff}, nil)
	r.Build(view)
	if name != "Ada" || changed {
		t.Fatal("invalid UTF-8 accepted")
	}
	paste()
	callbacks[3](make([]byte, edit.MaxClipboardBytes+1), nil)
	r.Build(view)
	if name != "Ada" || changed {
		t.Fatal("oversize accepted")
	}
	paste()
	callbacks[4](nil, errors.New("transfer failed"))
	r.Build(view)
	if debugDiagnostics && len(diagnostics) != 1 {
		t.Fatalf("missing error diagnostic: %v", diagnostics)
	}
}
