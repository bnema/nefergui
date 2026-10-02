package main

import "testing"

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
