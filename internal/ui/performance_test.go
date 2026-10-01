package ui

import (
	"os"
	"testing"

	"github.com/bnema/nefergui/internal/css"

	"github.com/bnema/nefergui/internal/text"
)

// demoFrame mirrors the product example's home view without opening a display.
func demoFrame(f *Frame) {
	root := f.Root(Class("app"), Class("light"), Class("comfortable"))
	header := root.Header(Class("header"))
	header.Heading("NeferGUI", Level(1), Class("logo"))
	header.Button("New", Key("new-document"))
	header.Text("Untitled.txt", Class("muted"))
	header.Spacer(Class("stretch"))
	header.Button("Dark mode", Key("dark-mode"), Class("theme-toggle"))
	body := root.Row(Class("body"))
	sidebar := body.Aside(Class("sidebar"))
	sidebar.Text("DOCUMENTS", Class("eyebrow"))
	menu := sidebar.Nav(Class("menu"))
	menu.Button("Untitled.txt", Key("home"), Class("menu-item"), Class("active"))
	menu.Button("Preferences", Key("settings"), Class("menu-item"))
	sidebar.Spacer(Class("stretch"))
	sidebar.Text("Local workspace", Class("sidebar-note"))
	sidebar.Text("1 document · in memory", Class("hint"))
	content := body.Main(Class("content"))
	workspace := content.Row(Class("workspace"))
	editor := workspace.Column(Class("editor-pane"))
	tab := editor.Row(Class("pane-bar"))
	tab.Text("Untitled.txt", Class("document-title"))
	tab.Spacer(Class("stretch"))
	tab.Text("Plain text", Class("hint"))
	notes := "Project notes\n\nA native text workspace built with NeferGUI.\n\nSelect text, edit a paragraph, or paste from your clipboard.\nUse the properties pane to try radio buttons and the slider.\n\nEverything stays in memory for this session."
	editor.Textarea("Document", &notes, Key("document"), Class("document-editor"), Placeholder("Start writing…"))
	inspector := workspace.Aside(Class("inspector"))
	inspector.Text("PROPERTIES", Class("eyebrow"))
	inspector.Text("Author", Class("field-label"))
	name := ""
	inspector.Input("Author", &name, Key("name"), Placeholder("Your name"))
	inspector.Button("Apply", Key("continue"), Disabled(true))
	inspector.Separator()
	inspector.Text("Interface density", Class("field-label"))
	density := "comfortable"
	inspector.Radio("Comfortable", "comfortable", &density, Key("comfortable"))
	inspector.Radio("Compact", "compact", &density, Key("compact"))
	inspector.Separator()
	volume := 40.0
	inspector.Slider("Volume 40 %", &volume, 0, 100, 5, Key("volume"))
	inspector.Spacer(Class("stretch"))
	inspector.Text("Changes stay in this session.", Class("hint"))
	footer := root.Footer(Class("status-bar"))
	footer.Text("Ready")
	footer.Spacer(Class("stretch"))
	footer.Text("UTF-8   •   Plain text", Class("hint"))
}

func demoRuntime(tb testing.TB) *runtime {
	tb.Helper()
	catalog, err := text.Load(text.DirectorySource("../../testdata/fonts"))
	if err != nil {
		tb.Fatal(err)
	}
	r := newRuntime()
	stylesheet, err := os.ReadFile("testdata/demo.css")
	if err != nil {
		tb.Fatal(err)
	}
	r.styles = css.Compile(css.UA(), css.Parse(string(stylesheet)))
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

func TestAllocDemoFrame(t *testing.T) {
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
	const budget = 600
	if allocs > budget {
		t.Fatalf("demo frame allocations: %.0f > %d", allocs, budget)
	}
}
