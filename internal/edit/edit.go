// Package edit owns headless, grapheme-safe text state. Offsets are UTF-8 bytes;
// caret movement is logical, not visual. Rendering may project these offsets
// onto bidi runs to paint discontiguous selection rectangles.
package edit

import (
	"github.com/go-text/typesetting/segmenter"
	"strings"
	"unicode/utf8"
)

const MaxClipboardBytes = 1 << 20

// Clipboard is a bounded text/plain;charset=utf-8 port. Implementations own
// transport, cancellation and file descriptors; the editor never retains one.
type Clipboard interface {
	ReadText(limit int) ([]byte, error)
	WriteText([]byte) error
}

// IME is a text-input-v3-shaped output port. All offsets are UTF-8 bytes.
type IME interface {
	Enable()
	Disable()
	Surrounding(text string, cursor, anchor int)
	CursorRect(x, y, w, h float64)
	ContentType(password, multiline bool)
}
type State struct {
	Value                    string
	Cursor, Anchor           int
	PreferredX               float64
	PreferredXValid          bool // retained across successive wrapped-line moves
	Preedit                  string
	PreeditBegin, PreeditEnd int
	active                   bool
}

func Boundaries(s string) []int {
	r := []rune(s)
	var seg segmenter.Segmenter
	seg.Init(r)
	it := seg.GraphemeIterator()
	b := []int{0}
	offset := 0
	for it.Next() {
		offset += len(string(it.Grapheme().Text))
		b = append(b, offset)
	}
	return b
}
func (s *State) Sync(value string) {
	if !utf8.ValidString(value) {
		value = strings.ToValidUTF8(value, "\uFFFD")
	}
	s.Value = value
	s.Cursor = clamp(value, s.Cursor)
	s.Anchor = clamp(value, s.Anchor)
}
func clamp(s string, pos int) int {
	b := Boundaries(s)
	if pos < 0 {
		return 0
	}
	last := 0
	for _, v := range b {
		if v > pos {
			break
		}
		last = v
	}
	return last
}
func (s *State) Select(start, end int) {
	s.Anchor = clamp(s.Value, start)
	s.Cursor = clamp(s.Value, end)
}
func (s *State) Range() (int, int) {
	if s.Cursor < s.Anchor {
		return s.Cursor, s.Anchor
	}
	return s.Anchor, s.Cursor
}
func (s *State) Move(to int, shift bool) {
	s.Cursor = clamp(s.Value, to)
	if !shift {
		s.Anchor = s.Cursor
	}
}
func (s *State) Left(shift bool) {
	b := Boundaries(s.Value)
	p := 0
	for _, v := range b {
		if v >= s.Cursor {
			break
		}
		p = v
	}
	s.Move(p, shift)
}
func (s *State) Right(shift bool) {
	for _, v := range Boundaries(s.Value) {
		if v > s.Cursor {
			s.Move(v, shift)
			return
		}
	}
	s.Move(len(s.Value), shift)
}
func (s *State) replace(start, end int, text string) bool {
	if !utf8.ValidString(text) {
		return false
	}
	start = clamp(s.Value, start)
	end = clamp(s.Value, end)
	if end < start {
		start, end = end, start
	}
	next := s.Value[:start] + text + s.Value[end:]
	if next == s.Value {
		return false
	}
	s.Value = next
	// A newly inserted combining mark can merge into the previous grapheme.
	// Round forward to the first boundary at or after the insertion endpoint.
	endOfInsert := start + len(text)
	for _, boundary := range Boundaries(next) {
		if boundary >= endOfInsert {
			s.Move(boundary, false)
			break
		}
	}
	return true
}
func (s *State) Insert(text string) bool { a, b := s.Range(); return s.replace(a, b, text) }
func (s *State) Delete(backward bool, word bool) bool {
	a, b := s.Range()
	if a == b {
		if word {
			if backward {
				a = s.WordLeft()
			} else {
				b = s.WordRight()
			}
		} else {
			if backward {
				p := *s
				p.Left(false)
				a = p.Cursor
			} else {
				p := *s
				p.Right(false)
				b = p.Cursor
			}
		}
	}
	return s.replace(a, b, "")
}
func (s *State) WordLeft() int {
	var seg segmenter.Segmenter
	seg.Init([]rune(s.Value))
	it := seg.WordIterator()
	p := 0
	for it.Next() {
		w := it.Word()
		start := len(string([]rune(s.Value)[:w.Offset]))
		if start >= s.Cursor {
			break
		}
		p = start
	}
	return clamp(s.Value, p)
}
func (s *State) WordRight() int {
	var seg segmenter.Segmenter
	seg.Init([]rune(s.Value))
	it := seg.WordIterator()
	for it.Next() {
		w := it.Word()
		end := len(string([]rune(s.Value)[:w.Offset+len(w.Text)]))
		if end > s.Cursor {
			return clamp(s.Value, end)
		}
	}
	return len(s.Value)
}

// WordAt returns exactly the word-iterator segment containing the byte hit.
// On a boundary it prefers the segment to the right; whitespace and punctuation
// are segments in their own right. Offsets and results are grapheme-aligned.
func (s *State) WordAt(offset int) (int, int) {
	offset = clamp(s.Value, offset)
	runes := []rune(s.Value)
	var seg segmenter.Segmenter
	seg.Init(runes)
	it := seg.WordIterator()
	previous := 0
	for it.Next() {
		w := it.Word()
		start := clamp(s.Value, len(string(runes[:w.Offset])))
		end := clamp(s.Value, len(string(runes[:w.Offset+len(w.Text)])))
		// The iterator skips non-word spans. Preserve those as their own
		// segment instead of expanding a hit across adjacent words.
		if offset >= previous && offset < start {
			return previous, start
		}
		if offset >= start && offset < end {
			return start, end
		}
		previous = end
	}
	if offset < len(s.Value) {
		return previous, len(s.Value)
	}
	return len(s.Value), len(s.Value)
}
func (s *State) Mask() string { return strings.Repeat("•", len(Boundaries(s.Value))-1) }
func (s *State) Copy(c Clipboard, password bool) error {
	if c == nil || password {
		return nil
	}
	a, b := s.Range()
	if b-a > MaxClipboardBytes {
		return nil
	}
	return c.WriteText([]byte(s.Value[a:b]))
}
func (s *State) Cut(c Clipboard, password bool) (bool, error) {
	if c == nil || password {
		return false, nil
	}
	a, b := s.Range()
	if a == b {
		return false, nil
	}
	if err := s.Copy(c, false); err != nil {
		return false, err
	}
	return s.replace(a, b, ""), nil
}
func (s *State) Paste(c Clipboard) (bool, error) {
	if c == nil {
		return false, nil
	}
	data, err := c.ReadText(MaxClipboardBytes + 1)
	if err != nil {
		return false, err
	}
	if len(data) > MaxClipboardBytes || !utf8.Valid(data) {
		return false, nil
	}
	return s.Insert(string(data)), nil
}
func (s *State) Focus(ime IME, password, multiline bool) {
	s.active = ime != nil
	if ime != nil {
		ime.Enable()
		ime.ContentType(password, multiline)
		s.Surround(ime, password)
	}
}
func (s *State) Blur(ime IME) {
	if s.active && ime != nil {
		ime.Disable()
	}
	s.active = false
	s.Preedit = ""
	s.PreeditBegin = 0
	s.PreeditEnd = 0
}
func (s *State) Surround(ime IME, password bool) {
	if ime == nil {
		return
	}
	if password {
		ime.Surrounding("", 0, 0)
	} else {
		ime.Surrounding(s.Value, s.Cursor, s.Anchor)
	}
}
func (s *State) IMEActive() bool { return s.active }

// IMEBatch is applied atomically on text-input-v3 done. Delete offsets are
// relative to the cursor in bytes, and rounded outwards to grapheme edges.
type IMEBatch struct {
	Preedit                   string
	Begin, End                int
	Commit                    string
	DeleteBefore, DeleteAfter int
}

func (s *State) Done(b IMEBatch) bool {
	changed := false
	if b.DeleteBefore > 0 || b.DeleteAfter > 0 {
		a := s.Cursor - b.DeleteBefore
		if a < 0 {
			a = 0
		}
		z := s.Cursor + b.DeleteAfter
		if z > len(s.Value) {
			z = len(s.Value)
		}
		a = clamp(s.Value, a)
		for _, v := range Boundaries(s.Value) {
			if v >= z {
				z = v
				break
			}
		}
		changed = s.replace(a, z, "")
	}
	if b.Commit != "" {
		changed = s.Insert(b.Commit) || changed
		// A commit consumes the outstanding preedit even when no new preedit is sent.
		s.Preedit = ""
		s.PreeditBegin, s.PreeditEnd = 0, 0
	}
	if utf8.ValidString(b.Preedit) {
		s.Preedit = b.Preedit
		s.PreeditBegin = b.Begin
		s.PreeditEnd = b.End
	} else {
		s.Preedit = ""
		s.PreeditBegin, s.PreeditEnd = 0, 0
	}
	return changed
}
