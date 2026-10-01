package ui

import (
	"reflect"
	"testing"

	"github.com/stretchr/testify/mock"
)

// newOrderedIME returns an IME mock that records Enable and Disable in order
// and fails on an enable before a disable or a double disable.
func newOrderedIME(t *testing.T) (*MockIME, *[]string) {
	t.Helper()
	ime := NewMockIME(t)
	calls := new([]string)
	active := false
	ime.EXPECT().Enable().Run(func() {
		if active {
			t.Error("IME enable before disable")
		}
		active = true
		*calls = append(*calls, "enable")
	}).Maybe()
	ime.EXPECT().Disable().Run(func() {
		if !active {
			t.Error("IME double disable")
		}
		active = false
		*calls = append(*calls, "disable")
	}).Maybe()
	ime.EXPECT().Surrounding(mock.Anything, mock.Anything, mock.Anything).Maybe()
	ime.EXPECT().CursorRect(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()
	ime.EXPECT().ContentType(mock.Anything, mock.Anything).Maybe()
	return ime, calls
}

func TestPasswordEditorNeverSendsSecretToIME(t *testing.T) {
	r := newRuntime()
	ime := NewMockIME(t)
	var surrounding []string
	password := false
	ime.EXPECT().Enable().Maybe()
	ime.EXPECT().Disable().Maybe()
	ime.EXPECT().CursorRect(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()
	ime.EXPECT().Surrounding(mock.Anything, mock.Anything, mock.Anything).Run(func(text string, _, _ int) {
		surrounding = append(surrounding, text)
	}).Maybe()
	ime.EXPECT().ContentType(mock.Anything, mock.Anything).Run(func(p, _ bool) { password = p }).Maybe()
	r.ime = ime
	value := "hunter2"
	view := func(f *Frame) { f.Root().Input("secret", &value, Key("secret"), Password(true)) }
	r.Build(view)
	r.route(key("Tab"))
	r.Build(view)
	if !password {
		t.Fatal("password content type not sent")
	}
	if len(surrounding) == 0 {
		t.Fatal("no surrounding text update")
	}
	for _, s := range surrounding {
		if s != "" {
			t.Fatalf("surrounding text leaked %q", s)
		}
	}
}

func TestIMEFocusTransferDisablesBeforeEnable(t *testing.T) {
	r := newRuntime()
	ime, calls := newOrderedIME(t)
	r.ime = ime
	a, b := "one", "two"
	view := func(f *Frame) { root := f.Root(); root.Input("a", &a, Key("a")); root.Textarea("b", &b, Key("b")) }
	r.Build(view)
	r.route(key("Tab"))
	r.Build(view)
	r.route(key("Tab"))
	r.Build(view)
	r.route(key("Tab"))
	r.Build(view)
	if want := []string{"enable", "disable", "enable", "disable", "enable"}; !reflect.DeepEqual(*calls, want) {
		t.Fatalf("focus transition calls %v want %v", *calls, want)
	}
	if !r.edits[idKey(r.Target(0))].IMEActive() || r.edits[idKey(r.Target(1))].IMEActive() {
		t.Fatal("wrong active editor")
	}
}
