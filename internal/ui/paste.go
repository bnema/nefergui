package ui

import (
	"context"
	"github.com/bnema/nefergui/internal/edit"
)

// startPaste is invoked during Build. A worker may only queue its completion;
// it must not touch the editor or any other frame-owned state.
func (r *runtime) startPaste(clipboard edit.AsyncClipboard, target *identity) error {
	if r.pasteCancel != nil {
		r.pasteCancel()
	}
	parent := r.pasteCtx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	r.pasteCancel = cancel
	r.pasteID++
	id := r.pasteID
	err := clipboard.ReadTextAsync(ctx, edit.MaxClipboardBytes, func(data []byte, err error) {
		r.mu.Lock()
		current := r.pasteID == id && ctx.Err() == nil
		r.mu.Unlock()
		if current {
			r.Queue(inputEvent{Target: target, Kind: "paste-result", Text: string(data), Err: err})
		}
	})
	if err != nil {
		cancel()
	}
	return err
}
