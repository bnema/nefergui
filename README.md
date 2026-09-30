# NeferGUI

A native Go GUI library for Linux: Wayland windows, Vulkan rendering, and CSS styling. Build your view from the current model; controls return events during that call. No cgo required.

> [!WARNING]
> **Early development.** No release yet. Expect bugs and breaking changes. Platform accessibility is not connected.

## A small example

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/bnema/nefergui"
)

func main() {
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
```

Default styles work without a stylesheet; `nefergui.Styles("app.css")` loads your own CSS.

## Try it

Requires Go 1.27, `libvulkan.so.1`, `libxkbcommon.so.0`, usable fonts, and a Wayland compositor supporting linux-dmabuf and linux-drm-syncobj. Fractional scaling also requires viewporter. No X11 or software-rendering fallback.

```sh
go get github.com/bnema/nefergui
```

From a checkout, run the [demo](examples/demo):

```sh
CGO_ENABLED=0 go run ./examples/demo
```

## Documentation

- [Controls](docs/controls.md) and [layout](docs/layout.md)
- [CSS properties and limitations](docs/css.md)
- [Runtime requirements and behavior](docs/runtime.md) and [troubleshooting](docs/troubleshooting.md)
- [Visual test harness](docs/harness.md) and [performance](docs/performance.md)
- [Architecture decisions](docs/adr)

## License

[GNU GPL v3](LICENSE). Third-party code and test fonts retain their own licenses and attribution.
