package nefergui

import (
	"reflect"
	"testing"
)

type orderedIME struct {
	calls  []string
	active bool
}

func (i *orderedIME) Enable() {
	if i.active {
		panic("IME enable before disable")
	}
	i.active = true
	i.calls = append(i.calls, "enable")
}
func (i *orderedIME) Disable() {
	if !i.active {
		panic("IME double disable")
	}
	i.active = false
	i.calls = append(i.calls, "disable")
}
func (i *orderedIME) Surrounding(string, int, int)                  {}
func (i *orderedIME) CursorRect(float64, float64, float64, float64) {}
func (i *orderedIME) ContentType(bool, bool)                        {}

type recordingIME struct {
	surrounding []string
	password    bool
}

func (i *recordingIME) Enable()  {}
func (i *recordingIME) Disable() {}
func (i *recordingIME) Surrounding(text string, _, _ int) {
	i.surrounding = append(i.surrounding, text)
}
func (i *recordingIME) CursorRect(float64, float64, float64, float64) {}
func (i *recordingIME) ContentType(password, _ bool)                  { i.password = password }

func TestPasswordEditorNeverSendsSecretToIME(t *testing.T) {
	r := newRuntime()
	ime := &recordingIME{}
	r.ime = ime
	value := "hunter2"
	view := func(f *Frame) { f.Root().Input("secret", &value, Key("secret"), Password(true)) }
	r.Build(view)
	r.route(key("Tab"))
	r.Build(view)
	if !ime.password {
		t.Fatal("password content type not sent")
	}
	if len(ime.surrounding) == 0 {
		t.Fatal("no surrounding text update")
	}
	for _, s := range ime.surrounding {
		if s != "" {
			t.Fatalf("surrounding text leaked %q", s)
		}
	}
}

func TestIMEFocusTransferDisablesBeforeEnable(t *testing.T) {
	r := newRuntime()
	ime := &orderedIME{}
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
	if want := []string{"enable", "disable", "enable", "disable", "enable"}; !reflect.DeepEqual(ime.calls, want) {
		t.Fatalf("focus transition calls %v want %v", ime.calls, want)
	}
	if !r.edits[idKey(r.Target(0))].IMEActive() || r.edits[idKey(r.Target(1))].IMEActive() {
		t.Fatal("wrong active editor")
	}
}
