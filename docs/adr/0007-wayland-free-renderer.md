# ADR 0007 — Wayland-free Renderer

Status: accepted. Supersedes ADR 0002 (Wayland bindings and FD ownership) and ADR 0004 (keyboard).

## Decision

NeferGUI is a rendering library. It builds views, lays them out, styles them with CSS and draws them with Vulkan into DMA-BUF buffers. It does not connect to Wayland, import a Wayland library, link xkbcommon, or own a window, an event loop or a goroutine.

The public entry point is `Renderer` (`NewRenderer`, `Resize`, `Input`, `Invalidate`, `Render`, `Released`, `Wake`, `Pending`, `Close`). The application owns the Wayland client and runs one owner goroutine:

- it gives the compositor's linux-dmabuf `main_device` and format list to `NewRenderer`;
- it forwards size, scale and input as plain `Input` values (keysyms are xkb values; text is UTF-8);
- `Render` fills an `Output`: DMA-BUF planes, syncobj timelines, acquire and release points, damage, cursor shape and input region. All file descriptors stay owned by the Renderer; the application duplicates them before a transport consumes them;
- an eventfd per buffer becomes readable when the compositor releases it; the application then calls `Released`.

Layering is application → Wayland client library → bindings. NeferGUI sits beside the client library, not above it. Applications copy plain fields between the two; neither imports the other. [github.com/bnema/neferclient](https://github.com/bnema/neferclient) is a client toolkit written for this split.

## Consequences

- `Run`, `RunFrames`, `RunLock`, the window and layer-shell options, `OnInput`, `OnResize`, `OnSurface`, `Wake` as an option, `SecretBuffer` and `WaylandSurface` are removed, together with the Wayland platform, keyboard, harness and example code. This is a breaking change.
- The module depends only on purego, purego-vulkan, go-text, x/image and x/sys. `go list -deps` shows no Wayland or xkbcommon module.
- Clipboard and input-method ports (`Clipboard`, `IME`) remain interfaces the application implements.
- `Password(true)` still masks the displayed value of an in-memory editor value.
- ADR 0003 still describes the Vulkan, DMA-BUF and explicit-sync design; the Wayland object handling it mentions now belongs to the application.
