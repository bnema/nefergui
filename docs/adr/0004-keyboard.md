# ADR 0004 — Keyboard interpretation

Status: accepted

## Decision

Keyboard interpretation uses `github.com/bnema/purego-xkbcommon`, which loads `libxkbcommon.so.0` at runtime without cgo. Raw bindings are generated from pinned upstream headers; a small handwritten layer owns reference counting, UTF-8 buffers, compose state and Wayland keymap FD ingestion. X11 APIs are not bound.

NeferGUI handles keymap replacement, modifier and group masks, non-US layouts including AltGr, compose and dead keys, key repeat and its cancellation, shortcuts, and IME/keyboard deduplication. The compositor sets repeat rate and delay.

While text-input v3 composition is active it takes precedence. Otherwise text comes from `xkb_state_key_get_utf8`.
