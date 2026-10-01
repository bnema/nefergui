// Package nefergui is a pure-Go immediate-mode GUI renderer styled with a
// bounded CSS dialect (see docs/css.md). It draws with Vulkan into DMA-BUF
// buffers and imports no Wayland library: the application owns the Wayland
// client (for example with github.com/bnema/neferclient) and feeds events to a
// Renderer.
//
// A view function builds each frame from a Frame; Frame and its Node handles
// are valid only during that call. Key identifies a child among its siblings
// across frames; without a key, position and type determine identity. ID is
// CSS metadata, not frame identity.
//
// The frame tree, controls, editors and input routing are implemented in
// internal/ui; this package is the public facade and re-exports those types.
//
// Builds support CGO_ENABLED=0. At runtime NeferGUI requires libvulkan.so.1 and
// a GPU with DMA-BUF export and DRM syncobj timelines. See docs/runtime.md and
// docs/troubleshooting.md.
package nefergui

import "github.com/bnema/nefergui/internal/ui"

// Frame is valid only during one call to the view function. Root builds the
// application element; Diagnostics reports identity problems in debug builds.
// Alias methods are listed by go doc github.com/bnema/nefergui/internal/ui.Frame.
type Frame = ui.Frame

// Node is an ephemeral handle into the current frame. Do not retain it between
// frames. Container methods (Box, Row, Column, Stack, Scroll, Header, Nav,
// Main, Section, Aside, Footer, Element) return child nodes; control methods
// (Button, Checkbox, Radio, Slider, Input, Textarea, Text, Heading, Image,
// Icon, Separator, Spacer) declare controls. Interactive controls return event
// snapshots.
// Alias methods are listed by go doc github.com/bnema/nefergui/internal/ui.Node.
type Node = ui.Node

// ButtonEvent is an immutable snapshot queried with Activated.
// Alias methods are listed by go doc github.com/bnema/nefergui/internal/ui.ButtonEvent.
type ButtonEvent = ui.ButtonEvent

// EditEvent is an immutable snapshot queried with Changed and Submitted.
// Alias methods are listed by go doc github.com/bnema/nefergui/internal/ui.EditEvent.
type EditEvent = ui.EditEvent

// ChangeEvent is an immutable snapshot queried with Changed.
// Alias methods are listed by go doc github.com/bnema/nefergui/internal/ui.ChangeEvent.
type ChangeEvent = ui.ChangeEvent

// Option families are sealed: ContainerOption, ButtonOption, EditOption,
// ValueOption and HeadingOption accept the common CSS options (Key, ID,
// Class, Inline) plus the control-specific options listed on each function.
type (
	ContainerOption = ui.ContainerOption
	ButtonOption    = ui.ButtonOption
	EditOption      = ui.EditOption
	ValueOption     = ui.ValueOption
	HeadingOption   = ui.HeadingOption
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

// Rect is an integer logical-pixel rectangle in surface coordinates, used by
// Frame.SetInputRects and Output.InputRects.
type Rect = ui.Rect

// InputKind classifies an Input; Modifiers is a bit set of held modifiers.
type (
	InputKind = ui.InputKind
	Modifiers = ui.Modifiers
)

const (
	InputPointerMotion  = ui.InputPointerMotion
	InputPointerPress   = ui.InputPointerPress
	InputPointerRelease = ui.InputPointerRelease
	InputPointerAxis    = ui.InputPointerAxis
	InputPointerLeave   = ui.InputPointerLeave
	InputKey            = ui.InputKey
	InputFocusIn        = ui.InputFocusIn
	InputFocusOut       = ui.InputFocusOut
	InputReset          = ui.InputReset

	ModShift = ui.ModShift
	ModCtrl  = ui.ModCtrl
)
