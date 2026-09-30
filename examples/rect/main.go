//go:build linux

// Rect exercises presentation directly, without the immediate API.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/bnema/nefergui/internal/css"
	"github.com/bnema/nefergui/internal/layout"
	"github.com/bnema/nefergui/internal/presentation/session"
	"github.com/bnema/nefergui/internal/presentation/vkdevice"
	"github.com/bnema/nefergui/internal/render"
	"github.com/bnema/nefergui/internal/text"
)

func main() {
	var frames, captureFrame int
	var size, textString string
	var textScale float64
	var transparent, static, list, styles, imageMode, changingImage bool
	flag.BoolVar(&static, "static", false, "fixed rectangle and color for visual acceptance tests")
	flag.BoolVar(&list, "list", false, "draw three overlapping instanced quads")
	flag.BoolVar(&styles, "styles", false, "draw CSS-style rounded border, outline and shadow")
	flag.BoolVar(&imageMode, "image", false, "draw two scaled checkerboard images")
	flag.BoolVar(&changingImage, "changing-image", false, "image mode: pass a new image value each frame")
	flag.StringVar(&textString, "text", "", "draw glyphs using the GPU atlas")
	flag.Float64Var(&textScale, "scale", 1, "text mode: physical pixels per logical pixel")
	flag.IntVar(&frames, "frames", 0, "frames before exit (0 = until close)")
	flag.IntVar(&captureFrame, "capture-frame", 0, "static: committed frame count for capture request (0 = frames-8)")
	flag.StringVar(&size, "size", "400x300", "initial logical window width x height")
	flag.BoolVar(&transparent, "transparent", false, "use ARGB and premultiplied transparency")
	flag.Parse()
	width, height, err := parseSize(size)
	if err == nil && static && frames > 0 {
		if captureFrame == 0 {
			captureFrame = frames - 8
		}
		if captureFrame < 1 || captureFrame >= frames {
			err = fmt.Errorf("--capture-frame must be between 1 and --frames-1")
		}
	}
	if err == nil && (textString != "" || imageMode || styles) && (textScale <= 0 || math.IsNaN(textScale) || math.IsInf(textScale, 0)) {
		err = fmt.Errorf("invalid text scale")
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	s, err := session.Open("", width, height, transparent)
	if err != nil {
		fmt.Fprintln(os.Stderr, "presentation:", err)
		os.Exit(1)
	}
	closed := false
	defer func() {
		if !closed {
			_ = s.Close()
		}
	}()
	fmt.Printf("presentation: ready main_device=%d render=%d:%d logical=%dx%d physical=%dx%d transparent=%v\n", s.Window.MainDevice, s.Device.Render.Major, s.Device.Render.Minor, s.Window.Width, s.Window.Height, s.Width, s.Height, transparent)
	var requested time.Time
	draw := func(i int) vkdevice.Rect {
		if static {
			// The render pass clears to transparent black (#00000000). XRGB
			// displays that as opaque black. Straight #ff8000 at alpha 1
			// reads back as #ff8000ff; at alpha 0.5 the shader writes
			// premultiplied RGB (~128,64,0) and alpha ~128.
			alpha := float32(1)
			if transparent {
				alpha = 0.5
			}
			return vkdevice.Rect{Bounds: [4]float32{0.25, 0.25, 0.5, 0.5}, Color: [4]float32{1, 128.0 / 255, 0, alpha}}
		}
		phase := float64(i) * 0.08
		alpha := float32(1)
		if transparent {
			alpha = 0.5
		}
		return vkdevice.Rect{Bounds: [4]float32{float32(0.25 + 0.2*math.Sin(phase)), 0.25, 0.5, 0.5}, Color: [4]float32{float32(0.5 + 0.5*math.Sin(phase)), 0.25, 0.8, alpha}}
	}
	committed := func(committed int) error {
		if static && committed == captureFrame && os.Getenv("NEFERGUI_DEBUG_DIR") != "" {
			// Publish only a complete timestamp: the harness may observe this
			// file while the client is still presenting subsequent frames.
			dir := os.Getenv("NEFERGUI_DEBUG_DIR")
			path := filepath.Join(dir, "capture-request")
			tmp := path + ".tmp"
			if err := os.WriteFile(tmp, []byte(strconv.FormatInt(time.Now().UnixNano(), 10)+"\n"), 0600); err != nil {
				return err
			}
			if err := os.Rename(tmp, path); err != nil {
				return err
			}
			requested = time.Now()
		}
		// Stay mapped long enough for the harness to see two later screenshots.
		if static && committed == frames && !requested.IsZero() {
			time.Sleep(time.Until(requested.Add(250 * time.Millisecond)))
		}
		return nil
	}
	var count int
	if imageMode {
		count, err = s.RunPreparedListWithCommit(ctx, frames, func(i int) (render.Frame, error) {
			return s.Preparer.Prepare(imageCommands(i, changingImage, float64(s.Width)/textScale, float64(s.Height)/textScale), textScale, int(s.Width), int(s.Height))
		}, committed)
	} else if textString != "" {
		_, source, _, _ := runtime.Caller(0)
		catalog, e := text.Load(text.DirectorySource(filepath.Join(filepath.Dir(source), "..", "..", "testdata", "fonts")))
		if e != nil {
			fmt.Fprintln(os.Stderr, "text fonts:", e)
			os.Exit(1)
		}
		measured, e := text.NewEngine(catalog).Measure(textString, text.Request{Families: []string{"Noto Sans"}, Size: 32}, 0)
		if e != nil {
			fmt.Fprintln(os.Stderr, "text measure:", e)
			os.Exit(1)
		}
		cmd := layout.Command{Op: "text", Color: css.Color{R: 1, G: 1, B: 1, A: 1}, Opacity: 1}
		for _, line := range measured.Lines {
			for _, shaped := range line.Runs {
				r := layout.Run{Face: shaped.Face, FaceID: shaped.Face.ID, Size: 32}
				for _, g := range shaped.Glyphs {
					r.Glyphs = append(r.Glyphs, layout.Glyph{ID: uint32(g.ID), X: 20 + g.X, Y: 40 + g.Y})
				}
				cmd.Runs = append(cmd.Runs, r)
			}
		}
		count, err = s.RunPreparedListWithCommit(ctx, frames, func(int) (render.Frame, error) {
			return s.Preparer.Prepare([]layout.Command{{Op: "rect", Rect: layout.Rect{W: float64(s.Width) / textScale, H: float64(s.Height) / textScale}, Color: css.Color{R: 17.0 / 255, G: 17.0 / 255, B: 17.0 / 255, A: 1}, Opacity: 1}, cmd}, textScale, int(s.Width), int(s.Height))
		}, committed)
	} else if styles {
		count, err = s.RunPreparedListWithCommit(ctx, frames, func(int) (render.Frame, error) {
			return s.Preparer.Prepare(styleCommands(), textScale, int(s.Width), int(s.Height))
		}, committed)
	} else if list {
		count, err = s.RunListWithCommit(ctx, frames, func(int) []vkdevice.Instance { return listQuads(s.Width, s.Height) }, committed)
	} else {
		count, err = s.RunWithCommit(ctx, frames, draw, committed)
	}
	windowClosed := s.Window.Closed
	closed = true
	if cerr := s.Close(); cerr != nil && (err == nil || errors.Is(err, context.Canceled)) {
		err = cerr
	}
	fmt.Printf("presentation: frames=%d closed=%v err=%v\n", count, windowClosed, err)
	if err != nil && !errors.Is(err, context.Canceled) {
		os.Exit(1)
	}
}

// imageCommands mixes ordinary and image quads to exercise painter's order.
func imageCommands(frame int, changing bool, width, height float64) []layout.Command {
	checker := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	checker.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	checker.SetNRGBA(1, 0, color.NRGBA{G: 255, A: 255})
	checker.SetNRGBA(0, 1, color.NRGBA{B: 255, A: 255})
	checker.SetNRGBA(1, 1, color.NRGBA{R: 255, G: 255, A: 255})
	if changing {
		checker.Pix[0] = uint8(frame)
	}
	second := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			second.SetNRGBA(x, y, color.NRGBA{R: 255, G: 128, A: 255})
		}
	}
	return []layout.Command{
		{Op: "rect", Rect: layout.Rect{W: width, H: height}, Color: css.Color{R: 17.0 / 255, G: 17.0 / 255, B: 17.0 / 255, A: 1}, Opacity: 1},
		{Op: "image", Rect: layout.Rect{X: 100, Y: 100, W: 64, H: 64}, Radii: [4]float64{8, 8, 8, 8}, Image: checker, Opacity: .5},
		{Op: "rect", Rect: layout.Rect{X: 180, Y: 100, W: 20, H: 20}, Color: css.Color{R: 1, A: 1}, Opacity: 1},
		{Op: "image", Rect: layout.Rect{X: 210, Y: 100, W: 64, H: 64}, Image: second, Opacity: 1},
	}
}

func parseSize(size string) (int32, int32, error) {
	w, h, ok := strings.Cut(size, "x")
	if !ok {
		return 0, 0, fmt.Errorf("invalid --size %q", size)
	}
	x, err := strconv.ParseInt(w, 10, 32)
	if err != nil || x <= 0 {
		return 0, 0, fmt.Errorf("invalid --size %q", size)
	}
	y, err := strconv.ParseInt(h, 10, 32)
	if err != nil || y <= 0 {
		return 0, 0, fmt.Errorf("invalid --size %q", size)
	}
	return int32(x), int32(y), nil
}

// listQuads uses physical pixels and keeps each probe away from antialiased edges.
func listQuads(w, h int32) []vkdevice.Instance {
	clip := [4]float32{0, 0, float32(w), float32(h)}
	return []vkdevice.Instance{
		{Bounds: [4]float32{100, 100, 200, 200}, Clip: clip, Radii: [4]float32{20, 20, 20, 20}, Color: [4]float32{1, 0, 0, 1}},
		{Bounds: [4]float32{220, 120, 200, 200}, Clip: [4]float32{220, 120, 100, 200}, Color: [4]float32{0, 1, 0, 1}},
		{Bounds: [4]float32{140, 180, 100, 100}, Clip: clip, Color: [4]float32{0, 0, 1, .5}},
	}
}

// styleCommands uses logical coordinates; --scale converts them to physical.
// Outset shadows precede the fill, while outlines follow the border.
func styleCommands() []layout.Command {
	box := layout.Rect{X: 120, Y: 120, W: 160, H: 120}
	radius := [4]float64{20, 20, 20, 20}
	sh := css.Shadow{X: css.Length{Value: 0, Unit: "px"}, Y: css.Length{Value: 8, Unit: "px"}, Blur: css.Length{Value: 12, Unit: "px"}, Color: css.Color{A: .9}}
	return []layout.Command{
		{Op: "rect", Rect: layout.Rect{W: 800, H: 600}, Color: css.Color{R: 17.0 / 255, G: 17.0 / 255, B: 17.0 / 255, A: 1}, Opacity: 1},
		{Op: "shadow", Rect: box, Radii: radius, Shadow: &sh, Opacity: 1},
		{Op: "rect", Rect: box, Radii: radius, Color: css.Color{R: 1, G: 1, B: 1, A: 1}, Opacity: 1},
		{Op: "border", Rect: box, Radii: radius, Widths: layout.Edges{Top: 8, Right: 8, Bottom: 8, Left: 8}, Colors: css.ColorSides{Top: css.Color{R: 1, A: 1}, Right: css.Color{G: 1, A: 1}, Bottom: css.Color{B: 1, A: 1}, Left: css.Color{R: 1, G: 1, A: 1}}, Color: css.Color{A: 1}, Opacity: 1},
		// Layout has already expanded this rect by the 4px outline offset.
		{Op: "outline", Rect: layout.Rect{X: 116, Y: 116, W: 168, H: 128}, Radii: radius, Widths: layout.Edges{Top: 3, Right: 3, Bottom: 3, Left: 3}, Color: css.Color{R: 1, G: 0, B: 1, A: 1}, Opacity: 1},
	}
}
