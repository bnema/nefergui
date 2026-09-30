# NeferGUI

Native immediate-mode GUI library for Go, built for Wayland with Vulkan rendering and CSS styling.

## Status

NeferGUI is under active development and has no release yet.

## Usage

```go
type Model struct{ Name, Status string }

func view(f *nefergui.Frame, m *Model) {
	root := f.Root(nefergui.Class("app"))
	root.Input("Name", &m.Name, nefergui.Key("name"))
	if root.Button("Greet", nefergui.Key("greet")).Activated() {
		m.Status = "Hello " + m.Name
	}
	root.Text(m.Status)
}

func main() {
	var m Model
	if err := nefergui.Run(context.Background(), &m, view, nefergui.Title("Hello"), nefergui.Size(640, 480), nefergui.Styles("app.css")); err != nil {
		log.Fatal(err)
	}
}
```

Runtime requirements: a Wayland compositor with linux-dmabuf and linux-drm-syncobj, `libvulkan.so.1` and `libxkbcommon.so.0`. Builds with `CGO_ENABLED=0`. See [docs/runtime.md](docs/runtime.md) and `examples/demo`.

## Documentation

- [CSS properties and limitations](docs/css.md)
- [Layout](docs/layout.md) · [Controls](docs/controls.md) · [Runtime](docs/runtime.md)
- [Troubleshooting](docs/troubleshooting.md) · [Harness](docs/harness.md) · [Performance](docs/performance.md)

## Design records

- [0002 Wayland bindings and FD ownership](docs/adr/0002-wayland-bindings-and-fd-ownership.md)
- [0003 Vulkan presentation through DMA-BUF](docs/adr/0003-vulkan-dmabuf-presentation.md)
- [0004 Keyboard interpretation](docs/adr/0004-keyboard.md)
- [0005 Immediate-mode identity and events](docs/adr/0005-identity-and-events.md)
- [0006 CSS and text scope](docs/adr/0006-css-and-text-scope.md)

## License

NeferGUI is licensed under [GNU GPL v3](LICENSE). Third-party code and test fonts retain their licenses and attribution notices in their respective directories.
