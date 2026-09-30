# ADR 0005 — Immediate-mode identity and events

Status: accepted

## Identity

- `Key` is a stable identity local to the parent. `ID` is CSS metadata only.
- Without a key, identity is `parent identity + local child position + element type`.
- Dynamic or reordered collections need explicit keys. Debug builds report duplicate sibling keys, stale `Node` use and suspicious unkeyed sequence changes.

## Events

- Input is hit-tested against the last committed layout. The target identity is queued, the UI loop wakes, pending events are batched and one frame is built.
- The event is delivered to the matching element in that frame, is idempotent while the frame runs, and expires at the end of the frame.
- No user code runs outside frame construction.
- Pointer-backed controls may change the pointed value during their call, never keep the pointer, and later calls in the same frame see the new value.
- Controls declared earlier in that frame painted the previous value, so every event batch is followed by exactly one event-free frame that shows the settled state. That frame schedules nothing further.
