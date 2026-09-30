# Wayland input boundary

`Connect` binds the optional initial `wl_seat`. `StartReader` owns all Wayland
callbacks and posts typed `Event` values. The session loop must call `ApplySeat`
for capability/removal events, `InputAdapter.ApplyInput` for input events, and
translate resulting `Input` values to the root `platformInput` later, at the
runtime wiring boundary. Callbacks never run the editor, keyboard interpreter,
or UI. On shutdown, `Window.Close` closes unconsumed keymap FDs in its queues.
A consumer that drops an event before dispatch must use `CloseEventFD`.

`wl_pointer` surface-local fixed-point coordinates are **already logical**;
do not apply window buffer scale. A version 5+ pointer frame becomes one ordered
`InputFrame`. For older pointers events flush individually. Axis deltas preserve
their native Wayland units; `value120`, stop, and source metadata are additional
axis events, not extra pixel scroll deltas. Consumers must avoid counting both
axis and value120 for the same frame. A button release outside the surface is
still delivered, for cancellation of pointer capture.

The bounded input queue evicts old motion first, then coalesces adjacent axis
values on the same axis/source (without crossing discrete events). If still full,
it discards the queued state and emits one `InputReset` (`Kind == "reset"`)
before later events. The runtime must treat reset as pointer leave + release of
all buttons + keyboard focus-out/cancel repeat, then re-sync from later events.
The adapter itself drops held keys and repeat on reset but keeps compositor
keyboard focus, so later key events apply without a new enter.
`InputOverflow` counts discarded inputs; merging lossless axes does not count.

A keymap event transfers its FD via `OwnedFD.Take()` from the reader to exactly
one loop event; `ApplyInput` passes it to `keyboard.ReplaceFD`, which closes it
on success or error. Repeat info and focus loss are applied only on the loop.
The compositor serial from keyboard enter or key press is exposed for selection
ownership. `Tick` and `NextRepeat` belong to that same loop, never the reader.
