# NeferGUI

A pure-Go GUI renderer for Linux: build your view from the current model, style it with CSS, and draw it with Vulkan into DMA-BUF buffers. Controls return events during the view call. No cgo required.

NeferGUI imports no Wayland library. Your application owns the Wayland client (connection, surface, linux-dmabuf feedback, buffer import, seat events) and drives a `Renderer`; [github.com/bnema/neferclient](https://github.com/bnema/neferclient) is a client toolkit built for this. The two do not import each other: the application copies plain fields between them.

> [!WARNING]
> **Early development.** No release yet. Expect bugs and breaking changes. Platform accessibility is not connected.

## A small example

The view is plain Go. The model type is yours.

```go
type Model struct{ Count int }

view := func(f *nefergui.Frame, m *Model) {
	root := f.Root().Column()
	root.Text("Hello, Wayland!")
	if root.Button("Count", nefergui.Key("count")).Activated() {
		m.Count++
	}
	root.Text(fmt.Sprintf("Clicked %d times", m.Count))
}
```

The client loop, on one owner goroutine:

```go
r, err := nefergui.NewRenderer(nefergui.RendererConfig{MainDevice: dev, Formats: formats})
r.Resize(320, 200, 1)          // on configure and scale changes
r.Input(&nefergui.Input{...})  // for each pointer, key and focus event

var out nefergui.Output
if ok, err := r.Render(&out, &model, view); ok && err == nil {
	// import out.Planes as a wl_buffer when out.NewBuffer, attach with the
	// acquire/release points, commit
}
r.Released(buffer) // when out.ReleaseFD becomes readable
```

`RendererConfig.Styles` loads your own CSS; default styles work without it. See `ExampleRenderer` and [runtime](docs/runtime.md) for the full contract.

## Requirements

Go 1.27, `libvulkan.so.1` and usable fonts. The GPU must export DMA-BUF and support DRM syncobj timelines (kernel 6.6 or later for eventfd waits). The compositor, reached through your Wayland client, must offer linux-dmabuf and linux-drm-syncobj; fractional scaling also needs viewporter. No X11 or software-rendering fallback.

```sh
go get github.com/bnema/nefergui
```

## Documentation

- [Controls](docs/controls.md) and [layout](docs/layout.md)
- [CSS properties and limitations](docs/css.md)
- [Runtime requirements and behavior](docs/runtime.md) and [troubleshooting](docs/troubleshooting.md)
- [Performance](docs/performance.md)
- [Architecture decisions](docs/adr)

## License

[GNU GPL v3](LICENSE). Third-party code and test fonts retain their own licenses and attribution.
