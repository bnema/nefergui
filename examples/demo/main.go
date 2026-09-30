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
	section := parent.Section(nefergui.Class("welcome"))
	section.Heading("Bienvenue", nefergui.Level(2))
	section.Text("Une interface native écrite entièrement en Go.")
	section.Input("Votre nom", &m.Name, nefergui.Key("name"), nefergui.Placeholder("Ada"))
	actions := section.Row(nefergui.Class("actions"))
	if actions.Button("Continuer", nefergui.Key("continue"), nefergui.Class("primary"), nefergui.Disabled(m.Name == "")).Activated() {
		m.Status = "Bonjour " + m.Name
	}
}
func settings(parent nefergui.Node, m *Model) {
	section := parent.Section(nefergui.Class("welcome"))
	section.Heading("Paramètres", nefergui.Level(2))
	for _, d := range []struct{ label, value string }{{"Confortable", "comfortable"}, {"Compacte", "compact"}} {
		section.Radio(d.label, d.value, &m.Density, nefergui.Key(d.value))
	}
	section.Slider(fmt.Sprintf("Volume %.0f %%", m.Volume), &m.Volume, 0, 100, 5, nefergui.Key("volume"))
}
func app(f *nefergui.Frame, m *Model) {
	root := f.Root(nefergui.Class("app"))
	header := root.Header(nefergui.Class("header"))
	header.Heading("Demo", nefergui.Level(1), nefergui.Class("logo"))
	menu := header.Nav(nefergui.Class("menu"))
	navButton(menu, m, "Accueil", "home")
	navButton(menu, m, "Paramètres", "settings")
	header.Checkbox("Thème sombre", &m.DarkMode, nefergui.Key("dark-mode"))
	content := root.Main(nefergui.Class("content"))
	switch m.Page {
	case "home":
		home(content, m)
	case "settings":
		settings(content, m)
	}
	root.Footer(nefergui.Class("status-bar")).Text(m.Status)
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
	model := Model{Page: "home", Status: "Prêt", Density: "comfortable", Volume: 40}
	start := time.Now()
	var requested time.Time
	commit := func(frame uint64) error {
		if dir != "" {
			data, err := json.Marshal(struct {
				Page, Name, Status, Density string
				DarkMode                    bool
				Volume                      float64
			}{model.Page, model.Name, model.Status, model.Density, model.DarkMode, model.Volume})
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
	opts := []nefergui.WindowOption{nefergui.Title("Demo"), nefergui.Size(960, 640), nefergui.Styles(path)}
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
