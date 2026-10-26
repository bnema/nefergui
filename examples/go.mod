module bnema.dev/nefergui/examples

go 1.27

// The examples always build against the NeferGUI code next to them.
replace bnema.dev/nefergui => ../

require (
	bnema.dev/neferclient v0.2.0
	bnema.dev/nefergui v0.0.0-00010101000000-000000000000
)

require (
	bnema.dev/go-wayland-bindings v0.1.0 // indirect
	bnema.dev/purego v0.13.0-bnema.1 // indirect
	bnema.dev/purego-vulkan v0.6.0 // indirect
	bnema.dev/purego-xkbcommon v0.2.0 // indirect
	bnema.dev/wlturbo v0.6.2 // indirect
	github.com/go-text/typesetting v0.3.5 // indirect
	golang.org/x/image v0.46.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
)
