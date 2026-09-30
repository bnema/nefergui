//go:build linux

package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/bnema/nefergui/internal/harness"
	"github.com/bnema/nefergui/internal/keyboard"
	"github.com/bnema/nefergui/internal/platform/wayland"
)

func TestSecretBufferEditing(t *testing.T) {
	b := NewSecretBuffer(8)
	b.append([]byte("a"))
	b.append([]byte("é")) // two bytes, one code point
	b.append([]byte("z"))
	if b.Len() != 3 || string(b.Bytes()) != "aéz" {
		t.Fatalf("len=%d", b.Len())
	}
	b.append([]byte{0xff}) // invalid UTF-8 is dropped
	b.append(bytes.Repeat([]byte("x"), 9))
	if b.Len() != 3 {
		t.Fatal("invalid or oversized chunk accepted")
	}
	b.backspace()
	b.backspace() // removes the two-byte code point whole
	if b.Len() != 1 || !bytes.Equal(b.Bytes(), []byte("a")) {
		t.Fatalf("backspace: len=%d", b.Len())
	}
	backing := b.buf
	b.Wipe()
	if b.Len() != 0 || len(b.Bytes()) != 0 || !bytes.Equal(backing, make([]byte, 8)) {
		t.Fatal("buffer not wiped")
	}
	if got := (LockState{Mask: 3}).Bullets(); got != "•••" {
		t.Fatal(got)
	}
}

func TestConsumeSubmitIsAsyncAndNeverUnlocks(t *testing.T) {
	var got [][]byte
	secret := NewSecretBuffer(8)
	l := &locker{cfg: LockConfig{Secret: secret, OnSubmit: func(b []byte) { got = append(got, bytes.Clone(b)) }}}
	enter := keyboard.SecretKey{Kind: keyboard.SecretReturn, Pressed: true}
	secret.append([]byte("pw"))
	l.consume(enter, nil) // before locked: only clears
	if len(got) != 0 || secret.Len() != 0 || l.unlock {
		t.Fatalf("submit before locked: got=%d len=%d unlock=%v", len(got), secret.Len(), l.unlock)
	}
	l.locked = true
	secret.append([]byte("pw"))
	l.consume(enter, nil)
	if len(got) != 1 || string(got[0]) != "pw" || secret.Len() != 0 || l.unlock {
		t.Fatalf("submit after locked: got=%d len=%d unlock=%v", len(got), secret.Len(), l.unlock)
	}
	l.consume(enter, nil) // empty buffer submits nothing
	if len(got) != 1 {
		t.Fatal("empty submit reached the callback")
	}
}

func TestShouldUnlockGate(t *testing.T) {
	l := &locker{conn: &wayland.Connection{}}
	if l.shouldUnlock() {
		t.Fatal("unlock without request")
	}
	l.unlock = true // a request held before locked
	if l.shouldUnlock() {
		t.Fatal("unlock before locked")
	}
	if err := l.handle(wayland.Event{Kind: wayland.LockAcquired}); err != nil {
		t.Fatal(err)
	}
	if !l.shouldUnlock() {
		t.Fatal("held request not honoured once locked")
	}
	l.unlock = false
	if l.shouldUnlock() {
		t.Fatal("unlock without request after locked")
	}
}

func TestConsumeWipesWhenOnSubmitPanics(t *testing.T) {
	secret := NewSecretBuffer(8)
	l := &locker{locked: true, cfg: LockConfig{Secret: secret, OnSubmit: func([]byte) { panic("boom") }}}
	secret.append([]byte("pw"))
	func() {
		defer func() { _ = recover() }()
		l.consume(keyboard.SecretKey{Kind: keyboard.SecretReturn, Pressed: true}, nil)
	}()
	if secret.Len() != 0 || !bytes.Equal(secret.buf, make([]byte, 8)) {
		t.Fatal("secret not wiped after OnSubmit panic")
	}
}

func TestForwardWakeStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { newRuntime().forwardWake(ctx, make(chan struct{})); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("forwardWake outlived its context")
	}
}

func TestHarnessRunLock(t *testing.T) {
	if os.Getenv("NEFERGUI_HARNESS") != "1" {
		t.Skip("set NEFERGUI_HARNESS=1 and NEFERGUI_NEFERWL for the session-lock harness")
	}
	root := t.TempDir()
	script := filepath.Join(root, "input.txt")
	// Enough time for lock + first frame; then a wrong secret, clear, and the right one.
	if err := os.WriteFile(script, []byte("sleep 4s\ntype wrong\nkey Escape\nsleep 200ms\ntype open\nkey Return\nsleep 1s\n"), 0600); err != nil {
		t.Fatal(err)
	}
	result, code := harness.Run(context.Background(), harness.Options{Size: "400x300", Scale: "1", Layout: "us", Background: "#111111", Out: filepath.Join(root, "artifacts"), AllowUnpinned: os.Getenv("NEFERGUI_NEFERWL") != "", Input: script, Timeout: 40 * time.Second, ReadyTimeout: 10 * time.Second, Client: []string{os.Args[0], "-test.run=^TestHarnessRunLockClient$", "-test.v"}})
	out, _ := os.ReadFile(result.Artifacts["client_stdout"])
	if code != harness.Pass {
		errOut, _ := os.ReadFile(result.Artifacts["client_stderr"])
		t.Fatalf("code=%d %s\nstdout:\n%s\nstderr:\n%s", code, result.Error, out, errOut)
	}
	t.Logf("client output:\n%s", out)
	// Frames coalesce, so only the shape is asserted: the mask rises, Escape
	// clears it, and the accepted secret is wiped.
	for _, want := range []string{"LOCK locked", "LOCK frame", "LOCK submit ok", "LOCK unlocked [0 ", "LOCK wake stopped"} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	// A mask above zero was drawn before the final wipe to zero.
	if !regexp.MustCompile(`LOCK unlocked \[0( [0-9]+)* [1-9][0-9]* 0\]`).Match(out) {
		t.Fatalf("no mask above 0 before the final 0 in:\n%s", out)
	}
}

func TestHarnessRunLockClient(t *testing.T) {
	if os.Getenv("NEFERGUI_DEBUG_DIR") == "" {
		t.Skip("harness child only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	secret := NewSecretBuffer(64)
	unlock, status := make(chan struct{}, 1), make(chan LockStatus, 4)
	frames := 0
	var maskSeen []int
	wake := make(chan struct{})
	err := RunLock(ctx, LockConfig{
		Secret: secret,
		Wake:   wake,
		View: func(f *Frame, s LockState) {
			if frames == 0 {
				fmt.Println("LOCK frame")
			}
			frames++
			if len(maskSeen) == 0 || maskSeen[len(maskSeen)-1] != s.Mask {
				maskSeen = append(maskSeen, s.Mask)
			}
			col := f.Root().Column()
			col.Text(s.Bullets(), Key("mask"))
		},
		OnLocked: func() { fmt.Println("LOCK locked") },
		Unlock:   unlock,
		Status:   status,
		OnSubmit: func(b []byte) {
			// Test-only comparison; real callers hand the bytes to a verifier.
			if string(b) == "open" {
				fmt.Println("LOCK submit ok")
				status <- LockBusy
				go func() { time.Sleep(300 * time.Millisecond); unlock <- struct{}{} }()
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if secret.Len() != 0 {
		t.Fatal("secret not wiped after submit")
	}
	select {
	case wake <- struct{}{}:
		t.Fatal("wake forwarder outlived RunLock")
	case <-time.After(200 * time.Millisecond):
		fmt.Println("LOCK wake stopped")
	}
	fmt.Println("LOCK unlocked", maskSeen)
}

func TestOutputFailurePolicy(t *testing.T) {
	boom := errors.New("boom")
	if err := outputFailure(true, "lock surface", "DP-1", boom); !errors.Is(err, boom) {
		t.Fatalf("initial output failure must abort: %v", err)
	}
	if err := outputFailure(false, "lock surface", "DP-2", boom); err != nil {
		t.Fatalf("hotplug failure must be skipped: %v", err)
	}
	var reported []string
	l := &locker{cfg: LockConfig{OnOutputError: func(o string, err error) {
		if errors.Is(err, boom) {
			reported = append(reported, o)
		}
	}}}
	if err := l.outputFailure(false, "lock surface", "DP-4", boom); err != nil || len(reported) != 1 || reported[0] != "DP-4" {
		t.Fatalf("hotplug failure must be reported and skipped: %v %v", err, reported)
	}
	if err := l.outputFailure(true, "lock surface", "DP-5", boom); err == nil || len(reported) != 1 {
		t.Fatalf("initial failure must abort without a skip report: %v %v", err, reported)
	}
	for _, initial := range []bool{true, false} {
		err := outputFailure(initial, "lock surface", "DP-3", fmt.Errorf("x: %w", wayland.ErrLockFinished))
		if !errors.Is(err, ErrLockFinished) || !errors.Is(err, wayland.ErrLockFinished) {
			t.Fatalf("finished (initial=%v) must map to ErrLockFinished: %v", initial, err)
		}
	}
}

// Events kept during setup reach handle() exactly as reader events do.
func TestHeldLockEventsDriveLocker(t *testing.T) {
	onLocked := 0
	l := &locker{conn: &wayland.Connection{}, cfg: LockConfig{OnLocked: func() { onLocked++ }}}
	if err := l.handle(wayland.Event{Kind: wayland.LockAcquired}); err != nil || !l.locked || onLocked != 1 {
		t.Fatalf("locked lost: err=%v locked=%v calls=%d", err, l.locked, onLocked)
	}
	l.unlock = true
	if !l.shouldUnlock() {
		t.Fatal("unlock not possible after a held locked")
	}
	if err := l.handle(wayland.Event{Kind: wayland.LockFinished}); !errors.Is(err, ErrLockFinished) {
		t.Fatalf("finished: %v", err)
	}
}

// A second locker is refused by the compositor with `finished`, which must
// surface as ErrLockFinished however early it arrives during setup.
func TestHarnessRunLockRefused(t *testing.T) {
	if os.Getenv("NEFERGUI_HARNESS") != "1" {
		t.Skip("set NEFERGUI_HARNESS=1 and NEFERGUI_NEFERWL for the session-lock harness")
	}
	result, code := harness.Run(context.Background(), harness.Options{Size: "400x300", Scale: "1", Layout: "us", Background: "#111111", Out: filepath.Join(t.TempDir(), "artifacts"), AllowUnpinned: os.Getenv("NEFERGUI_NEFERWL") != "", Timeout: 40 * time.Second, ReadyTimeout: 10 * time.Second, Client: []string{os.Args[0], "-test.run=^TestHarnessRunLockRefusedClient$", "-test.v"}})
	out, _ := os.ReadFile(result.Artifacts["client_stdout"])
	// A protected compositor withholds debug PNGs by design, so the harness may
	// report a missing frame; this test only needs the client's refusal result.
	noFrame := code == harness.ProcessFailure && strings.HasPrefix(result.Error, "no frame")
	if (code != harness.Pass && !noFrame) || !strings.Contains(string(out), "LOCK refused ok") {
		errOut, _ := os.ReadFile(result.Artifacts["client_stderr"])
		t.Fatalf("code=%d %s\nstdout:\n%s\nstderr:\n%s", code, result.Error, out, errOut)
	}
}

func TestHarnessRunLockRefusedClient(t *testing.T) {
	if os.Getenv("NEFERGUI_DEBUG_DIR") == "" {
		t.Skip("harness child only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	first, err := wayland.ConnectConnection(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if err = first.AcquireLock(); err != nil {
		t.Fatal(err)
	}
	// The first owner stays alive and unanswered; the compositor refuses the next.
	err = RunLock(ctx, LockConfig{Secret: NewSecretBuffer(8), View: func(*Frame, LockState) {}})
	if !errors.Is(err, ErrLockFinished) {
		t.Fatalf("refused lock: %v", err)
	}
	fmt.Println("LOCK refused ok")
}
