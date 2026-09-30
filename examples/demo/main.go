//go:build linux

package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"time"

	"github.com/bnema/nefergui"
)

//go:embed app.css
var stylesheet []byte

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
func main() {
	static := flag.Bool("static", false, "render a fixed number of frames and exit")
	frames := flag.Int("frames", 120, "static frame count")
	minDuration := flag.Duration("min-duration", 0, "static: keep presenting until at least this long after start (for scripted input)")
	captureFrame := flag.Int("capture-frame", 0, "commit number for harness capture request")
	flag.Parse()
	if *static && (*frames < 2 || *captureFrame < 0 || *captureFrame >= *frames) {
		fmt.Fprintln(os.Stderr, "invalid frame count or capture frame")
		os.Exit(2)
	}
	if *captureFrame == 0 {
		*captureFrame = *frames - 8
		if *captureFrame < 1 {
			*captureFrame = 1
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if *static {
		// Scripted runs must end even if the frame budget is never reached;
		// the interactive demo runs until closed or interrupted.
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
	}
	dir := os.Getenv("NEFERGUI_DEBUG_DIR")
	var path string
	if dir != "" {
		path = filepath.Join(dir, "app.css")
		if err := os.MkdirAll(dir, 0700); err != nil {
			fail(err)
		}
		if err := os.WriteFile(path, stylesheet, 0600); err != nil {
			fail(err)
		}
	} else {
		file, err := os.CreateTemp("", "nefergui-demo-*.css")
		if err != nil {
			fail(err)
		}
		path = file.Name()
		defer os.Remove(path)
		if _, err = file.Write(stylesheet); err != nil {
			file.Close()
			fail(err)
		}
		if err = file.Close(); err != nil {
			fail(err)
		}
	}
	model := Model{Page: "home", Status: "Ready", Density: "comfortable", Volume: 40, Notes: "Project notes\n\nA native text workspace built with NeferGUI.\n\nSelect text, edit a paragraph, or paste from your clipboard.\nUse the properties pane to try radio buttons and the slider.\n\nEverything stays in memory for this session."}
	start := time.Now()
	var requested time.Time
	commit := func(frame uint64) error {
		if dir != "" {
			data, err := json.Marshal(model)
			if err != nil {
				return err
			}
			if err = os.WriteFile(filepath.Join(dir, "demo-state.json"), data, 0600); err != nil {
				return err
			}
		}
		if *static && int(frame) == *captureFrame && dir != "" {
			p := filepath.Join(dir, "capture-request")
			tmp := p + ".tmp"
			if err := os.WriteFile(tmp, []byte(strconv.FormatInt(time.Now().UnixNano(), 10)+"\n"), 0600); err != nil {
				return err
			}
			if err := os.Rename(tmp, p); err != nil {
				return err
			}
			requested = time.Now()
		}
		if *static && int(frame) >= *frames && !requested.IsZero() {
			time.Sleep(time.Until(requested.Add(250 * time.Millisecond)))
		}
		// Frame count alone depends on render speed: keep presenting (and
		// handling input) until the minimum duration has also elapsed.
		if *static && int(frame) >= *frames && time.Since(start) >= *minDuration {
			return errDone
		}
		return nil
	}
	opts := []nefergui.WindowOption{nefergui.Title("NeferGUI — Untitled.txt"), nefergui.Size(960, 640), nefergui.Styles(path)}
	var err error
	if *static {
		// The commit callback ends the run with errDone; the frame limit is a backstop.
		err = nefergui.RunFrames(ctx, math.MaxInt32, &model, app, commit, opts...)
	} else {
		err = nefergui.Run(ctx, &model, app, opts...)
	}
	if err = unexpected(err); err != nil {
		fail(err)
	}
}

var errDone = errors.New("demo: static run complete")

// unexpected drops the normal stop reasons (errDone, cancellation) from a
// possibly joined error while keeping everything else, such as close errors.
func unexpected(err error) error {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var rest []error
		for _, e := range joined.Unwrap() {
			if e = unexpected(e); e != nil {
				rest = append(rest, e)
			}
		}
		return errors.Join(rest...)
	}
	if errors.Is(err, errDone) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
