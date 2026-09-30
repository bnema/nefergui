# ADR 0002 — Wayland bindings and file-descriptor ownership

Status: accepted

## Decision

WLTurbo owns the Wayland wire transport, object registry, XML scanner and the canonical generated bindings. NeferGUI consumes generated packages and never writes protocol code.

- `protocol/core` is generated from `wayland.xml`. Only `wl_display` and `wl_registry` bootstrap code is handwritten.
- Generated child objects keep their concrete type and typed event dispatch.
- Required extensions and the behavior when a global is missing:

| Binding | Use | Missing global |
|---|---|---|
| xdg-shell | window lifecycle | fatal capability error |
| linux-dmabuf v4 feedback | format and device selection | fatal capability error |
| linux-drm-syncobj v1 | explicit synchronization | fatal capability error |
| viewporter | fractional buffer mapping | fatal when fractional scale is active |
| fractional-scale v1 | preferred scale | integer-scale fallback |
| text-input v3 | IME | IME unavailable, keyboard input still works |
| core data-device | clipboard | clipboard unavailable |

Excluded: presentation-time, data-control, X11, compositor-private blur.

## File descriptors

- A request that sends an FD transfers ownership only after a complete successful send.
- A received message owns exactly the FDs declared by its signature. Socket reads are not message boundaries.
- Missing or extra FDs, truncated ancillary data, malformed payloads and unknown signatures are protocol errors. FD 0 is valid.
- Without a handler, owned FDs are closed. An owned FD reaches exactly one handler path.
- Decode, dispatch, cancellation, object destruction and shutdown close every unclaimed FD.
