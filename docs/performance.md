# Performance

## CPU budgets

Measured: **2026-09-30, 06:14–06:16 CEST (UTC+02:00)**. Implementation commit: **`7e46072588c8dce90eaa8bc11f89a5564f182b04`**.

AMD Ryzen 9 7900X3D; Go `go1.27.1-X:nodwarf5 linux/amd64`. Command: `CGO_ENABLED=0 GOWORK=off go test -run '^$' -bench . -benchmem -count=5` in the indicated packages. Entries are medians of five runs. Budgets allow roughly twice the measured medians; zero-allocation paths stay at zero. CPU numbers vary with machine load.

The demo-frame benchmark builds a headless home-view tree, computes layout and produces a display list after warmup. Prepare uses a representative text/box command mix and excludes GPU submission. Paint uses 100 cards with two shadows and text; ListBatches uses 2,000 quads with interleaved images. ListBufferReuse includes initial buffer allocation amortized over the run, so B/op is not a steady-state allocation cost.

| Package / benchmark | Median ns/op | Median B/op | Median allocs/op | Budget ns/op | Budget B/op | Budget allocs/op |
|---|---:|---:|---:|---:|---:|---:|
| internal/ui / DemoFrame | 25,865 | 39,272 | 193 | 55,000 | 80,000 | 500 |
| render / Prepare | 49,142 | 85,568 | 379 | 100,000 | 175,000 | 800 |
| render / PrepareBoxes | 67,026 | 229,377 | 1 | 140,000 | 460,000 | 2 |
| render / ListBatches | 112,291 | 420,571 | 2 | 230,000 | 850,000 | 4 |
| render / ListBufferReuse | 63,299 | 69 | 0 | 130,000 | 150 | 0 |
| layout / Paint | 115,660 | 479,360 | 4 | 240,000 | 960,000 | 8 |
| layout / LayoutPaintTree | 186,338 | 552,820 | 343 | 380,000 | 1,110,000 | 700 |
| layout / Layout1000 | 664,176 | 731,684 | 1,027 | 1,350,000 | 1,470,000 | 2,100 |
| text / AtlasWarmLookup | 31.84 | 0 | 0 | 65 | 0 | 0 |
| text / RasterDistinct1000 | 4,187,468 | 8,566,132 | 16,784 | 8,500,000 | 17,200,000 | 34,000 |
| text / ShapeParagraph (uncached) | 245,802 | 300,149 | 950 | 500,000 | 610,000 | 1,900 |
| text / ShapeParagraphCached | 79.34 | 0 | 0 | 160 | 0 | 0 |
| text / AtlasInsert | 564.9 | 968 | 3 | 1,150 | 2,000 | 6 |
| css / Engine/cold | 2,358,611 | 1,964,619 | 5,669 | 4,800,000 | 4,000,000 | 11,400 |
| css / Engine/warm | 100,682 | 1 | 0 | 205,000 | 2 | 0 |
| css / Engine/state-change | 348,826 | 149,107 | 363 | 700,000 | 300,000 | 750 |

`TestDemoFrameAllocationBudget` checks the 500 allocs/op limit with `testing.AllocsPerRun`. Focused tests also check constant quad allocation, bounded paint allocation, allocation-free warm list-buffer reuse, and allocation-free idle wait results. The other table budgets are reference targets, not automated assertions.

Frame construction limits allocation churn through:

- one pre-sized quad array per prepared frame, preserving independent frame lifetimes;
- detached display-list storage sized once for commands, shadows, runs and glyphs;
- a session-owned instance/batch buffer, consumed synchronously before reuse, with image references cleared after use and oversized storage discarded when smaller frames arrive;
- font family normalization at font load and identity path keys computed once per element;
- `text.Engine.Measure` caching by text, request and width. Entries unused for two frames are evicted; the cache holds at most 4,096 entries. Results are immutable; callers use `Layout.Clone` before adjusting positions.

`Run` builds a frame only on input or a redraw request. Idle frame construction stops. Expected release-wait timeouts return without constructing errors; real ioctl failures retain their context and wrapped errno.

On the same implementation commit, measured **2026-09-30, 06:15–06:16 CEST**, `BenchmarkWaitPointIdle` on an unsignaled real DRM timeline took a median **21,567,553 ns/op, 0 B/op, 0 allocs/op**. This includes the requested 20 ms timeout, not active CPU time:

```sh
NEFERGUI_RENDER_NODE=/dev/dri/renderD128 CGO_ENABLED=0 GOWORK=off \
  go test -run '^$' -bench BenchmarkWaitPointIdle -benchtime=10x -benchmem -count=5 ./internal/presentation/syncobj
```

Profile frame construction with:

```sh
go test -run '^$' -bench BenchmarkDemoFrame -benchmem -memprofile mem.out -memprofilerate=1 ./internal/ui
go tool pprof -sample_index=alloc_objects -top -cum mem.out
```

## Memory as a dependency

Measured: **2026-09-30, 06:12–06:14 CEST (UTC+02:00)**. Implementation commit: **`7e46072588c8dce90eaa8bc11f89a5564f182b04`**. Comparison baseline: **`1488e4a390e93603976ae3f5e9ac3fdb1bd00c1a`**, measured **2026-09-30, 06:06–06:10 CEST**.

An external Go module with minimal-counter and demo views, a 960×640 window at scale 1, and a private headless NeferWL compositor was profiled on AMD/RADV. Stack: Go `go1.27.1-X:nodwarf5 linux/amd64`, Mesa/RADV 26.2.3, NeferWL `1d16b83bf90f9e8189ad21b27772382c96b9f9a8`. Debug readbacks were disabled. Live Go heap was sampled after forced GC; RSS includes shared driver libraries, while PSS apportions shared pages.

| Measurement | Minimal view | Demo view |
|---|---:|---:|
| Live Go heap after 1,000 frames | 5.16 MiB | 5.29 MiB |
| Live Go heap after 3,000 frames | 5.19 MiB | 5.31 MiB |
| Process RSS / PSS after 1,000 frames | 62 / 39 MiB | 58 / 39 MiB |
| Temporary allocations per rendered frame | about 36 KiB | about 136 KiB |
| Baseline temporary allocations per rendered frame | about 61 KiB | about 238 KiB |

Heap/RSS/PSS use the default Go profiling rate. Allocation figures use separate `GODEBUG=memprofilerate=1` runs: subtract cumulative profiles at frames 1 and 1,000, exclude stacks containing the measurement sampler or `runtime/pprof`, sum the remaining flat allocated bytes, then divide by 999 frames. These runs recorded about **42–43% less allocation churn** than the baseline. They do not demonstrate an equivalent reduction in RSS or frame latency. Single-run process-memory values vary with runtime and driver behavior.

The minimal view starts at about 0.75 MiB live Go heap, reaches 5.1 MiB after the first frame, and returns to about 0.9 MiB after closing. No sustained live-heap growth was observed over 3,000 frames. Retained heap stayed close to baseline; the session's reusable conversion buffers retain a small amount of storage. Driver mappings and Go heap capacity can remain resident after closing; RSS is not a leak measurement on its own.

DRM counters reported about 41 MiB VRAM and 4 MiB GTT once all three presentation buffers were active, unchanged from baseline and released at shutdown. Window dimensions, scale, fonts and driver affect these figures. These are measurements on one stack, not portable limits.

A separate approximately 20-second idle profile recorded about **91 KiB** allocated under `syncobj.WaitPoint` in the baseline and **no allocations attributed to that path** on the measured implementation. Other runtime and consumer instrumentation allocations remain; this is not a claim that the whole process allocates nothing at idle.

Prepared quads and layout painting still allocate detached outputs. Glyph rasterization and glyph-key hashing also appear in cumulative allocation profiles. `NEFERGUI_DEBUG_DIR` enables readbacks and PNG work; leave it unset when measuring ordinary application cost.

Use `pprof`'s `alloc_space` to find cumulative allocation churn and `inuse_space` after GC to inspect retained Go memory. Use `/proc/<pid>/smaps_rollup` for RSS/PSS and DRM fdinfo for GPU counters; heap profiles do not include driver or GPU memory.
