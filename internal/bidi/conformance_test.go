package bidi

import (
	"bufio"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// nefergui: verify pinned Unicode 17 BidiCharacterTest paragraph level, L1
// character levels and L2 visual order against the full upstream corpus.
func TestBidiCharacterConformance(t *testing.T) {
	f, err := os.Open("testdata/BidiCharacterTest.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 4096), 2<<20)
	lineNo, failures, cases := 0, 0, 0
	for scan.Scan() {
		lineNo++
		s := strings.TrimSpace(scan.Text())
		if s == "" || s[0] == '#' {
			continue
		}
		parts := strings.Split(s, ";")
		if len(parts) != 5 {
			t.Fatalf("line %d: fields %d", lineNo, len(parts))
		}
		text := make([]rune, 0)
		for _, hex := range strings.Fields(parts[0]) {
			n, e := strconv.ParseUint(hex, 16, 32)
			if e != nil {
				t.Fatal(e)
			}
			text = append(text, rune(n))
		}
		mode, e := strconv.Atoi(parts[1])
		if e != nil || mode < 0 || mode > 2 {
			t.Fatalf("line %d: base %q", lineNo, parts[1])
		}
		base := Auto
		if mode == 0 {
			base = LTR
		} else if mode == 1 {
			base = RTL
		}
		breaks := []int{len(text)}
		levels, para, e := Resolve(text, base, breaks)
		if e != nil {
			t.Fatalf("line %d: %v", lineNo, e)
		}
		expectedPara, e := strconv.Atoi(parts[2])
		if e != nil {
			t.Fatal(e)
		}
		var expectedLevels []uint8
		for _, token := range strings.Fields(parts[3]) {
			if token == "x" {
				expectedLevels = append(expectedLevels, 255)
				continue
			}
			n, e := strconv.Atoi(token)
			if e != nil {
				t.Fatal(e)
			}
			expectedLevels = append(expectedLevels, uint8(n))
		}
		order, e := Reorder(text, levels, breaks)
		if e != nil {
			t.Fatal(e)
		}
		var expectedOrder []int
		for _, token := range strings.Fields(parts[4]) {
			n, e := strconv.Atoi(token)
			if e != nil {
				t.Fatal(e)
			}
			expectedOrder = append(expectedOrder, n)
		}
		if len(expectedOrder) == 0 {
			expectedOrder = []int{}
		}
		if len(order) != 1 {
			t.Fatal("missing line")
		}
		if int(para) != expectedPara || len(levels) != len(expectedLevels) || !reflect.DeepEqual(order[0], expectedOrder) || !matchLevels(text, levels, expectedLevels) {
			failures++
			if failures <= 10 {
				t.Errorf("line %d: para %d/%d levels %v/%v order %v/%v", lineNo, para, expectedPara, levels, expectedLevels, order[0], expectedOrder)
			}
		}
		cases++
	}
	if err := scan.Err(); err != nil {
		t.Fatal(err)
	}
	if failures > 0 {
		t.Fatalf("%d/%d conformance failures", failures, cases)
	}
	t.Logf("%d Unicode 17 BidiCharacterTest cases", cases)
}
func matchLevels(text []rune, got, want []uint8) bool {
	if len(got) != len(want) {
		return false
	}
	for i, n := range want {
		if n == 255 {
			p, _ := LookupRune(text[i])
			if !isRemovedByX9(p.Class()) {
				panic(fmt.Sprintf("non-X9 x at %d", i))
			}
			continue
		}
		if got[i] != n {
			return false
		}
	}
	return true
}
