package text

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"image"
	"image/draw"
	"math"

	"github.com/go-text/typesetting/font"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

// Key identifies a grayscale glyph mask. Size is physical px in 26.6 units;
// Phase is one of four horizontal quarter-pixel origins (vertical phase is zero).
type Key struct {
	FaceID     string
	Glyph      font.GID
	Size       fixed.Int26_6
	Variations [32]byte
	Phase      uint8
}
type Mask struct {
	Alpha  *image.Alpha
	Origin image.Point
}

// VariationHash hashes the axis coordinates without allocating for up to 16
// axes (fonts rarely have more).
func VariationHash(v []font.Variation) [32]byte {
	var buf [16 * 8]byte
	b := buf[:0]
	if len(v) > 16 {
		b = make([]byte, 0, len(v)*8)
	}
	for _, coord := range v {
		b = binary.BigEndian.AppendUint32(b, uint32(coord.Tag))
		b = binary.BigEndian.AppendUint32(b, math.Float32bits(coord.Value))
	}
	return sha256.Sum256(b)
}
func GlyphKey(face *Face, id font.GID, physicalSize, originX float64, variations []font.Variation) (Key, error) {
	if face == nil || VariationHash(variations) != VariationHash(face.Variations) || physicalSize <= 0 || physicalSize > 4096 || math.IsNaN(physicalSize) || math.IsInf(physicalSize, 0) || math.IsNaN(originX) || math.IsInf(originX, 0) {
		return Key{}, errors.New("invalid glyph key")
	}
	frac := originX - math.Floor(originX)
	return Key{FaceID: face.ID, Glyph: id, Size: fixed.Int26_6(math.Round(physicalSize * 64)), Phase: uint8(math.Floor(frac * 4)), Variations: VariationHash(variations)}, nil
}

// Rasterize uses sfnt for static faces and go-text outlines for variable faces.
// Neither path hints glyphs. Masks have one pixel of transparent padding for filtering.
func Rasterize(face *Face, key Key) (Mask, error) {
	if face == nil || key.FaceID != face.ID || key.Size <= 0 || key.Size > 4096*64 || key.Phase > 3 {
		return Mask{}, errors.New("invalid raster request")
	}
	if VariationHash(face.Variations) != key.Variations {
		return Mask{}, errors.New("variation key does not match face")
	}
	if key.Glyph == font.EmptyGlyph {
		return Mask{Alpha: image.NewAlpha(image.Rect(0, 0, 0, 0))}, nil
	}
	var segments []outlineSegment
	if face.Variable {
		outline, ok := face.Shape.GlyphData(key.Glyph).(font.GlyphOutline)
		if !ok {
			return Mask{}, errors.New("outline unavailable")
		}
		scale := float64(key.Size) / 64 / float64(face.Shape.Upem())
		for _, s := range outline.Segments {
			seg := outlineSegment{op: int(s.Op)}
			for i, p := range s.ArgsSlice() {
				seg.points[i] = point{float64(p.X) * scale, -float64(p.Y) * scale}
			}
			segments = append(segments, seg)
		}
	} else {
		if key.Glyph > 65535 {
			return Mask{}, errors.New("glyph id out of sfnt range")
		}
		var buf sfnt.Buffer
		segs, err := face.Raster.LoadGlyph(&buf, sfnt.GlyphIndex(key.Glyph), key.Size, nil)
		if err != nil {
			return Mask{}, err
		}
		for _, s := range segs {
			seg := outlineSegment{op: int(s.Op)}
			n := 1
			if s.Op == sfnt.SegmentOpQuadTo {
				n = 2
			} else if s.Op == sfnt.SegmentOpCubeTo {
				n = 3
			}
			for i, p := range s.Args[:n] {
				seg.points[i] = point{float64(p.X) / 64, float64(p.Y) / 64}
			}
			segments = append(segments, seg)
		}
	}
	if len(segments) == 0 {
		return Mask{Alpha: image.NewAlpha(image.Rect(0, 0, 0, 0))}, nil
	}
	phase := float64(key.Phase) / 4
	loX, loY := math.Inf(1), math.Inf(1)
	hiX, hiY := math.Inf(-1), math.Inf(-1)
	for _, s := range segments {
		n := 1
		if s.op == 2 {
			n = 2
		}
		if s.op == 3 {
			n = 3
		}
		for _, p := range s.points[:n] {
			loX = math.Min(loX, p.x+phase)
			hiX = math.Max(hiX, p.x+phase)
			loY = math.Min(loY, p.y)
			hiY = math.Max(hiY, p.y)
		}
	}
	x0, y0 := int(math.Floor(loX))-1, int(math.Floor(loY))-1
	x1, y1 := int(math.Ceil(hiX))+1, int(math.Ceil(hiY))+1
	if x1-x0 > 8192 || y1-y0 > 8192 || x1 <= x0 || y1 <= y0 {
		return Mask{}, errors.New("invalid glyph bounds")
	}
	dst := image.NewAlpha(image.Rect(0, 0, x1-x0, y1-y0))
	v := vector.NewRasterizer(dst.Rect.Dx(), dst.Rect.Dy())
	v.DrawOp = draw.Src
	for _, s := range segments {
		p := s.points
		a := func(i int) (float32, float32) {
			return float32(p[i].x + phase - float64(x0)), float32(p[i].y - float64(y0))
		}
		switch s.op {
		case 0:
			x, y := a(0)
			v.MoveTo(x, y)
		case 1:
			x, y := a(0)
			v.LineTo(x, y)
		case 2:
			x, y := a(0)
			xx, yy := a(1)
			v.QuadTo(x, y, xx, yy)
		case 3:
			x, y := a(0)
			xx, yy := a(1)
			xxx, yyy := a(2)
			v.CubeTo(x, y, xx, yy, xxx, yyy)
		}
	}
	v.Draw(dst, dst.Bounds(), image.Opaque, image.Point{})
	return Mask{Alpha: dst, Origin: image.Pt(x0, y0)}, nil
}

type point struct{ x, y float64 }
type outlineSegment struct {
	op     int
	points [3]point
}
