// Package ui owns the immediate-mode frame tree, controls, editors, input
// routing and the Renderer behind the public nefergui facade.
package ui

import (
	"fmt"
	"image"

	"github.com/bnema/nefergui/internal/css"
	"github.com/bnema/nefergui/internal/edit"
	"github.com/bnema/nefergui/internal/layout"
)

// Identity is a path of sibling-local keys or positional element identities.
// It is opaque to applications; ID is CSS metadata, not identity.
type identity struct {
	parent   *identity
	key      string
	position int
	typ      string
	explicit bool
	path     string // idKey result, set when the identity is built
}

func (i *identity) same(j *identity) bool {
	if i == nil || j == nil {
		return i == j
	}
	return i.key == j.key && i.position == j.position && i.typ == j.typ && i.explicit == j.explicit && i.parent.same(j.parent)
}

// Frame is valid only during one call to the view function.
type Frame struct {
	generation    uint64
	active        bool
	root          *element
	previous      *element
	events        []inputEvent
	styles        *css.Engine
	edits         map[string]*edit.State
	clipboard     edit.Clipboard
	owner         *runtime
	ime           edit.IME
	state         interactionState
	layout        layout.Output
	diagnostics   []string
	width, height float64
}

// Node is an ephemeral handle into the current frame. Do not retain it between frames.
type Node struct {
	frame      *Frame
	element    *element
	generation uint64
}
type element struct {
	identity      *identity
	typ, role, id string
	classes       []string
	inline        *css.Declarations
	computed      *css.Computed
	parent        *element
	children      []*element
	usedKeys      map[string]bool
	events        []inputEvent
	disabled      bool
	checked       bool
	fraction      float64 // slider value position in [0,1]; negative when unknown
	placeholder   string
	text          string
	password      bool
	imageSize     layout.Size
	image         image.Image
	level         int
	rect          layout.Rect // absolute geometry, see Node.Rect
	hasRect       bool
}

func (n Node) valid() bool {
	return n.frame != nil && n.frame.active && n.generation == n.frame.generation && n.element != nil
}
func (n Node) check() bool {
	if n.valid() {
		return true
	}
	if debugDiagnostics {
		panic("nefergui: stale Node")
	}
	return false
}
func (f *Frame) Root(options ...ContainerOption) Node {
	if !f.active {
		panic("nefergui: Root outside frame")
	}
	if f.root != nil {
		panic("nefergui: Root called twice")
	}
	root := &element{typ: "app", identity: &identity{typ: "app", path: "root"}, usedKeys: make(map[string]bool)}
	f.root = root
	n := Node{f, root, f.generation}
	applyContainer(root, options)
	f.compute(root)
	f.deliver(root)
	return n
}
func (f *Frame) compute(e *element) {
	if f.styles != nil {
		var p *css.Computed
		if e.parent != nil {
			p = e.parent.computed
		}
		e.computed = f.styles.Compute(&css.Element{Type: e.typ, ID: e.id, Classes: e.classes, Inline: e.inline, State: f.state.flags(e)}, p)
	}
}
func (f *Frame) deliver(e *element) {
	for _, ev := range f.events {
		if ev.Target.same(e.identity) {
			e.events = append(e.events, ev)
		}
	}
}
func (n Node) child(typ, role string, options []ContainerOption) Node {
	if !n.check() {
		return Node{}
	}
	e := &element{typ: typ, role: role, parent: n.element, usedKeys: make(map[string]bool)}
	applyContainer(e, options)
	i := &identity{parent: n.element.identity, typ: typ, position: len(n.element.children)}
	if e.identity != nil {
		i.explicit = true
		i.key = e.identity.key
		i.position = 0
		i.typ = ""
		if n.element.usedKeys[i.key] && debugDiagnostics {
			n.frame.diagnostics = append(n.frame.diagnostics, fmt.Sprintf("duplicate sibling key %q", i.key))
		}
		n.element.usedKeys[i.key] = true
	}
	i.path = identityPath(i)
	e.identity = i
	if debugDiagnostics && !i.explicit && n.frame.previous != nil {
		if prior := findIdentity(n.frame.previous, n.element.identity); prior != nil && i.position < len(prior.children) {
			old := prior.children[i.position]
			if !old.identity.explicit && old.typ != typ {
				n.frame.diagnostics = append(n.frame.diagnostics, fmt.Sprintf("unkeyed child changed type at position %d: %s -> %s", i.position, old.typ, typ))
			}
		}
	}
	n.element.children = append(n.element.children, e)
	n.frame.compute(e)
	n.frame.deliver(e)
	return Node{n.frame, e, n.generation}
}
func findIdentity(e *element, id *identity) *element {
	if e == nil {
		return nil
	}
	if e.identity.same(id) {
		return e
	}
	for _, child := range e.children {
		if found := findIdentity(child, id); found != nil {
			return found
		}
	}
	return nil
}
func (n Node) Element(typ string, options ...ContainerOption) Node { return n.child(typ, "", options) }
func (n Node) Box(options ...ContainerOption) Node                 { return n.child("box", "", options) }
func (n Node) Row(options ...ContainerOption) Node                 { return n.child("row", "", options) }
func (n Node) Column(options ...ContainerOption) Node              { return n.child("column", "", options) }
func (n Node) Stack(options ...ContainerOption) Node               { return n.child("stack", "", options) }
func (n Node) Scroll(options ...ContainerOption) Node              { return n.child("scroll", "", options) }
func (n Node) Header(options ...ContainerOption) Node              { return n.child("header", "banner", options) }
func (n Node) Nav(options ...ContainerOption) Node                 { return n.child("nav", "navigation", options) }
func (n Node) Main(options ...ContainerOption) Node                { return n.child("main", "main", options) }
func (n Node) Section(options ...ContainerOption) Node             { return n.child("section", "region", options) }
func (n Node) Aside(options ...ContainerOption) Node {
	return n.child("aside", "complementary", options)
}
func (n Node) Footer(options ...ContainerOption) Node {
	return n.child("footer", "contentinfo", options)
}

// Diagnostics returns a copy of frame diagnostics (populated with -tags nefergui_debug).
func (f *Frame) Diagnostics() []string { return append([]string(nil), f.diagnostics...) }

// Size is the logical surface size this frame is laid out against.
func (f *Frame) Size() (width, height float64) { return f.width, f.height }

// Rect positions the node at x, y with size w, h in logical pixels, relative
// to the content box of its Stack parent, without CSS parsing or per-frame
// strings or style copies. The geometry is border-box and overrides authored
// width, height and margin. Rect is valid only on a direct child of Stack;
// elsewhere it is ignored, keeping normal flex and block layout, and debug
// builds (-tags nefergui_debug) report it through Frame.Diagnostics.
func (n Node) Rect(x, y, w, h float64) Node {
	if !n.check() {
		return n
	}
	if p := n.element.parent; p == nil || p.typ != "stack" {
		if debugDiagnostics {
			n.frame.diagnostics = append(n.frame.diagnostics, "Rect used on a node whose parent is not a Stack")
		}
		return n
	}
	n.element.rect, n.element.hasRect = layout.Rect{X: x, Y: y, W: w, H: h}, true
	return n
}
