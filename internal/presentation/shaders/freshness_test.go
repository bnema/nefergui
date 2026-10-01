package shaders

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestEmbeddedSPIRV(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
	}{{"list vertex", ListVertex}, {"list fragment", ListFragment}} {
		if len(tc.data) < 20 || !bytes.Equal(tc.data[:4], []byte{3, 2, 35, 7}) || len(tc.data)%4 != 0 {
			t.Fatalf("%s: not SPIR-V", tc.name)
		}
	}
}

func TestShaderFreshness(t *testing.T) {
	bin, err := exec.LookPath("glslc")
	if err != nil {
		t.Skip("glslc not installed; committed SPIR-V remains usable")
	}
	for _, tc := range []struct {
		file, stage string
		embedded    []byte
	}{{"list.vert", "vertex", ListVertex}, {"list.frag", "fragment", ListFragment}} {
		out := filepath.Join(t.TempDir(), "shader.spv")
		cmd := exec.Command(bin, "-O", "-fshader-stage="+tc.stage, "-o", out, tc.file)
		if log, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("compile %s: %v: %s", tc.file, err, log)
		}
		actual, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(actual, tc.embedded) {
			t.Fatalf("%s SPIR-V stale; run go generate ./...", tc.file)
		}
	}
}
