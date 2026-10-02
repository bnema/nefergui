// Command demo shows a desktop document workspace (toolbar, document list,
// text editor and properties pane, light and dark themes) in a Wayland window.
//
// NeferGUI draws and neferclient owns the Wayland window; the two libraries do
// not import each other, so this file copies plain fields between them. The
// glue follows neferclient's examples/layer program, with an xdg-toplevel
// window instead of a layer surface.
//
// Everything runs on one owner goroutine: the loop in run waits for a wake-up,
// calls Conn.Dispatch, then renders and presents.
package main

import (
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bnema/neferclient"
	"github.com/bnema/nefergui"
)

//go:embed app.css
var stylesheet []byte

// guiMods is the set of modifiers NeferGUI knows. Both libraries use the same
// bit order, so the plain bits can be copied.
const guiMods = neferclient.ModShift | neferclient.ModCtrl | neferclient.ModAlt | neferclient.ModSuper |
	neferclient.ModCapsLock | neferclient.ModNumLock

type demo struct {
	neferclient.NopHandler // methods this program does not need

	conn *neferclient.Conn
	surf *neferclient.Surface
	seat *neferclient.Seat

	styles string // path of the stylesheet file for RendererConfig.Styles
	r      *nefergui.Renderer
	rwake  <-chan struct{} // Renderer.Wake: nil until the renderer exists
	out    nefergui.Output
	model  Model
	damage []neferclient.Rect

	configured  bool
	canPresent  bool // configured, and the last frame callback fired
	haveAcquire bool
	cursor      neferclient.CursorShape

	frames, shown int // -frames limit and frames acknowledged so far
	done          bool
	err           error
}

func main() {
	frames := flag.Int("frames", 0, "redraw continuously and exit after this many frames (0: run until closed)")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *frames); err != nil {
		fmt.Fprintln(os.Stderr, "demo:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, frames int) (err error) {
	styles, err := writeStyles()
	if err != nil {
		return err
	}
	defer os.Remove(styles)

	conn, err := neferclient.Connect(ctx, "") // WAYLAND_DISPLAY
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	d := &demo{conn: conn, styles: styles, frames: frames, cursor: neferclient.CursorDefault, model: Model{
		Page: "home", Status: "Ready", Density: "comfortable", Volume: 40,
		Notes: "Project notes\n\nA native text workspace built with NeferGUI.\n\nSelect text, edit a paragraph, or paste from your clipboard.\nUse the properties pane to try radio buttons and the slider.\n\nEverything stays in memory for this session.",
	}}
	// Close the connection first (it destroys every Wayland object), then
	// free the renderer and its GPU resources.
	defer func() {
		err = errors.Join(err, conn.Close())
		if d.r != nil {
			err = errors.Join(err, d.r.Close())
		}
	}()
	if d.surf, err = conn.NewToplevel("NeferGUI — Untitled.txt", 960, 640); err != nil {
		return fmt.Errorf("window: %w", err)
	}
	d.seat = conn.Seat()

	var tick <-chan time.Time
	for !d.done {
		select {
		case <-ctx.Done():
			return nil // interrupt or SIGTERM: a clean exit
		case <-conn.Wake():
			if err = conn.Dispatch(d); err != nil {
				return fmt.Errorf("dispatch: %w", err)
			}
		case <-d.rwake: // another goroutine asked for a frame
		case <-tick: // a built frame was waiting for the GPU
		}
		if d.err != nil {
			return d.err
		}
		if err = d.draw(); err != nil {
			return err
		}
		tick = nil
		if d.r != nil && d.r.Pending() {
			tick = time.After(2 * time.Millisecond)
		}
	}
	return nil
}

// writeStyles writes the embedded stylesheet to a private temporary file:
// RendererConfig.Styles takes a path.
func writeStyles() (string, error) {
	f, err := os.CreateTemp("", "nefergui-demo-*.css")
	if err != nil {
		return "", fmt.Errorf("stylesheet: %w", err)
	}
	_, err = f.Write(stylesheet)
	if err = errors.Join(err, f.Close()); err != nil {
		os.Remove(f.Name())
		return "", fmt.Errorf("stylesheet: %w", err)
	}
	return f.Name(), nil
}

// FeedbackDone: dmabuf feedback → nefergui.RendererConfig. Only the first
// complete feedback is used; this demo assumes a single GPU.
func (d *demo) FeedbackDone(neferclient.SurfaceID) { d.setup() }

func (d *demo) Configure(neferclient.SurfaceID, int32, int32) {
	if !d.configured { // later configures must not lift the frame-callback gate
		d.canPresent = true
	}
	d.configured = true
	d.resize()
	d.setup()
}

func (d *demo) Scale(neferclient.SurfaceID, float64) { d.resize() }

func (d *demo) setup() {
	fb := d.surf.Feedback()
	if d.r != nil || !d.configured || fb == nil {
		return
	}
	cfg := nefergui.RendererConfig{MainDevice: fb.MainDevice, Styles: d.styles, Formats: make([]nefergui.Format, len(fb.Formats))}
	for i, f := range fb.Formats {
		cfg.Formats[i] = nefergui.Format{FourCC: f.FourCC, Modifier: f.Modifier}
	}
	r, err := nefergui.NewRenderer(cfg)
	if err != nil {
		d.fail(fmt.Errorf("renderer: %w", err))
		return
	}
	d.r, d.rwake = r, r.Wake()
	d.resize()
}

// resize forwards the logical size and scale to the renderer.
func (d *demo) resize() {
	if d.r == nil {
		return
	}
	w, h, scale := d.surf.Size()
	d.r.Resize(int(w), int(h), scale)
}

// Frame: the compositor showed the last commit; the next Present is allowed.
func (d *demo) Frame(neferclient.SurfaceID) {
	d.canPresent = true
	d.shown++
	if d.frames > 0 {
		if d.shown >= d.frames {
			d.done = true
			return
		}
		d.r.Invalidate()
	}
}

func (d *demo) Closed(neferclient.SurfaceID) { d.done = true }

func (d *demo) Error(err error) { d.fail(err) }

func (d *demo) fail(err error) {
	if d.err == nil {
		d.err = err
	}
}

// FDReady: a buffer's release eventfd is readable (id is the buffer).
func (d *demo) FDReady(id uint64) {
	if err := d.r.Released(id); err != nil {
		d.fail(fmt.Errorf("release buffer %d: %w", id, err))
	}
}

// Pointer and Key: neferclient events → nefergui.Input, field by field.
func (d *demo) Pointer(ev *neferclient.PointerEvent) {
	if d.r == nil {
		return
	}
	in := nefergui.Input{X: ev.X, Y: ev.Y}
	switch ev.Kind {
	case neferclient.PointerEnter:
		in.Kind = nefergui.InputPointerMotion
		// The cursor shape is only valid after the enter: set it again.
		if err := d.seat.SetCursor(d.cursor); err != nil {
			d.fail(fmt.Errorf("cursor: %w", err))
		}
	case neferclient.PointerMotion:
		in.Kind = nefergui.InputPointerMotion
	case neferclient.PointerLeave:
		in.Kind = nefergui.InputPointerLeave
	case neferclient.PointerButton:
		in.Kind, in.Button = nefergui.InputPointerRelease, ev.Button
		if ev.Pressed {
			in.Kind = nefergui.InputPointerPress
		}
	case neferclient.PointerAxis:
		in.Kind, in.DX, in.DY = nefergui.InputPointerAxis, ev.DX, ev.DY
	default:
		return
	}
	d.r.Input(&in)
}

func (d *demo) Key(ev *neferclient.KeyEvent) {
	if d.r == nil {
		return
	}
	d.r.Input(&nefergui.Input{
		Kind:      nefergui.InputKey,
		Keysym:    ev.Keysym,
		Text:      ev.Text, // valid only during the call, as for Input
		Pressed:   ev.Pressed,
		Repeat:    ev.Repeat,
		Modifiers: nefergui.Modifiers(ev.Modifiers & guiMods),
	})
}

func (d *demo) KeyboardFocus(_ neferclient.SurfaceID, focused bool) {
	if d.r == nil {
		return
	}
	kind := nefergui.InputFocusOut
	if focused {
		kind = nefergui.InputFocusIn
	}
	d.r.Input(&nefergui.Input{Kind: kind})
}

// draw renders a frame when something changed and presents it.
func (d *demo) draw() error {
	if d.r == nil || !d.canPresent {
		return nil
	}
	ok, err := d.r.Render(&d.out, &d.model, app)
	if err != nil {
		return fmt.Errorf("render: %w", err)
	}
	if !ok {
		return nil
	}
	if err = d.present(); err != nil {
		return err
	}
	return d.setCursor()
}

// present: Renderer.Render output → ImportBuffer, ImportTimeline, Present.
func (d *demo) present() error {
	out := &d.out
	// A resize retired buffers: stop watching and destroy them first.
	for _, rt := range out.Retired {
		if err := d.conn.UnwatchFD(rt.ReleaseFD); err != nil {
			return fmt.Errorf("unwatch retired buffer %d: %w", rt.Buffer, err)
		}
		if err := d.surf.DestroyBuffer(rt.Buffer); err != nil {
			return fmt.Errorf("destroy retired buffer %d: %w", rt.Buffer, err)
		}
		// The release timeline of a buffer is imported under the buffer's id.
		if err := d.surf.DestroyTimeline(rt.Buffer); err != nil {
			return fmt.Errorf("destroy retired timeline %d: %w", rt.Buffer, err)
		}
	}
	if out.NewBuffer {
		buf := neferclient.Buffer{
			Width: out.Width, Height: out.Height, FourCC: out.FourCC, Modifier: out.Modifier,
			PlaneCount: out.PlaneCount,
		}
		for i, p := range out.Planes {
			buf.Planes[i] = neferclient.Plane{FD: p.FD, Offset: p.Offset, Stride: p.Stride}
		}
		if err := d.surf.ImportBuffer(out.Buffer, &buf); err != nil {
			return fmt.Errorf("import buffer: %w", err)
		}
		// The release eventfd is stable per buffer: watch it once.
		if err := d.conn.WatchFD(out.ReleaseFD, out.Buffer); err != nil {
			return fmt.Errorf("watch release fd: %w", err)
		}
	}
	if out.NewTimelines {
		if !d.haveAcquire { // the acquire timeline is shared by every buffer
			if err := d.surf.ImportTimeline(out.Acquire.ID, out.Acquire.FD); err != nil {
				return fmt.Errorf("import acquire timeline: %w", err)
			}
			d.haveAcquire = true
		}
		if err := d.surf.ImportTimeline(out.Release.ID, out.Release.FD); err != nil {
			return fmt.Errorf("import release timeline: %w", err)
		}
	}
	d.damage = d.damage[:0]
	for _, r := range out.Damage {
		d.damage = append(d.damage, neferclient.Rect{X: r.X, Y: r.Y, Width: r.Width, Height: r.Height})
	}
	err := d.surf.Present(&neferclient.Present{
		Buffer:          out.Buffer,
		AcquireTimeline: out.Acquire.ID,
		ReleaseTimeline: out.Release.ID,
		AcquirePoint:    out.AcquirePoint,
		ReleasePoint:    out.ReleasePoint,
		Damage:          d.damage,
		Opaque:          true, // XRGB8888: RendererConfig.Transparent is false
	})
	if err != nil {
		return fmt.Errorf("present: %w", err)
	}
	d.canPresent = false // until Frame
	return nil
}

// setCursor: the cursor the hovered element asked for → Seat.SetCursor.
func (d *demo) setCursor() error {
	switch d.out.Cursor {
	case nefergui.CursorPointer:
		d.cursor = neferclient.CursorPointer
	case nefergui.CursorText:
		d.cursor = neferclient.CursorText
	case nefergui.CursorNotAllowed:
		d.cursor = neferclient.CursorNotAllowed
	default:
		d.cursor = neferclient.CursorDefault
	}
	if err := d.seat.SetCursor(d.cursor); err != nil {
		return fmt.Errorf("cursor: %w", err)
	}
	return nil
}
