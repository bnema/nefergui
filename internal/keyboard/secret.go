package keyboard

import (
	"errors"
	"time"
	"unicode/utf8"

	xkb "github.com/bnema/purego-xkbcommon"
	"github.com/bnema/purego-xkbcommon/raw"
)

// SecretKind is the limited editing vocabulary accepted by secret consumers.
type SecretKind uint8

const (
	SecretNone SecretKind = iota
	SecretBytes
	SecretBackspace
	SecretReturn
	SecretEscape
	SecretClear // Control+U
)

// SecretKey contains scalar metadata only, never names or text. Bytes are
// supplied separately and exclusively within EventSecret/TickSecret callbacks.
type SecretKey struct {
	Physical, Logical uint32
	Kind              SecretKind
	Pressed, Repeat   bool
}

var errSecretCallback = errors.New("keyboard: secret callback is nil")

// ClearSecret cancels secret-held keys, repeat and local compose. It retains
// the compositor's current keymap and modifier mask. No text is cached here.
func (k *Keyboard) ClearSecret() {
	clear(k.secretHeld)
	clear(k.held) // fail-closed even when secret entry fails from ordinary mode
	k.repeating = 0
	k.next = time.Time{}
	k.composePending = false
	if k.compose != nil {
		_ = k.compose.Reset()
	}
}

// EventSecret interprets a physical event without calling any string-producing
// XKB API or populating Key/held. scratch length must be 1..512, including space
// for native NUL (max payload 511). The callback is synchronous and must not
// retain bytes or convert them to strings. scratch is valid only for that call;
// this method clears it on entry and every exit, in addition to caller wiping.
// Enter, Escape and Control+U are initial-press controls, never repeats.
// Release callbacks carry no bytes and Pressed=false; consumers must act on
// controls only when Pressed=true. Switching APIs cancels the other path.
func (k *Keyboard) EventSecret(evdev uint32, pressed bool, now time.Time, scratch []byte, consume func(SecretKey, []byte)) error {
	clear(scratch)
	defer clear(scratch)
	if len(scratch) < 1 || len(scratch) > xkb.MaxUTF8Buffer {
		k.ClearSecret()
		return xkb.ErrUTF8Buffer
	}
	if consume == nil {
		k.ClearSecret()
		return errSecretCallback
	}
	if !k.secretMode {
		k.Focus(k.focus) // discard ordinary held text and compose on mode entry
		k.secretMode = true
		if k.secretHeld == nil {
			k.secretHeld = make(map[uint32]SecretKey)
		}
	}
	if !pressed {
		key, ok := k.secretHeld[evdev]
		delete(k.secretHeld, evdev)
		if k.repeating == evdev {
			k.repeating = 0
		}
		if ok && key.Kind != SecretNone {
			key.Pressed = false
			consume(key, nil)
		}
		return nil
	}
	if !k.focus || k.state == nil {
		return nil
	}
	_, repeat := k.secretHeld[evdev]
	key, n, err := k.secretKey(evdev, repeat, scratch)
	if err != nil {
		k.ClearSecret()
		return err
	}
	k.secretHeld[evdev] = key
	if key.Kind == SecretReturn || key.Kind == SecretEscape || key.Kind == SecretClear {
		// Clear/submit/cancel must stop a previously held printable repeat
		// before the consumer runs; releasing Control must not resurrect it.
		k.repeating = 0
		k.next = time.Time{}
	}
	if k.rate > 0 && secretRepeatable(key) {
		k.repeating = evdev
		k.next = now.Add(k.delay)
	}
	if key.Kind != SecretNone {
		consume(key, scratch[:n:n])
	}
	return nil
}

func secretRepeatable(key SecretKey) bool {
	return key.Kind == SecretBackspace || key.Kind == SecretBytes && repeatable(key.Logical)
}

func (k *Keyboard) secretKey(evdev uint32, repeat bool, scratch []byte) (SecretKey, int, error) {
	code := xkb.WaylandKeycode(evdev)
	sym, err := k.state.KeySym(code)
	if err != nil {
		return SecretKey{}, 0, err
	}
	key := SecretKey{Physical: evdev, Logical: sym, Pressed: true, Repeat: repeat}
	_, ctrl, err := k.modifiers()
	if err != nil {
		return SecretKey{}, 0, err
	}
	switch {
	case sym == raw.XKB_KEY_BackSpace:
		key.Kind = SecretBackspace
	case sym == raw.XKB_KEY_Return || sym == raw.XKB_KEY_KP_Enter:
		key.Kind = SecretReturn
	case sym == raw.XKB_KEY_Escape:
		key.Kind = SecretEscape
	case ctrl && (sym == raw.XKB_KEY_u || sym == raw.XKB_KEY_U):
		key.Kind = SecretClear
	}
	if key.Kind != SecretNone {
		if k.compose != nil {
			_ = k.compose.Reset()
		}
		k.composePending = false
		if repeat && key.Kind != SecretBackspace {
			key.Kind = SecretNone
		}
		return key, 0, nil
	}
	// Other Control combinations are not secret bytes or application shortcuts.
	if ctrl {
		return key, 0, nil
	}
	if k.compose != nil && !repeat {
		if _, err := k.compose.Feed(sym); err != nil {
			return key, 0, err
		}
		status, err := k.compose.Status()
		if err != nil {
			return key, 0, err
		}
		switch status {
		case raw.XKB_COMPOSE_COMPOSING:
			k.composePending = true
			return key, 0, nil
		case raw.XKB_COMPOSE_CANCELLED:
			_ = k.compose.Reset()
			k.composePending = false
			return key, 0, nil
		case raw.XKB_COMPOSE_COMPOSED:
			n, err := k.compose.UTF8Into(scratch)
			_ = k.compose.Reset()
			k.composePending = false
			if err != nil {
				return key, 0, err
			}
			return classifySecretBytes(key, scratch, n), n, nil
		default:
			k.composePending = false
		}
	}
	if k.composePending {
		return key, 0, nil
	}
	n, err := k.state.UTF8Into(code, scratch)
	if err != nil {
		return key, 0, err
	}
	return classifySecretBytes(key, scratch, n), n, nil
}

func classifySecretBytes(key SecretKey, scratch []byte, n int) SecretKey {
	if n == 0 || !utf8.Valid(scratch[:n]) {
		return key
	}
	for rest := scratch[:n]; len(rest) > 0; {
		r, size := utf8.DecodeRune(rest)
		if r < 0x20 || r == 0x7f {
			return key
		}
		rest = rest[size:]
	}
	key.Kind = SecretBytes
	return key
}

// TickSecret resolves repeat bytes using the current mask/group, not press-time
// plaintext. Compose is never fed by repeats. It reports whether a callback ran.
func (k *Keyboard) TickSecret(now time.Time, scratch []byte, consume func(SecretKey, []byte)) (bool, error) {
	clear(scratch)
	defer clear(scratch)
	if len(scratch) < 1 || len(scratch) > xkb.MaxUTF8Buffer {
		k.ClearSecret()
		return false, xkb.ErrUTF8Buffer
	}
	if consume == nil {
		k.ClearSecret()
		return false, errSecretCallback
	}
	if !k.secretMode || !k.focus || k.rate <= 0 || k.repeating == 0 || now.Before(k.next) {
		return false, nil
	}
	if _, ok := k.secretHeld[k.repeating]; !ok {
		return false, nil
	}
	key, n, err := k.secretKey(k.repeating, true, scratch)
	if err != nil {
		k.ClearSecret()
		return false, err
	}
	if !secretRepeatable(key) {
		k.repeating = 0
		return false, nil
	}
	k.next = now.Add(time.Second / time.Duration(k.rate))
	consume(key, scratch[:n:n])
	return true, nil
}
