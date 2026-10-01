# Performance

## CPU budgets

Measured: **2026-09-30, 07:52–07:54 CEST (UTC+02:00)** on commit `7ad3316fd1add28c1507e28c36883afaeaaad434`.

AMD Ryzen 9 7900X3D; Go `go1.27.1-X:nodwarf5 linux/amd64`. Command: `CGO_ENABLED=0 GOWORK=off go test -mod=readonly -p=1 -run '^$' -bench . -benchmem -count=5 ./internal/ui ./internal/render ./internal/layout ./internal/text ./internal/css`. Package benchmarks run serially; external profiling runs are separate. Entries are medians of five runs. Budgets allow roughly twice the measured medians; zero-allocation paths stay at zero. DemoFrame has a deliberately tighter 1,100-allocation limit to catch editor-workspace regressions. CPU numbers vary with machine load.

The demo-frame benchmark builds the light, comfortable document workspace with a demo stylesheet (`internal/ui/testdata/demo.css`), computes layout and produces a display list after warmup. It includes the toolbar, document list, populated multiline editor and properties pane with input, radios and slider; it excludes GPU submission. Its workload is larger than the older unstyled fixture, so those results are not directly comparable. Prepare uses a representative text/box command mix and excludes GPU submission. Paint uses 100 cards with two shadows and text; ListBatches uses 2,000 quads with interleaved images. ListBufferReuse and LayoutPaintTree warm their buffers and text cache before resetting the timer; their figures measure steady state.

| Package / benchmark | Median ns/op | Median B/op | Median allocs/op | Budget ns/op | Budget B/op | Budget allocs/op |
|---|---:|---:|---:|---:|---:|---:|
| internal/ui / DemoFrame | 212,479 | 373,984 | 955 | 430,000 | 750,000 | 1,100 |
| render / Prepare | 43,776 | 85,568 | 379 | 100,000 | 175,000 | 800 |
| render / PrepareBoxes | 67,867 | 229,377 | 1 | 140,000 | 460,000 | 2 |
| render / ListBatches | 114,240 | 420,482 | 2 | 230,000 | 850,000 | 4 |
| render / ListBufferReuse | 61,718 | 0 | 0 | 130,000 | 0 | 0 |
| layout / Paint | 114,138 | 479,360 | 4 | 240,000 | 960,000 | 8 |
| layout / LayoutPaintTree | 184,427 | 546,648 | 313 | 380,000 | 1,100,000 | 650 |
| layout / Layout1000 | 893,140 | 731,926 | 1,027 | 1,800,000 | 1,470,000 | 2,100 |
| text / AtlasWarmLookup | 31.80 | 0 | 0 | 65 | 0 | 0 |
| text / RasterDistinct1000 | 4,214,124 | 8,566,131 | 16,784 | 8,500,000 | 17,200,000 | 34,000 |
| text / ShapeParagraph (uncached) | 230,170 | 280,984 | 895 | 500,000 | 570,000 | 1,800 |
| text / ShapeParagraphCached | 80.76 | 0 | 0 | 160 | 0 | 0 |
| text / AtlasInsert | 558.2 | 968 | 3 | 1,150 | 2,000 | 6 |
| css / Engine/cold | 2,330,406 | 1,964,620 | 5,669 | 4,800,000 | 4,000,000 | 11,400 |
| css / Engine/warm | 101,909 | 1 | 0 | 205,000 | 2 | 0 |
| css / Engine/state-change | 339,806 | 149,105 | 363 | 700,000 | 300,000 | 750 |

Natural container measurement prevents nested flex text from collapsing, at a CPU cost that grows with nesting depth. Layout1000 measures about 36% slower than the earlier 656 µs run; allocation count stays at 1,027. A per-layout node/width cache increased time and retained allocation in a trial and is not used. LayoutPaintTree stays near its previous cost. These are correctness/performance tradeoffs, not a claim of universal speed improvement.

`TestAllocDemoFrame` checks the 1,100 allocs/op limit with `testing.AllocsPerRun`. `TestAllocRendererSteadyFrame` measures `Renderer.Render` for a view whose text changes through `TextInt`; it measured 244 allocations per steady Render+Released cycle on 2026-10-01 (the guard fails above that baseline), needs a GPU, and is skipped unless `NEFERGUI_RENDER_NODE` is set. Focused tests also check constant quad allocation, bounded paint allocation, allocation-free warm list-buffer reuse and allocation-free idle wait results (the real-ioctl variant is opt-in via `NEFERGUI_RENDER_NODE`). The other table budgets are reference targets, not automated assertions.

Frame construction limits allocation churn through:

- a detached quad array initially reserved up to 512 quads; larger visible frames grow after culling, so offscreen content cannot force an unbounded speculative allocation;
- detached display-list storage sized once for commands, shadows, runs and glyphs;
- a target-owned instance/batch buffer, consumed synchronously before reuse, with image references cleared after use and oversized storage discarded when smaller frames arrive;
- font family normalization at font load and identity path keys computed once per element;
- `text.Engine.Measure` caching by text, request and width. Entries unused for two frames are evicted; the cache holds at most 4,096 entries. Results are immutable; callers use `Layout.Clone` before adjusting positions.

`Renderer.Render` builds a frame only on input or a redraw request. Idle frame construction stops.

Profile frame construction with:

```sh
go test -run '^$' -bench BenchmarkDemoFrame -benchmem -memprofile mem.out -memprofilerate=1 ./internal/ui
go tool pprof -sample_index=alloc_objects -top -cum mem.out
```

## Memory as a dependency

These external profiles use the earlier minimal and simplified demo fixtures, not the desktop document workspace measured above. They describe library allocation behavior at the pinned commit, not the current example's full memory footprint.

Measured: **2026-09-30, 06:34–06:36 CEST (UTC+02:00)**. Measured commit: **`9064a730c898b48f967b3f2228fb705474b4fdeb`**. Comparison baseline: **`1488e4a390e93603976ae3f5e9ac3fdb1bd00c1a`**, measured **2026-09-30, 06:06–06:10 CEST**.

An external Go module with minimal-counter and demo views, a 960×640 window at scale 1, and a private headless NeferWL compositor was profiled on AMD/RADV. Stack: Go `go1.27.1-X:nodwarf5 linux/amd64`, Mesa/RADV 26.2.3, NeferWL `1d16b83bf90f9e8189ad21b27772382c96b9f9a8`. Debug readbacks were disabled. Live Go heap was sampled after forced GC; RSS includes shared driver libraries, while PSS apportions shared pages.

| Measurement | Minimal view | Demo view |
|---|---:|---:|
| Live Go heap after 1,000 frames | 5.19 MiB | 5.32 MiB |
| Baseline live Go heap after 1,000 frames | 5.17 MiB | 5.27 MiB |
| Live Go heap after 3,000 frames | 5.23 MiB | 5.34 MiB |
| Baseline live Go heap after 3,000 frames | 5.20 MiB | 5.31 MiB |
| Process RSS / PSS after 1,000 frames | 65 / 42 MiB | 59 / 40 MiB |
| Baseline process RSS / PSS after 1,000 frames | 63 / 40 MiB | 60 / 41 MiB |
| GPU VRAM / GTT with three buffers, current and baseline | 41 / 4 MiB | 41 / 4 MiB |
| Temporary allocations per rendered frame | about 35.5 KiB | about 136 KiB |
| Baseline temporary allocations per rendered frame | about 61 KiB | about 238 KiB |

Heap/RSS/PSS use the default Go profiling rate. Allocation figures use separate `GODEBUG=memprofilerate=1` runs: subtract cumulative profiles at frames 1 and 1,000, exclude stacks containing the measurement sampler or `runtime/pprof`, sum the remaining flat allocated bytes, then divide by 999 frames. These runs recorded about **41–43% less allocation churn** than the baseline. They do not demonstrate an equivalent reduction in RSS or frame latency. Single-run process-memory values vary with runtime and driver behavior.

The minimal view starts at about 0.75 MiB live Go heap, reaches 5.1 MiB after the first frame, and returns to about 0.9 MiB after closing. No sustained live-heap growth was observed over 3,000 frames. Retained heap stayed close to baseline; the reusable conversion buffers retain a small amount of storage. Driver mappings and Go heap capacity can remain resident after closing; RSS is not a leak measurement on its own.

DRM counters reported about 41 MiB VRAM and 4 MiB GTT once all three presentation buffers were active, unchanged from baseline and released at shutdown. Window dimensions, scale, fonts and driver affect these figures. These are measurements on one stack, not portable limits.

Prepared quads and layout painting still allocate detached outputs. Editor and indicator command insertion can also reallocate the display list; those UI-owned costs are included in the demo measurements and are not optimized here. Glyph rasterization and glyph-key hashing also appear in cumulative allocation profiles.

Use `pprof`'s `alloc_space` to find cumulative allocation churn and `inuse_space` after GC to inspect retained Go memory. Use `/proc/<pid>/smaps_rollup` for RSS/PSS and DRM fdinfo for GPU counters; heap profiles do not include driver or GPU memory.
