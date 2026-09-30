package edit

import "testing"

func TestInsertCombiningMarkLeavesCaretAfterMergedGrapheme(t *testing.T) {
	var s State
	if !s.Insert("e") || !s.Insert("\u0301") {
		t.Fatal("insert failed")
	}
	if s.Value != "e\u0301" || s.Cursor != len(s.Value) || s.Anchor != s.Cursor {
		t.Fatalf("merged grapheme caret: %+v", s)
	}
	if !s.Insert("x") || s.Value != "e\u0301x" || s.Cursor != len(s.Value) {
		t.Fatalf("subsequent insertion: %+v", s)
	}
}
