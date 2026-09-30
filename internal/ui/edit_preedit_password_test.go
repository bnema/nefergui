package ui

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/bnema/nefergui/internal/css"
	"github.com/bnema/nefergui/internal/edit"
	"github.com/bnema/nefergui/internal/text"
)

func TestPasswordPreeditNeverLeaksIntoDisplay(t *testing.T) {
	catalog, err := text.Load(text.DirectorySource("../../testdata/fonts"))
	if err != nil {
		t.Fatal(err)
	}
	r := newRuntime()
	r.setTextEngine(text.NewEngine(catalog))
	r.styles = css.Compile(css.UA(), css.Parse(`input {width:250px;height:28px;font-size:16px}`))
	value := "a"
	view := func(f *Frame) { f.Root().Input("secret", &value, Password(true)) }
	r.Build(view)
	r.route(key("Tab"))
	r.Build(view)
	secret := "e\u0301猫"
	r.route(platformInput{Kind: "ime-done", IME: edit.IMEBatch{Preedit: secret, Begin: len("e\u0301")}})
	r.Build(view)
	data, err := json.Marshal(r.output.Display)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(secret)) || bytes.Contains(data, []byte("猫")) || bytes.Contains(data, []byte("e\u0301")) {
		t.Fatalf("plaintext leaked: %s", data)
	}
	if value != "a" {
		t.Fatalf("preedit modified model: %q", value)
	}
	found := false
	for _, c := range r.output.Display {
		if c.Op == "preedit-text" {
			found = true
			if c.Text != "••" || len(c.Runs) == 0 {
				t.Fatalf("wrong grapheme masking: %+v", c)
			}
		}
	}
	if !found {
		t.Fatal("missing masked preedit")
	}
}
