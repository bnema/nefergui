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

// Modifiers is a bit set of keyboard modifiers held during a key or pointer
// event. ModShift, ModCtrl, ModAlt and ModSuper report held keys; ModCapsLock
// and ModNumLock report the latched lock state. The Renderer reads only
// ModShift and ModCtrl, so lock bits never change shortcuts or text entry.
// The Renderer does not expose modifiers to views: an application that shows a
// lock indicator records the lock bits of Input.Modifiers in its own model.
type Modifiers uint8

const (
	ModShift Modifiers = 1 << iota
	ModCtrl
	ModAlt
	ModSuper
	ModCapsLock
	ModNumLock
)
