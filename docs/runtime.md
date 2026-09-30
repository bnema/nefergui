# Immediate runtime

`Run(ctx, &model, view, options...)` opens a Wayland toplevel, renders with Vulkan and returns when the window closes (`nil`) or `ctx` is cancelled (`ctx.Err()`). Options: `Title`, `Size` (logical pixels), `Styles` (CSS file path; a read error is returned) and `Transparent` (windows are opaque by default). Setup failures (no fonts, no Wayland display, no usable Vulkan device) are returned as errors. Accessibility is not connected.

The UI loop never blocks on the GPU or the compositor. It applies Wayland events, drains seat input, fires key repeat from a timer, and builds `view` once per input batch or redraw request. A frame is prepared and presented only when the build or the window size/scale changed. Clipboard reads are asynchronous: paste starts a read and the text is inserted by the next frame. A new paste cancels the previous one. Input-method (text-input v3) batches are applied during frame construction.

The unexported `runtime` constructs views synchronously. A concurrent or re-entrant build never waits: it requests a redraw, posts a wake token and returns false. The in-progress frame commits first; queued input remains for the next build.

The unexported UI-loop port `platformInput` accepts logical pointer motion/press/release/axis/leave, interpreted `keyboard.Key` events, focus-in/out and logical viewport resize/scale. `route` hit-tests against the last committed layout (reverse paint order and ancestor clips), resolves the result to a frame identity and wakes the next build only when state changes. `Target` is a committed-child-path helper for tests. A build computes CSS pseudo-state and commits layout, display list and tree together. The headless viewport defaults to 800×600 logical pixels; `Run` sets it from the window. Scale is used by the renderer only and never affects hit testing: Wayland surface coordinates are already logical. A seat input-queue overflow is delivered as a reset (pointer leave, all buttons released, keyboard focus-out). `Run` installs a text engine from system fonts; headless tests install one explicitly, and without one text commands have no measured glyph runs.

## Example

`examples/demo` is the product example with an embedded stylesheet:

```sh
GOWORK=off go run ./examples/demo
```

`--static --frames N` renders N frames and exits (used by the visual harness).

Tab traverses visible, enabled controls in document order and wraps; Shift+Tab reverses. Keyboard focus sets `:focus-visible`; pointer focus does not. Enter activates focused buttons and checkboxes on initial key press, ignoring repeats. Space sets `:active` on initial press and activates on release only if focus is unchanged; focus loss/change or Escape cancels it, and repeats are ignored. Disabled controls cannot focus or activate. When a focused control is removed or disabled, focus falls back to **none** (the next Tab starts at the first available control). The first pressed pointer button owns capture until its matching release, even outside the target; other button presses/releases cannot steal it or change focus. Only primary-button release on the captured control activates it. Pointer leave clears hover but preserves capture until matching release (an out-of-surface release cannot activate); removal or platform focus loss cancels capture. Scroll wheel deltas apply to the nearest scrollable ancestor and clamp to measured content bounds; offsets are discarded when that identity is no longer a laid-out scroll container. Unchanged state does not redraw. No user callback runs outside frame construction.

Offline keyboard tests require libxkbcommon.so.0 and a usable XKB data installation; a host without those prerequisites may opt out with `NEFERGUI_SKIP_XKB=1` (other test failures still fail). The valid-FD test fixture is a French Wayland keymap generated with `xkbcli compile-keymap --layout fr --output-format 1` from libxkbcommon 1.13.2; SHA-256 `f6bcde4e4e03293d05c9426d08cdc2e9a78e7b8faef39dc97c5d267ad7355607`.

Compile with `-tags nefergui_debug` to report duplicate sibling keys, changed unkeyed child types and stale Node use (stale use panics). Without this tag, stale handles are inert. Do not retain a Node across frames. `ID` is only for CSS, while `Key` identifies an element within its parent. Unkeyed children are identified by their position and type.

System font discovery indexes family and aspect from font metadata without retaining font files in memory. Text measurement loads and caches only selected faces; CSS family requests and script fallback select faces on demand. User font directories precede system directories, with sorted paths breaking ties. Fonts that fail to load are skipped and reported in catalog diagnostics.
