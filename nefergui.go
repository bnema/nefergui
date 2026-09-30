// Package nefergui provides a pure-Go immediate-mode GUI for Wayland, rendered with
// Vulkan and styled with a bounded CSS dialect (see docs/css.md).
//
// Run opens a window and calls the view function to build each frame. The view
// receives a Frame; Frame and its Node handles are valid only during that call.
// Key identifies a child among its siblings across frames; without a key,
// position and type determine identity. ID is CSS metadata, not frame identity.
// Options such as Title, Size, Styles and Transparent configure the window.
//
// The frame tree, controls, editors and input routing are implemented in
// internal/ui; this package is the public facade and re-exports those types.
//
// Builds support CGO_ENABLED=0. At runtime, NeferGUI requires libvulkan.so.1,
// libxkbcommon.so.0 and a Wayland compositor supporting linux-dmabuf and
// linux-drm-syncobj. See docs/runtime.md and docs/troubleshooting.md.
package nefergui

import (
	"context"

	"github.com/bnema/nefergui/internal/ui"
)

// Frame is valid only during one call to the view function. Root builds the
// application element; Diagnostics reports identity problems in debug builds.
type Frame = ui.Frame

// Node is an ephemeral handle into the current frame. Do not retain it between
// frames. Container methods (Box, Row, Column, Stack, Scroll, Header, Nav,
// Main, Section, Aside, Footer, Element) return child nodes; control methods
// (Button, Checkbox, Radio, Slider, Input, Textarea, Text, Heading, Image,
// Icon, Separator, Spacer) declare controls. Interactive controls return event
// snapshots.
type Node = ui.Node

// Control event values are immutable snapshots; querying them never consumes events.
type (
	ButtonEvent = ui.ButtonEvent
	EditEvent   = ui.EditEvent
	ChangeEvent = ui.ChangeEvent
)

// Option families are sealed: ContainerOption, ButtonOption, EditOption,
// ValueOption and HeadingOption accept the common CSS options (Key, ID,
// Class, Inline) plus the control-specific options listed on each function.
type (
	ContainerOption = ui.ContainerOption
	ButtonOption    = ui.ButtonOption
	EditOption      = ui.EditOption
	ValueOption     = ui.ValueOption
	HeadingOption   = ui.HeadingOption
	WindowOption    = ui.WindowOption
)

// commonOption belongs to every option family; disabledOption to every
// interactive control family. Both are concrete internal types, aliased so
// the constructors below can return them without a second implementation.
type (
	commonOption   = ui.CommonOption
	disabledOption = ui.DisabledOption
)

// Key identifies an element among its siblings across frames.
func Key(key string) commonOption { return ui.Key(key) }

// ID sets the CSS ID; it does not affect frame identity.
func ID(id string) commonOption { return ui.ID(id) }

// Class adds a CSS class.
func Class(class string) commonOption { return ui.Class(class) }

// Inline applies inline CSS declarations.
func Inline(src string) commonOption { return ui.Inline(src) }

// Disabled blocks focus and interaction on an interactive control.
func Disabled(v bool) disabledOption { return ui.Disabled(v) }

// Level sets the accessibility heading level (1 through 6).
func Level(level int) HeadingOption { return ui.Level(level) }

// Placeholder sets the text shown in an empty editor.
func Placeholder(v string) EditOption { return ui.Placeholder(v) }

// Password masks displayed editor text and blocks copy and cut.
func Password(v bool) EditOption { return ui.Password(v) }

// Title sets the window title.
func Title(s string) WindowOption { return ui.Title(s) }

// Size sets the initial logical window size.
func Size(w, h int) WindowOption { return ui.Size(w, h) }

// Styles loads an author stylesheet from a file path.
func Styles(s string) WindowOption { return ui.Styles(s) }

// Transparent requests an alpha-capable Wayland surface (opaque by default).
func Transparent() WindowOption { return ui.Transparent() }

// Run builds and presents an immediate view on the Wayland session owner loop.
func Run[T any](ctx context.Context, model *T, view func(*Frame, *T), options ...WindowOption) error {
	return ui.Run(ctx, model, view, options...)
}

// RunFrames is a deterministic harness entry: each commit requests a redraw
// and the loop stops after the given number of committed frames.
func RunFrames[T any](ctx context.Context, frames int, model *T, view func(*Frame, *T), committed func(uint64) error, options ...WindowOption) error {
	return ui.RunFrames(ctx, frames, model, view, committed, options...)
}
