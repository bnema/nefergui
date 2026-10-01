# ADR 0007 — Wayland-free Renderer

Status: accepted

## Decision

NeferGUI is a rendering library. It builds views, lays them out, styles them with CSS and draws them with Vulkan into DMA-BUF buffers. The library does not own a Wayland connection.

The public entry point is `Renderer` (`NewRenderer`, `Resize`, `Input`, `Invalidate`, `Render`, `Released`, `Wake`, `Pending`, `Close`). The application owns the Wayland client and runs one owner goroutine:

- it gives the compositor's linux-dmabuf `main_device` and format list to `NewRenderer`;
- it forwards size, scale and input as plain `Input` values (keysyms are xkb values; text is UTF-8);
- `Render` fills an `Output`: DMA-BUF planes, syncobj timelines, acquire and release points, damage, cursor shape and input region. All file descriptors stay owned by the Renderer; the application duplicates them before a transport consumes them;
- an eventfd per buffer becomes readable when the compositor releases it; the application then calls `Released`.

Layering is application → Wayland client library → bindings. NeferGUI sits beside the client library, not above it. Applications copy plain fields between the two; neither imports the other. [github.com/bnema/neferclient](https://github.com/bnema/neferclient) is a client toolkit written for this split.

## Consequences

- The module depends only on purego, purego-vulkan, go-text, x/image and x/sys.
- Clipboard and input-method ports (`Clipboard`, `IME`) are interfaces the application implements.
- `Password(true)` masks the displayed value of an in-memory editor value.
- ADR 0003 describes the Vulkan, DMA-BUF and explicit-sync design.
