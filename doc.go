// Package nefergui provides a pure-Go immediate-mode GUI for Wayland, rendered with
// Vulkan and styled with a bounded CSS dialect (see docs/css.md).
//
// Run opens a window and calls the view function to build each frame. The view
// receives a Frame; Frame and its Node handles are valid only during that call.
// Key identifies a child among its siblings across frames; without a key,
// position and type determine identity. ID is CSS metadata, not frame identity.
// Options such as Title, Size, Styles and Transparent configure the window.
//
// Builds support CGO_ENABLED=0. At runtime, NeferGUI requires libvulkan.so.1,
// libxkbcommon.so.0 and a Wayland compositor supporting linux-dmabuf and
// linux-drm-syncobj. See docs/runtime.md and docs/troubleshooting.md.
package nefergui
