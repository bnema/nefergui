package ui

import (
	"math"
	"unicode/utf8"

	"github.com/bnema/nefergui/internal/css"
	"github.com/bnema/nefergui/internal/edit"
	"github.com/bnema/nefergui/internal/layout"
	"github.com/bnema/nefergui/internal/text"
)

// shapeAt shapes s in the element's font and letter spacing, as layout
// measures editor text, and places its runs from the logical point (x, y).
// wrap > 0 breaks lines at that width. box > 0 aligns each line within a box
// of that width by text-align; zero keeps lines starting at x.
func shapeAt(engine *text.Engine, st *css.Style, s string, x, y, wrap, box float64) (text.Layout, []layout.Run, bool) {
	req := text.Request{Families: st.FontFamily, Weight: float32(st.FontWeight), Italic: st.FontStyle == css.KeywordItalic || st.FontStyle == css.KeywordOblique, Size: st.FontSize.Value}
	if st.LetterSpacing.Unit == "px" { // percentages resolve to 0, as in layout
		req.LetterSpacing = st.LetterSpacing.Value
	}
	if req.Size <= 0 {
		req.Size = 16
	}
	measured, err := engine.Measure(s, req, wrap)
	if err != nil || len(measured.Lines) == 0 {
		return text.Layout{}, nil, false
	}
	var runs []layout.Run
	for _, line := range measured.Lines {
		lx := x
		if box > 0 {
			lx += layout.AlignOffset(st.TextAlign, line.Direction, box, line.Width)
		}
		for _, run := range line.Runs {
			rr := layout.Run{Start: run.Start, End: run.End, X: lx + run.X, Y: y + run.Y, Advance: run.Advance, Face: run.Face, FaceID: run.Face.ID, Size: req.Size}
			for _, g := range run.Glyphs {
				rr.Glyphs = append(rr.Glyphs, layout.Glyph{ID: uint32(g.ID), X: lx + g.X, Y: y + g.Y, Advance: g.Advance, Cluster: g.Cluster})
			}
			runs = append(runs, rr)
		}
	}
	return measured, runs, true
}

// placeholderCommand paints the placeholder of an empty editor in its content
// box, aligned like editor text and dimmed to half the text opacity. Textarea
// placeholders wrap at the content width. It never enters the bound value.
func placeholderCommand(e *element, n *layout.Result, engine *text.Engine) []layout.Command {
	if e.computed == nil {
		return nil
	}
	st := e.computed.Style
	x, y := n.Content.X-n.ScrollX, n.Content.Y-n.ScrollY
	wrap := 0.0
	if e.typ == "textarea" {
		wrap = n.Content.W
	}
	measured, runs, ok := shapeAt(engine, st, e.placeholder, x, y, wrap, n.Content.W)
	if !ok {
		return nil
	}
	opacity := math.Min(1, st.Opacity) * 0.5
	return []layout.Command{{Op: "text", ID: n.ID + "#placeholder", Rect: layout.Rect{X: x, Y: y, W: n.Content.W, H: measured.Height}, Text: e.placeholder, Runs: runs, Color: st.Color, Opacity: opacity}}
}

// preeditCommands shapes the composition independently of the bound value.
// Its text command begins at the model caret and carries its own rune clusters;
// the underline and composition cursor refer to UTF-8 byte positions.
func preeditCommands(s *edit.State, n *layout.Result, e *element, engine *text.Engine, display []layout.Command) []layout.Command {
	if s.Preedit == "" || n == nil || e.computed == nil {
		return nil
	}
	// The composition starts at the same caret position that is painted.
	geometry := newEditorGeometry(s, n, display, e.password)
	origin, found := geometry.caret(s.Cursor)
	x, y := n.Content.X, n.Content.Y
	if found {
		x, y = origin.x, origin.y
	}
	st := e.computed.Style
	preedit := s.Preedit
	if e.password {
		preedit = (&edit.State{Value: s.Preedit}).Mask()
	}
	measured, runs, ok := shapeAt(engine, st, preedit, x, y, 0, 0)
	if !ok {
		return nil
	}
	h := measured.LineHeight
	if h <= 0 {
		h = n.Content.H
	}
	width := measured.Width
	color := st.Color
	cmds := []layout.Command{{Op: "preedit-text", ID: n.ID, Rect: layout.Rect{X: x, Y: y, W: width, H: h}, Text: preedit, Runs: runs, Color: color, Opacity: 1}, {Op: "preedit-underline", ID: n.ID, Rect: layout.Rect{X: x, Y: y + h - 1, W: width, H: 1}, Color: color, Opacity: 1}}
	pos := s.PreeditBegin
	if pos < 0 {
		pos = 0
	}
	if pos > len(s.Preedit) {
		pos = len(s.Preedit)
	}
	clamped := 0
	for _, b := range edit.Boundaries(s.Preedit) {
		if b > pos {
			break
		}
		clamped = b
	}
	pos = clamped
	// Preedit offsets are bytes in the original text; a password shows one
	// bullet per original grapheme, including combined clusters.
	pos = newDisplayText(s.Preedit, e.password).toShown(pos)
	// The preedit cursor uses shaped cluster advances, not raw rune count.
	px := x
	pr := utf8.RuneCountInString(preedit[:pos])
	for _, run := range measured.Lines[0].Runs {
		for _, g := range run.Glyphs {
			if g.Cluster < pr {
				px = math.Max(px, x+g.X+g.Advance)
			}
		}
	}
	cmds = append(cmds, layout.Command{Op: "preedit-caret", ID: n.ID, Rect: layout.Rect{X: px, Y: y, W: 1, H: h}, Color: color, Opacity: 1})
	return cmds
}
