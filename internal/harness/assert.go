package harness

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"regexp"
)

// Rect uses half-open pixel coordinates.
type Rect struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}
type Check struct {
	Name   string `json:"name"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail"`
}
type Probe struct {
	Name      string `json:"name"`
	X         int    `json:"x"`
	Y         int    `json:"y"`
	Color     string `json:"color"`
	Tolerance uint8  `json:"tolerance"`
}
type Region struct {
	Name      string `json:"name"`
	Rect      Rect   `json:"rect"`
	Color     string `json:"color,omitempty"`
	Tolerance uint8  `json:"tolerance,omitempty"`
}
type Golden struct {
	Name         string  `json:"name"`
	Path         string  `json:"path"`
	Tolerance    uint8   `json:"tolerance"`
	MaxDiffRatio float64 `json:"max_diff_ratio"`
}
type Expectations struct {
	Probes  []Probe  `json:"probes"`
	Regions []Region `json:"regions"`
	Goldens []Golden `json:"goldens"`
}

func LoadPNG(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return png.Decode(f)
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func ParseColor(s string) (color.NRGBA, error) {
	var r, g, b uint8
	if !hexColor.MatchString(s) {
		return color.NRGBA{}, fmt.Errorf("color %q: expected #RRGGBB", s)
	}
	if _, err := fmt.Sscanf(s, "#%02x%02x%02x", &r, &g, &b); err != nil {
		return color.NRGBA{}, fmt.Errorf("color %q: %w", s, err)
	}
	return color.NRGBA{r, g, b, 255}, nil
}
func channels(c color.Color) color.NRGBA { return color.NRGBAModel.Convert(c).(color.NRGBA) }
func closeColor(a, b color.Color, t uint8) bool {
	x, y := channels(a), channels(b)
	for _, v := range [][2]uint8{{x.R, y.R}, {x.G, y.G}, {x.B, y.B}, {x.A, y.A}} {
		d := int(v[0]) - int(v[1])
		if d < 0 {
			d = -d
		}
		if d > int(t) {
			return false
		}
	}
	return true
}
func BoundsCheck(img image.Image, r Rect) error {
	b := img.Bounds()
	if r.Width <= 0 || r.Height <= 0 || r.X < b.Min.X || r.Y < b.Min.Y || r.X > b.Max.X-r.Width || r.Y > b.Max.Y-r.Height {
		return fmt.Errorf("region %+v outside image %v or empty", r, b)
	}
	return nil
}
func PixelProbe(img image.Image, x, y int, want color.Color, t uint8) error {
	if !image.Pt(x, y).In(img.Bounds()) {
		return fmt.Errorf("pixel (%d,%d) outside %v", x, y, img.Bounds())
	}
	got := img.At(x, y)
	if !closeColor(got, want, t) {
		return fmt.Errorf("pixel (%d,%d): got %v, want %v (tolerance %d)", x, y, channels(got), channels(want), t)
	}
	return nil
}
func UniformRegion(img image.Image, r Rect, want color.Color, t uint8) error {
	if err := BoundsCheck(img, r); err != nil {
		return err
	}
	for y := r.Y; y < r.Y+r.Height; y++ {
		for x := r.X; x < r.X+r.Width; x++ {
			if err := PixelProbe(img, x, y, want, t); err != nil {
				return err
			}
		}
	}
	return nil
}

// CompareGolden produces an opaque heatmap for every pixel, even on a mismatch.
// Pixels within tolerance are black; differing pixels show absolute RGB deltas,
// with alpha delta in the red channel so alpha-only changes remain visible.
func CompareGolden(got, want image.Image, t uint8, maxRatio float64) (bool, float64, *image.NRGBA, error) {
	if maxRatio < 0 || maxRatio > 1 {
		return false, 0, nil, fmt.Errorf("max_diff_ratio must be between 0 and 1")
	}
	if got.Bounds() != want.Bounds() {
		return false, 0, nil, fmt.Errorf("bounds differ: %v vs %v", got.Bounds(), want.Bounds())
	}
	b := got.Bounds()
	if b.Empty() {
		return false, 0, nil, fmt.Errorf("empty golden")
	}
	diff := image.NewNRGBA(b)
	draw.Draw(diff, b, &image.Uniform{C: color.NRGBA{0, 0, 0, 255}}, image.Point{}, draw.Src)
	n := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			a, c := channels(got.At(x, y)), channels(want.At(x, y))
			if closeColor(a, c, t) {
				continue
			}
			n++
			delta := func(a, b uint8) uint8 {
				if a > b {
					return a - b
				}
				return b - a
			}
			r := delta(a.R, c.R)
			if alpha := delta(a.A, c.A); alpha > r {
				r = alpha
			}
			diff.SetNRGBA(x, y, color.NRGBA{r, delta(a.G, c.G), delta(a.B, c.B), 255})
		}
	}
	ratio := float64(n) / float64(b.Dx()*b.Dy())
	return ratio <= maxRatio, ratio, diff, nil
}
func SavePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err = png.Encode(f, img); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
