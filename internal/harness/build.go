package harness

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

const NeferWLSHA = "1d16b83bf90f9e8189ad21b27772382c96b9f9a8"

func NeferWL(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if path := os.Getenv("NEFERGUI_NEFERWL"); path != "" {
		if fi, err := os.Stat(path); err != nil || fi.IsDir() || fi.Mode()&0111 == 0 {
			return "", fmt.Errorf("NEFERGUI_NEFERWL is not executable: %s", path)
		}
		return path, nil
	}
	cache := os.Getenv("XDG_CACHE_HOME")
	if cache == "" {
		var err error
		cache, err = os.UserCacheDir()
		if err != nil {
			return "", err
		}
	}
	dir := filepath.Join(cache, "nefergui", "harness", "neferwl-"+NeferWLSHA)
	bin := filepath.Join(dir, "neferwl")
	if fi, err := os.Stat(bin); err == nil && fi.Mode().IsRegular() && fi.Mode()&0111 != 0 {
		return bin, nil
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	stage, err := os.MkdirTemp(dir, "build-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	cmd := exec.Command("go", "install", "github.com/bnema/neferwl/cmd/neferwl@"+NeferWLSHA)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = withEnv(os.Environ(), map[string]string{"GOWORK": "off", "GOBIN": stage})
	stop := context.AfterFunc(ctx, func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	})
	output, err := cmd.CombinedOutput()
	stop()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil {
		return "", fmt.Errorf("neferwl build: %w: %s", err, strings.TrimSpace(string(output)))
	}
	if err := os.Rename(filepath.Join(stage, "neferwl"), bin); err != nil {
		return "", err
	}
	return bin, nil
}

// isolatedEnv drops host display credentials before session-specific values are added.
func isolatedEnv(base []string) []string {
	out := make([]string, 0, len(base))
	for _, e := range base {
		k, _, _ := strings.Cut(e, "=")
		if k != "DISPLAY" && k != "WAYLAND_DISPLAY" && k != "WAYLAND_SOCKET" {
			out = append(out, e)
		}
	}
	return out
}
func withEnv(base []string, overrides map[string]string) []string {
	out := make([]string, 0, len(base)+len(overrides))
	for _, e := range base {
		k, _, _ := strings.Cut(e, "=")
		if _, ok := overrides[k]; ok || k == "WAYLAND_SOCKET" {
			continue
		}
		out = append(out, e)
	}
	for k, v := range overrides {
		out = append(out, k+"="+v)
	}
	return out
}
