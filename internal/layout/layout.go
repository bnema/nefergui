// Package layout computes logical-pixel boxes and an ordered, serializable paint list.
// Input nodes and CSS styles are borrowed for one synchronous Layout call.
package layout

import (
	"errors"
	"image"
	"math"

	"github.com/bnema/nefergui/internal/css"
	"github.com/bnema/nefergui/internal/text"
	"github.com/go-text/typesetting/di"
)

// Kind selects intrinsic and container behavior. CSS display:none suppresses all kinds;
// display:flex takes precedence over Kind, and row/column flex defaults are overridable.
type Kind string

const (
	Box    Kind = "box"
	Row    Kind = "row"
	Column Kind = "column"
	Stack  Kind = "stack"
	Scroll Kind = "scroll"
	Text   Kind = "text"
	Image  Kind = "image"
)
const limit = 1 << 24

// Rect uses logical pixels, including fractional positions.
type Rect struct{ X, Y, W, H float64 }

func (r Rect) Contains(x, y float64) bool { return x >= r.X && y >= r.Y && x < r.X+r.W && y < r.Y+r.H }
func (r Rect) intersect(q Rect) Rect {
	x := math.Max(r.X, q.X)
	y := math.Max(r.Y, q.Y)
	return Rect{x, y, math.Max(0, math.Min(r.X+r.W, q.X+q.W)-x), math.Max(0, math.Min(r.Y+r.H, q.Y+q.H)-y)}
}
func safe(v float64) float64 {
	if math.IsNaN(v) || v < 0 {
		return 0
	}
	if math.IsInf(v, 0) || v > limit {
		return limit
	}
	return v
}
func coord(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	if math.IsInf(v, 1) {
		return limit
	}
	if math.IsInf(v, -1) {
		return -limit
	}
	return math.Max(-limit, math.Min(limit, v))
}
func length(l css.Length, basis float64) (float64, bool) {
	switch l.Unit {
	case "px":
		return coord(l.Value), true
	case "%":
		return coord(safe(basis) * l.Value / 100), true
	}
	return 0, false
}
func positive(l css.Length, basis float64) float64 { v, _ := length(l, basis); return safe(v) }
func edges(s css.LengthSides, basis float64) Edges {
	return Edges{coordValue(s.Top, basis), coordValue(s.Right, basis), coordValue(s.Bottom, basis), coordValue(s.Left, basis)}
}
func coordValue(l css.Length, basis float64) float64 { v, _ := length(l, basis); return v }

// Edges are in top/right/bottom/left order.
type Edges struct{ Top, Right, Bottom, Left float64 }

func (e Edges) horizontal() float64 { return e.Left + e.Right }
func (e Edges) vertical() float64   { return e.Top + e.Bottom }
func nonnegative(e Edges) Edges {
	return Edges{safe(e.Top), safe(e.Right), safe(e.Bottom), safe(e.Left)}
}

// Measure receives the available content width (zero means unbounded), and must
// return finite logical dimensions and optionally shaped lines. It may return an error.
// TextEngine, when supplied, is used for Text nodes without an explicit callback.
type Measure func(width float64) (Size, []text.Line, error)
type Size struct{ W, H float64 }

// Node has no parent pointers or renderer resources. ID identifies hit targets and
// display commands; callers must supply stable, unique IDs if they need identity.
// ImageSize is the decoded image's intrinsic size; image ownership remains with caller.
// Image is borrowed through painting. Treat it as immutable while passed to Image:
// pass a new image value to change pixels. Renderer cache identity requires a
// comparable image.Image interface value (for example, *image.RGBA).
type Node struct {
	ID               string
	Kind             Kind
	Style            *css.Style
	Children         []*Node
	Content          string
	ImageSize        Size
	Image            image.Image `json:"-"`
	Measure          Measure     `json:"-"`
	ScrollX, ScrollY float64
	NoWrap           bool // measure text at unbounded width; useful for single-line editors
	EditorScroll     bool // clip and clamp own text on both axes, without child scrolling
	// Rect, when HasRect is set on a direct child of a Stack, is the child's
	// border-box geometry relative to the Stack's content box. It replaces the
	// child's authored width, height and margin; it is ignored elsewhere.
	// A flex-styled Stack with a visible Rect child is rejected.
	Rect    Rect
	HasRect bool
}

// Result is a detached layout snapshot. Style is deliberately not retained.
type Result struct {
	ID                            string
	Kind                          Kind
	Border, Content, Clip         Rect
	Margin, Padding, BorderWidths Edges
	ContentSize                   Size
	ScrollX, ScrollY              float64
	Lines                         []text.Line `json:"-"`
	Children                      []*Result
	Clipped                       bool
}

// Command is a paint operation. Text line ranges refer to rune offsets
// in Text; image pixels are borrowed and excluded from serialized display lists.
type Command struct {
	Op      string
	ID      string
	Rect    Rect
	Color   css.Color
	Opacity float64
	Widths  Edges
	Colors  css.ColorSides
	Radii   [4]float64
	Shadow  *css.Shadow `json:",omitempty"`
	Text    string      `json:",omitempty"`
	Runs    []Run       `json:",omitempty"`
	Image   image.Image `json:"-"`
}
type Run struct {
	Start, End    int
	X, Y, Advance float64
	// Face is borrowed for in-process rasterization. FaceID and Size preserve
	// the shaping identity and logical pixel size in serialized display lists.
	Face   *text.Face `json:"-"`
	FaceID string
	Size   float64
	Glyphs []Glyph
}
type Glyph struct {
	ID            uint32
	X, Y, Advance float64
	Cluster       int
}

// Output contains a JSON-safe tree and paint list in back-to-front command order.
type Output struct {
	Tree    *Result
	Display []Command
}

// Options provide logical viewport size and an optional text shaping engine.
type Options struct {
	Width, Height float64
	TextEngine    *text.Engine
}

var ErrDepth = errors.New("layout: tree depth exceeds 256")

// Layout does not mutate inputs. The caller must not create cycles in the node tree.
func Layout(root *Node, options Options) (Output, error) {
	if root == nil {
		return Output{}, nil
	}
	ctx := context{options: options}
	viewport := Rect{0, 0, safe(options.Width), safe(options.Height)}
	tree, err := ctx.place(root, viewport.X, viewport.Y, viewport.W, viewport.H, nil, nil, 0)
	if err != nil {
		return Output{}, err
	}
	out := Output{Tree: tree}
	out.Display = ctx.display(root, tree)
	return out, nil
}

// display paints the placed tree into a list sized once from the tree.
func (c context) display(root *Node, tree *Result) []Command {
	n := countCommands(root, tree)
	if n.commands == 0 {
		return nil
	}
	out := make([]Command, 0, n.commands)
	c.shadows, c.runs, c.glyphs = newSlab[css.Shadow](n.shadows), newSlab[Run](n.runs), newSlab[Glyph](n.glyphs)
	c.paint(root, tree, &out)
	return out
}

type context struct {
	options Options
	// Painter-owned display storage, sized by countCommands; nil is valid.
	shadows *slab[css.Shadow]
	runs    *slab[Run]
	glyphs  *slab[Glyph]
}

func style(n *Node) css.Style {
	if n.Style != nil {
		return *n.Style
	}
	return css.Style{Display: css.KeywordBlock, OverflowX: css.KeywordVisible, OverflowY: css.KeywordVisible, FlexShrink: 1}
}
func validateStackRects(n *Node, s css.Style) error {
	if n.Kind == Stack && s.Display == css.KeywordFlex {
		for _, child := range n.Children {
			if child != nil && child.HasRect && style(child).Display != css.KeywordNone {
				return errors.New("layout: Rect is unsupported in a flex-styled Stack")
			}
		}
	}
	return nil
}
func bounded(v float64, minL, maxL css.Length, basis float64) float64 {
	v = safe(v)
	if min, ok := length(minL, basis); ok {
		v = math.Max(v, safe(min))
	}
	if max, ok := length(maxL, basis); ok {
		v = math.Min(v, safe(max))
	}
	return safe(v)
}

// boxBound applies constraints to the CSS sizing space, not to the outer box.
func boxBound(border float64, minL, maxL css.Length, basis, insets float64, borderBox bool) float64 {
	if borderBox {
		return math.Max(insets, bounded(border, minL, maxL, basis))
	}
	return safe(bounded(safe(border-insets), minL, maxL, basis) + insets)
}
func (c context) intrinsic(n *Node, w float64) (Size, []text.Line, error) {
	return c.intrinsicDepth(n, w, 0)
}

func (c context) intrinsicDepth(n *Node, w float64, depth int) (Size, []text.Line, error) {
	if depth > 256 {
		return Size{}, nil, ErrDepth
	}
	st := style(n)
	if err := validateStackRects(n, st); err != nil {
		return Size{}, nil, err
	}
	if n.Measure != nil {
		s, lines, err := n.Measure(w)
		if err != nil {
			return Size{}, nil, err
		}
		for _, line := range lines {
			if math.IsNaN(line.Width) || math.IsInf(line.Width, 0) || math.IsNaN(line.Height) || math.IsInf(line.Height, 0) {
				return Size{}, nil, errors.New("layout: invalid measured text")
			}
		}
		return Size{safe(s.W), safe(s.H)}, lines, nil
	}
	if n.Kind == Image {
		return Size{safe(n.ImageSize.W), safe(n.ImageSize.H)}, nil, nil
	}
	if n.Kind == Text && c.options.TextEngine != nil {
		st := style(n)
		req := text.Request{Families: st.FontFamily, Weight: float32(st.FontWeight), Italic: st.FontStyle == css.KeywordItalic || st.FontStyle == css.KeywordOblique, Size: safe(st.FontSize.Value), LetterSpacing: coordValue(st.LetterSpacing, 0)}
		if req.Size == 0 {
			req.Size = 16
		}
		content := n.Content
		if content == "" && n.EditorScroll {
			// Preserve one font-metric line for empty editors, caret geometry
			// and IME composition without painting a visible glyph.
			content = " "
		}
		l, err := c.options.TextEngine.Measure(content, req, w)
		if err != nil {
			return Size{}, nil, err
		}
		lineHeight := l.LineHeight
		switch st.LineHeightKind {
		case css.KeywordNumber:
			lineHeight = safe(req.Size * st.LineHeightNumber)
		case css.KeywordNormal:
		default:
			if st.LineHeight.Unit != "auto" && st.LineHeight.Unit != "" {
				lineHeight = positive(st.LineHeight, req.Size)
			}
		}
		// Keep the glyphs' shaped positions; adjust baseline and line origins, not outlines.
		if lineHeight != l.LineHeight && len(l.Lines) > 0 {
			l = l.Clone() // Measure results are shared with the engine cache
			for i := range l.Lines {
				line := &l.Lines[i]
				dy := float64(i)*(lineHeight-l.LineHeight) + (lineHeight-l.LineHeight)/2
				line.Baseline += dy
				line.Height = lineHeight
				for j := range line.Runs {
					run := &line.Runs[j]
					run.Y += dy
					for k := range run.Glyphs {
						run.Glyphs[k].Y += dy
					}
				}
			}
			l.Height = float64(len(l.Lines)) * lineHeight
		}
		return Size{safe(l.Width), safe(l.Height)}, l.Lines, nil
	}
	if depth == 0 {
		return Size{}, nil, nil
	}
	// Containers contribute their children to auto flex sizing. Measuring them
	// as zero compresses nested columns and makes empty editors collapse.
	row := st.Display == css.KeywordFlex && st.FlexDirection != css.KeywordColumn
	gap := positive(st.RowGap, w)
	if row {
		gap = positive(st.ColumnGap, w)
	}
	var size Size
	count := 0
	for _, child := range n.Children {
		if child == nil || style(child).Display == css.KeywordNone {
			continue
		}
		if n.Kind == Stack && child.HasRect {
			size.W = math.Max(size.W, safe(child.Rect.X+child.Rect.W))
			size.H = math.Max(size.H, safe(child.Rect.Y+child.Rect.H))
			count++
			continue
		}
		cs := style(child)
		inW, inH := flexInsets(cs, w)
		measured, _, err := c.intrinsicDepth(child, safe(w-inW), depth+1)
		if err != nil {
			return Size{}, nil, err
		}
		cw, ch := measured.W+inW, measured.H+inH
		if value, ok := length(cs.Width, w); ok && cs.Width.Unit != "%" {
			cw = value
			if cs.BoxSizing != css.KeywordBorderBox {
				cw += inW
			}
		}
		// Percent heights need a definite containing height, unavailable here.
		if cs.Height.Unit != "%" {
			if value, ok := length(cs.Height, 0); ok {
				ch = value
				if cs.BoxSizing != css.KeywordBorderBox {
					ch += inH
				}
			}
		}
		cw = boxBound(cw, cs.MinWidth, cs.MaxWidth, w, inW, cs.BoxSizing == css.KeywordBorderBox)
		ch = boxBound(ch, cs.MinHeight, cs.MaxHeight, 0, inH, cs.BoxSizing == css.KeywordBorderBox)
		// Natural measurement is an unwrapped envelope; actual placement
		// distributes flex-basis and wraps against the definite container size.
		margin := edges(cs.Margin, w)
		cw += margin.horizontal()
		ch += margin.vertical()
		if row {
			if count > 0 {
				size.W += gap
			}
			size.W += cw
			size.H = math.Max(size.H, ch)
		} else if n.Kind == Stack {
			size.W = math.Max(size.W, cw)
			size.H = math.Max(size.H, ch)
		} else {
			if count > 0 && st.Display == css.KeywordFlex {
				size.H += gap
			}
			size.W = math.Max(size.W, cw)
			size.H += ch
		}
		count++
	}
	return size, nil, nil
}
func (c context) place(n *Node, x, y, availableW, availableH float64, forcedW, forcedH *float64, depth int) (*Result, error) {
	if depth > 256 {
		return nil, ErrDepth
	}
	if n == nil {
		return nil, nil
	}
	s := style(n)
	if s.Display == css.KeywordNone {
		return nil, nil
	}
	if err := validateStackRects(n, s); err != nil {
		return nil, err
	}
	availableW = safe(availableW)
	availableH = safe(availableH)
	margin := edges(s.Margin, availableW)
	padding := nonnegative(edges(s.Padding, availableW))
	border := nonnegative(edges(s.BorderWidth, availableW))
	if s.BorderStyle.Top != css.KeywordSolid {
		border.Top = 0
	}
	if s.BorderStyle.Right != css.KeywordSolid {
		border.Right = 0
	}
	if s.BorderStyle.Bottom != css.KeywordSolid {
		border.Bottom = 0
	}
	if s.BorderStyle.Left != css.KeywordSolid {
		border.Left = 0
	}
	outerW := safe(availableW - margin.horizontal())
	insetW := padding.horizontal() + border.horizontal()
	insetH := padding.vertical() + border.vertical()
	w, explicitW := length(s.Width, availableW)
	h, explicitH := length(s.Height, availableH)
	borderBox := s.BoxSizing == css.KeywordBorderBox
	// Forced flex lengths are border-box; authored dimensions and constraints use box-sizing.
	if forcedW != nil {
		w, explicitW = *forcedW, true
	} else if explicitW {
		w = bounded(w, s.MinWidth, s.MaxWidth, availableW)
		if !borderBox {
			w += insetW
		}
	}
	if forcedH != nil {
		h, explicitH = *forcedH, true
	} else if explicitH {
		h = bounded(h, s.MinHeight, s.MaxHeight, availableH)
		if !borderBox {
			h += insetH
		}
	}
	if !explicitW {
		w = outerW
		if n.Kind == Image {
			w = 0
		}
	}
	if !explicitW && n.Kind != Image {
		w = boxBound(w, s.MinWidth, s.MaxWidth, availableW, insetW, borderBox)
	}
	w = safe(math.Max(w, insetW))
	contentW := safe(w - insetW)
	measureW := contentW
	if n.Kind == Text && n.NoWrap {
		measureW = 0
	}
	measured, lines, err := c.intrinsic(n, measureW)
	if err != nil {
		return nil, err
	}
	if !explicitW && n.Kind == Image {
		w = boxBound(measured.W+insetW, s.MinWidth, s.MaxWidth, availableW, insetW, borderBox)
		contentW = safe(w - insetW)
		measured, lines, err = c.intrinsic(n, contentW)
		if err != nil {
			return nil, err
		}
	}
	if !explicitH {
		h = measured.H + insetH
		if n.Kind == Image && measured.W > 0 && explicitW {
			h = measured.H*contentW/measured.W + insetH
		}
	}
	if n.Kind == Image && !explicitW && explicitH && measured.H > 0 {
		w = boxBound(measured.W*safe(h-insetH)/measured.H+insetW, s.MinWidth, s.MaxWidth, availableW, insetW, borderBox)
		contentW = safe(w - insetW)
	}
	h = safe(math.Max(h, insetH))
	if !explicitH {
		h = boxBound(h, s.MinHeight, s.MaxHeight, availableH, insetH, borderBox)
	}
	r := &Result{ID: n.ID, Kind: n.Kind, Border: Rect{coord(x + margin.Left), coord(y + margin.Top), w, h}, Margin: margin, Padding: padding, BorderWidths: border, Lines: lines}
	r.Content = Rect{coord(r.Border.X + border.Left + padding.Left), coord(r.Border.Y + border.Top + padding.Top), contentW, safe(h - insetH)}
	r.ContentSize = Size{measured.W, measured.H}
	// Block and stack auto heights depend on their children's envelopes.
	flex := s.Display == css.KeywordFlex
	if flex {
		// An auto-height flex container needs its children's natural height
		// before distributing space. Otherwise every child is shrunk into zero.
		// This repeats subtree measurement for nested auto-height containers;
		// a node/width map cache cost more time and memory in our benchmarks.
		if !explicitH {
			natural, _, measureErr := c.intrinsicDepth(n, contentW, 1)
			if measureErr != nil {
				return nil, measureErr
			}
			r.Border.H = boxBound(math.Max(natural.H+insetH, h), s.MinHeight, s.MaxHeight, availableH, insetH, borderBox)
			r.Content.H = safe(r.Border.H - insetH)
		}
		err = c.flex(n, r, s, depth)
	} else {
		err = c.children(n, r, s, depth)
	}
	if err != nil {
		return nil, err
	}
	if !explicitH {
		r.Border.H = boxBound(math.Max(safe(h-insetH), r.ContentSize.H)+insetH, s.MinHeight, s.MaxHeight, availableH, insetH, borderBox)
		r.Content.H = safe(r.Border.H - insetH)
	}
	clipX := overflowClips(s.OverflowX) || n.Kind == Scroll || n.EditorScroll
	clipY := overflowClips(s.OverflowY) || n.Kind == Scroll || n.EditorScroll
	if clipX || clipY {
		r.Clipped = true
		r.Clip = r.Content
		if !clipX {
			r.Clip.X = -limit
			r.Clip.W = 2 * limit
		}
		if !clipY {
			r.Clip.Y = -limit
			r.Clip.H = 2 * limit
		}
		if clipX && (s.OverflowX == css.KeywordScroll || s.OverflowX == css.KeywordAuto || n.Kind == Scroll || n.EditorScroll) {
			r.ScrollX = math.Min(safe(n.ScrollX), safe(r.ContentSize.W-r.Content.W))
		}
		if clipY && (s.OverflowY == css.KeywordScroll || s.OverflowY == css.KeywordAuto || n.Kind == Scroll || n.EditorScroll) {
			r.ScrollY = math.Min(safe(n.ScrollY), safe(r.ContentSize.H-r.Content.H))
		}
		for _, child := range r.Children {
			shift(child, -r.ScrollX, -r.ScrollY)
		}
	}
	return r, nil
}
func overflowClips(k css.Keyword) bool {
	return k == css.KeywordHidden || k == css.KeywordScroll || k == css.KeywordAuto
}
func shift(r *Result, dx, dy float64) {
	r.Border.X = coord(r.Border.X + dx)
	r.Border.Y = coord(r.Border.Y + dy)
	r.Content.X = coord(r.Content.X + dx)
	r.Content.Y = coord(r.Content.Y + dy)
	r.Clip.X = coord(r.Clip.X + dx)
	r.Clip.Y = coord(r.Clip.Y + dy)
	for _, child := range r.Children {
		shift(child, dx, dy)
	}
}
func envelope(r *Result) (float64, float64) {
	return safe(r.Border.W + r.Margin.horizontal()), safe(r.Border.H + r.Margin.vertical())
}
func (c context) children(n *Node, r *Result, s css.Style, depth int) error {
	y := r.Content.Y
	for _, ch := range n.Children {
		if ch == nil {
			continue
		}
		var child *Result
		var err error
		absolute := n.Kind == Stack && ch.HasRect
		if absolute {
			// Direct geometry: forced border-box size, and the origin is offset
			// by the resolved margin so the border box lands exactly at Rect.
			w, h := safe(ch.Rect.W), safe(ch.Rect.H)
			m := edges(style(ch).Margin, r.Content.W)
			child, err = c.place(ch, coord(r.Content.X+ch.Rect.X-m.Left), coord(r.Content.Y+ch.Rect.Y-m.Top), r.Content.W, r.Content.H, &w, &h, depth+1)
		} else {
			child, err = c.place(ch, r.Content.X, y, r.Content.W, r.Content.H, nil, nil, depth+1)
		}
		if err != nil {
			return err
		}
		if child == nil {
			continue
		}
		cw, chh := envelope(child)
		if absolute {
			cw, chh = safe(ch.Rect.X+ch.Rect.W), safe(ch.Rect.Y+ch.Rect.H)
		}
		r.Children = append(r.Children, child)
		r.ContentSize.W = math.Max(r.ContentSize.W, cw)
		if n.Kind == Stack {
			r.ContentSize.H = math.Max(r.ContentSize.H, chh)
		} else {
			y = coord(y + chh)
			r.ContentSize.H = math.Max(r.ContentSize.H, y-r.Content.Y)
		}
	}
	return nil
}

type flexItem struct {
	node                                                     *Node
	base, size, cross, grow, shrink, marginMain, marginCross float64 // sizes are border boxes
	min, max                                                 float64
	frozen                                                   bool
}

// flexInsets computes effective border-box insets, using the same border-style
// treatment as place. Percentage padding is relative to the containing width.
func flexInsets(s css.Style, basis float64) (float64, float64) {
	p := nonnegative(edges(s.Padding, basis))
	b := nonnegative(edges(s.BorderWidth, basis))
	if s.BorderStyle.Top != css.KeywordSolid {
		b.Top = 0
	}
	if s.BorderStyle.Right != css.KeywordSolid {
		b.Right = 0
	}
	if s.BorderStyle.Bottom != css.KeywordSolid {
		b.Bottom = 0
	}
	if s.BorderStyle.Left != css.KeywordSolid {
		b.Left = 0
	}
	return p.horizontal() + b.horizontal(), p.vertical() + b.vertical()
}
func flexBounds(s css.Style, main bool, basis, inset float64) (float64, float64) {
	minL, maxL := s.MinWidth, s.MaxWidth
	if !main {
		minL, maxL = s.MinHeight, s.MaxHeight
	}
	low, high := inset, float64(limit)
	if v, ok := length(minL, basis); ok {
		if s.BoxSizing != css.KeywordBorderBox {
			low = math.Max(inset, safe(v)+inset)
		} else {
			low = math.Max(inset, safe(v))
		}
	}
	if v, ok := length(maxL, basis); ok {
		high = safe(v)
		if s.BoxSizing != css.KeywordBorderBox {
			high += inset
		}
		high = math.Max(high, inset)
	}
	return low, math.Max(low, high)
}

// resolveFlex freezes violating items, then redistributes the remaining free
// space among unfrozen items. Shrink weights always use the original base.
func resolveFlex(items []flexItem, available float64) {
	total := 0.0
	for i := range items {
		it := &items[i]
		it.size = it.base
		it.frozen = false
		total += it.base + it.marginMain
	}
	grow := available > total
	// Each pass freezes at least one violator, or all remaining items.
	for range items {
		free, weight, factors := available, 0.0, 0.0
		for i := range items {
			it := &items[i]
			free -= it.marginMain
			if it.frozen {
				free -= it.size
				continue
			}
			free -= it.base
			factor := it.grow
			if !grow {
				factor = it.shrink * it.base
			}
			weight += factor
			if grow {
				factors += it.grow
			}
		}
		// CSS resolve-flexible-lengths limits growth when total grow factors < 1.
		if grow && factors < 1 {
			free *= factors
		}
		violation := 0.0
		for i := range items {
			it := &items[i]
			if it.frozen {
				continue
			}
			factor := it.grow
			if !grow {
				factor = it.shrink * it.base
			}
			tentative := it.base
			if weight > 0 {
				tentative += free * factor / weight
			}
			it.size = math.Max(it.min, math.Min(it.max, safe(tentative)))
			violation += it.size - tentative
		}
		if math.Abs(violation) < 1e-9 {
			for i := range items {
				items[i].frozen = true
			}
			break
		}
		for i := range items {
			it := &items[i]
			if it.frozen {
				continue
			}
			factor := it.grow
			if !grow {
				factor = it.shrink * it.base
			}
			tentative := it.base
			if weight > 0 {
				tentative += free * factor / weight
			}
			if violation > 0 && it.size > tentative || violation < 0 && it.size < tentative {
				it.frozen = true
			}
		}
	}
}
func (c context) flex(n *Node, r *Result, s css.Style, depth int) error {
	row := s.FlexDirection != css.KeywordColumn
	mainSize, crossSize := r.Content.W, r.Content.H
	gap, crossGap := positive(s.ColumnGap, r.Content.W), positive(s.RowGap, r.Content.W)
	if !row {
		mainSize, crossSize = crossSize, mainSize
		gap, crossGap = crossGap, gap
	}
	items := make([]flexItem, 0, len(n.Children))
	for _, ch := range n.Children {
		if ch == nil || style(ch).Display == css.KeywordNone {
			continue
		}
		st := style(ch)
		m := edges(st.Margin, r.Content.W)
		inW, inH := flexInsets(st, r.Content.W)
		inMain, inCross := inW, inH
		if !row {
			inMain, inCross = inH, inW
		}
		it := flexItem{node: ch, grow: safe(st.FlexGrow), shrink: safe(st.FlexShrink)}
		if row {
			it.marginMain = m.horizontal()
			it.marginCross = m.vertical()
		} else {
			it.marginMain = m.vertical()
			it.marginCross = m.horizontal()
		}
		dim, crossDim := st.Width, st.Height
		minCross, maxCross := st.MinHeight, st.MaxHeight
		if !row {
			dim, crossDim = st.Height, st.Width
			minCross, maxCross = st.MinWidth, st.MaxWidth
		}
		measureWidth := 0.0
		if row {
			measureWidth = mainSize
		} else {
			measureWidth = crossSize
		}
		measured, _, err := c.intrinsicDepth(ch, measureWidth, 1)
		if err != nil {
			return err
		}
		intrinsicMain, intrinsicCross := measured.W, measured.H
		if !row {
			intrinsicMain, intrinsicCross = measured.H, measured.W
		}
		value, ok := length(st.FlexBasis, mainSize)
		if !ok {
			value, ok = length(dim, mainSize)
		}
		if ok {
			it.base = safe(value)
			if st.BoxSizing != css.KeywordBorderBox {
				it.base += inMain
			}
		} else {
			it.base = intrinsicMain + inMain
		}
		value, ok = length(crossDim, crossSize)
		if ok {
			it.cross = safe(value)
			if st.BoxSizing != css.KeywordBorderBox {
				it.cross += inCross
			}
		} else {
			it.cross = intrinsicCross + inCross
		}
		it.min, it.max = flexBounds(st, row, mainSize, inMain)
		it.cross = boxBound(it.cross, minCross, maxCross, crossSize, inCross, st.BoxSizing == css.KeywordBorderBox)
		items = append(items, it)
	}
	lines := [][]flexItem{}
	line := []flexItem{}
	used := 0.0
	for _, item := range items {
		need := item.base + item.marginMain
		if len(line) > 0 {
			need += gap
		}
		if s.FlexWrap == css.KeywordWrap && len(line) > 0 && used+need > mainSize {
			lines = append(lines, line)
			line = nil
			used = 0
			need = item.base + item.marginMain
		}
		line = append(line, item)
		used += need
	}
	if len(line) > 0 {
		lines = append(lines, line)
	}
	crossPos := 0.0
	for _, line := range lines {
		lineCross := 0.0
		for _, it := range line {
			lineCross = math.Max(lineCross, it.cross+it.marginCross)
		}
		if len(lines) == 1 && crossSize > 0 && (row && s.Height.Unit != "auto" && s.Height.Unit != "" || !row && s.Width.Unit != "auto" && s.Width.Unit != "") {
			lineCross = math.Max(lineCross, crossSize)
		}
		resolveFlex(line, mainSize-gap*float64(len(line)-1))
		occupied := gap * float64(len(line)-1)
		for _, it := range line {
			occupied += it.size + it.marginMain
		}
		remaining := safe(mainSize - occupied)
		start, spacing := 0.0, gap
		switch s.JustifyContent {
		case css.KeywordFlexEnd:
			start = remaining
		case css.KeywordCenter:
			start = remaining / 2
		case css.KeywordSpaceBetween:
			if len(line) > 1 {
				spacing += remaining / float64(len(line)-1)
			}
		case css.KeywordSpaceAround:
			spacing += remaining / float64(len(line))
			start = remaining / float64(2*len(line))
		case css.KeywordSpaceEvenly:
			spacing += remaining / float64(len(line)+1)
			start = remaining / float64(len(line)+1)
		}
		pos := start
		for _, it := range line {
			align := style(it.node).AlignSelf
			if align == css.KeywordAuto || align == css.KeywordNone {
				align = s.AlignItems
			}
			cross := it.cross
			if align == css.KeywordStretch {
				cross = safe(lineCross - it.marginCross)
			}
			offset := 0.0
			switch align {
			case css.KeywordCenter:
				offset = safe(lineCross-cross-it.marginCross) / 2
			case css.KeywordFlexEnd:
				offset = safe(lineCross - cross - it.marginCross)
			}
			main := safe(it.size)
			cross = safe(cross)
			var x, y float64
			var w, h *float64
			if row {
				x = r.Content.X + pos
				y = r.Content.Y + crossPos + offset
				w = &main
				h = &cross
			} else {
				x = r.Content.X + crossPos + offset
				y = r.Content.Y + pos
				w = &cross
				h = &main
			}
			child, err := c.place(it.node, x, y, r.Content.W, r.Content.H, w, h, depth+1)
			if err != nil {
				return err
			}
			if child != nil {
				r.Children = append(r.Children, child)
				cw, ch := envelope(child)
				r.ContentSize.W = math.Max(r.ContentSize.W, child.Border.X-r.Content.X+cw)
				r.ContentSize.H = math.Max(r.ContentSize.H, child.Border.Y-r.Content.Y+ch)
			}
			pos += main + it.marginMain + spacing
		}
		crossPos += lineCross + crossGap
	}
	return nil
}

// slab hands out detached values or sub-slices from one exactly sized block,
// so a display list costs a few allocations rather than one per shadow, run or
// text command. Blocks are never resized or reused, so pointers and slices
// stay valid for the life of the returned Output. Sub-slices have their
// capacity clipped, so a caller's append cannot overwrite a neighbour. A
// miscount, or a nil slab, only falls back to individual allocations.
type slab[T any] struct{ block []T }

func newSlab[T any](n int) *slab[T] {
	if n <= 0 {
		return nil
	}
	return &slab[T]{block: make([]T, 0, n)}
}

// one stores v in the slab and returns its address.
func (a *slab[T]) one(v T) *T {
	if a == nil || len(a.block) == cap(a.block) {
		p := new(T) // copy, so v itself never escapes
		*p = v
		return p
	}
	a.block = append(a.block, v)
	return &a.block[len(a.block)-1]
}

// take returns an empty slice with capacity exactly n (nil for n == 0).
func (a *slab[T]) take(n int) []T {
	if n <= 0 {
		return nil
	}
	if a == nil || cap(a.block)-len(a.block) < n {
		return make([]T, 0, n)
	}
	i := len(a.block)
	a.block = a.block[:i+n]
	return a.block[i : i : i+n]
}

// radiiOf resolves border-radius percentages against basis.
func radiiOf(s *css.Style, basis float64) [4]float64 {
	return [4]float64{positive(s.BorderRadius.TopLeft, basis), positive(s.BorderRadius.TopRight, basis), positive(s.BorderRadius.BottomRight, basis), positive(s.BorderRadius.BottomLeft, basis)}
}

func hasOutline(s *css.Style, r *Result) bool {
	return s.OutlineStyle == css.KeywordSolid && positive(s.OutlineWidth, r.Border.W) > 0
}

// countCommands returns how many commands and shadow copies paint emits for
// the subtree, so Layout can size the display list once. It mirrors paint's
// conditions; if it ever drifts, paint still appends correctly and only regrows.
func countCommands(n *Node, r *Result) (c counts) {
	if r == nil {
		return c
	}
	s := style(n)
	total := len(s.BoxShadow)
	c.shadows = len(s.BoxShadow)
	if s.BackgroundColor.A > 0 {
		total++
	}
	if r.BorderWidths != (Edges{}) || radiiOf(&s, r.Border.W) != ([4]float64{}) {
		total++
	}
	if hasOutline(&s, r) {
		total++
	}
	if r.Clipped {
		total += 2
	}
	if n.Kind == Image || n.Kind == Text {
		total++
	}
	if n.Kind == Text {
		c.runs, c.glyphs = textCounts(r)
	}
	j := 0
	for _, child := range n.Children {
		if child == nil || style(child).Display == css.KeywordNone {
			continue
		}
		if j < len(r.Children) {
			cc := countCommands(child, r.Children[j])
			total += cc.commands
			c.shadows += cc.shadows
			c.runs += cc.runs
			c.glyphs += cc.glyphs
			j++
		}
	}
	c.commands += total
	return c
}

type counts struct{ commands, shadows, runs, glyphs int }

func textCounts(r *Result) (runs, glyphs int) {
	for i := range r.Lines {
		runs += len(r.Lines[i].Runs)
		for j := range r.Lines[i].Runs {
			glyphs += len(r.Lines[i].Runs[j].Glyphs)
		}
	}
	return runs, glyphs
}

func (c context) paint(n *Node, r *Result, out *[]Command) {
	if r == nil {
		return
	}
	s := style(n)
	base := Command{ID: r.ID, Rect: r.Border, Opacity: math.Min(1, safe(s.Opacity))}
	borderRadii := radiiOf(&s, r.Border.W)
	for i := range s.BoxShadow {
		if s.BoxShadow[i].Inset {
			continue
		}
		cmd := base
		cmd.Op = "shadow"
		cmd.Shadow = c.shadows.one(s.BoxShadow[i])
		cmd.Radii = borderRadii
		*out = append(*out, cmd)
	}
	if s.BackgroundColor.A > 0 {
		cmd := base
		cmd.Op = "rect"
		cmd.Color = s.BackgroundColor
		cmd.Radii = borderRadii
		*out = append(*out, cmd)
	}
	cmd := base
	cmd.Op = "border"
	cmd.Widths = r.BorderWidths
	cmd.Colors = s.BorderColor
	cmd.Radii = borderRadii
	if cmd.Widths != (Edges{}) || cmd.Radii != ([4]float64{}) {
		cmd.Color = s.BorderColor.Top
		*out = append(*out, cmd)
	}
	for i := range s.BoxShadow {
		if !s.BoxShadow[i].Inset {
			continue
		}
		cmd := base
		cmd.Op = "shadow"
		cmd.Shadow = c.shadows.one(s.BoxShadow[i])
		cmd.Radii = borderRadii
		*out = append(*out, cmd)
	}
	if hasOutline(&s, r) {
		cmd = base
		cmd.Op = "outline"
		cmd.Color = s.OutlineColor
		cmd.Radii = borderRadii
		width := positive(s.OutlineWidth, r.Border.W)
		cmd.Widths = Edges{Top: width, Right: width, Bottom: width, Left: width}
		offset := coordValue(s.OutlineOffset, r.Border.W)
		cmd.Rect = Rect{X: coord(r.Border.X - offset), Y: coord(r.Border.Y - offset), W: safe(r.Border.W + 2*offset), H: safe(r.Border.H + 2*offset)}
		*out = append(*out, cmd)
	}
	if r.Clipped {
		cmd = base
		cmd.Op = "clip-push"
		cmd.Rect = r.Clip
		*out = append(*out, cmd)
	}
	if n.Kind == Image {
		cmd = base
		cmd.Op = "image"
		cmd.Rect = r.Content
		cmd.Image = n.Image
		cmd.Radii = radiiOf(&s, r.Content.W)
		*out = append(*out, cmd)
	}
	if n.Kind == Text {
		cmd = base
		cmd.Op = "text"
		cmd.Rect = r.Content
		cmd.Text = n.Content
		cmd.Color = s.Color
		runs, glyphs := textCounts(r)
		cmd.Runs = c.runs.take(runs)
		backing := c.glyphs.take(glyphs)
		for _, line := range r.Lines {
			offset := AlignOffset(s.TextAlign, line.Direction, r.Content.W, line.Width)
			for _, run := range line.Runs {
				rr := Run{Start: run.Start, End: run.End, X: run.X + r.Content.X + offset - r.ScrollX, Y: run.Y + r.Content.Y - r.ScrollY, Advance: run.Advance, Face: run.Face, Size: s.FontSize.Value}
				if rr.Size <= 0 {
					rr.Size = 16 // matches the intrinsic text.Measure request fallback
				}
				if run.Face != nil {
					rr.FaceID = run.Face.ID
				}
				if len(run.Glyphs) > 0 { // keep nil for glyph-less runs
					first := len(backing)
					for _, g := range run.Glyphs {
						backing = append(backing, Glyph{uint32(g.ID), g.X + r.Content.X + offset - r.ScrollX, g.Y + r.Content.Y - r.ScrollY, g.Advance, g.Cluster})
					}
					rr.Glyphs = backing[first:len(backing):len(backing)]
				}
				cmd.Runs = append(cmd.Runs, rr)
			}
		}
		*out = append(*out, cmd)
	}
	j := 0
	for _, child := range n.Children {
		if child == nil || style(child).Display == css.KeywordNone {
			continue
		}
		if j < len(r.Children) {
			c.paint(child, r.Children[j], out)
			j++
		}
	}
	if r.Clipped {
		cmd = base
		cmd.Op = "clip-pop"
		*out = append(*out, cmd)
	}
}

// AlignOffset is the horizontal offset of a line of width lineW inside a
// content box of width contentW under text-align and the line's direction.
func AlignOffset(align css.Keyword, direction di.Direction, contentW, lineW float64) float64 {
	switch align {
	case css.KeywordCenter:
		return safe(contentW-lineW) / 2
	case css.KeywordRight:
		return safe(contentW - lineW)
	case css.KeywordStart:
		if direction == di.DirectionRTL {
			return safe(contentW - lineW)
		}
	case css.KeywordEnd:
		if direction != di.DirectionRTL {
			return safe(contentW - lineW)
		}
	}
	return 0
}

// Hit returns the frontmost node ID whose border box contains the logical point.
// Ancestor content clips constrain descendants, but not the ancestor border itself.
func Hit(r *Result, x, y float64) string {
	if math.IsNaN(x) || math.IsNaN(y) || math.IsInf(x, 0) || math.IsInf(y, 0) {
		return ""
	}
	return hit(r, x, y, Rect{-limit, -limit, 2 * limit, 2 * limit})
}
func hit(r *Result, x, y float64, clip Rect) string {
	if r == nil || !clip.Contains(x, y) {
		return ""
	}
	childClip := clip
	if r.Clipped {
		childClip = clip.intersect(r.Clip)
	}
	if childClip.Contains(x, y) {
		for i := len(r.Children) - 1; i >= 0; i-- {
			if id := hit(r.Children[i], x, y, childClip); id != "" {
				return id
			}
		}
	}
	if r.Border.Contains(x, y) {
		return r.ID
	}
	return ""
}

// Physical is the explicit renderer boundary; all layout and hit testing stay logical.
// It rounds edges independently to avoid accumulated fractional-scale drift.
func Physical(r Rect, scale float64) (x, y, w, h int) {
	if math.IsNaN(scale) || math.IsInf(scale, 0) || scale <= 0 {
		return
	}
	scale = math.Min(scale, 1024)
	x = int(math.Round(coord(r.X) * scale))
	y = int(math.Round(coord(r.Y) * scale))
	w = int(math.Round(coord(r.X+safe(r.W))*scale)) - x
	h = int(math.Round(coord(r.Y+safe(r.H))*scale)) - y
	return
}
