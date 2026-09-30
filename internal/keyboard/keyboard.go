// Package keyboard interprets Wayland evdev key events on the UI loop.
package keyboard

import (
	"errors"
	"os"
	"time"

	xkb "github.com/bnema/purego-xkbcommon"
	"github.com/bnema/purego-xkbcommon/raw"
)

type Key struct {
	Physical        uint32
	Logical         uint32
	Name, Text      string
	Pressed, Repeat bool
	Shift, Ctrl     bool
	Group           uint32
}
type Keyboard struct {
	context        *xkb.Context
	keymap         *xkb.Keymap
	state          *xkb.State
	compose        *xkb.ComposeState
	held           map[uint32]Key
	secretHeld     map[uint32]SecretKey
	secretMode     bool
	rate           int
	delay          time.Duration
	next           time.Time
	repeating      uint32
	focus          bool
	composePending bool
}

func New(locale string) (*Keyboard, error) {
	c, err := xkb.NewContext()
	if err != nil {
		return nil, err
	}
	k := &Keyboard{context: c, held: make(map[uint32]Key), focus: true}
	if locale != "" {
		t, e := c.NewComposeTable(locale)
		if e != nil {
			c.Close()
			return nil, e
		}
		k.compose, e = t.NewState()
		t.Close()
		if e != nil {
			c.Close()
			return nil, e
		}
	}
	return k, nil
}
func (k *Keyboard) Close() {
	k.Focus(false)
	if k.compose != nil {
		k.compose.Close()
	}
	if k.state != nil {
		k.state.Close()
	}
	if k.keymap != nil {
		k.keymap.Close()
	}
	if k.context != nil {
		k.context.Close()
	}
}

// ReplaceFD consumes the received FD even on failure; the previous keymap remains valid on failure.
func (k *Keyboard) ReplaceFD(fd, size int) error {
	if fd < 0 {
		return errors.New("keyboard: invalid fd")
	}
	file := os.NewFile(uintptr(fd), "keymap")
	if file == nil {
		return errors.New("keyboard: invalid fd")
	}
	defer file.Close()
	km, err := k.context.NewKeymapFD(fd, size)
	if err != nil {
		return err
	}
	return k.replace(km)
}
func (k *Keyboard) Rules(layout, options string) error {
	km, err := k.context.NewKeymapRules("evdev", "pc105", layout, "", options)
	if err != nil {
		return err
	}
	return k.replace(km)
}
func (k *Keyboard) replace(km *xkb.Keymap) error {
	s, err := km.NewState()
	if err != nil {
		km.Close()
		return err
	}
	k.Focus(false)
	if k.state != nil {
		k.state.Close()
	}
	if k.keymap != nil {
		k.keymap.Close()
	}
	k.keymap = km
	k.state = s
	k.Focus(true)
	return nil
}
func (k *Keyboard) Mask(depressed, latched, locked, groupDepressed, groupLatched, groupLocked uint32) error {
	if k.state == nil {
		return errors.New("keyboard: no keymap")
	}
	_, err := k.state.UpdateMask(depressed, latched, locked, groupDepressed, groupLatched, groupLocked)
	return err
}
func (k *Keyboard) Focus(on bool) {
	k.focus = on
	k.held = make(map[uint32]Key)
	clear(k.secretHeld)
	k.secretMode = false
	k.repeating = 0
	k.next = time.Time{}
	if k.compose != nil {
		k.compose.Reset()
	}
	k.composePending = false
}
func (k *Keyboard) RepeatInfo(rate int, delay time.Duration) {
	k.rate = rate
	k.delay = delay
	if rate <= 0 {
		k.repeating = 0
	}
}
func (k *Keyboard) Event(evdev uint32, pressed bool, now time.Time) (Key, error) {
	if k.secretMode {
		k.Focus(k.focus)
	}
	if !pressed {
		key := k.held[evdev]
		delete(k.held, evdev)
		if k.repeating == evdev {
			k.repeating = 0
		}
		key.Pressed = false
		key.Text = ""
		return key, nil
	}
	if !k.focus || k.state == nil {
		return Key{}, nil
	}
	code := xkb.WaylandKeycode(evdev)
	sym, err := k.state.KeySym(code)
	if err != nil {
		return Key{}, err
	}
	text, err := k.state.UTF8(code)
	if err != nil {
		return Key{}, err
	}
	name, _ := xkb.KeysymName(sym)
	group, _ := k.state.Layout()
	key := Key{Physical: evdev, Logical: sym, Name: name, Text: text, Pressed: true, Group: group}
	if key.Shift, key.Ctrl, err = k.modifiers(); err != nil {
		return Key{}, err
	}
	if k.compose != nil {
		if _, err = k.compose.Feed(sym); err != nil {
			return Key{}, err
		}
		status, _ := k.compose.Status()
		switch status {
		case raw.XKB_COMPOSE_COMPOSING, raw.XKB_COMPOSE_CANCELLED:
			key.Text = ""
			k.composePending = status == raw.XKB_COMPOSE_COMPOSING
			if status == raw.XKB_COMPOSE_CANCELLED {
				k.compose.Reset()
			}
		case raw.XKB_COMPOSE_COMPOSED:
			key.Text, err = k.compose.UTF8()
			k.compose.Reset()
			k.composePending = false
			if err != nil {
				return Key{}, err
			}
		default:
			k.composePending = false
		}
	}
	k.held[evdev] = key
	if k.rate > 0 && repeatable(sym) {
		k.repeating = evdev
		k.next = now.Add(k.delay)
	}
	return key, nil
}

// Modifier, dead and compose keys never repeat. The compositor provides rate/delay;
// its keymap is authoritative for text and layout.
func repeatable(sym uint32) bool {
	switch sym {
	case raw.XKB_KEY_Shift_L, raw.XKB_KEY_Shift_R, raw.XKB_KEY_Control_L, raw.XKB_KEY_Control_R,
		raw.XKB_KEY_Alt_L, raw.XKB_KEY_Alt_R, raw.XKB_KEY_Super_L, raw.XKB_KEY_Super_R,
		raw.XKB_KEY_Caps_Lock, raw.XKB_KEY_Num_Lock, raw.XKB_KEY_Multi_key:
		return false
	}
	return sym < raw.XKB_KEY_dead_grave || sym > raw.XKB_KEY_dead_currency
}

// Tick returns at most one repeat for a due deadline, without accumulating missed ticks.
// It resolves the held physical key against the current modifier/group mask.
// Compose is fed only by physical presses: synthetic repeats neither advance a
// compose sequence nor replay the press-time composed text.
func (k *Keyboard) Tick(now time.Time) (Key, bool) {
	if k.secretMode || !k.focus || k.rate <= 0 || k.repeating == 0 || now.Before(k.next) {
		return Key{}, false
	}
	physical := k.repeating
	if _, ok := k.held[physical]; !ok {
		return Key{}, false
	}
	code := xkb.WaylandKeycode(physical)
	sym, err := k.state.KeySym(code)
	if err != nil || !repeatable(sym) {
		k.repeating = 0
		return Key{}, false
	}
	text, err := k.state.UTF8(code)
	if err != nil {
		k.repeating = 0
		return Key{}, false
	}
	group, err := k.state.Layout()
	if err != nil {
		k.repeating = 0
		return Key{}, false
	}
	name, _ := xkb.KeysymName(sym)
	if k.composePending {
		text = ""
	}
	key := Key{Physical: physical, Logical: sym, Name: name, Text: text, Pressed: true, Repeat: true, Group: group}
	// Repeats carry the current modifiers so e.g. held Shift+arrow keeps extending.
	if key.Shift, key.Ctrl, err = k.modifiers(); err != nil {
		k.repeating = 0
		return Key{}, false
	}
	k.next = now.Add(time.Second / time.Duration(k.rate))
	return key, true
}

// modifiers reports whether Shift and Control are active in the current state.
func (k *Keyboard) modifiers() (shift, ctrl bool, err error) {
	mods, err := k.state.Mods()
	if err != nil {
		return false, false, err
	}
	if idx, e := k.keymap.ModIndex("Shift"); e == nil && idx < 32 {
		shift = mods&(1<<idx) != 0
	}
	if idx, e := k.keymap.ModIndex("Control"); e == nil && idx < 32 {
		ctrl = mods&(1<<idx) != 0
	}
	return shift, ctrl, nil
}
func (k *Keyboard) NextRepeat() (time.Time, bool) { return k.next, k.repeating != 0 }
