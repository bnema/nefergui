package ui

import "github.com/bnema/nefergui/internal/css"

// ContainerOption, ButtonOption and EditOption are separate option families.
type ContainerOption interface{ container(*element) }
type ButtonOption interface{ button(*element) }
type EditOption interface{ edit(*element) }

// ValueOption accepts CSS options and Disabled for pointer-backed controls.
type ValueOption interface{ valueOption(*element) }

// CommonOption is the concrete type of Key, ID, Class and Inline; it belongs
// to every option family. It is exported only so the root facade can return it.
// It is plain data rather than a closure, so creating one never allocates.
type CommonOption struct {
	kind commonKind
	s    string
}

type commonKind uint8

const (
	optKey commonKind = iota
	optID
	optClass
	optInline
)

func (o CommonOption) apply(e *element) {
	switch o.kind {
	case optKey:
		e.key, e.hasKey = o.s, true
	case optID:
		e.id = o.s
	case optClass:
		e.classes = append(e.classes, o.s)
	case optInline:
		e.inline, _ = css.ParseInline(o.s)
	}
}
func (o CommonOption) container(e *element)   { o.apply(e) }
func (o CommonOption) button(e *element)      { o.apply(e) }
func (o CommonOption) edit(e *element)        { o.apply(e) }
func (o CommonOption) valueOption(e *element) { o.apply(e) }
func Key(key string) CommonOption             { return CommonOption{optKey, key} }
func ID(id string) CommonOption               { return CommonOption{optID, id} }
func Class(class string) CommonOption         { return CommonOption{optClass, class} }
func Inline(src string) CommonOption          { return CommonOption{optInline, src} }
func applyContainer(e *element, opts []ContainerOption) {
	for _, o := range opts {
		o.container(e)
	}
}

// DisabledOption is the concrete type of Disabled. It applies to every
// interactive control (buttons, value controls and editors) but not to
// containers or headings. Exported only so the root facade can return it.
type DisabledOption struct{ disabled bool }

func (o DisabledOption) button(e *element)      { e.disabled = o.disabled }
func (o DisabledOption) edit(e *element)        { e.disabled = o.disabled }
func (o DisabledOption) valueOption(e *element) { e.disabled = o.disabled }

// Disabled blocks focus and interaction on an interactive control.
func Disabled(v bool) DisabledOption { return DisabledOption{v} }

// HeadingOption accepts common CSS options and Level, but not control options.
type HeadingOption interface{ heading(*element) }

func (o CommonOption) heading(e *element) { o.apply(e) }

type headingLevel int

func (level headingLevel) heading(e *element) { e.level = int(level) }

// Level sets the accessibility heading level (1 through 6).
func Level(level int) HeadingOption {
	if level < 1 || level > 6 {
		panic("nefergui: heading level must be between 1 and 6")
	}
	return headingLevel(level)
}

type editOnly struct{ placeholder string }

func (o editOnly) edit(e *element)    { e.placeholder = o.placeholder }
func Placeholder(v string) EditOption { return editOnly{v} }

type passwordOption bool

func (o passwordOption) edit(e *element) { e.password = bool(o) }
func Password(v bool) EditOption         { return passwordOption(v) }
