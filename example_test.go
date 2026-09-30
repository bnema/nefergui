package nefergui_test

import (
	"context"
	"fmt"
	"log"

	"github.com/bnema/nefergui"
)

// The README counter. Run needs a Wayland session, so this example is
// compiled but not executed by go test.
func Example_counter() {
	count := 0
	view := func(f *nefergui.Frame, count *int) {
		root := f.Root().Column()
		root.Text("Hello, Wayland!")
		if root.Button("Count", nefergui.Key("count")).Activated() {
			*count++
		}
		root.Text(fmt.Sprintf("Clicked %d times", *count))
	}
	if err := nefergui.Run(context.Background(), &count, view,
		nefergui.Title("Hello"), nefergui.Size(320, 200)); err != nil {
		log.Fatal(err)
	}
}

// The product-design example. Run needs a Wayland session, so this example is
// compiled but not executed by go test; examples/demo runs it.
func Example_compileOnly() {
	type Model struct {
		Page, Name, Status string
		DarkMode           bool
	}
	model := Model{Page: "home", Status: "Prêt"}
	navButton := func(parent nefergui.Node, m *Model, label, page string) {
		opts := []nefergui.ButtonOption{nefergui.Key(page), nefergui.Class("menu-item")}
		if m.Page == page {
			opts = append(opts, nefergui.Class("active"))
		}
		if parent.Button(label, opts...).Activated() {
			m.Page = page
		}
	}
	home := func(parent nefergui.Node, m *Model) {
		section := parent.Section(nefergui.Class("welcome"))
		section.Heading("Bienvenue", nefergui.Level(2))
		section.Text("Une interface native écrite entièrement en Go.")
		section.Input("Votre nom", &m.Name, nefergui.Key("name"), nefergui.Placeholder("Ada"))
		actions := section.Row(nefergui.Class("actions"))
		if actions.Button("Continuer", nefergui.Key("continue"), nefergui.Class("primary"), nefergui.Disabled(m.Name == "")).Activated() {
			m.Status = "Bonjour " + m.Name
		}
	}
	app := func(f *nefergui.Frame, m *Model) {
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
			home(content, m)
		}
		root.Footer(nefergui.Class("status-bar")).Text(m.Status)
	}
	_ = nefergui.Run(context.Background(), &model, app, nefergui.Title("Demo"), nefergui.Size(960, 640), nefergui.Styles("app.css"))
}
