//go:build linux

package wayland

import (
	"errors"
	"math"
	"unicode/utf8"

	"github.com/bnema/nefergui/internal/edit"
	"github.com/bnema/wlturbo"
	"github.com/bnema/wlturbo/protocol/core"
	"github.com/bnema/wlturbo/protocol/textinput"
	"github.com/bnema/wlturbo/wl"
)

// IMEEvent is posted from the Wayland reader to the session loop. Only a done
// event produces an edit.IMEBatch; intermediate protocol events are buffered.
type IMEEvent struct {
	Kind          string
	Surface       uint32
	Text          string
	Begin, End    int32
	Before, After uint32
	Serial        uint32
}

// IME is loop-owned. On errors in the edit.IME interface, Err records the
// first protocol send failure; the session owner should inspect it each frame.
type IME struct {
	manager                      *textinput.TextInputManagerV3
	input                        *textinput.TextInputV3
	surface                      uint32
	post                         func(IMEEvent)
	batch                        edit.IMEBatch
	commits                      uint32
	entered, enabled             bool
	requested, synced            bool
	pendingSurround              string
	hasSurround                  bool
	pendingCursor, pendingAnchor int
	pendingContent               bool
	password, multiline          bool
	pendingRect                  bool
	rect                         [4]int32
	Err                          error
}

var _ edit.IME = (*IME)(nil)

// NewIME returns nil if text-input-v3 is not announced; keyboard remains usable.
func NewIME(d *wl.Display, seat *core.Seat, surface *core.Surface, post func(IMEEvent)) (*IME, error) {
	if d == nil || seat == nil || surface == nil {
		return nil, nil
	}
	manager := textinput.NewTextInputManagerV3(d.Context())
	if _, err := d.Registry().BindNegotiated(textinput.TextInputManagerV3Interface, 2, manager); err != nil {
		d.Context().Unregister(manager)
		if errors.Is(err, wlturbo.ErrGlobalNotFound) {
			return nil, nil
		}
		return nil, err
	}
	input, err := manager.GetTextInput(seat)
	if err != nil {
		_ = manager.Destroy()
		return nil, err
	}
	m := &IME{manager: manager, input: input, surface: surface.ID(), post: post}
	input.OnEnter(func(id uint32) { post(IMEEvent{Kind: "enter", Surface: id}) })
	input.OnLeave(func(id uint32) { post(IMEEvent{Kind: "leave", Surface: id}) })
	input.OnPreeditString(func(s string, b, e int32) { post(IMEEvent{Kind: "preedit", Text: s, Begin: b, End: e}) })
	input.OnCommitString(func(s string) { post(IMEEvent{Kind: "commit", Text: s}) })
	input.OnDeleteSurroundingText(func(b, a uint32) { post(IMEEvent{Kind: "delete", Before: b, After: a}) })
	input.OnDone(func(serial uint32) { post(IMEEvent{Kind: "done", Serial: serial}) })
	return m, nil
}
func (m *IME) send(err error) {
	if err != nil && m.Err == nil {
		m.Err = err
	}
}
func (m *IME) commit() {
	if m == nil || m.input == nil {
		return
	}
	if err := m.input.Commit(); err != nil {
		m.send(err)
	} else {
		m.commits++
	}
}
func (m *IME) Enable() {
	if m == nil {
		return
	}
	m.requested = true
	if !m.entered || m.enabled {
		return
	}
	m.send(m.input.Enable())
	if m.Err == nil {
		m.enabled = true
		m.synced = true
		// Initial state is double-buffered with enable in one commit.
		if m.pendingContent {
			m.pendingContent = false
			m.send(m.input.SetContentType(m.contentHint(), m.contentPurpose()))
		}
		if m.hasSurround {
			m.hasSurround = false
			m.send(m.input.SetSurroundingText(m.pendingSurround, int32(m.pendingCursor), int32(m.pendingAnchor)))
		}
		if m.pendingRect {
			m.pendingRect = false
			m.send(m.input.SetCursorRectangle(m.rect[0], m.rect[1], m.rect[2], m.rect[3]))
		}
		if m.Err == nil {
			m.commit()
		}
	}
}
func (m *IME) Disable() {
	if m == nil {
		return
	}
	m.requested = false
	if !m.enabled {
		return
	}
	m.send(m.input.Disable())
	m.enabled = false
	m.synced = true
	m.commit()
	m.batch = edit.IMEBatch{}
}
func (m *IME) Surrounding(text string, cursor, anchor int) {
	if m == nil {
		return
	}
	text, cursor, anchor = surroundingWindow(text, cursor, anchor)
	if !m.enabled || !m.synced {
		m.pendingSurround, m.pendingCursor, m.pendingAnchor = text, cursor, anchor
		m.hasSurround = true
		return
	}
	m.send(m.input.SetSurroundingText(text, int32(cursor), int32(anchor)))
	m.commit()
}
func (m *IME) CursorRect(x, y, w, h float64) {
	if m == nil {
		return
	}
	if !validRect(x, y, w, h) {
		return
	}
	m.rect = [4]int32{int32(math.Round(x)), int32(math.Round(y)), int32(math.Round(w)), int32(math.Round(h))}
	if !m.enabled || !m.synced {
		m.pendingRect = true
		return
	}
	m.send(m.input.SetCursorRectangle(m.rect[0], m.rect[1], m.rect[2], m.rect[3]))
	m.commit()
}
func validRect(x, y, w, h float64) bool {
	for _, v := range []float64{x, y, w, h} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < -math.MaxInt32 || v > math.MaxInt32 {
			return false
		}
	}
	return w >= 0 && h >= 0
}
func (m *IME) ContentType(password, multiline bool) {
	if m == nil {
		return
	}
	if !m.enabled || !m.synced {
		m.pendingContent = true
		m.password, m.multiline = password, multiline
		return
	}
	m.password, m.multiline = password, multiline
	m.send(m.input.SetContentType(m.contentHint(), m.contentPurpose()))
	m.commit()
}
func (m *IME) contentHint() uint32 {
	hint := uint32(textinput.CONTENT_HINT_NONE)
	if m.password {
		return textinput.CONTENT_HINT_SENSITIVE_DATA | textinput.CONTENT_HINT_HIDDEN_TEXT
	}
	if m.multiline {
		return textinput.CONTENT_HINT_MULTILINE
	}
	return hint
}
func (m *IME) contentPurpose() uint32 {
	if m.password {
		return textinput.CONTENT_PURPOSE_PASSWORD
	}
	return textinput.CONTENT_PURPOSE_NORMAL
}

// surroundingWindow keeps a UTF-8 aligned 4000-byte window including the
// cursor and as much of the selection as possible, centered near the cursor.
func surroundingWindow(s string, cursor, anchor int) (string, int, int) {
	if !utf8.ValidString(s) {
		s = ""
	}
	cursor = max(0, min(cursor, len(s)))
	anchor = max(0, min(anchor, len(s)))
	for cursor > 0 && cursor < len(s) && !utf8.RuneStart(s[cursor]) {
		cursor--
	}
	for anchor > 0 && anchor < len(s) && !utf8.RuneStart(s[anchor]) {
		anchor--
	}
	if len(s) <= 4000 {
		return s, cursor, anchor
	}
	start := max(0, cursor-2000)
	if anchor < cursor && cursor-anchor <= 4000 {
		start = min(start, anchor)
	}
	if anchor > cursor && anchor-cursor <= 4000 {
		start = max(start, anchor-4000)
	}
	start = min(start, len(s)-4000)
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	end := min(len(s), start+4000)
	for end > start && end < len(s) && !utf8.RuneStart(s[end]) {
		end--
	}
	cursor -= start
	anchor = max(0, min(anchor-start, end-start))
	return s[start:end], cursor, anchor
}

// ApplyIME buffers reader events until done. The serial is the compositor's
// count of client commit requests, not a keyboard serial. Per text-input-v3,
// even a mismatched serial must apply the edit batch as normal; it only
// prohibits changing protocol state until a matching done is received.
func (m *IME) ApplyIME(ev IMEEvent) (edit.IMEBatch, bool) {
	if m == nil {
		return edit.IMEBatch{}, false
	}
	switch ev.Kind {
	case "enter":
		if ev.Surface == m.surface {
			m.entered = true
			m.synced = true
			m.batch = edit.IMEBatch{}
			if m.requested {
				m.enabled = false
				m.Enable()
			}
		}
	case "leave":
		if ev.Surface == m.surface {
			m.entered = false
			m.enabled = false
			m.synced = false
			m.batch = edit.IMEBatch{}
		}
	case "preedit":
		if !m.entered || !m.enabled {
			return edit.IMEBatch{}, false
		}
		m.batch.Preedit = ev.Text
		m.batch.Begin = int(ev.Begin)
		m.batch.End = int(ev.End)
	case "commit":
		if !m.entered || !m.enabled {
			return edit.IMEBatch{}, false
		}
		m.batch.Commit = ev.Text
	case "delete":
		if !m.entered || !m.enabled {
			return edit.IMEBatch{}, false
		}
		m.batch.DeleteBefore = int(min(ev.Before, 4000))
		m.batch.DeleteAfter = int(min(ev.After, 4000))
	case "done":
		batch := m.batch
		m.batch = edit.IMEBatch{}
		if !m.entered || !m.enabled {
			return edit.IMEBatch{}, false
		}
		m.synced = ev.Serial == m.commits
		if m.synced {
			m.flushPending()
		}
		return batch, true
	}
	return edit.IMEBatch{}, false
}

// flushPending sends state only after a matching done. Mismatched done batches
// still reach the editor, but do not modify the text-input object's state.
func (m *IME) flushPending() {
	if m.pendingContent {
		m.pendingContent = false
		m.ContentType(m.password, m.multiline)
	}
	if m.hasSurround {
		s, c, a := m.pendingSurround, m.pendingCursor, m.pendingAnchor
		m.hasSurround = false
		m.Surrounding(s, c, a)
	}
	if m.pendingRect {
		m.pendingRect = false
		m.send(m.input.SetCursorRectangle(m.rect[0], m.rect[1], m.rect[2], m.rect[3]))
		m.commit()
	}
}
func (m *IME) Close() {
	if m == nil {
		return
	}
	if m.input != nil {
		_ = m.input.Destroy()
		m.input = nil
	}
	if m.manager != nil {
		_ = m.manager.Destroy()
		m.manager = nil
	}
}
