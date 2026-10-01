# Visual harness

`go run ./cmd/nefergui-harness run --size 800x600 --scale 1.5 --layout fr --input script.txt --expect expect.json --out artifacts -- <client> [args...]`

`--size 800x600,320x200` starts one headless output per size (`HEADLESS-1`, `HEADLESS-2`, ...); screenshots and expectations then use the first output, in `frames/HEADLESS-1/`. Omit `-- <client>` for a compositor-only run. The output directory must not exist. `--background '#336699'` sets the compositor background (default `#111111`). `--timeout 15s` limits the whole run, including the build (which is separately capped at three minutes); `--ready-timeout 8s` bounds socket discovery. `--keep` preserves the isolated runtime/config directory and records its location in `result.json`.

The harness installs NeferWL from pinned commit `1d16b83bf90f9e8189ad21b27772382c96b9f9a8` into `$XDG_CACHE_HOME/nefergui/harness/neferwl-<sha>/` on first use. `NEFERGUI_NEFERWL` selects an existing executable instead, but requires `--allow-unpinned`; this records `compositor.pinned=false` and `compositor.binary` in `result.json`. Pinned runs record `compositor.pinned=true` and `compositor.sha`. Installation uses `GOWORK=off`, a remote module, and no local replacement. A host Vulkan GPU and working NeferWL runtime are needed for integration.

The session provides a private `XDG_RUNTIME_DIR`, NeferWL config, and discovered `WAYLAND_DISPLAY`; the compositor also gets a private `XDG_STATE_HOME`. The config enables NeferWL `wayland` debug logging; its structured run log is copied to `logs/compositor-run.log` and recorded as `artifacts.compositor_run_log` when NeferWL wrote one (a failed copy fails the run), where each client cursor shape change appears as a `cursor` entry. Host `DISPLAY`, `WAYLAND_DISPLAY`, and `WAYLAND_SOCKET` are removed from compositor and client environments. Clients also receive `NEFERGUI_DEBUG_DIR`. The harness only creates this directory; clients own their debug traces. To check a mapped surface before client shutdown, a client atomically publishes `client-debug/capture-request` (write a temporary file and rename) containing its wall-clock UnixNano immediately after committing its chosen frame, then remains mapped for at least two more compositor frames. NeferWL writes screenshots sequentially, so the first one finished after the timestamp may have been rendered before it; the harness selects the **second** `frame-NNNNNN.png` with modification time after that timestamp (retrying until it decodes completely), copies it to `frames/capture.png`, records `artifacts.capture`, and evaluates expectations against that capture. Without a request, expectations still use `frames/latest.png` (including compositor-only runs). A request without a later screenshot fails within the run timeout. Frames are in `frames/`, client debug files in `client-debug/`, logs in `logs/`, and machine-readable `result.json` uses schema `nefergui-harness/v1`. Run-specific paths and elapsed build/ready/client/total times are recorded. Temporary session directories disappear by default; artifacts remain.

Example `expect.json`:

```json
{
  "probes": [{"name":"background", "x":40, "y":40, "color":"#336699", "tolerance":1}],
  "regions": [{"name":"swatch", "rect":{"x":10,"y":10,"width":30,"height":20}, "color":"#336699", "tolerance":1}],
  "goldens": [{"name":"screen", "path":"reference.png", "tolerance":2, "max_diff_ratio":0.001}]
}
```

Colors are `#RRGGBB`; tolerance is an inclusive per-channel 8-bit delta including alpha. Rectangles use half-open pixel coordinates. A region without `color` checks bounds only. Golden paths are relative to the expectations file; bounds must match. Each golden emits `diff-N.png` (absolute RGB channel deltas with alpha delta shown in red, black where within tolerance). `max_diff_ratio` is the fraction of pixels with **any** channel outside tolerance. GPU sRGB conversion can differ from the configured background by one channel value, so background tests allow tolerance 1.

Exit codes: 0 all checks pass, 1 an assertion fails, 2 invalid usage, 3 NeferWL/build/runtime unavailable, 4 client or compositor fails after readiness or fails to produce a frame. Run the opt-in compositor smoke test with `NEFERGUI_HARNESS=1 GOWORK=off go test -v ./internal/harness -run TestHeadlessBackground -count=1`.
