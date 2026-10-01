package edit

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/mock"
)

func TestGraphemesAndIME(t *testing.T) {
	s := State{}
	s.Sync("e\u0301👩‍💻a")
	s.Move(len(s.Value), false)
	s.Left(false)
	if s.Cursor != len("e\u0301👩‍💻") {
		t.Fatal(s.Cursor)
	}
	s.Left(false)
	if s.Cursor != len("e\u0301") {
		t.Fatal(s.Cursor)
	}
	s.Select(0, len("e\u0301"))
	if !s.Delete(true, false) || s.Value != "👩‍💻a" {
		t.Fatalf("%+v", s)
	}
	s.Select(len("👩‍💻"), len("👩‍💻"))
	if !s.Done(IMEBatch{DeleteBefore: 1, Commit: "é"}) || s.Value != "éa" {
		t.Fatalf("delete bytes: %+v", s)
	}
	s.Done(IMEBatch{Preedit: "候補", Begin: 0, End: len("候補")})
	if s.Preedit != "候補" || s.Value != "éa" {
		t.Fatal(s)
	}
	ime := NewMockIME(t)
	ime.EXPECT().Enable().Once()
	ime.EXPECT().Surrounding("éa", mock.Anything, mock.Anything).Maybe()
	ime.EXPECT().CursorRect(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()
	ime.EXPECT().ContentType(false, false).Once()
	ime.EXPECT().Disable().Once()
	s.Focus(ime, false, false)
	s.Blur(ime)
	if s.Preedit != "" {
		t.Fatal(s)
	}
}
func TestClipboardAndMask(t *testing.T) {
	s := State{}
	s.Sync("s3crét")
	s.Select(0, len(s.Value))
	c := NewMockClipboard(t) // a masked copy must not write
	if err := s.Copy(c, true); err != nil {
		t.Fatal(err)
	}
	if s.Mask() != "••••••" {
		t.Fatal(s.Mask())
	}
	read := func(data []byte) {
		c.EXPECT().ReadText(mock.Anything).Return(data, nil).Once()
	}
	read([]byte{0xff})
	if changed, _ := s.Paste(c); changed {
		t.Fatal(s)
	}
	read([]byte(strings.Repeat("x", MaxClipboardBytes+1)))
	if changed, _ := s.Paste(c); changed {
		t.Fatal(s)
	}
	read([]byte("ok"))
	if changed, _ := s.Paste(c); !changed || s.Value != "ok" {
		t.Fatal(s)
	}
}
func FuzzEdit(f *testing.F) {
	f.Add([]byte("e\u0301👩‍💻🧪"))
	f.Add([]byte{0xff, 0x80, 0x00})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 2048 {
			return
		}
		var s State
		for _, op := range data {
			switch op % 7 {
			case 0:
				s.Left(false)
			case 1:
				s.Right(true)
			case 2:
				s.Insert("e\u0301")
			case 3:
				s.Insert("👩‍💻")
			case 4:
				s.Delete(true, false)
			case 5:
				s.Delete(false, true)
			case 6:
				s.Sync(strings.ToValidUTF8(string(data), "�"))
			}
			if !utf8.ValidString(s.Value) {
				t.Fatal("invalid UTF-8")
			}
			for _, p := range []int{s.Cursor, s.Anchor} {
				ok := false
				for _, v := range Boundaries(s.Value) {
					if p == v {
						ok = true
					}
				}
				if !ok {
					t.Fatalf("not boundary %d in %q", p, s.Value)
				}
			}
		}
	})
}
