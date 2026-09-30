# text-input v3 adapter

`NewIME` optionally binds `zwp_text_input_manager_v3` and creates one
text-input object per seat. `nil` means unavailable: ordinary xkb text remains
active. A session posts reader callbacks as `IMEEvent`s, invokes `ApplyIME` on
the UI loop, and passes only the completed `edit.IMEBatch` to the editor.
Ordinary xkb text stays active while text-input is enabled, so enabling it
without a running input method does not block typing. An input method that
grabs the keyboard receives keys instead of `wl_keyboard`. For an input method
that does not grab, keys reaching `wl_keyboard` are typed except while its
preedit is shown; the input method owns that text until it commits.
`Enable` requested before `enter` is deferred until the
surface gains text-input focus; leave drops the pending composition.

Every successfully sent `zwp_text_input_v3.commit` increments the per-object
commit count. The protocol's `done.serial` is this **count**, not the keyboard
serial. The text-input-v3 XML says a mismatched done still applies its preedit,
commit and delete batch normally, but the client must not modify the protocol
object's current state; pending surrounding/content/rectangle updates are
held until a matching done arrives. `ApplyIME` implements this rule. Requests
are double-buffered by protocol commits. The cursor rectangle is surface-local
logical pixels; with negotiated v2 it is applied on the next surface commit.
A password sends sensitive+hidden hints, password purpose, and empty
surrounding text (the editor owns that privacy decision).

Surrounding text is limited to 4000 UTF-8 bytes and clipped around the cursor
at rune boundaries. Reader callbacks never touch editor state. NeferWL does
not expose text-input-v3; tests use a socketpair fake server only.
