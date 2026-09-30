package nefergui

import "github.com/bnema/nefergui/internal/css"

// ContainerOption, ButtonOption and EditOption are separate option families.
type ContainerOption interface{ container(*element) }
type ButtonOption interface{ button(*element) }
type EditOption interface{ edit(*element) }

// ValueOption accepts CSS options and Disabled for pointer-backed controls.
type ValueOption interface{ valueOption(*element) }
type commonOption struct{ apply func(*element) }

func (o commonOption) container(e *element)   { o.apply(e) }
func (o commonOption) button(e *element)      { o.apply(e) }
func (o commonOption) edit(e *element)        { o.apply(e) }
func (o commonOption) valueOption(e *element) { o.apply(e) }
func Key(key string) commonOption {
	return commonOption{func(e *element) { e.identity = &identity{key: key} }}
}
func ID(id string) commonOption { return commonOption{func(e *element) { e.id = id }} }
func Class(class string) commonOption {
	return commonOption{func(e *element) { e.classes = append(e.classes, class) }}
}
func Inline(src string) commonOption {
	return commonOption{func(e *element) { e.inline, _ = css.ParseInline(src) }}
}
func applyContainer(e *element, opts []ContainerOption) {
	for _, o := range opts {
		o.container(e)
	}
}

// disabledOption applies to every interactive control (buttons, value controls
// and editors) but not to containers or headings.
type disabledOption struct{ disabled bool }

func (o disabledOption) button(e *element)      { e.disabled = o.disabled }
func (o disabledOption) edit(e *element)        { e.disabled = o.disabled }
func (o disabledOption) valueOption(e *element) { e.disabled = o.disabled }

// Disabled blocks focus and interaction on an interactive control.
func Disabled(v bool) disabledOption { return disabledOption{v} }

// HeadingOption accepts common CSS options and Level, but not control options.
type HeadingOption interface{ heading(*element) }

func (o commonOption) heading(e *element) { o.apply(e) }

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
