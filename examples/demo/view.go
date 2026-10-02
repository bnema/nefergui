package main

import (
	"fmt"

	"github.com/bnema/nefergui"
)

type Model struct {
	Page, Name, Status string
	Notes              string
	NewPending         bool
	DarkMode           bool
	Density            string
	Volume             float64
}

func navButton(parent nefergui.Node, m *Model, label, page string) {
	opts := []nefergui.ButtonOption{nefergui.Key(page), nefergui.Class("menu-item")}
	if m.Page == page {
		opts = append(opts, nefergui.Class("active"))
	}
	if parent.Button(label, opts...).Activated() {
		m.Page = page
	}
}
func home(parent nefergui.Node, m *Model) {
	workspace := parent.Row(nefergui.Class("workspace"))
	editor := workspace.Column(nefergui.Class("editor-pane"))
	tab := editor.Row(nefergui.Class("pane-bar"))
	tab.Text("Untitled.txt", nefergui.Class("document-title"))
	tab.Spacer(nefergui.Class("stretch"))
	tab.Text("Plain text", nefergui.Class("hint"))
	if editor.Textarea("Document", &m.Notes, nefergui.Key("document"), nefergui.Class("document-editor"), nefergui.Placeholder("Start writing…")).Changed() {
		m.NewPending = false
		m.Status = "Unsaved changes · session only"
	}
	inspector := workspace.Aside(nefergui.Class("inspector"))
	inspector.Text("PROPERTIES", nefergui.Class("eyebrow"))
	inspector.Text("Author", nefergui.Class("field-label"))
	inspector.Input("Author", &m.Name, nefergui.Key("name"), nefergui.Placeholder("Your name"))
	if inspector.Button("Apply", nefergui.Key("continue"), nefergui.Disabled(m.Name == "")).Activated() {
		m.Status = "Author set to " + m.Name + " · session only"
	}
	inspector.Separator()
	inspector.Text("Interface density", nefergui.Class("field-label"))
	for _, d := range []struct{ label, value string }{{"Comfortable", "comfortable"}, {"Compact", "compact"}} {
		inspector.Radio(d.label, d.value, &m.Density, nefergui.Key(d.value))
	}
	inspector.Separator()
	inspector.Slider(fmt.Sprintf("Volume %.0f %%", m.Volume), &m.Volume, 0, 100, 5, nefergui.Key("volume"))
	inspector.Spacer(nefergui.Class("stretch"))
	inspector.Text("Changes stay in this session.", nefergui.Class("hint"))
}
func settings(parent nefergui.Node, m *Model) {
	bar := parent.Row(nefergui.Class("pane-bar"))
	bar.Text("Preferences", nefergui.Class("document-title"))
	pane := parent.Column(nefergui.Class("preferences-pane"))
	pane.Text("Appearance", nefergui.Class("field-label"))
	pane.Checkbox("Dark mode", &m.DarkMode, nefergui.Key("appearance"))
	pane.Text("Rendering", nefergui.Class("field-label"))
	pane.Text("Wayland · Vulkan · UTF-8", nefergui.Class("muted"))
	pane.Text("Document properties are available in the editor's right pane.", nefergui.Class("hint"))
}

// requestNew arms confirmation without changing the document. A second request
// discards it; editing or Cancel disarms the pending request.
func (m *Model) requestNew() {
	if m.Notes != "" && !m.NewPending {
		m.NewPending = true
		m.Status = "Click Discard & new to replace the current document"
		return
	}
	m.Notes = ""
	m.NewPending = false
	m.Page = "home"
	m.Status = "New document · session only"
}

func app(f *nefergui.Frame, m *Model) {
	theme := "light"
	if m.DarkMode {
		theme = "dark"
	}
	root := f.Root(nefergui.Class("app"), nefergui.Class(theme), nefergui.Class(m.Density))
	header := root.Header(nefergui.Class("header"))
	header.Heading("NeferGUI", nefergui.Level(1), nefergui.Class("logo"))
	newLabel := "New"
	if m.NewPending {
		newLabel = "Discard & new"
	}
	if header.Button(newLabel, nefergui.Key("new-document")).Activated() {
		m.requestNew()
	}
	if m.NewPending && header.Button("Cancel", nefergui.Key("cancel-new")).Activated() {
		m.NewPending = false
		m.Status = "Ready"
	}
	header.Text("Untitled.txt", nefergui.Class("muted"))
	header.Spacer(nefergui.Class("stretch"))
	label := "Dark mode"
	if m.DarkMode {
		label = "Light mode"
	}
	if header.Button(label, nefergui.Key("dark-mode"), nefergui.Class("theme-toggle")).Activated() {
		m.DarkMode = !m.DarkMode
	}
	body := root.Row(nefergui.Class("body"))
	sidebar := body.Aside(nefergui.Class("sidebar"))
	sidebar.Text("DOCUMENTS", nefergui.Class("eyebrow"))
	menu := sidebar.Nav(nefergui.Class("menu"))
	navButton(menu, m, "Untitled.txt", "home")
	navButton(menu, m, "Preferences", "settings")
	sidebar.Spacer(nefergui.Class("stretch"))
	sidebar.Text("Local workspace", nefergui.Class("sidebar-note"))
	sidebar.Text("1 document · in memory", nefergui.Class("hint"))
	content := body.Main(nefergui.Class("content"))
	switch m.Page {
	case "home":
		home(content, m)
	case "settings":
		settings(content, m)
	}
	footer := root.Footer(nefergui.Class("status-bar"))
	footer.Text(m.Status)
	footer.Spacer(nefergui.Class("stretch"))
	footer.Text("UTF-8   •   Plain text", nefergui.Class("hint"))
}
