package edit

import (
	"context"
	"unicode/utf8"
)

// AsyncClipboard starts an asynchronous paste. The callback must be delivered
// to the UI loop, never invoked by a Wayland reader or transport goroutine.
// Unlike Clipboard.ReadText, ReadTextAsync does not wait for another process.
// Implementations must cap reads at limit bytes plus one and honor cancellation.
type AsyncClipboard interface {
	ReadTextAsync(ctx context.Context, limit int, deliver func([]byte, error)) error
	WriteText([]byte) error
}

// PasteAsync applies a completed paste on the UI loop. It is the counterpart
// of Paste for asynchronous transports. It retains the same UTF-8/size checks.
func (s *State) PasteAsync(data []byte, err error) (bool, error) {
	if err != nil {
		return false, err
	}
	if len(data) > MaxClipboardBytes || !utf8.Valid(data) {
		return false, nil
	}
	return s.Insert(string(data)), nil
}
