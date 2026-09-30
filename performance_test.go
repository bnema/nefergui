package nefergui

import (
	"testing"

	"github.com/bnema/nefergui/internal/text"
)

// demoFrame mirrors the product example's home view without opening a display.
func demoFrame(f *Frame) {
	root := f.Root(Class("app"))
	header := root.Header(Class("header"))
	header.Heading("Demo", Level(1), Class("logo"))
	menu := header.Nav(Class("menu"))
	menu.Button("Accueil", Key("home"), Class("menu-item"))
	menu.Button("Paramètres", Key("settings"), Class("menu-item"))
	dark := false
	header.Checkbox("Thème sombre", &dark, Key("dark-mode"))
	content := root.Main(Class("content"))
	section := content.Section(Class("welcome"))
	section.Heading("Bienvenue", Level(2))
	section.Text("Une interface native écrite entièrement en Go.")
	name := "Ada"
	section.Input("Votre nom", &name, Key("name"), Placeholder("Ada"))
	section.Row(Class("actions")).Button("Continuer", Key("continue"), Class("primary"))
	root.Footer(Class("status-bar")).Text("Bonjour Ada")
}

func demoRuntime(tb testing.TB) *runtime {
	tb.Helper()
	catalog, err := text.Load(text.DirectorySource("testdata/fonts"))
	if err != nil {
		tb.Fatal(err)
	}
	r := newRuntime()
	r.setTextEngine(text.NewEngine(catalog))
	return r
}

func BenchmarkDemoFrame(b *testing.B) {
	r := demoRuntime(b)
	if !r.Build(demoFrame) {
		b.Fatal("initial build failed")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.Redraw()
		if !r.Build(demoFrame) {
			b.Fatal("build failed")
		}
		if len(r.output.Display) == 0 {
			b.Fatal("empty display list")
		}
	}
}

func TestDemoFrameAllocationBudget(t *testing.T) {
	r := demoRuntime(t)
	if !r.Build(demoFrame) {
		t.Fatal("initial build failed")
	}
	allocs := testing.AllocsPerRun(10, func() {
		r.Redraw()
		if !r.Build(demoFrame) || len(r.output.Display) == 0 {
			panic("demo frame failed")
		}
	})
	const budget = 500
	if allocs > budget {
		t.Fatalf("demo frame allocations: %.0f > %d", allocs, budget)
	}
}
