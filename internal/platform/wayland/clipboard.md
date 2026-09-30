# Clipboard adapter

`Clipboard` is constructed on the session loop with a seat, a function returning
the most recent keyboard enter/key serial, and a **reader-to-loop** event post
function. Deliver each `ClipboardEvent` to `ApplyClipboard` on that loop.
A missing `wl_data_device_manager` returns nil (clipboard unavailable).
Selection offers have independent MIME sets. Only the latest selected offer is
used; superseded offers and old sources are destroyed. Selection write offers
`text/plain;charset=utf-8`, `UTF8_STRING`, and `text/plain`. The same priority
is used when choosing a MIME for reads. FD-bearing send events transfer their
`OwnedFD` to a bounded-time writer; read transfers are bounded at the caller's
limit plus one byte (at most `edit.MaxClipboardBytes+1`) and timed out after
three seconds. Close the adapter before tearing down the data-device manager.

The synchronous `edit.Clipboard.ReadText` contract would deadlock the UI loop:
Wayland receive requires another process to write a pipe while the UI must
continue dispatching. Therefore `edit.AsyncClipboard` is an additive port.
`ReadTextAsync` starts the transfer without waiting for the peer; its completion
callback runs on a **worker** and must enqueue a value for the next UI frame.
Never mutate the editor in that callback. Call `edit.State.PasteAsync` on the
UI loop to validate UTF-8 and bounds and apply the result. Synchronous
`ReadText` explicitly returns an error rather than blocking.

Drag-and-drop is unsupported in V1. The data-device enter offer is tracked
separately from selection and discarded on leave, drop, or a subsequent enter;
selection replacement must not destroy an active drag offer.
