package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestNewDocumentRequiresConfirmation(t *testing.T) {
	m := Model{Notes: "Keep this text", Page: "home"}
	m.requestNew()
	if m.Notes != "Keep this text" || !m.NewPending {
		t.Fatalf("first request discarded text: %+v", m)
	}
	m.NewPending = false // Cancel
	m.requestNew()
	if m.Notes != "Keep this text" || !m.NewPending {
		t.Fatalf("cancel failed to reset confirmation: %+v", m)
	}
	m.requestNew()
	if m.Notes != "" || m.NewPending {
		t.Fatalf("confirmation did not clear document: %+v", m)
	}
}

func TestUnexpectedKeepsCloseErrors(t *testing.T) {
	closeErr := errors.New("close failed")
	cases := []struct {
		name string
		in   error
		want error
	}{
		{"nil", nil, nil},
		{"done", errDone, nil},
		{"canceled", fmt.Errorf("run: %w", context.Canceled), nil},
		{"done and close", errors.Join(errDone, closeErr), closeErr},
		{"close only", errors.Join(nil, closeErr), closeErr},
	}
	for _, c := range cases {
		got := unexpected(c.in)
		if c.want == nil && got != nil || c.want != nil && !errors.Is(got, c.want) {
			t.Errorf("%s: unexpected(%v) = %v, want %v", c.name, c.in, got, c.want)
		}
	}
	if got := unexpected(errors.Join(errDone, closeErr)); errors.Is(got, errDone) {
		t.Errorf("errDone kept in %v", got)
	}
}
