package wayland

import "github.com/bnema/wlturbo/protocol/cursorshape"

// Cursor shapes NeferGUI maps from CSS `cursor`, in wp_cursor_shape_device_v1
// enum values.
const (
	CursorDefault    = cursorshape.SHAPE_DEFAULT
	CursorPointer    = cursorshape.SHAPE_POINTER
	CursorText       = cursorshape.SHAPE_TEXT
	CursorNotAllowed = cursorshape.SHAPE_NOT_ALLOWED
)

// cursorState is owned by the session loop. The shape device belongs to the
// current wl_pointer; set_shape needs the serial of its latest enter.
type cursorState struct {
	device *cursorshape.WpCursorShapeDevice
	serial uint32 // latest enter serial, 0 while outside the surface
	shape  uint32 // last shape sent at serial
}

// PointerEntered records the enter serial and resets the sent shape: the
// compositor forgets the client cursor when the pointer leaves the surface.
// An enter from a released pointer (gen not current) is ignored.
func (w *Window) PointerEntered(serial uint32, gen uint64) {
	if w.Pointer == nil || gen != w.pointerGen {
		return
	}
	w.cursor.serial, w.cursor.shape = serial, 0
}

// PointerLeft marks the pointer outside; shapes are not sent until enter.
func (w *Window) PointerLeft() { w.cursor.serial, w.cursor.shape = 0, 0 }

// SetCursor requests shape for the pointer over the surface. It is a no-op
// without cursor-shape support, outside the surface, or when unchanged.
func (w *Window) SetCursor(shape uint32) error {
	if w.CursorShapes == nil || w.Pointer == nil || w.cursor.serial == 0 || shape == w.cursor.shape {
		return nil
	}
	if w.cursor.device == nil {
		device, err := w.CursorShapes.GetPointer(w.Pointer)
		if err != nil {
			return err
		}
		w.cursor.device = device
	}
	if err := w.cursor.device.SetShape(w.cursor.serial, shape); err != nil {
		return err
	}
	w.cursor.shape = shape
	return nil
}

// dropCursorDevice destroys the shape device; call it before its wl_pointer
// is released. The enter serial belongs to that pointer, so it is cleared:
// a replacement pointer sets shapes only after its own enter.
func (w *Window) dropCursorDevice() {
	if w.cursor.device != nil {
		_ = w.cursor.device.Destroy()
	}
	w.cursor = cursorState{}
}
