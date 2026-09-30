package harness

import (
	"errors"
	"image"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestCaptureSelectsByCommitTimestamp(t *testing.T) {
	dir := t.TempDir()
	token := time.Now().Add(-time.Minute).Truncate(time.Second)
	for _, tc := range []struct {
		name     string
		mtime    time.Time
		complete bool
	}{
		{"frame-000001.png", token.Add(-time.Second), true},
		{"frame-000002.png", token.Add(time.Second), true},
		{"frame-000003.png", token.Add(2 * time.Second), false},
		{"frame-000004.png", token.Add(3 * time.Second), true},
		{"latest.png", token.Add(4 * time.Second), true},
	} {
		path := filepath.Join(dir, tc.name)
		if tc.complete {
			if err := SavePNG(path, image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(path, []byte("incomplete"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, tc.mtime, tc.mtime); err != nil {
			t.Fatal(err)
		}
	}
	request := filepath.Join(dir, "capture-request")
	if err := os.WriteFile(request, []byte(strconv.FormatInt(token.UnixNano(), 10)), 0600); err != nil {
		t.Fatal(err)
	}
	after, err := captureRequestTime(request)
	if err != nil || !after.Equal(token) {
		t.Fatalf("token %v %v", after, err)
	}
	// frame-2 finished after the token but may have been rendered before it.
	name, err := nextFrame(dir, after)
	if err != nil || name != "frame-000003.png" {
		t.Fatalf("selected %q %v", name, err)
	}
	if name, err := nextFrame(dir, token.Add(2500*time.Millisecond)); err != nil || name != "" {
		t.Fatalf("one screenshot after the token must not be selected: %q %v", name, err)
	}
	capture := filepath.Join(dir, "capture.png")
	if err := copyNextFrame(dir, after, capture); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("incomplete frame accepted: %v", err)
	}
	if err := SavePNG(filepath.Join(dir, name), image.NewNRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(dir, name), token.Add(2*time.Second), token.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := copyNextFrame(dir, after, capture); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadPNG(capture); err != nil || got.Bounds().Dx() != 2 {
		t.Fatalf("capture %v %v", got, err)
	}
	if err := os.WriteFile(request, []byte("not-a-time"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRequestTime(request); err == nil {
		t.Fatal("invalid token accepted")
	}
}
