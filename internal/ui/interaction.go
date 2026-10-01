package ui

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/bnema/nefergui/internal/css"
	"github.com/bnema/nefergui/internal/edit"
	"github.com/bnema/nefergui/internal/layout"
	"github.com/bnema/nefergui/internal/text"
)

// platformInput is the UI-loop port. Coordinates and viewport dimensions are
// logical pixels; the platform converts physical pointer coordinates once.
type platformInput struct {
	Kind                 string // motion, press, release, axis, leave, key, focus-in, focus-out, resize
	X, Y, DX, DY         float64
	Width, Height, Scale float64
	Key                  keyEvent
	Button               uint32 // evdev button code (BTN_LEFT is primary)
	Shift, Ctrl          bool
	IME                  edit.IMEBatch
	Time                 time.Time // optional event timestamp; zero uses receipt time
}

// keyEvent is the key part of a platformInput.
type keyEvent struct {
	Name, Text      string
	Pressed, Repeat bool
	Shift, Ctrl     bool
}

const pointerPrimary uint32 = 0x110 // Linux BTN_LEFT

type interactionState struct {
	hover, capture, focus, space *identity
	button                       uint32
	down, visible, inside        bool
	x, y                         float64
	lastTextPress                time.Time
	lastTextX, lastTextY         float64
	lastTextTarget               *identity
	scroll                       map[string]layout.Size
}

func (s interactionState) flags(e *element) css.StateFlags {
	var flags css.StateFlags
	if e.identity.same(s.hover) {
		flags |= css.Hover
	}
	if (e.identity.same(s.capture) && s.down && s.button == pointerPrimary) || (s.space != nil && e.identity.same(s.space)) {
		flags |= css.Active
	}
	if e.identity.same(s.focus) {
		flags |= css.Focus
		if s.visible {
			flags |= css.FocusVisible
		}
	}
	if e.disabled {
		flags |= css.Disabled
	}
	if e.checked {
		flags |= css.Checked
	}
	return flags
}

// idKey is the stable string form of an identity path, computed once when
// the identity is built so identities stay immutable and safe to share.
func idKey(i *identity) string {
	if i == nil {
		return ""
	}
	if i.path == "" {
		return identityPath(i)
	}
	return i.path
}
func identityPath(i *identity) string {
	switch {
	case i.parent == nil:
		return "root"
	case i.explicit:
		return idKey(i.parent) + "/k" + strconv.Itoa(len(i.key)) + ":" + i.key
	default:
		return idKey(i.parent) + "/p" + strconv.Itoa(i.position) + ":" + strconv.Itoa(len(i.typ)) + ":" + i.typ
	}
}
func layoutKind(e *element) layout.Kind {
	switch e.typ {
	case "row":
		return layout.Row
	case "column":
		return layout.Column
	case "stack":
		return layout.Stack
	case "scroll":
		return layout.Scroll
	// Labelled controls measure and paint their label like text.
	case "text", "heading", "input", "textarea", "button", "checkbox", "radio", "slider":
		return layout.Text
	case "image":
		return layout.Image
	default:
		return layout.Box
	}
}

// layoutTree fills the layout node embedded in each element. layout.Layout
// does not retain its input, so the nodes are rebuilt in place every call.
func (r *runtime) layoutTree(e *element) *layout.Node {
	n := &e.ln
	*n = layout.Node{ID: idKey(e.identity), Kind: layoutKind(e), Content: e.text, ImageSize: e.imageSize, Image: e.image, NoWrap: e.typ == "input", Children: n.Children[:0]}
	if e.computed != nil {
		n.Style = e.computed.Style
	}
	n.Rect, n.HasRect = e.rect, e.hasRect
	if offset, ok := r.state.scroll[n.ID]; ok {
		n.ScrollX, n.ScrollY = offset.W, offset.H
	}
	if e.typ == "input" || e.typ == "textarea" {
		// Editor clips are intrinsic even if the authored CSS leaves overflow visible.
		n.EditorScroll = true
	}
	for _, child := range e.children {
		n.Children = append(n.Children, r.layoutTree(child))
	}
	return n
}
func (r *runtime) lookup(id string) *element {
	var walk func(*element) *element
	walk = func(e *element) *element {
		if e == nil {
			return nil
		}
		if idKey(e.identity) == id {
			return e
		}
		for _, child := range e.children {
			if found := walk(child); found != nil {
				return found
			}
		}
		return nil
	}
	return walk(r.committed)
}
func (r *runtime) hit(x, y float64) *element { return r.lookup(layout.Hit(r.output.Tree, x, y)) }

// cursor is the CSS cursor of the element under the pointer, from the last
// committed styles. While a primary button is captured, the capturing element
// keeps its cursor even if the pointer leaves it (as when dragging a slider).
// auto and unknown values map to default.
func (r *runtime) cursor() css.Keyword {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := r.state.hover
	if r.state.down && r.state.capture != nil {
		id = r.state.capture
	}
	if !r.state.inside || id == nil {
		return css.KeywordDefault
	}
	if e := r.lookup(idKey(id)); e != nil && e.computed != nil {
		return e.computed.Style.Cursor
	}
	return css.KeywordDefault
}
func focusable(e *element) bool {
	if e == nil || e.disabled {
		return false
	}
	switch e.typ {
	case "button", "checkbox", "input", "radio", "slider", "textarea":
		return true
	}
	return false
}
func (r *runtime) focusOrder() []*element {
	var result []*element
	var walk func(*layout.Result)
	walk = func(n *layout.Result) {
		if n == nil {
			return
		}
		e := r.lookup(n.ID)
		if focusable(e) {
			result = append(result, e)
		}
		for _, child := range n.Children {
			walk(child)
		}
	}
	walk(r.output.Tree)
	return result
}
func (r *runtime) setFocus(e *element, visible bool) bool {
	var id *identity
	if focusable(e) {
		id = e.identity
	}
	if r.state.focus.same(id) && (id == nil || r.state.visible == visible) {
		return false
	}
	if !r.state.focus.same(id) {
		r.state.space = nil // changing focus cancels a held Space
	}
	r.state.focus, r.state.visible = id, id != nil && visible
	return true
}
func (r *runtime) tab(reverse bool) bool {
	order := r.focusOrder()
	if len(order) == 0 {
		return r.setFocus(nil, false)
	}
	idx := -1
	for i, e := range order {
		if e.identity.same(r.state.focus) {
			idx = i
			break
		}
	}
	if reverse {
		if idx < 0 {
			idx = 0
		}
		idx = (idx - 1 + len(order)) % len(order)
	} else {
		idx = (idx + 1) % len(order)
	}
	return r.setFocus(order[idx], true)
}
func scrollable(e *element, n *layout.Result) bool {
	if e == nil || n == nil || !n.Clipped || e.computed == nil {
		return false
	}
	st := e.computed.Style
	return e.typ == "scroll" || st.OverflowX == css.KeywordScroll || st.OverflowX == css.KeywordAuto || st.OverflowY == css.KeywordScroll || st.OverflowY == css.KeywordAuto
}

// physicalToLogical is used only at the platform boundary, never for layout or Hit.
func physicalToLogical(x, y, scale float64) (float64, float64, bool) {
	if !validPoint(x, y) || !validPoint(scale, 0) || scale <= 0 {
		return 0, 0, false
	}
	return x / scale, y / scale, true
}
func validPoint(x, y float64) bool {
	return !math.IsNaN(x) && !math.IsNaN(y) && !math.IsInf(x, 0) && !math.IsInf(y, 0)
}
func (r *runtime) route(in platformInput) {
	r.mu.Lock()
	changed := false
	s := &r.state
	queue := func(e *element, kind string) {
		if e != nil {
			r.pending = append(r.pending, inputEvent{Target: e.identity, Kind: kind})
			changed = true
		}
	}
	switch in.Kind {
	case "resize":
		if validPoint(in.Width, in.Height) && in.Width >= 0 && in.Height >= 0 && (r.width != in.Width || r.height != in.Height) {
			r.width, r.height = in.Width, in.Height
			changed = true
		}
		if in.Scale > 0 && !math.IsInf(in.Scale, 0) && !math.IsNaN(in.Scale) && r.scale != in.Scale {
			r.scale = in.Scale
			changed = true
		}
	case "focus-out":
		if s.inside || s.hover != nil || s.down || s.capture != nil || s.space != nil {
			s.inside = false
			s.hover = nil
			s.capture = nil
			s.down = false
			s.space = nil
			changed = true
		}
		if r.setFocus(nil, false) {
			changed = true
		}
	case "focus-in": // keyboard focus returns only after an explicit traversal/click
	case "ime-done":
		if e := r.lookup(idKey(s.focus)); focusable(e) && (e.typ == "input" || e.typ == "textarea") {
			r.pending = append(r.pending, inputEvent{Target: e.identity, Kind: "ime-done", IME: in.IME})
			changed = true
		}
	case "leave":
		// A pointer leave is not a button release. Keep capture for the
		// matching release, but do not hit-test its stale coordinates.
		if s.inside || s.hover != nil {
			s.inside = false
			s.hover = nil
			changed = true
		}
	case "motion", "press", "release", "axis":
		if !validPoint(in.X, in.Y) {
			break
		}
		// A release after leave must not turn stale in-surface coordinates
		// into a new hover or an activation.
		if in.Kind != "release" || s.inside {
			s.x, s.y = in.X, in.Y
			s.inside = true
		}
		var target *element
		if s.inside {
			target = r.hit(in.X, in.Y)
		}
		var hover *identity
		if target != nil {
			hover = target.identity
		}
		if !s.hover.same(hover) {
			s.hover = hover
			changed = true
		}
		if s.down && s.button == pointerPrimary && in.Kind == "motion" {
			if captured := r.lookup(idKey(s.capture)); focusable(captured) && (captured.typ == "input" || captured.typ == "textarea") {
				r.pending = append(r.pending, inputEvent{Target: captured.identity, Kind: "edit-pointer", X: in.X, Y: in.Y, Shift: true})
				changed = true
			}
			if captured := r.lookup(idKey(s.capture)); focusable(captured) && captured.typ == "slider" {
				r.pending = append(r.pending, inputEvent{Target: captured.identity, Kind: "slider-pointer", X: in.X, Track: resultByID(r.output.Tree, idKey(captured.identity)).Content})
				changed = true
			}
		}
		switch in.Kind {
		case "press":
			if !s.down {
				if in.Button != pointerPrimary || target == nil || (target.typ != "input" && target.typ != "textarea") {
					s.lastTextPress = time.Time{}
				}
				s.down = true
				s.button = in.Button
				changed = true
				if target != nil {
					s.capture = target.identity
				}
				if in.Button == pointerPrimary && r.setFocus(target, false) {
					changed = true
				}
				if in.Button == pointerPrimary && focusable(target) && (target.typ == "input" || target.typ == "textarea") {
					at := in.Time
					if at.IsZero() {
						at = time.Now()
					}
					double := target.identity.same(s.lastTextTarget) && !s.lastTextPress.IsZero() && at.Sub(s.lastTextPress) >= 0 && at.Sub(s.lastTextPress) <= 500*time.Millisecond && math.Hypot(in.X-s.lastTextX, in.Y-s.lastTextY) <= 4
					kind := "edit-pointer"
					if double {
						kind = "edit-word"
					}
					r.pending = append(r.pending, inputEvent{Target: target.identity, Kind: kind, X: in.X, Y: in.Y, Shift: in.Shift})
					s.lastTextPress, s.lastTextX, s.lastTextY, s.lastTextTarget = at, in.X, in.Y, target.identity
					changed = true
				}
				if in.Button == pointerPrimary && focusable(target) && target.typ == "slider" {
					r.pending = append(r.pending, inputEvent{Target: target.identity, Kind: "slider-pointer", X: in.X, Track: resultByID(r.output.Tree, idKey(target.identity)).Content})
					changed = true
				}
			}
		case "release":
			if s.down && in.Button == s.button {
				if s.button == pointerPrimary {
					if captured := r.lookup(idKey(s.capture)); focusable(captured) && captured.typ == "slider" && s.inside {
						r.pending = append(r.pending, inputEvent{Target: captured.identity, Kind: "slider-pointer", X: in.X, Track: resultByID(r.output.Tree, idKey(captured.identity)).Content})
						changed = true
					}
					if captured := r.lookup(idKey(s.capture)); captured != nil && captured == target && resultByID(r.output.Tree, idKey(s.capture)) != nil && focusable(captured) {
						if captured.typ == "button" || captured.typ == "checkbox" || captured.typ == "radio" {
							queue(captured, "activate")
						}
					}
				}
				s.down = false
				s.capture = nil
				changed = true
			}
		case "axis":
			if !validPoint(in.DX, in.DY) {
				break
			}
			for e := target; e != nil; e = e.parent {
				n := resultByID(r.output.Tree, idKey(e.identity))
				if !scrollable(e, n) {
					continue
				}
				old := s.scroll[n.ID]
				next := layout.Size{W: math.Max(0, math.Min(n.ContentSize.W-n.Content.W, old.W+in.DX)), H: math.Max(0, math.Min(n.ContentSize.H-n.Content.H, old.H+in.DY))}
				if next != old {
					if s.scroll == nil {
						s.scroll = make(map[string]layout.Size)
					}
					s.scroll[n.ID] = next
					changed = true
				}
				break
			}
		}
	case "key":
		name := strings.ToLower(in.Key.Name)
		if name == "space" && !in.Key.Pressed {
			if s.space != nil {
				if e := r.lookup(idKey(s.space)); e != nil && e.identity.same(s.focus) && focusable(e) && resultByID(r.output.Tree, idKey(s.space)) != nil {
					queue(e, "activate")
				}
				s.space = nil
				changed = true
			}
			break
		}
		if !in.Key.Pressed {
			break
		}
		if e := r.lookup(idKey(s.focus)); focusable(e) && (e.typ == "input" || e.typ == "textarea" || e.typ == "slider") {
			if name == "tab" || name == "iso_left_tab" {
				if !in.Key.Repeat && r.tab(in.Shift || name == "iso_left_tab") {
					changed = true
				}
				break
			}
			if e.typ == "slider" {
				switch name {
				case "left", "right", "up", "down", "home", "end", "page_up", "page_down":
					queue(e, "slider-key")
					r.pending[len(r.pending)-1].Text = name
				}
			} else {
				switch name {
				case "left", "right", "up", "down", "home", "end", "backspace", "delete":
					r.pending = append(r.pending, inputEvent{Target: e.identity, Kind: "edit-key", Text: name, Ctrl: in.Ctrl, Shift: in.Shift})
					changed = true
				case "a", "c", "x", "v":
					if in.Ctrl && !in.Key.Repeat {
						r.pending = append(r.pending, inputEvent{Target: e.identity, Kind: "edit-key", Text: name, Ctrl: true, Shift: in.Shift})
						changed = true
					} else if !in.Ctrl && in.Key.Text != "" && !r.composing(e.identity) {
						r.pending = append(r.pending, inputEvent{Target: e.identity, Kind: "text", Text: in.Key.Text})
						changed = true
					}
				case "return", "kp_enter":
					if e.typ == "textarea" {
						if !r.composing(e.identity) {
							r.pending = append(r.pending, inputEvent{Target: e.identity, Kind: "text", Text: "\n"})
							changed = true
						}
					} else {
						queue(e, "submit")
					}
				default:
					if in.Key.Text != "" && !in.Ctrl && !r.composing(e.identity) {
						r.pending = append(r.pending, inputEvent{Target: e.identity, Kind: "text", Text: in.Key.Text})
						changed = true
					}
				}
			}
			break
		}
		if in.Key.Repeat {
			break
		}
		switch name {
		case "escape":
			if s.space != nil {
				s.space = nil
				changed = true
			}
		case "tab", "iso_left_tab":
			if r.tab(in.Shift || name == "iso_left_tab") {
				changed = true
			}
		case "return", "kp_enter":
			if e := r.lookup(idKey(s.focus)); focusable(e) && resultByID(r.output.Tree, idKey(s.focus)) != nil && (e.typ == "button" || e.typ == "checkbox" || e.typ == "radio") {
				queue(e, "activate")
			}
		case "space":
			if e := r.lookup(idKey(s.focus)); s.space == nil && focusable(e) && resultByID(r.output.Tree, idKey(s.focus)) != nil && (e.typ == "button" || e.typ == "checkbox" || e.typ == "radio") {
				s.space = e.identity
				changed = true
			}
		default:
		}
	}
	if changed {
		r.redraw = true
	}
	r.mu.Unlock()
	if changed {
		select {
		case r.wake <- struct{}{}:
		default:
		}
	}
}
func resultByID(n *layout.Result, id string) *layout.Result {
	if n == nil {
		return nil
	}
	if n.ID == id {
		return n
	}
	for _, child := range n.Children {
		if found := resultByID(child, id); found != nil {
			return found
		}
	}
	return nil
}

// viewport and text engine are UI-loop inputs, never physical layout units.
func (r *runtime) setTextEngine(engine *text.Engine) {
	r.mu.Lock()
	r.textEngine = engine
	r.mu.Unlock()
	r.Redraw()
}

// composing reports an input-method composition in progress on the editor.
// Keys reaching wl_keyboard were not grabbed by the input method and are typed,
// except while a preedit is shown: a non-grabbing input method owns that text
// until it commits, so plain keys would interleave with the composition.
func (r *runtime) composing(id *identity) bool {
	editor := r.edits[idKey(id)]
	return editor != nil && editor.IMEActive() && editor.Preedit != ""
}
