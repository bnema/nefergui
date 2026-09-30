package keyboard

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"testing"
	"time"

	xkb "github.com/bnema/purego-xkbcommon"
	"github.com/bnema/purego-xkbcommon/raw"
)

func TestSecretASCIIAndMultibyteWithoutRetainedText(t *testing.T) {
	k := requiredKeyboard(t, "en_US.UTF-8")
	defer k.Close()
	for _, tc := range []struct {
		layout string
		code   uint32
		mod    string
		want   []byte
	}{
		{"us", 16, "", []byte{'q'}}, {"de", 18, "Mod5", []byte{0xe2, 0x82, 0xac}},
	} {
		if err := k.Rules(tc.layout, ""); err != nil {
			t.Fatal(err)
		}
		if tc.mod != "" {
			idx, err := k.keymap.ModIndex(tc.mod)
			if err != nil {
				t.Fatal(err)
			}
			if err := k.Mask(1<<idx, 0, 0, 0, 0, 0); err != nil {
				t.Fatal(err)
			}
		}
		var scratch [512]byte
		calls := 0
		err := k.EventSecret(tc.code, true, time.Now(), scratch[:], func(key SecretKey, data []byte) {
			calls++
			if key.Kind != SecretBytes || !key.Pressed || key.Repeat || !bytes.Equal(data, tc.want) {
				t.Fatal("wrong scoped bytes/metadata")
			}
			if cap(data) != len(data) {
				t.Fatal("callback can access scratch beyond payload")
			}
			if len(data) > 0 && &data[0] != &scratch[0] {
				t.Fatal("bytes copied instead of caller scratch")
			}
		})
		if err != nil || calls != 1 {
			t.Fatal(calls, err)
		}
		if !bytes.Equal(scratch[:], make([]byte, len(scratch))) || len(k.held) != 0 {
			t.Fatal("plaintext retained")
		}
		if key := k.secretHeld[tc.code]; key.Kind != SecretBytes || key.Physical != tc.code {
			t.Fatal("missing scalar held metadata")
		}
	}
	for _, typ := range []reflect.Type{reflect.TypeFor[SecretKey]()} {
		for i := 0; i < typ.NumField(); i++ {
			if typ.Field(i).Type.Kind() == reflect.String || typ.Field(i).Type.Kind() == reflect.Slice {
				t.Fatal("secret metadata retains payload")
			}
		}
	}
}

func TestSecretComposeAndClear(t *testing.T) {
	k := requiredKeyboard(t, "en_US.UTF-8")
	defer k.Close()
	if err := k.Rules("de", ""); err != nil {
		t.Fatal(err)
	}
	var scratch [512]byte
	now := time.Now()
	calls := 0
	consume := func(key SecretKey, data []byte) {
		if !key.Pressed {
			return
		}
		calls++
		if key.Kind != SecretBytes || !bytes.Equal(data, []byte{0xc3, 0xaa}) {
			t.Fatal("compose bytes incorrect")
		}
	}
	for _, ev := range []struct {
		code uint32
		down bool
	}{{41, true}, {41, false}, {18, true}} {
		if err := k.EventSecret(ev.code, ev.down, now, scratch[:], consume); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 || k.composePending {
		t.Fatal("compose did not finish")
	}
	k.ClearSecret()
	if len(k.secretHeld) != 0 || k.repeating != 0 {
		t.Fatal("clear retained held/repeat")
	}
	if err := k.EventSecret(41, true, now, scratch[:], consume); err != nil {
		t.Fatal(err)
	}
	k.Focus(false)
	k.Focus(true)
	if err := k.EventSecret(18, true, now, scratch[:], func(key SecretKey, data []byte) {
		if !bytes.Equal(data, []byte{'e'}) {
			t.Fatal("focus loss preserved compose")
		}
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSecretControlsAndRepeat(t *testing.T) {
	k := requiredKeyboard(t, "en_US.UTF-8")
	defer k.Close()
	if err := k.Rules("us", ""); err != nil {
		t.Fatal(err)
	}
	k.RepeatInfo(10, 100*time.Millisecond)
	now := time.Now()
	var scratch [512]byte
	for _, tc := range []struct {
		code   uint32
		kind   SecretKind
		repeat bool
	}{{14, SecretBackspace, true}, {28, SecretReturn, false}, {1, SecretEscape, false}} {
		k.ClearSecret()
		calls := 0
		consume := func(key SecretKey, data []byte) {
			if len(data) != 0 || key.Kind != tc.kind {
				t.Fatal("control leaked text")
			}
			calls++
		}
		if err := k.EventSecret(tc.code, true, now, scratch[:], consume); err != nil {
			t.Fatal(err)
		}
		if err := k.EventSecret(tc.code, true, now, scratch[:], consume); err != nil {
			t.Fatal(err)
		}
		got, err := k.TickSecret(now.Add(100*time.Millisecond), scratch[:], consume)
		if err != nil || got != tc.repeat {
			t.Fatal(got, err)
		}
		want := 1
		if tc.repeat {
			want = 3
		}
		if calls != want {
			t.Fatalf("control callbacks=%d want=%d", calls, want)
		}
	}
	k.ClearSecret()
	idx, err := k.keymap.ModIndex("Control")
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Mask(1<<idx, 0, 0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := k.EventSecret(22, true, now, scratch[:], func(key SecretKey, data []byte) {
		if key.Kind != SecretClear || len(data) != 0 {
			t.Fatal("CtrlU not named control")
		}
	}); err != nil {
		t.Fatal(err)
	}
	k.ClearSecret()
	_ = k.Mask(0, 0, 0, 0, 0, 0)
	if err := k.EventSecret(16, true, now, scratch[:], func(SecretKey, []byte) {}); err != nil {
		t.Fatal(err)
	}
	shift, _ := k.keymap.ModIndex("Shift")
	_ = k.Mask(1<<shift, 0, 0, 0, 0, 0)
	got, err := k.TickSecret(now.Add(100*time.Millisecond), scratch[:], func(key SecretKey, data []byte) {
		if !key.Repeat || key.Logical != raw.XKB_KEY_Q || !bytes.Equal(data, []byte{'Q'}) {
			t.Fatal("repeat cached press-time bytes")
		}
	})
	if err != nil || !got {
		t.Fatal(got, err)
	}
	if _, ok := k.Tick(now.Add(time.Second)); ok {
		t.Fatal("ordinary repeat exposed secret")
	}
	k.Focus(false)
	if got, err := k.TickSecret(now.Add(time.Second), scratch[:], func(SecretKey, []byte) { t.Fatal("repeat after focus loss") }); got || err != nil {
		t.Fatal(got, err)
	}
}

func TestSecretBoundsAndOrdinaryIsolation(t *testing.T) {
	k := requiredKeyboard(t, "")
	defer k.Close()
	if err := k.Rules("us", ""); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if _, err := k.Event(16, true, now); err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{0, 513} {
		buf := make([]byte, size)
		if err := k.EventSecret(30, true, now, buf, func(SecretKey, []byte) { t.Fatal("invalid bound callback") }); !errors.Is(err, xkb.ErrUTF8Buffer) {
			t.Fatal(err)
		}
	}
	short := []byte{0xff}
	if err := k.EventSecret(30, true, now, short, func(SecretKey, []byte) { t.Fatal("truncated callback") }); !errors.Is(err, io.ErrShortBuffer) {
		t.Fatal(err)
	}
	if short[0] != 0 || len(k.held) != 0 || len(k.secretHeld) != 0 {
		t.Fatal("error retained bytes/state")
	}
	var scratch [512]byte
	if err := k.EventSecret(30, true, now, scratch[:], func(SecretKey, []byte) {}); err != nil {
		t.Fatal(err)
	}
	if key, err := k.Event(30, false, now); err != nil || key.Text != "" || key.Name != "" {
		t.Fatal("ordinary release exposed secret")
	}
}

func TestSecretRepeatBoundedAllocations(t *testing.T) {
	k := requiredKeyboard(t, "")
	defer k.Close()
	if err := k.Rules("us", ""); err != nil {
		t.Fatal(err)
	}
	k.RepeatInfo(20, 0)
	var scratch [512]byte
	now := time.Now()
	consume := func(SecretKey, []byte) {}
	if err := k.EventSecret(16, true, now, scratch[:], consume); err != nil {
		t.Fatal(err)
	}
	allocs := testing.AllocsPerRun(100, func() {
		now = now.Add(time.Second)
		if _, err := k.TickSecret(now, scratch[:], consume); err != nil {
			t.Fatal(err)
		}
	})
	// Fixed native-dispatch/modifier-query overhead is allowed; no allocation
	// should scale with payload length or retain a text-bearing Key.
	t.Logf("secret repeat fixed allocations: %v", allocs)
	if allocs > 24 {
		t.Fatalf("secret repeat allocations=%v", allocs)
	}
}

func TestSecretControlsCancelPriorPrintableRepeat(t *testing.T) {
	for _, tc := range []struct {
		code uint32
		kind SecretKind
		ctrl bool
	}{{28, SecretReturn, false}, {1, SecretEscape, false}, {22, SecretClear, true}} {
		k := requiredKeyboard(t, "en_US.UTF-8")
		if err := k.Rules("us", ""); err != nil {
			t.Fatal(err)
		}
		k.RepeatInfo(20, 0)
		now := time.Now()
		var scratch [512]byte
		if err := k.EventSecret(16, true, now, scratch[:], func(SecretKey, []byte) {}); err != nil {
			t.Fatal(err)
		}
		if tc.ctrl {
			idx, err := k.keymap.ModIndex("Control")
			if err != nil {
				t.Fatal(err)
			}
			if err := k.Mask(1<<idx, 0, 0, 0, 0, 0); err != nil {
				t.Fatal(err)
			}
		}
		if err := k.EventSecret(tc.code, true, now, scratch[:], func(key SecretKey, data []byte) {
			if key.Kind != tc.kind || len(data) != 0 || k.repeating != 0 || !k.next.IsZero() {
				t.Fatal("control callback preceded repeat cancellation")
			}
		}); err != nil {
			t.Fatal(err)
		}
		if err := k.Mask(0, 0, 0, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
		if got, err := k.TickSecret(now.Add(time.Second), scratch[:], func(SecretKey, []byte) { t.Fatal("bytes after control") }); got || err != nil {
			t.Fatal(got, err)
		}
		k.Close()
	}
}

func TestSecretValidationErrorsClearAllInputState(t *testing.T) {
	for _, ordinary := range []bool{false, true} {
		for _, tick := range []bool{false, true} {
			for _, invalid := range []string{"empty", "oversize", "nil-callback"} {
				k := requiredKeyboard(t, "en_US.UTF-8")
				if err := k.Rules("de", ""); err != nil {
					t.Fatal(err)
				}
				k.RepeatInfo(20, 0)
				now := time.Now()
				var scratch [512]byte
				if ordinary {
					if _, err := k.Event(16, true, now); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := k.EventSecret(16, true, now, scratch[:], func(SecretKey, []byte) {}); err != nil {
						t.Fatal(err)
					}
				}
				// Real native composing state must also be reset on validation failure.
				if _, err := k.compose.Feed(raw.XKB_KEY_dead_circumflex); err != nil {
					t.Fatal(err)
				}
				k.composePending = true
				buf := scratch[:]
				consume := func(SecretKey, []byte) { t.Fatal("invalid call delivered") }
				want := xkb.ErrUTF8Buffer
				switch invalid {
				case "empty":
					buf = nil
				case "oversize":
					buf = make([]byte, 513)
				case "nil-callback":
					consume = nil
					want = errSecretCallback
				}
				var err error
				if tick {
					_, err = k.TickSecret(now.Add(time.Second), buf, consume)
				} else {
					err = k.EventSecret(16, false, now, buf, consume)
				} // release validation must not retain held state
				if !errors.Is(err, want) {
					t.Fatal(invalid, err)
				}
				status, err := k.compose.Status()
				if err != nil || status != raw.XKB_COMPOSE_NOTHING {
					t.Fatal("compose survived validation error")
				}
				if len(k.held) != 0 || len(k.secretHeld) != 0 || k.repeating != 0 || !k.next.IsZero() || k.composePending {
					t.Fatal("validation error retained ordinary/secret state")
				}
				if _, ok := k.Tick(now.Add(time.Second)); ok {
					t.Fatal("ordinary repeat after failed secret entry")
				}
				if got, err := k.TickSecret(now.Add(time.Second), scratch[:], func(SecretKey, []byte) { t.Fatal("secret repeat after failed validation") }); got || err != nil {
					t.Fatal(got, err)
				}
				k.Close()
			}
		}
	}
}

func TestSecretCallbackPanicWipesScratch(t *testing.T) {
	k := requiredKeyboard(t, "")
	defer k.Close()
	if err := k.Rules("us", ""); err != nil {
		t.Fatal(err)
	}
	var scratch [512]byte
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("callback did not panic")
			}
		}()
		_ = k.EventSecret(16, true, time.Now(), scratch[:], func(_ SecretKey, data []byte) {
			if !bytes.Equal(data, []byte{'q'}) {
				t.Fatal("no bytes before panic")
			}
			panic("consumer failure")
		})
	}()
	if !bytes.Equal(scratch[:], make([]byte, len(scratch))) {
		t.Fatal("panic retained scratch payload")
	}
}

func TestSecretRepeatTracksLayoutGroup(t *testing.T) {
	k := requiredKeyboard(t, "")
	defer k.Close()
	if err := k.Rules("us,fr", ""); err != nil {
		t.Fatal(err)
	}
	k.RepeatInfo(20, 0)
	now := time.Now()
	var scratch [512]byte
	if err := k.EventSecret(16, true, now, scratch[:], func(_ SecretKey, data []byte) {
		if !bytes.Equal(data, []byte{'q'}) {
			t.Fatal("initial group")
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := k.Mask(0, 0, 0, 0, 0, 1); err != nil {
		t.Fatal(err)
	}
	if ok, err := k.TickSecret(now.Add(time.Second), scratch[:], func(key SecretKey, data []byte) {
		if !key.Repeat || !bytes.Equal(data, []byte{'a'}) {
			t.Fatal("repeat cached previous group")
		}
	}); err != nil || !ok {
		t.Fatal(ok, err)
	}
}
