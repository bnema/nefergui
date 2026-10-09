//go:build linux

package nefergui

import "github.com/bnema/nefergui/internal/ui"

// Renderer renders views into DMA-BUF buffers for a Wayland client the
// application owns. It imports no Wayland library, starts no goroutine and is
// used from one owner goroutine. Typical loop: forward Wayland events with
// Input and Resize, call Render when a redraw is needed, import and present
// the Output buffer, and call Released when its ReleaseFD becomes readable.
//
// Pending() reports a built frame waiting for the GPU rather than the
// compositor; arm a short timer (about 2 ms) and call Render again.
//
// Render is generic over the model type, so it is a generic method:
//
//	ok, err := r.Render(&out, &model, view)
//
// Measure sizes a view before a surface exists (owner goroutine, not for every
// frame); it changes no Renderer state:
//
//	width, height, err := r.Measure(&model, view, maxWidth)
type Renderer = ui.Renderer

// RendererConfig configures NewRenderer. MainDevice and Formats come from the
// compositor's linux-dmabuf feedback.
type RendererConfig = ui.RendererConfig

// Format is a DRM format and modifier the compositor accepts.
type Format = ui.Format

// Input is one platform input event; Text is valid only during Renderer.Input.
type Input = ui.Input

// Output describes one frame to present, in physical pixels. All file
// descriptors stay owned by the Renderer: duplicate them before handing them to
// a transport that consumes descriptors. Slices are valid until the next Render.
type Output = ui.Output

// Plane, Timeline and Retired are parts of Output. A Timeline FD is valid only
// when Output.NewTimelines is set. The client must stop watching and destroy
// each Retired buffer before the next Render.
type (
	Plane    = ui.Plane
	Timeline = ui.Timeline
	Retired  = ui.Retired
)

// Clipboard is an optional asynchronous clipboard; IME an optional input method.
type (
	Clipboard = ui.Clipboard
	IME       = ui.IME
)

// Cursor is the pointer shape the surface wants.
type Cursor = ui.Cursor

const (
	CursorDefault    = ui.CursorDefault
	CursorPointer    = ui.CursorPointer
	CursorText       = ui.CursorText
	CursorNotAllowed = ui.CursorNotAllowed
)

// NewRenderer opens the GPU named by cfg.MainDevice and loads fonts and
// styles. It allocates no images.
func NewRenderer(cfg RendererConfig) (*Renderer, error) { return ui.NewRenderer(cfg) }
