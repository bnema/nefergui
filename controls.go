package nefergui

import (
	"fmt"
	"github.com/bnema/nefergui/internal/edit"
	"strings"
)

// Control event values are immutable snapshots; querying them never consumes events.
type ButtonEvent struct{ activated bool }

func (e ButtonEvent) Activated() bool { return e.activated }

type EditEvent struct{ changed, submitted bool }

func (e EditEvent) Changed() bool   { return e.changed }
func (e EditEvent) Submitted() bool { return e.submitted }

type ChangeEvent struct{ changed bool }

func (e ChangeEvent) Changed() bool { return e.changed }
func (n Node) Button(label string, options ...ButtonOption) ButtonEvent {
	child := n.buttonLike("button", "button", label, options)
	return ButtonEvent{activated: activated(child)}
}

func (n Node) buttonLike(typ, role, label string, options []ButtonOption) Node {
	opts := make([]ContainerOption, 0, len(options))
	for _, o := range options {
		if c, ok := o.(ContainerOption); ok {
			opts = append(opts, c)
		}
	}
	child := n.child(typ, role, opts)
	if !child.valid() {
		return child
	}
	child.element.text = label
	for _, o := range options {
		if _, common := o.(ContainerOption); !common {
			o.button(child.element)
		}
	}
	child.frame.compute(child.element) // disabled is set by control-only options
	return child
}

func activated(child Node) bool {
	if !child.valid() || child.element.disabled {
		return false
	}
	for _, input := range child.element.events {
		if input.Kind == "activate" {
			return true
		}
	}
	return false
}
func (n Node) Input(label string, value *string, options ...EditOption) EditEvent {
	return n.editControl("input", label, value, options)
}
func (n Node) Textarea(label string, value *string, options ...EditOption) EditEvent {
	return n.editControl("textarea", label, value, options)
}
func (n Node) editControl(typ, label string, value *string, options []EditOption) EditEvent {
	opts := make([]ContainerOption, 0, len(options))
	for _, o := range options {
		if c, ok := o.(ContainerOption); ok {
			opts = append(opts, c)
		}
	}
	child := n.child(typ, "textbox", opts)
	if !child.valid() {
		return EditEvent{}
	}
	child.element.text = label
	for _, o := range options {
		if _, common := o.(ContainerOption); !common {
			o.edit(child.element)
		}
	}
	var ev EditEvent
	if value == nil {
		return ev
	}
	id := idKey(child.element.identity)
	editor := child.frame.edits[id]
	if editor == nil {
		editor = &edit.State{}
		editor.Sync(*value)
		editor.Move(len(editor.Value), false)
		child.frame.edits[id] = editor
	}
	if editor.Value != *value {
		editor.Sync(*value)
	}
	if !child.element.disabled {
		for _, input := range child.element.events {
			switch input.Kind {
			case "edit-pointer", "edit-word":
				editor.PreferredXValid = false
				place := newEditorGeometry(editor, resultByID(child.frame.layout.Tree, id), child.frame.layout.Display, child.element.password).nearest(input.X, input.Y)
				if input.Kind == "edit-word" {
					start, end := editor.WordAt(place)
					editor.Select(start, end)
				} else {
					editor.Move(place, input.Shift)
				}
			case "text":
				editor.PreferredXValid = false
				ev.changed = editor.Insert(input.Text) || ev.changed
			case "edit-key":
				if (input.Text == "up" || input.Text == "down") && typ == "textarea" {
					moveVisualLine(editor, resultByID(child.frame.layout.Tree, id), child.frame.layout.Display, child.element.password, input.Text == "down", input.Shift)
				} else {
					editor.PreferredXValid = false
					if input.Ctrl && input.Text == "v" && child.frame.owner != nil {
						if async, ok := child.frame.clipboard.(edit.AsyncClipboard); ok {
							if err := child.frame.owner.startPaste(async, child.element.identity); err != nil && debugDiagnostics {
								child.frame.diagnostics = append(child.frame.diagnostics, fmt.Sprintf("paste: %v", err))
							}
							break
						}
					}
					ev.changed = editKey(editor, input, child.frame.clipboard, child.element.password) || ev.changed
				}
			case "paste-result":
				changed, err := editor.PasteAsync([]byte(input.Text), input.Err)
				if err != nil && debugDiagnostics {
					child.frame.diagnostics = append(child.frame.diagnostics, fmt.Sprintf("paste: %v", err))
				}
				ev.changed = changed || ev.changed
			case "ime-done":
				ev.changed = editor.Done(input.IME) || ev.changed
			case "submit":
				ev.submitted = true
			}
		}
	}
	*value = editor.Value
	if child.element.password {
		child.element.text = editor.Mask()
	} else {
		child.element.text = editor.Value
	}
	// The composition is projected into the display list at the cursor;
	// it never enters the bound model before IME commit.
	if ev.changed {
		editor.Surround(child.frame.ime, child.element.password)
	}
	return ev
}
func editKey(s *edit.State, ev inputEvent, clipboard edit.Clipboard, password bool) bool {
	name := strings.ToLower(ev.Text)
	if ev.Ctrl {
		switch name {
		case "a":
			s.Select(0, len(s.Value))
			return false
		case "c":
			_ = s.Copy(clipboard, password)
			return false
		case "x":
			changed, _ := s.Cut(clipboard, password)
			return changed
		case "v":
			changed, _ := s.Paste(clipboard)
			return changed
		case "left":
			s.Move(s.WordLeft(), ev.Shift)
			return false
		case "right":
			s.Move(s.WordRight(), ev.Shift)
			return false
		case "backspace":
			return s.Delete(true, true)
		case "delete":
			return s.Delete(false, true)
		}
	}
	switch name {
	case "left":
		s.Left(ev.Shift)
	case "right":
		s.Right(ev.Shift)
	case "home":
		s.Move(0, ev.Shift)
	case "end":
		s.Move(len(s.Value), ev.Shift)
	case "backspace":
		return s.Delete(true, false)
	case "delete":
		return s.Delete(false, false)
	}
	return false
}
func (n Node) Checkbox(label string, value *bool, options ...ButtonOption) ChangeEvent {
	child := n.buttonLike("checkbox", "checkbox", label, options)
	if child.valid() && value != nil {
		child.element.checked = *value
	}
	if activated(child) && value != nil {
		*value = !*value
		child.element.checked = *value
		return ChangeEvent{true}
	}
	return ChangeEvent{}
}
func (n Node) Text(text string, options ...ContainerOption) {
	child := n.child("text", "", options)
	if child.valid() {
		child.element.text = text
	}
}
func (n Node) Heading(text string, options ...HeadingOption) {
	opts := make([]ContainerOption, 0, len(options))
	for _, o := range options {
		if c, ok := o.(ContainerOption); ok {
			opts = append(opts, c)
		}
	}
	child := n.child("heading", "heading", opts)
	if !child.valid() {
		return
	}
	child.element.text = text
	child.element.level = 2
	for _, o := range options {
		if _, common := o.(ContainerOption); !common {
			o.heading(child.element)
		}
	}
}
