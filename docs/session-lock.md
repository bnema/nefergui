# Session lock

`nefergui.RunLock` implements the client side of `ext-session-lock-v1`. It acquires the lock, covers every output with opaque black and hosts the caller's view on one output. Authentication is the caller's; NeferGUI never unlocks on its own.

## Behavior

- One Wayland connection, seat and reader serve every lock surface. One lock surface is created per output present at acquisition; outputs added later get a black surface, removed outputs lose theirs.
- Rendering uses the normal Vulkan, DMA-BUF and explicit-sync path only: no shared-memory buffer, no readback, no implicit-sync fallback. A surface allocates and attaches a buffer only after its configure was acknowledged and its dmabuf feedback round completed (`Window.FeedbackDone`).
- Exactly one output hosts the view: the one whose surface has keyboard focus, initially the first. Every other output shows plain black (an empty frame).
- `OnLocked` fires when the compositor sends `locked`. `finished` (refused or ended) makes `RunLock` return `ErrLockFinished`; the lock is then destroyed with a plain `destroy`, never an unlock.
- Verification is the caller's and asynchronous: `OnSubmit` never blocks and never unlocks. Unlock happens only when the caller sends on `LockConfig.Unlock` (after its own verification); a request made before `locked` is held until `locked`, closing the channel is not a request. Then: `unlock_and_destroy`, a display roundtrip, and the connection closes. The caller shows progress with `LockConfig.Status` (`LockIdle`, `LockBusy`, `LockFailed`), surfaced as `LockState.Status`. Cancellation or any error closes the connection without unlocking, which keeps the session locked.

## Configuration

- `Display` is the Wayland socket name; empty uses the environment.
- `Wake` requests a redraw for each value received. Its forwarder is scoped to `RunLock` and stops when it returns.
- `OnOutputError(output, err)` reports, with the connector name, an output added after acquisition that could not get a lock surface or render path. Such hotplug failures are skipped (the compositor keeps the output black); failures on outputs present at acquisition abort `RunLock`.
- `NewSecretBuffer` panics unless the capacity is between 1 and 4096 bytes. `RunLock` wipes `Secret` when it returns, and the buffer is wiped after `OnSubmit` even if it panics.
- Outputs are promoted in ascending global order, so the first host is deterministic.
- Keyboard focus-out drops held keys and ignores key presses until focus returns; typed bytes stay in `Secret`, and input continues when any lock surface regains focus (that surface becomes the host).

## Secret input

Typed keys go through the keyboard secret path (`EventSecret`/`TickSecret`) into a caller-owned `SecretBuffer`. They never become a Go string, and never enter the model, the frame tree, render text, IME, pre-edit or the clipboard (lock surfaces bind neither). The view receives `LockState.Mask`, the number of typed code points, and draws bullets.

Enter calls `OnSubmit` (after `locked` only; the buffer is wiped right after it returns, so it must copy or consume the slice and must not block), Escape and Ctrl+U clear, Backspace removes the last code point. Before `locked`, Enter only clears. Scratch buffers are zeroed after every key.

## Example

```go
secret := nefergui.NewSecretBuffer(256)
defer secret.Wipe()
err := nefergui.RunLock(ctx, nefergui.LockConfig{
	Secret: secret,
	Styles: "lock.css",
	View: func(f *nefergui.Frame, s nefergui.LockState) {
		box := f.Root(nefergui.Class("screen")).Column(nefergui.Class("box"))
		box.Text(s.Bullets(), nefergui.Key("mask"))
	},
	Unlock: unlock, // chan struct{}: send after verification succeeded
	Status: status, // chan nefergui.LockStatus: LockBusy / LockFailed / LockIdle
	OnSubmit: func(pw []byte) { // copy or consume pw now; it is wiped on return
		go verify(bytes.Clone(pw), unlock, status)
	},
})
```

## Verification

Wire tests cover the shared connection (reader pause and queue saturation, keyboard and pointer generations, feedback completion). `NEFERGUI_HARNESS=1` with a NeferWL binary in `NEFERGUI_NEFERWL` runs the headless lock tests (`TestHarnessConnectionLock`, `TestHarnessRunLock`, `TestHarnessRunLockRefused`).
