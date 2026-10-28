module bnema.dev/nefergui/examples

go 1.27

// The examples always build against the NeferGUI code next to them.
replace bnema.dev/nefergui => ../

require (
	bnema.dev/neferclient v0.5.0
	bnema.dev/nefergui v0.0.0-00010101000000-000000000000
)

require (
	bnema.dev/go-wayland-bindings v0.2.0 // indirect
	bnema.dev/purego v0.13.0-bnema.3 // indirect
	bnema.dev/purego-vulkan v0.7.0 // indirect
	bnema.dev/purego-xkbcommon v0.3.0 // indirect
	bnema.dev/wlturbo v0.7.0 // indirect
	github.com/go-text/typesetting v0.3.5 // indirect
	golang.org/x/image v0.47.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.43.0 // indirect
)
