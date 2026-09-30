package nefergui

import (
	"github.com/bnema/nefergui/internal/css"
	"github.com/bnema/nefergui/internal/platform/wayland"
)

// cursorShape maps a CSS cursor keyword to a Wayland cursor shape.
func cursorShape(k css.Keyword) uint32 {
	switch k {
	case css.KeywordPointer:
		return wayland.CursorPointer
	case css.KeywordText:
		return wayland.CursorText
	case css.KeywordNotAllowed:
		return wayland.CursorNotAllowed
	}
	return wayland.CursorDefault
}

// Wayland surface coordinates are logical; only the renderer scales pixels.
func (r *runtime) routeInput(in wayland.Input) {
	if in.Kind == wayland.InputReset {
		r.route(platformInput{Kind: "leave"})
		r.route(platformInput{Kind: "focus-out"}) // cancels all captured buttons
		return
	}
	if in.Kind == "axis" && in.DX == 0 && in.DY == 0 {
		return
	}
	r.route(platformInput{Kind: in.Kind, X: in.X, Y: in.Y, DX: in.DX, DY: in.DY, Button: in.Button, Key: in.Key, Shift: in.Key.Shift, Ctrl: in.Key.Ctrl})
}
