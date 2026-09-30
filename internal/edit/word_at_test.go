package edit

import "testing"

func TestWordAtSegmentAndRightBoundary(t *testing.T) {
	s := State{Value: "one two!"}
	for _, tc := range []struct{ offset, start, end int }{{0, 0, 3}, {2, 0, 3}, {3, 3, 4}, {4, 4, 7}, {6, 4, 7}, {7, 7, 8}, {8, 8, 8}} {
		a, b := s.WordAt(tc.offset)
		if a != tc.start || b != tc.end {
			t.Errorf("WordAt(%d)=%d:%d want %d:%d", tc.offset, a, b, tc.start, tc.end)
		}
	}
	s.Sync("é 猫")
	if a, b := s.WordAt(len("é ")); a != len("é ") || b != len(s.Value) {
		t.Fatalf("UTF-8 boundary %d:%d", a, b)
	}
}
