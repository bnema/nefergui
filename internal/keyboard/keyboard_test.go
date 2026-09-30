package keyboard

import (
	"os"
	"testing"
	"time"

	"github.com/bnema/purego-xkbcommon/raw"
)

func TestLayoutsAndRepeat(t *testing.T) {
	k := requiredKeyboard(t, "en_US.UTF-8")
	defer k.Close()
	now := time.Now()
	for _, tc := range []struct {
		layout string
		code   uint32
		want   string
	}{{"us", 16, "q"}, {"fr", 16, "a"}, {"de", 21, "z"}} {
		if err := k.Rules(tc.layout, ""); err != nil {
			t.Fatal(err)
		}
		key, err := k.Event(tc.code, true, now)
		if err != nil || key.Text != tc.want || key.Physical != tc.code || key.Logical == 0 {
			t.Fatalf("%s: %+v %v", tc.layout, key, err)
		}
		k.Event(tc.code, false, now)
	}
	if err := k.Rules("de", ""); err != nil {
		t.Fatal(err)
	}
	idx, err := k.keymap.ModIndex("Mod5")
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Mask(1<<idx, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	key, err := k.Event(18, true, now)
	if err != nil || key.Text != "€" {
		t.Fatalf("AltGr: %+v %v", key, err)
	}
	k.Event(18, false, now)
	k.Mask(0, 0, 0, 0, 0, 0)
	k.RepeatInfo(20, 300*time.Millisecond)
	k.Event(16, true, now)
	if _, ok := k.Tick(now.Add(299 * time.Millisecond)); ok {
		t.Fatal("early repeat")
	}
	if _, ok := k.Tick(now.Add(300 * time.Millisecond)); !ok {
		t.Fatal("missing repeat")
	}
	k.Focus(false)
	if _, ok := k.Tick(now.Add(time.Second)); ok {
		t.Fatal("repeat after focus loss")
	}
	k.Focus(true)
	k.Event(16, true, now)
	k.Event(16, false, now)
	if _, ok := k.Tick(now.Add(time.Second)); ok {
		t.Fatal("repeat after release")
	}
}
func TestComposeAndGroups(t *testing.T) {
	k := requiredKeyboard(t, "en_US.UTF-8")
	var err error
	defer k.Close()
	if err = k.Rules("de", ""); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	dead, err := k.Event(41, true, now)
	if err != nil || dead.Logical != raw.XKB_KEY_dead_circumflex || dead.Text != "" {
		t.Fatalf("dead: %+v %v", dead, err)
	}
	k.Event(41, false, now)
	composed, err := k.Event(18, true, now)
	if err != nil || composed.Text != "ê" {
		t.Fatalf("compose: %+v %v", composed, err)
	}
	if err = k.Rules("us,fr", ""); err != nil {
		t.Fatal(err)
	}
	if err = k.Mask(0, 0, 0, 0, 0, 1); err != nil {
		t.Fatal(err)
	}
	grouped, err := k.Event(16, true, now)
	if err != nil || grouped.Text != "a" || grouped.Group != 1 {
		t.Fatalf("group: %+v %v", grouped, err)
	}
}

func TestKeymapFDReplacement(t *testing.T) {
	k := requiredKeyboard(t, "")
	var err error
	defer k.Close()
	if err = k.Rules("us", ""); err != nil {
		t.Fatal(err)
	}
	// A malformed regular file must be consumed without discarding the old map.
	f, err := os.CreateTemp(t.TempDir(), "bad-keymap")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.WriteString("bad keymap"); err != nil {
		t.Fatal(err)
	}
	if err = k.ReplaceFD(int(f.Fd()), len("bad keymap")); err == nil {
		t.Fatal("bad keymap accepted")
	}
	if _, err = f.Stat(); err == nil {
		t.Fatal("keymap FD not closed")
	}
	if err = k.ReplaceFD(-1, 0); err == nil {
		t.Fatal("invalid fd accepted")
	}
	key, err := k.Event(16, true, time.Now())
	if err != nil || key.Text != "q" {
		t.Fatalf("old map lost: %+v %v", key, err)
	}
	k.Event(16, false, time.Now())
}

func TestValidFDReplacementCancelsRepeat(t *testing.T) {
	k := requiredKeyboard(t, "")
	defer k.Close()
	if err := k.Rules("us", ""); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	k.RepeatInfo(20, 100*time.Millisecond)
	press, err := k.Event(16, true, now)
	if err != nil || press.Text != "q" {
		t.Fatalf("old map press: %+v %v", press, err)
	}
	data, err := os.ReadFile("testdata/fr.xkb")
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.CreateTemp(t.TempDir(), "keymap")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write(append(data, 0)); err != nil {
		t.Fatal(err)
	}
	if err = k.ReplaceFD(int(file.Fd()), len(data)+1); err != nil {
		t.Fatal(err)
	}
	if _, err = file.Stat(); err == nil {
		t.Fatal("FD not consumed")
	}
	if _, ok := k.Tick(now.Add(time.Second)); ok {
		t.Fatal("repeat survived keymap replacement")
	}
	if _, ok := k.NextRepeat(); ok {
		t.Fatal("repeat deadline survived replacement")
	}
	key, err := k.Event(16, true, now)
	if err != nil || key.Text != "a" {
		t.Fatalf("new map press: %+v %v", key, err)
	}
}

// Required offline gate: only explicitly opted-out hosts may skip libxkbcommon.
func requiredKeyboard(t *testing.T, locale string) *Keyboard {
	t.Helper()
	k, err := New(locale)
	if err != nil {
		if os.Getenv("NEFERGUI_SKIP_XKB") == "1" {
			t.Skipf("xkb unavailable: %v", err)
		}
		t.Fatal(err)
	}
	return k
}

func TestRepeatTracksCurrentMaskAndCompose(t *testing.T) {
	k := requiredKeyboard(t, "en_US.UTF-8")
	defer k.Close()
	if err := k.Rules("us,fr", ""); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	k.RepeatInfo(10, 100*time.Millisecond)
	press, err := k.Event(16, true, now)
	if err != nil || press.Text != "q" {
		t.Fatalf("press: %+v %v", press, err)
	}
	shift, err := k.keymap.ModIndex("Shift")
	if err != nil {
		t.Fatal(err)
	}
	if err = k.Mask(1<<shift, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	rep, ok := k.Tick(now.Add(100 * time.Millisecond))
	if !ok || rep.Text != "Q" || rep.Logical != raw.XKB_KEY_Q || !rep.Repeat || !rep.Shift || rep.Ctrl {
		t.Fatalf("shift repeat: %+v %t", rep, ok)
	}
	if err = k.Mask(0, 0, 0, 0, 0, 1); err != nil {
		t.Fatal(err)
	}
	rep, ok = k.Tick(now.Add(200 * time.Millisecond))
	if !ok || rep.Text != "a" || rep.Group != 1 {
		t.Fatalf("layout repeat: %+v %t", rep, ok)
	}
	k.Focus(false)
	k.Focus(true)
	if err = k.Rules("de", ""); err != nil {
		t.Fatal(err)
	}
	k.Event(41, true, now)
	k.Event(41, false, now)
	// A repeat must not replay the composed press-time character.
	composed, err := k.Event(18, true, now)
	if err != nil || composed.Text != "ê" {
		t.Fatalf("compose press: %+v %v", composed, err)
	}
	rep, ok = k.Tick(now.Add(100 * time.Millisecond))
	if !ok || rep.Text != "e" {
		t.Fatalf("post-compose repeat: %+v %t", rep, ok)
	}
}
