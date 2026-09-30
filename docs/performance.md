# CPU performance budgets

AMD Ryzen 9 7900X3D 12-Core Processor; `CGO_ENABLED=0 GOWORK=off go test -run '^$' -bench . -benchmem -count=5` in the indicated packages. Entries are medians of five runs. Budgets are approximately twice the measured medians (zero allocations remain zero). CPU numbers vary with machine load. The demo-frame benchmark builds a headless home-view tree, computes layout and produces a display list after warmup; Prepare uses a representative warm text/box command mix and excludes GPU submission.

| Package / benchmark | Median ns/op | Median B/op | Median allocs/op | Budget ns/op | Budget B/op | Budget allocs/op |
|---|---:|---:|---:|---:|---:|---:|
| root / DemoFrame | 21,238 | 32,816 | 220 | 45,000 | 66,000 | 500 |
| render / Prepare | 59,024 | 158,720 | 386 | 125,000 | 320,000 | 800 |
| text / AtlasWarmLookup | 29.07 | 0 | 0 | 60 | 0 | 0 |
| text / RasterDistinct1000 | 4,139,638 | 8,566,137 | 16,784 | 8,300,000 | 17,200,000 | 34,000 |
| text / ShapeParagraph (uncached) | 226,223 | 300,146 | 950 | 460,000 | 600,000 | 1,900 |
| text / ShapeParagraphCached | 78.16 | 0 | 0 | 160 | 0 | 0 |
| text / AtlasInsert | 508.3 | 966 | 3 | 1,050 | 2,000 | 6 |
| css / Engine/cold | 2,310,093 | 1,956,258 | 5,608 | 4,700,000 | 4,000,000 | 11,300 |
| css / Engine/warm | 95,553 | 1 | 0 | 195,000 | 2 | 0 |
| css / Engine/state-change | 334,293 | 149,130 | 363 | 680,000 | 300,000 | 750 |
| layout / Layout1000 | 623,154 | 731,626 | 1,027 | 1,250,000 | 1,470,000 | 2,100 |

`TestDemoFrameAllocationBudget` checks the 500 allocs/op limit with `testing.AllocsPerRun`.

Frame construction stays low-allocation because:

- font family names are normalized once when fonts load, not per face on every match;
- `text.Engine.Measure` caches results by text, request and width; entries unused for two frames are evicted and the cache holds at most 4,096 entries (callers treat results as immutable and use `Layout.Clone` before adjusting positions);
- identity path keys are computed once per element.

`Run` builds a frame only on input or a redraw request, so an idle window allocates nothing. Profile with:

```sh
go test -run '^$' -bench BenchmarkDemoFrame -benchmem -memprofile mem.out -memprofilerate=1 .
go tool pprof -sample_index=alloc_objects -top -cum mem.out
```

A 400-frame demo harness run (960×640, scale 1, input: name Ada then continue) recorded `submit_to_commit_ns` median **184,545 ns** and nearest-rank p95 **481,550 ns** in `frame-timings.jsonl`. This is host-side submission-to-Wayland-commit latency, not GPU execution time. `atlas_pages` stayed at **1** allocated 1024×1024 grayscale page (maximum 4) for all 400 frames. These figures depend on the compositor, driver and machine load.
