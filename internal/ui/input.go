package ui

// InputKind classifies an InputEvent.
type InputKind uint8

const (
	InputPointerMotion InputKind = iota + 1
	InputPointerPress
	InputPointerRelease
	InputPointerAxis
	InputPointerLeave
	InputKey
	InputFocusIn
	InputFocusOut
	InputReset // platform dropped queued input; treat held state as released
)

// Modifiers is a bit set of keyboard modifiers held during a key event.
type Modifiers uint8

const (
	ModShift Modifiers = 1 << iota
	ModCtrl
	ModAlt
	ModSuper
)
