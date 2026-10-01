package harness

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// sizePattern accepts one headless output size, or several comma-separated
// ones (HEADLESS-1, HEADLESS-2, ... left to right).
var sizePattern = regexp.MustCompile(`^[1-9][0-9]*x[1-9][0-9]*(,[1-9][0-9]*x[1-9][0-9]*)*$`)
var layoutPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
var socketPattern = regexp.MustCompile(`^wayland-[0-9]+$`)

func Config(size, scale, layout, background string) (string, error) {
	if !sizePattern.MatchString(size) || !layoutPattern.MatchString(layout) {
		return "", fmt.Errorf("invalid size or layout")
	}
	if scale != "1" && scale != "1.25" && scale != "1.5" && scale != "2" {
		return "", fmt.Errorf("unsupported scale %q", scale)
	}
	if _, err := ParseColor(background); err != nil {
		return "", err
	}
	// Wayland debug logs record each client cursor shape change, which
	// systemtests read from the compositor run log.
	var b strings.Builder
	for i, s := range strings.Split(size, ",") {
		fmt.Fprintf(&b, "output.HEADLESS-%d = %s\noutput.HEADLESS-%d.scale = %s\n", i+1, s, i+1, scale)
	}
	fmt.Fprintf(&b, "keyboard.layout = %s\nbackground = %s\nlog.debug = wayland\n", layout, background)
	return b.String(), nil
}

// FramesDir is where NeferWL writes the screenshots of the first output: the
// frames directory itself, or its HEADLESS-1 subdirectory with several outputs.
func FramesDir(frames, size string) string {
	if strings.Contains(size, ",") {
		return filepath.Join(frames, "HEADLESS-1")
	}
	return frames
}

// DiscoverSocket only accepts a real Unix socket paired with NeferWL's state file.
// A fresh private runtime directory prevents picking up a foreign session.
func DiscoverSocket(ctx context.Context, runtime string) (string, error) {
	entries, err := os.ReadDir(filepath.Join(runtime, "neferwl"))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var names []string
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		name, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || !socketPattern.MatchString(name) {
			continue
		}
		fi, err := os.Lstat(filepath.Join(runtime, name))
		if err == nil && fi.Mode()&os.ModeSocket != 0 {
			c, err := (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(runtime, name))
			if err == nil {
				_ = c.Close()
				names = append(names, name)
			}
		}
	}
	if len(names) > 1 {
		return "", fmt.Errorf("multiple compositor sockets: %v", names)
	}
	if len(names) == 1 {
		return names[0], nil
	}
	return "", nil
}
