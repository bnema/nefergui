package ui

import (
	"testing"

	"github.com/bnema/nefergui/internal/edit"
)

func typed(name, text string) platformInput {
	return platformInput{Kind: "key", Key: keyEvent{Name: name, Text: text, Pressed: true}}
}

// With text-input v3 the compositor routes keys to a grabbing input method;
// keys that still arrive on wl_keyboard were not consumed and must be typed.
func TestKeyboardTextInsertedWhileIMEEnabled(t *testing.T) {
	r := newRuntime()
	ime := &orderedIME{}
	r.ime = ime
	value := "ab"
	view := func(f *Frame) { f.Root().Textarea("message", &value) }
	r.Build(view)
	r.route(key("Tab"))
	r.Build(view)
	if !r.edits[idKey(r.Target(0))].IMEActive() {
		t.Fatal("IME not enabled on focus")
	}
	for _, k := range []platformInput{typed("c", "c"), typed("a", "a"), key("Return"), key("KP_Enter")} {
		r.route(k)
		r.Build(view)
	}
	if value != "abca\n\n" || len(ime.calls) == 0 {
		t.Fatalf("keyboard text lost while IME enabled: %q", value)
	}

	// A shown preedit belongs to the input method until it commits: plain
	// keys and Return must not interleave with it.
	r.route(platformInput{Kind: "ime-done", IME: edit.IMEBatch{Preedit: "ni", Begin: 2, End: 2}})
	r.Build(view)
	for _, k := range []platformInput{typed("x", "x"), typed("a", "a"), key("Return")} {
		r.route(k)
		r.Build(view)
	}
	if value != "abca\n\n" {
		t.Fatalf("plain keys inserted during preedit: %q", value)
	}
	r.route(platformInput{Kind: "ime-done", IME: edit.IMEBatch{Commit: "你"}})
	r.Build(view)
	r.route(typed("z", "z"))
	r.Build(view)
	if value != "abca\n\n你z" {
		t.Fatalf("typing after commit: %q", value)
	}
}
