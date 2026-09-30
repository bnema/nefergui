//go:build linux

// Package layershell contains local typed bindings for wlr-layer-shell v1.
// wlturbo v0.3.0 has no generated layer-shell package, so the bindings are
// generated here by its scanner from the vendored protocol XML.
package layershell

//go:generate go run github.com/bnema/wlturbo/cmd/wlturbo-scanner -p layershell -o wlr-layer-shell-unstable-v1_generated.go -import wl_surface=github.com/bnema/wlturbo/protocol/core -import wl_output=github.com/bnema/wlturbo/protocol/core -import xdg_popup=github.com/bnema/wlturbo/protocol/xdgshell wlr-layer-shell-unstable-v1.xml
