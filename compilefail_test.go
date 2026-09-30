package nefergui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestOptionCompileFailures(t *testing.T) {
	for _, name := range []string{"button_placeholder", "input_level"} {
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(filepath.Join("testdata", "compilefail", name+".go.txt"))
			if err != nil {
				t.Fatal(err)
			}
			// A temporary directory inside the module resolves the local package without a replace.
			dir, err := os.MkdirTemp(".", "compilefail-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			if err = os.WriteFile(filepath.Join(dir, "bad.go"), src, 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("go", "test", "./"+dir)
			cmd.Env = append(os.Environ(), "GOWORK=off", "CGO_ENABLED=0")
			out, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(out), "does not implement") {
				t.Fatalf("expected type error, got %v: %s", err, out)
			}
		})
	}
}
