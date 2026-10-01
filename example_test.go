package nefergui_test

import (
	"fmt"
	"log"

	"github.com/bnema/nefergui"
)

// ExampleRenderer shows the shape of a client loop. The Wayland side (display
// connection, linux-dmabuf feedback, buffer import, event dispatch) belongs to
// the application, for instance through github.com/bnema/neferclient; it is
// elided here. The example needs a GPU and a compositor, so it is compiled but
// not run by go test.
func ExampleRenderer() {
	type model struct{ count int }
	view := func(f *nefergui.Frame, m *model) {
		root := f.Root().Column()
		root.Text("Hello, Wayland!")
		if root.Button("Count", nefergui.Key("count")).Activated() {
			m.count++
		}
		root.Text(fmt.Sprintf("Clicked %d times", m.count))
	}

	// mainDevice and formats come from the compositor's linux-dmabuf feedback.
	var (
		mainDevice uint64
		formats    []nefergui.Format
	)
	r, err := nefergui.NewRenderer(nefergui.RendererConfig{MainDevice: mainDevice, Formats: formats})
	if err != nil {
		log.Fatal(err)
	}
	defer r.Close()

	// Forward the surface size, then wire events as they arrive.
	r.Resize(320, 200, 1)
	r.Input(&nefergui.Input{Kind: nefergui.InputPointerMotion, X: 10, Y: 10})

	m := &model{}
	var out nefergui.Output
	for {
		drawn, err := r.Render(&out, m, view)
		if err != nil {
			log.Fatal(err)
		}
		if drawn {
			// Import out.Planes as a wl_buffer when out.NewBuffer is set, attach
			// it with the Acquire/Release points, commit, and call r.Released
			// when out.ReleaseFD becomes readable.
			_ = out.Buffer
		}
		// Sleep until a Wayland event, <-r.Wake(), or a short timer when
		// r.Pending() reports a frame waiting for the GPU.
		return
	}
}
