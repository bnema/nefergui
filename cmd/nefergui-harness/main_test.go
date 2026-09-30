package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUsage(t *testing.T) {
	if code := run(nil); code != 2 {
		t.Fatalf("missing run: %d", code)
	}
	if code := run([]string{"run"}); code != 2 {
		t.Fatalf("missing out: %d", code)
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "expect.json")
	for _, text := range []string{`{"probes":[],"unexpected":1}`, `{} {}`, `{"probes":[]}{`} {
		if err := os.WriteFile(p, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		if code := run([]string{"run", "--expect", p, "--out", filepath.Join(dir, "out")}); code != 2 {
			t.Fatalf("%q: %d", text, code)
		}
	}
}
func TestUnpinnedFlag(t *testing.T) {
	t.Setenv("NEFERGUI_NEFERWL", "/bin/true")
	dir := t.TempDir()
	if code := run([]string{"run", "--out", filepath.Join(dir, "denied")}); code != 2 {
		t.Fatalf("override without flag: %d", code)
	}
	if code := run([]string{"run", "--allow-unpinned", "--out", filepath.Join(dir, "allowed"), "--timeout", "1s", "--ready-timeout", "500ms"}); code == 2 {
		t.Fatalf("override with flag: %d", code)
	}
}
