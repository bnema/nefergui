//go:build linux

package session

import (
	"path/filepath"
	"strings"
	"testing"
)

// A requested debug artifact that cannot be written must fail Close, so Run
// cannot report success with missing evidence.
func TestCloseReportsDebugSummaryWriteError(t *testing.T) {
	s := &Session{debugDir: filepath.Join(t.TempDir(), "missing")}
	err := s.Close()
	if err == nil || !strings.Contains(err.Error(), "resource summary") {
		t.Fatalf("Close error = %v, want resource summary write error", err)
	}
	ok := &Session{debugDir: t.TempDir()}
	if err := ok.Close(); err != nil {
		t.Fatalf("Close with writable debug dir: %v", err)
	}
}
