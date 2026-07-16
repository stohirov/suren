# suren — native-GPU renderer plan

> **What this document is.** This is the project's lab notebook, kept verbatim
> rather than summarized. It was written as a plan and it became a record: each
> phase carries what was measured, what the measurement overturned, and what was
> tried and reverted. It is the source of truth for what exists and what does
> not.
>
> **Completed phases are kept for their measurements, not as status.** A ✅ here
> does not mean "shipped, move on" — it means the numbers under it are the
> evidence for a claim made elsewhere in the docs. The reverted work is kept for
> the same reason: the precomputed-backdrop pass (Phase 8) is a full
> implementation that was measured and removed, and that measurement is why the
> current design is what it is. Deleting it would leave the design unexplained.
> Several findings here also record a prediction this project got *wrong* — the
> mesh crack, where the CPU reference was the buggy backend, is the clearest —
> and those are the most useful entries in the file.
>
> **⏳ means not done.** Phases 18–22 and 12d's non-Apple coverage are unbuilt or
> unrun. The register throughout is deliberate: where this project has not
> verified something, it says so rather than passing quietly.
>
> Reading order for a newcomer: [README](../README.md) →
> [architecture](architecture.md) → [correctness](correctness.md) → this file.
> The narrative distillation of Sections A/B lives in
> [correctness.md](correctness.md); this file is the primary record it cites.

The CPU vector engine (retained scene → AA rasterizer → PNG/SVG/window, gradients,
strokes, clips) is **complete**. That engine's CPU-bound frame time — logged by the
Ebiten window backend (~14 ms/frame, ~68 fps) — is the motivation for a second
`render.Renderer` that runs on the GPU. This document is the roadmap for that work;
the original CPU-engine plan is preserved in git history at commit `cf8f167`.

## Guiding decisions (these shape the GPU port)

1. **The `render.Renderer` seam is the whole point.** `Render(*scene.Scene) error`
   already abstracts CPU vs SVG vs window. The GPU renderer is one more implementor;
   nothing above the backend changes.
2. **The retained `scene.Scene` feeds the GPU directly.** Whole-frame geometry is
   exactly what a GPU pipeline needs to batch. The encoder flattens it into flat GPU
   buffers (segments, per-node records, gradient stops) — no per-call CPU assumptions.
3. **Correctness is defined as parity with the CPU renderer.** Every GPU stage is
   validated by rendering the same scene on both and diffing raw premultiplied RGBA
   within a tiny tolerance — a stronger, colorspace-free check than PNG comparison.
   The exact signed-area coverage algorithm is *ported*, not re-approximated.
4. **The cgo/native dependency is quarantined.** `backend/gpu` has its own `go.mod`
   (like `backend/window`), so `geom`/`path`/`paint`/`raster`/`cpu`/`png` stay
   literally zero-dep and pure-Go.
5. **Ship in parity-preserving increments.** Skeleton → fill parity → binning →
   tiling → gradients. The parity test is the safety net that made each rewrite safe.

## Package layout (additions)

```
backend/cpu/     // extracted shared scene→RGBA renderer (was inside backend/png)
backend/png/     // now just Encode() over cpu.Render
backend/gpu/     // native WebGPU renderer — own go.mod, cgo (cogentcore/webgpu)
  device.go        wgpu instance/adapter/device/queue lifecycle
  encode.go        scene → segments + node records + stops + 2D tile bins
  target.go        offscreen rgba8unorm storage texture + readback
  raster.go        compute pipeline, bind groups, dispatch
  raster.wgsl      the fine rasterizer (ported signed-area coverage)
  renderer.go      Renderer implementing render.Renderer
```

Technology: **WebGPU compute** via `github.com/cogentcore/webgpu` (cgo → wgpu-native,
Metal/Vulkan/DX12). Verified building and creating a Metal device headlessly on
darwin/arm64.

---

## Extraction — shared scene→RGBA renderer out of `backend/png`  ✅ `d390dda`

- [x] Move the scene-walking renderer (`Renderer`, `Render`, shaders) from
      `backend/png` into a neutral `backend/cpu` package; `png` becomes a thin
      `Encode` over `cpu.Render`; `backend/window` depends on `cpu`, not `png`.
- [x] Render-logic tests + golden images moved to `backend/cpu`.

## Phase 1 — instrument + cheap CPU wins (know the baseline)  ✅ `d390dda`

- [x] Benchmark harness over sample + many-nodes scenes; `oldRender` in-binary so
      before/after is one comparison.
- [x] Reuse one `raster.Rasterizer` across nodes (`Resize`) instead of allocating a
      framebuffer-sized rasterizer per node per frame.
- [x] Bbox-bounded sweep: accumulate/sweep/reset only the node's bounding box
      (`FillPaint` + no-alloc `path.TransformedBounds`). Fixed a latent off-by-one
      (a vertical edge at integer x writes column `floor(maxX)`; reset upper bound
      must be `floor(maxX)+1`, not `ceil`).

**Result:** sample 812 µs → 433 µs (1.9×); **many-nodes 2.33 s → 11 ms (210×)**,
allocation 14 GB → 1.4 MB/frame. Golden output byte-identical.

## Phase 2 — GPU skeleton  ✅ `8e94158`

- [x] Spike first: cgo build + headless Metal device creation confirmed.
- [x] `device.go` lifecycle; `encode.go` scene→buffers (pure Go, unit-tested);
      `target.go` offscreen texture + readback; `Renderer` satisfies
      `render.Renderer` (compile-checked).
- [x] "Present a cleared frame" validated offscreen (clear → texture → copy → map →
      readback → assert), since the sandbox has no display.

## Phase 3 — fill-only fine rasterizer, solid paint, CPU parity  ✅ `ba8be86`

- [x] `raster.wgsl`: **one compute invocation per scanline**, porting the CPU
      signed-area algorithm exactly (no float atomics — each row-thread owns its row).
      Per node: accumulate segments crossing the row, horizontal running-sum sweep,
      premultiplied src-over compositing → `rgba8unorm` storage texture.
- [x] All-scalar WGSL struct layout matching the Go struct's tight 4-byte packing,
      so `ToBytes` uploads map straight on with no std430 padding surprises.
- [x] `TestParitySolid`: GPU vs `cpu.Render`, raw RGBA diff. **Max channel delta 1**
      (final 8-bit quantization only).

## Phase 4 — binning + GPU-vs-CPU benchmark  ✅ `bb1c3bb`

- [x] Per-row node bins (built in `Encode`, scene order preserved) so a scanline only
      touches nodes whose Y-bbox covers it; X-bbox-bounded sweep per node.
- [x] Shared `sample.ManyNodes` scene; GPU-vs-CPU benchmark in one place; moved the
      readback copy out of the hot `Render` path into `ReadRGBA`.

**Result:** GPU ~6.6 ms vs CPU ~11 ms (~1.9×). Parity: sample Δ=1, many-nodes Δ=0.
Diagnosis: row-serial design caps parallelism at H threads; global scratch dominates.

## 2D-tiling rewrite (performance pivot)  ✅ `0d192c0`

- [x] 16×16 tiles; one thread per (tile-column, scanline) owning a 16-wide span with
      **private** `cover`/`area`/`fb` arrays — global scratch buffers deleted.
- [x] **Per-tile backdrop** keeps exact-coverage tiling correct: each binned node's
      segments route per-column cover to a scalar backdrop (left of tile) / private
      arrays (in tile) / ignored (right); the sweep seeds its accumulator with the
      backdrop. Closed paths wind to zero, so 2D bbox binning alone suffices.
- [x] `buildTileBins` (2D), 6 bindings, 2D dispatch. `TestParityClip` covers clips
      crossing tile boundaries.

**Result:** GPU **2.93 ms**, **3.75× over CPU** (10.99 ms); parallelism 720 → ~57,600
threads. Parity: sample Δ=1, many-nodes Δ=0, clip Δ=0.

## Phase 5 — gradients in the fine shader  ✅

- [x] Evaluate `Kind == Linear/Radial` in `raster.wgsl` per pixel: apply the node's
      inverse matrix to the pixel center, compute the gradient parameter, interpolate
      the stop table (pad spread) — the CPU `gradShader` math ported verbatim. Encoder
      already emitted gradient geometry, inverse transform, and stops; added a 7th
      binding for the stop buffer and premultiply-on-eval to match the CPU shader.
- [x] Parity test on `sample.GradientScene()` (linear fill, radial fill with a
      transparent end stop, gradient-painted stroke). **Max channel delta 1.**

**Result:** GPU matches CPU on the gradient sample scene (Δ=1, 8-bit quantization
only); solid/many-nodes/clip parity unchanged (Δ=1/0/0).

---

# Future implementations (post-parity roadmap)

Parity is done: the GPU renderer matches the CPU reference on solid, gradient, clip, and
many-node scenes. What remains turns that correct offscreen rasterizer into a real-time,
scalable, feature-complete renderer. Phases are ordered by value and dependency; within
each, the parity/roundtrip tests stay the safety net that let every prior rewrite land.

**Dependency sketch:**

```
Phase 6 (present) ─┬─ needs 6a target/rasterizer Resize ──► reused by 7a buffer reuse
                   └─ 6b native surface (blit tested headless; window on-device)
Phase 7 (frame cost) ── independent of 6, but makes 6 worth watching (fast frames)
Phase 8 (coarse lists) ── needs Phase 7's EncodeInto scratch reuse to stay cheap
Phase 9 (GPU flatten/stroke) ── gated on Phase 8 diagnosis: only if CPU encode dominates
Phase 10 (feature parity: blend modes, path clips) ── touches BOTH cpu + gpu + wgsl
```

## Phase 6 — windowed present (real-time on screen)  ✅

The whole motivation was the CPU window's ~14 ms/frame. The GPU renders offscreen today;
this phase puts it on a live surface. Ship in two steps so a regression is visible early.

### 6a — interim bridge through the existing Ebiten window  ✅

- [x] `window.RunGPU`: a `gpu`-backed `backend` alongside the CPU one, sharing the loop.
      Per frame `Render(scene)` → `Sync()` → `ReadRGBA()` → `screen.WritePixels`; reuses
      the proven readback path (`target.readRGBA`, `align256`). `backend/window` now
      requires the quarantined `backend/gpu` module (`replace ../gpu`).
- [x] `logTiming` labels the active backend (`cpu raster` / `gpu raster`) so the two
      compare in one run; example takes a `-gpu` flag to pick the backend.
- [x] `target.resize` + `Renderer.Resize(w,h)` (rasterizer w/h + dims uniform) reallocate
      the storage texture and readback buffer on window resize; `Layout` drives it and the
      CPU backend reallocates its `image.RGBA` in lockstep. `TestResizeParity` (gpu, Δ=0
      after resize) + `TestBackendParityAndResize` (window: cpu vs gpu backend, initial and
      post-resize, Δ≤2) verify it headlessly since the sandbox has no display.

**Result:** GPU renderer drives a window via the readback bridge; CPU/GPU backends produce
matching pixels through the exact `frame`/`resize` code the loop calls. Live per-frame
comparison and the display itself are on-device (the sandbox has no GL context). Accepts
one GPU→CPU→GPU roundtrip — removed in 6b, where it was priced at **~0.4 ms and 3.69 MB per
frame** on unified memory (1280×720; `BenchmarkPresentViaReadback` vs `ViaBlit`). Smaller
than this phase assumed; see 6b for why, and for the benchmark bug that first said 2.5 ms.

### 6b — native wgpu surface present (`present.go`, on-device)  ✅

- [x] glfw window + `wgpu.Surface`, quarantined behind `//go:build gpupresent`. Verified
      rather than asserted: `go list -deps -test ./...` links **0** glfw packages untagged
      and 2 tagged, so the default build and the CI test binary carry no display library.
- [x] The compute shader writes a `storage<rgba8unorm, write>` texture; swapchain images
      are `RenderAttachment`, **not** storage-writable. So: compute into the offscreen
      target as today, then a fullscreen-triangle **blit pipeline** writes the frame.
      `target` gained `TextureBinding` so the blit can read it.
- [x] Present loop: acquire → dispatch → blit render pass → present; on resize, reconfigure
      the surface **and** call `target.resize` from 6a. A failed acquire (invalidated
      swapchain) reconfigures and drops the frame rather than erroring.
- [x] The surface is created **before** `RequestAdapter` and passed as `CompatibleSurface`,
      so the adapter is filtered for one that can actually present to this window
      (`newDeviceForSurface`). `NewDeviceOn` cannot do this — it has no window to bind —
      hence `newRendererOnDevice`.
- [x] Teardown is ordered and single-path. The first cut released the renderer before the
      surface, which was wrong: `Renderer.Release` bundles queue+device+adapter+**instance**,
      so it took the instance out from under a live surface. `Presenter.Release` now goes
      blit → surface → device → window → `glfw.Terminate`, tolerates a half-built Presenter
      (every constructor error path calls it), and is proven to run — `-frames N` closes the
      window from inside the loop, and both demo modes exit 0 through it with no validation
      error. Killing a window can never test an unwind.

**Measured (Apple M4, Metal, darwin/arm64):** `surface format=bgra8unorm present=immediate
framebuffer=1280x960 scale=2x`. The sample scene and the animated scene both present with
no readback.

**The format prediction was wrong, and in the useful direction.** This plan said the surface
format "is typically `BGRA8UnormSrgb`" and budgeted a shader conversion for it. The M4's
surface offers plain **`bgra8unorm`**, so `pickFormat` takes it and the blit is an exact
passthrough — no sRGB conversion runs on this host at all. Two things follow. The channel
half of "RGBA→BGRA" was never work either: BGRA8Unorm is a *memory* layout, and a fragment
shader still writes `.r` to red, so the swizzle is the hardware's and only surfaces when
reading bytes back as an `image.RGBA`. And the sRGB half is a real path that this machine
cannot exercise — which is why it is tested rather than trusted (below).

**Δ=0, not "eyeball it".** This phase planned to validate by screenshotting a window and
comparing against the offscreen PNG by eye. That step was replaced by a stronger one and the
plan was wrong to accept it. A swapchain image is a texture; pointed at an offscreen texture
*of the surface's own format*, the real blitter is exercised headlessly, and
`TestBlitToUnormSurfaceIsExact` gates it at **Δ=0** against the very target the PNG comes
from — the triangle covers the frame, the fetch does not shift or flip it, the format is
handled. So the blit lives in `blit.go`, **outside** the tag; only the window plumbing is
behind it. Behind the tag it would have been the one new thing in the pixel path that CI
could never run.

The sRGB path is gated the same way even though no local surface uses it:
`TestBlitToSRGBSurfaceRoundTrips` measures **Δ=0** but holds the gate at the quantization
floor (Δ≤1) — it crosses a float pipeline (a WGSL `pow` against the driver's own encode),
which is precisely the contract's stated case for the floor; pinning it to 0 would pin
Metal's `pow`, not the round trip. And because a passing test proves nothing if the bug it
guards is invisible, `TestSRGBSurfaceWouldWashOutWithoutLinearize` forces the passthrough
shader into an sRGB surface and asserts it **fails**: the double encode moves **73/255** at
its worst, over 30,830 channels. Without that, the linearize could have been a no-op.

**Retina forced a decision 6a never faced.** `GetFramebufferSize` is 2× `GetSize` here, and
the blit is 1:1. Rendering at *point* size would have filled a quarter of the window. So the
renderer draws at framebuffer resolution and `RunPresent` pre-scales the canvas by the
content scale — which is also just the right answer for a GPU rasterizer.

**What the roundtrip cost — and the first answer was ~6× too good** (1280×720, `ManyNodes`,
headless, `-benchtime 1000x`, 5 runs):

| present path | ns/op (range) | B/op |
|---|---:|---:|
| dispatch only (`BenchmarkGPUDispatchManyNodes`) | 1.79–2.21 ms | — |
| via readback — 6a's approach (`BenchmarkPresentViaReadback`) | **2.56–3.14 ms** | **3,687,779** |
| via blit — 6b (`BenchmarkPresentViaBlit`) | **2.23–2.53 ms** | **1,136** |

The blit saves roughly **0.4 ms/frame** and the whole 3.69 MB. Not 2.5 ms — which is what
this section claimed until the benchmark was corrected, and the correction is the entry
worth keeping.

**The bug was in the benchmark, and it flattered us.** The readback loop was written as
`dispatch → Sync → ReadRGBA`, mirroring `window.RunGPU`'s frame verbatim. But `ReadRGBA`
already polls until its map completes, which cannot happen before the dispatch and the copy
have — so the leading `Sync` is a *second* full stall, and the blit path only ever paid one.
That redundant stall, not the readback, was most of the "2× faster" result. Copying the
production code into the benchmark felt like fidelity and was actually a rigged comparison:
6a's redundancy is a bug in 6a, not a cost of readback, and beating it proves nothing.
Priced against a readback that syncs once, the win is ~15%.

**Why so small: this machine has no bus to cross.** The framing throughout Phase 6 was
"GPU → CPU → GPU per frame", which reads as a PCIe transfer. On Apple silicon memory is
unified, so the readback is a shared-memory copy plus a map round trip — real, but nothing
like the cost the phrase implies. On a discrete GPU the same benchmark should separate much
further, and **this project has no discrete GPU to check that on** (12d's standing limit),
so the claim is scoped to unified memory and no further.

**What is unambiguous is the allocation:** 3,687,779 → 1,136 B/op. That figure is exactly
1280×720×4 — the frame itself landing on the Go heap every frame, hitting the collector,
and it is simply gone. Note also what neither number includes: `window.RunGPU` then hands
those bytes to `screen.WritePixels`, which uploads them *back* to the GPU. The benchmark
prices the GPU→CPU leg only, so it understates the full bridge — but the fix for that is to
measure it, not to assume it.

**The window's own fps could not answer that**, which is why the benchmark exists. Under
vsync the demo reports 8.33 ms / 120.0 fps — 1/120 to three digits, i.e. the *display*, not
the renderer; a renderer 6× faster would print the same number. `Options.Unsynced` picks
`Immediate` and the same scene runs **~1.3–1.5 ms/frame (~650–770 fps)** at 1280×960. Even
that is only indicative: macOS throttles an unfocused window, and the first attempt to
compare 6a's bridge this way produced 9.4 fps from a backgrounded Ebiten window. Hence the
headless pair above, and hence `PresentMode()` is exported — a frame rate read without
checking it is a claim about the monitor.

**Known gaps, carried deliberately:**

- **A failed acquire cannot be detected** with cogentcore/webgpu v0.23.0 — it drops
  `WGPUSurfaceTexture.status` and hands back a NULL texture with a nil error, which
  `CreateView` would turn into a process abort. `Frame` refuses to acquire from a minimised
  window, which removes the reachable trigger; the residual (a >1s stall under load) needs
  an upstream fix. See the risk register.
- **Live resize on macOS draws nothing.** The drag runs a nested Cocoa run loop, so
  `PollEvents` does not return until it ends; the documented answer is a window-refresh
  callback, which this does not register. The window snaps to the new size on release.
- **A resized window does not rescale the scene** under `RunPresent` — the callback is
  handed a canvas and never learns the new size, so the scene keeps its authored extent.
  Same as `window.Run`. Drive a `Presenter` directly and read `Size()` to follow the window.
- **The blit rebuilds its bind group every frame** (~57 allocs/op), as `rasterizer.run`
  does. Consistent with the existing code and not on the measured critical path; Phase 7's
  argument for caching applies here whenever it is worth making.

**Done:** the sample scene presents directly from GPU memory with no readback on
darwin/arm64 (Metal). CI gates the blit at Δ=0 offscreen; the window itself stays on-device.
Untested elsewhere: no Vulkan/DX12 host was available (12d's standing limit), so the sRGB
blit and any non-`bgra8unorm` surface remain covered by test, not by hardware — and the
present-path benchmark's answer is a unified-memory answer only.

## Phase 7 — kill per-frame cost (buffer reuse for static & animated scenes)  ✅

Every `Render` used to run `Encode` (fresh Go slices), `releaseBuffers`, then recreate all
five GPU buffers via `CreateBufferInit`. Encode was ~22% of the frame (~0.65 ms) and buffer
churn added more. Three independent wins, cheapest first.

### 7a — reuse GPU buffers in place  ✅ `7e483aa`

- [x] Keep the five `*wgpu.Buffer`s and `queue.WriteBuffer` into them when the new byte
      length fits; recreate (grow ×1.5 with slack) only when it doesn't. Buffers carry
      `BufferUsageCopyDst`; the shader indexes by node/tile records (never `arrayLength`),
      so slack past the written data is never read.

### 7b — reuse encoder scratch (no per-frame Go allocation)  ✅ `ca51963`

- [x] `EncodeInto(e *Encoded, s, w, h)`: reset slice lengths, keep capacity, append into
      the retained `Encoded` (the `Renderer` owns one). `path.FlattenInto` takes a reusable
      point-scratch buffer so per-path curve flattening stops allocating. `buildTileBins`
      rewritten as a **counting sort** into reused `TileOffsets`/`TileNodes`/cursor scratch,
      killing the per-tile `[]uint32` slices (the background rect alone touched every tile →
      ~3600 allocs/frame). Scene order within a tile preserved.

**Result:** `BenchmarkEncodeManyNodes` **10136 → 0 allocs/op** (2.87 MB → 0 B/op), encode
647 → 433 µs. GPU per-frame (changing scene) **10187 → 51 allocs/op, 2.87 MB → 976 B/op**,
4.45 → 3.32 ms — the residual is cgo command-encoder/bind-group churn, not the encoder.

### 7c — skip work when the scene is unchanged  ✅ `7f69d05`

- [x] FNV-1a fingerprint (word-wise, via `unsafe`, 0-alloc, no cgo pulled into the pure-Go
      encoder) over `Width/Height` + flattened `Segments`+`Nodes`+`Stops` bytes, computed in
      `EncodeInto`. If it matches the last frame's, `Render` skips the upload **and** the
      dispatch and re-presents the retained target texture. `Resize` invalidates the retained
      frame (`haveFrame=false`). `TestUnchangedSceneSkips` asserts a dispatch counter: skip on
      repeat, re-dispatch on change, correct pixels on both.
- [x] Trade measured: hash costs ~55 µs; it saves ~2.8 ms of upload+dispatch+sync when the
      scene repeats — 6.7× on static, 1.7% overhead on always-changing frames. Net keep.

**Result:** steady-state (unchanged scene) GPU **3.32 → 0.49 ms (6.7×)**, 0 allocs/op; parity
(solid Δ=1, many-nodes Δ=0, gradient Δ=1, clip Δ=0) and resize parity unchanged; window
cpu-vs-gpu bridge parity unchanged.

## Phase 8 — coarse segment lists (scalability for complex paths)  ⏳ in progress

The known weak spot: each tile re-iterated a node's **full** segment list (`routeSeg`
early-outs by x but still loops every segment). For a single huge many-segment path spanning
many tiles that's O(tiles × segments) redundant work — the Vello coarse-pass answer removes it.

### Measure first  ✅ `d511207`

- [x] `sample.ManySegments` (one dense star polygon: 2 nodes, ~4 k line segments, bbox over
      most of the canvas) + `BenchmarkGPUDispatch*` (dispatch-only, bypassing 7c's skip) and
      a `TestPhase8Redundancy` diagnostic that counts naive segment-scans from the encoder.

**Measured (1280×720):** many-segs did **127 M** segment-scans (**31,706×** amplification) vs
many-nodes' 1.4 M (86×), and its GPU dispatch ran **5.34 ms vs 2.38 ms** — 2.2× slower on 4×
*fewer* segments. The redundancy is real on a realistic single complex path.

### Per-tile segment lists  ✅ `7c00227`

- [x] Encoder emits per-(tile,node) segment sublists (`TileSegOff`/`TileSegIdx`) via a
      **segment-centric scatter** (O(segment-memberships), not O(tiles × segments)), fully
      scratch-reused so encode stays **0 allocs/op**. Rule: a segment is listed in a tile iff
      its bbox y-band overlaps the tile **and** `minx < tile.right` — this keeps left-of-tile
      segments so the fine shader's existing per-scanline `routeSeg` backdrop is unchanged
      (precomputed backdrops deferred; that's the memory-optimal follow-up).
- [x] Fine shader iterates `tileSegOff[k]..tileSegOff[k+1]` (indices into `tileSegIdx`) for
      node-entry `k`, instead of `nd.segStart..segCount`. Two new storage bindings (7 storage
      buffers total, under the 8 limit).
- [x] Memory: a left segment is referenced by every tile to its right in its band — bounded,
      **not capped** (a cap would corrupt the backdrop). Measured `len(TileSegIdx)`:
      many-nodes 31 k refs (~125 KB), many-segs 722 k refs (~2.9 MB). Documented as the
      signal for the precomputed-backdrop upgrade if it ever grows.

**Result:** dispatch **many-segs 5.34 → 2.78 ms (1.9×)**, **many-nodes 2.38 → 1.54 ms (1.5×)**;
segment work cut **45×** (many-nodes) / **176×** (many-segs). Parity holds everywhere (solid
Δ=1, many-nodes Δ=0, gradient Δ=1, **many-segs Δ=1**, clip Δ=0, resize Δ=0). Encode adds the
scatter cost (many-nodes 433 → 747 µs, many-segs ~1.0 ms), still 0 allocs/op — paid per frame
only when the scene changes; a static scene still skips upload+dispatch via 7c.

### Remaining (optional, deferred)

- [x] **Precomputed per-scanline backdrop** (full Vello coarse pass) — *implemented, measured,
      reverted as a net regression.* A horizontal winding prefix-sum in the encoder produced a
      per-(tile-entry, scanline) backdrop so the fine shader lists only intersecting segments
      (segment refs many-segs **722 k → 174 k**, many-nodes 31 k → 21 k). Parity held exactly.
      But it **lost on every other axis** in a same-session A/B: encode **+41 %** (many-nodes
      748 µs → 1055 µs) / **+66 %** (many-segs 1.03 → 1.72 ms), dispatch neutral-to-worse
      (many-nodes ~1.57 → ~2.33 ms — an extra per-entry `f32` load, a 9th storage buffer, and a
      352 KB backdrop buffer), and it *added* net memory on typical scenes (the backdrop buffer
      dwarfs the ref savings). Root cause: the per-tile lists already made segment scanning
      cheap, so removing the residual left-segment scan buys almost nothing while its costs are
      real. Kept the per-tile-lists baseline. Revisit only if a memory-bound target (huge
      complex paths) makes segment-list bytes — not frame time — the binding constraint.
- [ ] **Skip `buildTiles` on unchanged scene** (7c refinement): fingerprint before the tile
      build and reuse last frame's tile slices — removes the coarse-pass encode cost on static
      scenes (restores the ~0.5 ms static frame).
      *Measured cost of not doing this (2026-07-16, M4/Metal, many-nodes 1280×720):* the
      static frame is now **0.76 ms**, not 7c's 0.49 ms, and `BenchmarkEncodeManyNodes` is
      **0.76 ms** — i.e. an unchanged frame is *entirely* `EncodeInto`, since 7c already skips
      the upload and the dispatch. This phase's segment scatter (encode 433 → ~750 µs) is the
      whole difference, exactly as predicted above. Nothing regressed; this is the known trade,
      recorded so the next reader does not re-derive it.
      The fix is a reordering, and `fingerprint()` does not stand in its way: it hashes
      dims + `Segments` + `Nodes` + `Stops` + `FallbackTiles` and **never reads the tile
      slices**. It is only *sequenced* after `buildTiles()` in `EncodeInto`, so `Render`'s skip
      check cannot fire until the scatter is already paid. Moving the hash (and
      `markFallbackTiles`, whose output it covers and which needs only node bboxes and
      `NTilesX/Y`) above `buildTiles`, then reusing last frame's tile slices on a match, is
      what restores ~0.5 ms. Note `FallbackTiles` must stay in the hash regardless — Phase 14's
      reason still holds: toggling the mark leaves `Segments`/`Nodes`/`Stops` byte-identical.
- [ ] **GPU coarse pass** if the CPU scatter ever dominates the frame for changing scenes.

**Done when (baseline):** parity holds on the pathological scene **and** all existing scenes,
with a measured speedup on the many-segment case and no regression on typical scenes. ✅

## Phase 9 — GPU-side flattening / stroke expansion (only if encode dominates)

Today `strokeOutline` (CPU) expands strokes to fill outlines and `appendSegments` flattens
curves to line segments on CPU. Gated strictly on measurement — currently encode is ~22%
and strokes are a fraction of that.

- [ ] **Flattening on GPU:** upload curve control points instead of pre-flattened
      segments; a compute pass does adaptive subdivision to tolerance → segment buffer.
      Do this first — it's the mechanical half and directly shrinks encode for curve-heavy
      scenes.
- [ ] **Stroke expansion on GPU:** the hard half (joins, caps, miters, dashes on GPU).
      Large undertaking; defer until flattening proves the CPU path is the bottleneck for
      stroke-heavy scenes.

**Done when:** `BenchmarkEncode*` shows encode as the frame bottleneck for a curve-/stroke-
heavy scene, and moving flattening to GPU measurably reduces total frame time at parity.

## Phase 10 — renderer feature parity beyond opaque fills  ⏳ in progress

These are engine features the CPU side also lacks or only partially has; each must land on
**both** renderers with a parity test, not GPU-only.

### Blend modes  ✅ `19e3ba3`

- [x] Added the W3C separable set to `paint.BlendMode` (Multiply, Screen, Overlay, Darken,
      Lighten, ColorDodge, ColorBurn, HardLight, SoftLight, Difference, Exclusion; SrcOver=0).
      Mirrored the enum in `raster` (like `FillRule`). Both renderers composite in the same
      premultiplied space, so the general path unpremultiplies → applies the separable blend
      `B(Cb,Cs)` → recomposes via the W3C formula `Co = αs(1-αb)Cs + αs·αb·B + (1-αs)αb·Cb`;
      `SrcOver` keeps the original fast premultiplied path on both sides.
- [x] `raster/fill.go` `blend` (f64) and `raster.wgsl` `composite`/`blendCh` (f32) are line-for-line
      the same formula; encoder passes `Node.Op` in the previously-unused `Node.Flags`. `Canvas.SetBlend`
      exposes it (Save/Restore-scoped state, zero value = SrcOver).
- [x] `TestParityBlendModes`: per-mode CPU-vs-GPU on `sample.BlendScene` (opaque / translucent /
      empty backdrop regions so αb spans its range). 10/12 modes **Δ≤1**; the two division-based
      modes (ColorDodge/ColorBurn) **Δ≤3** — f32-vs-f64 divergence at the `min(1,·)` clamp on
      4/172800 channels, a precision artifact absorbed by tolerance, not a logic error.
### Arbitrary-path clips  ✅ `17f58a6`

- [x] `scene.Node.Clips []ClipPath` holds **device-space** clip paths (+ fill rule); the
      rect `Clip` stays as a cheap bbox pre-filter (intersection of all clip bboxes). `Canvas.ClipPath`
      transforms by the CTM and force-copies onto the immutable clip stack so Save/Restore and
      nesting compose correctly. Rect clips still nest via bbox intersection.
- [x] **GPU:** encoder appends each clip path's segments to the `segs` buffer (not tile-binned —
      accessed directly), emits per-clip `ClipRec{segStart,segCount,rule}` into a new `clips`
      storage buffer (9th binding, 8 storage buffers total — at the default limit), and stores
      `clipStart/clipCount` on the node (reused the old `Pad`). The fine shader, per node-entry,
      runs a second signed-area sweep over each clip's segments → per-column coverage, multiplies
      them into a `clipf[16]` factor, and multiplies that into the fill alpha before compositing.
      Simple clips iterate all their segments per tile (no per-tile clip lists) — fine since clips
      are small; a per-tile clip list is the optimization if a huge clip path shows up.
- [x] **CPU:** `Renderer.clipMask` rasterizes each clip path with the same `Rasterizer` and
      multiplies their coverages into one mask (product = intersection); `FillPaint` multiplies
      `alpha` by the mask. Same coverage algorithm as the shader → parity.
- [x] `TestParityClipPath` (single + nested) plus a clipped stroke: **Δ=1**; existing rect-clip
      parity unchanged (Δ=0). Nesting verified as `fill ∩ A ∩ B`.
- [ ] *Deferred:* per-tile clip segment lists (only if a complex clip path is slow); CPU mask
      caching across nodes that share a clip stack; SVG backend still ignores path clips.
- [ ] **Colorspace / sRGB correctness.** This was gated on "once 6b introduces an sRGB
      surface", and 6b did not: the M4's surface offers `bgra8unorm`, so `pickFormat` takes
      the non-sRGB format and the blit is a passthrough that never converts. The item is
      therefore **not** unblocked by 6b and is not answered by it — it is still Phase 19's,
      and it is now the *encoding in the target itself* that is in question, not the
      surface's. 6b only pinned down what the blit does with whatever the target holds.

**Done when:** each feature renders identically (within tolerance) on CPU and GPU via a
dedicated parity scene. (Blend modes ✅; path clips ✅; sRGB remains, and 6b did not gate
it after all — see Phase 19.)

## Cross-cutting (fold into the phases above, not standalone work)

- **Dynamic resize** — introduced in 6a (`target.resize`), consumed by 6b's surface
  reconfigure; the renderer must never assume a fixed `w,h` after construction.
- **Device-loss / error surface** — wire wgpu's uncaptured-error callback and return
  errors from `Render` rather than logging; relevant once a long-lived present loop exists.
- **Huge canvases** — a canvas exceeding the max 2D texture size needs framebuffer tiling
  (render in texture-sized chunks). Note now; implement only when a real target needs it.

---

## Testing strategy

- **GPU/CPU parity** is the primary correctness gate: same scene both renderers, raw
  premultiplied RGBA diff within tolerance (solid, many-nodes, clip, gradient today; add a
  pathological many-segment scene in Phase 8, per-mode blend and path-clip scenes in
  Phase 10). Since Phase 12 the scenes are a **named corpus** (`internal/corpus`, each entry
  carrying the tolerance it earned) plus **generated** scenes — property laws (12b) and a
  differential fuzzer (12c) whose finds land back in the corpus as `regress/*.json`.
- **Independent oracles per feature** — the third leg, and the one the other two cannot replace.
  Parity compares the two backends against *each other* and a golden compares the CPU against
  *itself*, so both are blind to a semantic error the renderers share and to a bug in the reference.
  Phase 16 hit the second case for real: the reference cracked a mesh along every interior diagonal
  and the GPU was right, so the golden would have recorded the crack as expected output forever. Each
  feature therefore also gets a test that hand-computes what it MEANS, asking neither renderer —
  `raster.alphaOracle` (Porter-Duff, from coverage geometry), `TestConicParameterMapping` (hand-
  computed angles), `TestMeshAtInterpolatesBarycentrically` (corners, centroid, edge midpoints),
  and Phase 17's set in `paint/image_test.go`: a hand-written `EdgeMode.Wrap` table (Mirror's period
  is 2n, and the plausible off-by-one — reflecting about the texel centre — would make both backends
  agree with each other and disagree with every other renderer), `TestBilinearAtATexelCentreIsThatTexel`
  (weights are exactly (1,0,0,0) there, so a half-texel shift in either direction is caught), and
  `TestTexelIsNotTransposed` over a non-square image.
- **Encoder unit tests** (pure Go, no GPU): node/segment/stop counts, kinds, bbox,
  clip flags, tile-bin structure; per-tile segment ranges (Phase 8); atlas layout, dedup and the
  texel fingerprint (Phase 17 — a wrong atlas origin that happens to land on identical texels is
  invisible to a pixel gate).
- **Headless GPU roundtrip** (`TestDeviceInit`, offscreen render + readback) so the
  full pipeline is exercised without a display. Stays the CI gate. 6b shrank what "on-device
  only" covers: a swapchain image is just a texture, so the blit is gated headlessly against
  one of the surface's own format (`TestBlitToUnormSurfaceIsExact`, Δ=0), and only the
  acquire/Present pacing is left to manual validation. When something is display-only, the
  question worth asking is which part actually is.
- **Per-backend reconciliation** (`TestReconcileBackends`, Phase 12d) runs the corpus on each
  native backend the host exposes and reports the ones it could not. On Apple silicon that is
  Metal alone, so a green `go test` proves Metal parity and explicitly disclaims the rest.
- **Benchmarks** — `-benchmem` guards Phase 7 (allocs/op → ~0; steady-state frame time);
  a dedicated many-segment benchmark guards Phase 8; encode-heavy scenes gate Phase 9.

## Risk register

| Risk | Mitigation |
|---|---|
| Exact coverage on GPU diverges from CPU | port the algorithm verbatim; per-tile backdrop; raw-RGBA parity test on every stage |
| cgo/native dep won't build or run headless | spiked before committing — Metal device confirmed headless; dep quarantined in its own module |
| No display in CI/sandbox | validate offscreen (readback); windowed present is on-device only. 6b narrowed this: the blit is tested headlessly against a texture of the surface's format (Δ=0), so only acquire/Present is display-only |
| Tiling breaks winding across tile edges | backdrop routing + clip-across-tiles parity test |
| Per-frame encode becomes the bottleneck | measured (~22%); Phase 7 buffer/scratch reuse, Phase 9 GPU flattening |
| Huge many-segment path × many tiles (backdrop re-iteration) | acceptable now; Phase 8 coarse per-tile segment lists |
| Swapchain image not storage-writable (can't compute into surface) | ✅ 6b computes offscreen then blits via a fullscreen render pass |
| Surface format is BGRA/sRGB, not linear RGBA | ✅ measured, not assumed: Metal offers `bgra8unorm` and `pickFormat` prefers non-sRGB, making the blit a passthrough (Δ=0). The sRGB path exists and is tested, plus a guard asserting the un-linearized version *fails*; no local surface exercises it |
| glfw drags a display dep into headless builds/CI | ✅ quarantined behind `//go:build gpupresent`; `go list -deps -test` confirms 0 glfw packages untagged |
| A window's fps measures the display, not the renderer | vsync pins it to the refresh exactly (8.33 ms / 120.0 fps) and macOS throttles unfocused windows; price present paths with the headless `BenchmarkPresentVia*` pair, and read `PresentMode()` before quoting any fps |
| **A failed surface acquire is unreportable by this binding** | ⚠️ **open, upstream.** wgpu signals Timeout/Outdated/Lost via `WGPUSurfaceTexture.status`; cogentcore/webgpu v0.23.0's shim returns only `ref.texture` and discards it, and its error scope only catches Validation errors — so a failed acquire arrives as (NULL handle, nil error) and `CreateView` would abort the process. Unreachable from Go: `Texture.ref` is unexported and every getter derefs it. Mitigated by never acquiring from a minimised window (the trigger this project can reach); a >1s stall under extreme load stays exposed. Real fix is upstream |
| Benchmarking the present path against production code | the readback benchmark first copied `window.RunGPU`'s `Sync`-then-`ReadRGBA` and so charged readback for 6a's redundant stall — a 6× overstatement. Mirroring production is not fidelity when production has a bug in it; benchmark the best version of the thing being replaced |
| Window resize invalidates fixed-size target | `target.resize` (6a) reallocates texture/readback/dims; surface reconfigured on resize (6b) |
| Static-scene fingerprint costs more than it saves | Phase 7c: measure hash cost vs upload+dispatch; keep only if net win |
| Ill-conditioned ops (dodge/burn) make the differential oracle meaningless | Phase 12c: measured with both backends correct; excluded from the generator, still gated by the corpus where inputs are bit-identical — at the plain floor since Phase 13 |
| GPU accumulates a tile in f32 while the CPU re-quantizes per node | Phase 13: shader rounds per node (`quant8`); `blend-stack-*` corpus entries gate it at Δ=0 at 64 layers, where a regression shows as Δ=10 |
| Go fuses FMA on some architectures, making CPU goldens arch-dependent | Phase 13: measured — fusion happens on arm64 but is unobservable at 8 bits (f64 has ~13 orders of headroom); all 21 goldens reproduce bit-for-bit pinned or not |
| A backend-selection API that is silently ignored reports parity for a backend that never ran | Phase 12d: `RequestAdapterOptions.BackendType` does exactly this — use the instance mask, and verify the adapter's own reported backend |
| "GPU parity" quietly means "parity on one laptop" | Phase 12d: reconciliation runs per named backend and **logs which ones it did not cover**; the claim is auditable from the test output |
| A generated scene renders nothing and passes every gate vacuously | Phase 12c: opaque background by construction; `parity.NonTrivial` skips the residue; the skip rate itself is gated at 3% |
| A fuzz find stops reproducing when the generator changes | Phase 12c: finds are stored as explicit `Spec` JSON, not seeds — a seed's scene shifted the moment two blend modes were excluded |
| A feature that cannot be made exact forces a tolerance widening on every scene containing it | Phase 14: per-tile CPU fallback confines the remedy to the node's own tiles; corpus keeps the full-frame gate at `Identical()` |
| **A feature that introduces NO arithmetic is assumed exact, and the assumption skips the only question that matters** | Phase 17: nearest sampling picks one texel and averages nothing, which is why the plan called it exact. It is a STEP FUNCTION, and `floor(q)` is a threshold with no headroom in front of it — measured Δ=255 from a 1e-9 nudge, both backends correct, magnitude set by the texels rather than by any arithmetic. The question is never how much arithmetic a feature does, it is whether the feature is CONTINUOUS in the pixel position. Bilinear, which the plan hedged against, is continuous and needs no hedge |
| A hardware sampler's filtering is driver-defined, so no CPU reference can match it | Phase 17: `textureLoad` and no sampler; the kernel is computed from `paint.Filter` on both sides. Same call as Phase 13's `quant8` — an implementation-defined rule cannot be half of a parity contract |
| A paint whose pixels live outside the scene graph is mutated in place and the frame skip serves a stale frame | Phase 17: `Encoded.Atlas` bytes are folded into the fingerprint. `Segments`/`Nodes`/`Stops` record where an image sits and how to sample it, never what is in it — Phase 14's `FallbackTiles` trap one field up. Priced (~155µs/MB) rather than dodged by declaring `Pix` immutable |
| A paint whose colour is DISCONTINUOUS in the pixel position amplifies the f32-vs-f64 floor to the full distance between two stops, with both backends correct | Phase 16: conic's seam. Unlike dodge/burn there is no derivative to bound — the magnitude is the stop colours' — so no budget can be fitted. The generator emits closed loops only (`randConic`), and the hazard is measured rather than asserted (`TestConicSeamDivergesWithoutBound`: Δ=255) so the restriction has evidence behind it |
| **The CPU reference is the buggy one, so every gate that compares against it agrees — and the golden records the bug as expected output** | Phase 16's mesh crack, the first instance: two triangles rejected a point on their shared edge because f64 landed both weights a hair below zero, and the f32 GPU was *right*. Goldens are generated by the reference and cannot see this; only the differential can, and only where a second implementation disagrees. The general defence is what Phases 15/16 added per feature — an INDEPENDENT oracle (`alphaOracle`, `TestConicParameterMapping`, `TestMeshAtInterpolatesBarycentrically`) that hand-computes what the feature means rather than asking either renderer |
| A predicate whose two branches are computed by different expressions is exact in real arithmetic and unreliable in float | Phase 16: `l >= 0` in one triangle vs `1-l0-l1 >= 0` in its neighbour are complementary on paper and independent roundings of the same zero in practice, so both can fail and leave a crack. `paint.MeshEps` gives the test slack in normalized units; the fix is that both backends make the SAME choice, not the right one |
| An adversarial probe of a float hazard aims at the right pixel and measures nothing | Phase 16: the first seam probe was Δ=0 — putting the seam *on* a pixel centre is not enough, since both precisions round the same true angle to the same f32. The hazard needs the true value inside f32's blind spot; the nudge=0 control is kept so the probe cannot silently stop probing |
| A bbox estimate that was merely advisory becomes load-bearing when a new caller uses it to DROP work | Found in review after Phase 15: `cpu.nodeBounds` padded a stroke by `w/2`, but a miter reaches `miterLimit*w/2` (4x by default) — measured 8.9px short. Harmless while `culled()` only dropped fully off-canvas nodes; Phase 14's `tileCulled` drops against a few flagged tiles, so a miter spike escaping into one made the CPU patch overwrite correct GPU pixels (Δ=220, visible corruption). Bound now comes from `path.Stroker.MaxExtent`; `fallback-stroke_test` pins it |
| A scene built to prove the fallback works is exact on GPU anyway, so the gate measures nothing | Phase 14: nearly shipped exactly this — the first probe was Δ=0 with the fallback off. Fixed by a node that diverges by measurement, and `TestFallbackBuysExactness` now asserts the off/on difference rather than the on result |
| The fallback becomes the cheap general answer to "it's not exact" | Phase 14: priced at ~6µs/tile and recorded — full-frame fallback is ~14× a GPU frame and worse than the CPU backend. The benchmark reports `%frame` so a feature cannot quietly fall back on everything |

## Critical files

- `render/render.go` — `Renderer` interface, the CPU/GPU seam
- `internal/parity/contract.go` — the tolerance contract every gate in the tree cites
- `internal/corpus/corpus.go` — the named scene corpus (+ `regress/` for fuzz finds)
- `internal/parity/fuzz/` — scene generator, shrinker, bisect (Phase 12c)
- `backend/cpu/cpu.go` + `raster/fill.go` — CPU reference the GPU must match
- `raster/tile.go` — `TileMask`: gates a fill's writes without touching its coverage sweep (Phase 14)
- `paint/paint.go` — paint types + `MeshAt`/`MeshEps`: the canonical mesh evaluator `raster.wgsl` ports
- `paint/image.go` — `Image`/`Filter`/`EdgeMode` + `ImageAt`/`EdgeMode.Wrap`: the canonical sampler
  `raster.wgsl` ports, and where nearest's discontinuity is recorded (Phase 17)
- `backend/gpu/atlas.go` — the image atlas texture + upload skip (Phase 17)
- `backend/gpu/fallback.go` — per-tile CPU fallback + `Stats` (Phase 14)
- `backend/gpu/encode.go` — scene → GPU buffers + tile bins (Phase 7 `EncodeInto`, Phase 8 coarse lists)
- `backend/gpu/raster.wgsl` — the fine rasterizer (Phase 8 per-tile lists, Phase 10 blend/clip)
- `backend/gpu/renderer.go` — encode → upload → dispatch (Phase 6 `Resize`, Phase 7 buffer reuse)
- `backend/gpu/target.go` — offscreen texture + readback (Phase 6a `resize`, 6b `TextureBinding`)
- `backend/gpu/blit.go` + `blit.wgsl` — target → surface-format blit, format/present-mode choice
  (Phase 6b). Deliberately **untagged**: it is the pixel path, so CI tests it headlessly.
- `backend/gpu/present.go` — glfw window + surface + present loop (Phase 6b; `gpupresent` build
  tag). Only the parts that genuinely need a display.
- `backend/window/window.go` — window loop; gains a GPU-backed variant (Phase 6a)

---

# Part II — correctness-hardening & feature-completeness roadmap

Part I got the GPU renderer to *parity on the scenes we hand-wrote*. Part II makes parity a
**machine-proven property over a large input space**, then spends that safety net on the
remaining rendering features. The order is deliberate: build the correctness machine first
(Section A), make cross-backend determinism achievable (Section B), then land features
(Section C) each gated by the new machine, and instrument throughout (Section D). Section E
records the two scope decisions that bound everything else.

**What is already done (do not re-plan):** fill rules (NonZero + EvenOdd, `raster/raster.go`
`coverage()`, GPU `encode.go:98`), the W3C separable blend set (Phase 10), linear/radial
gradients (Phase 5), rect + arbitrary-path + nested clips (Phase 10), and the tile-based
compute pipeline (2D-tiling rewrite + Phase 8). Those appear below only as the baseline the
new work extends.

**Cross-cutting principle (unchanged from Part I):** every feature lands on **both** CPU and
GPU with a parity test in the same commit. GPU-only or CPU-only is never "done".

**Dependency sketch (Part II):**

```
Section A (correctness machine)
  11 contracts (tolerance + AA + scope) ── pure spec, gates every "Done when" below
  12a golden corpus + perceptual ΔE/SSIM ─┐
  12b property invariants ────────────────┼─ all consume 11's tolerance definition
  12c differential fuzz + shrink + bisect ─┘   and the shared sample corpus
  12d cross-platform reconciliation ✅ harness ── needs a non-Apple runner for coverage

Section B (make exactness reachable)
  13 float determinism controls ✅ ── rounding pinned; what it could not control (FMA
     contraction) is named and handed to 12d
  14 per-tile CPU fallback ✅ ── the escape hatch when 13 can't make a feature exact;
     priced at ~6µs/tile, so it is a local remedy and never a global one

Section C (features — each uses A as its gate, B where exactness is at risk)
  15 Porter-Duff operators ─┐
  16 conic + mesh gradients ─┤ independent of each other; all touch cpu + gpu + wgsl
  17 patterns + image sampling ✅ ┤ needed 11's AA/filter contract; the tiled-SCENE half
                                 │   waits on 18's offscreen-layer pass
  18 group opacity + masks ──┘ 18 needs an offscreen-layer pass (new compositing stage)
  19 sRGB vs linear toggle ── merges Part I's deferred sRGB item; interacts with 6b surface
  20 stroking parity audit ── mostly CPU-done; question is the GPU expansion story
  21 unified scene/command encoding ── refactor; 15–20 make the duplication expensive first
  24 SVG conformance audit ── independent of all of the above; NOT a parity question,
     which is why it drifted: five silent gaps, three of them wrong output rather than
     missing output (a Multiply node exports as Normal)

Section D (observability — build alongside C, not after)
  22 per-stage timing + tile occupancy + visual-diff overlay + equal-correctness bench

Section E (scope) — 23 text/glyph parity (in/out decision, recorded below)
```

---

## Section A — correctness / test infrastructure

The single highest-leverage investment: turn "parity on N hand-written scenes" into "parity
proven over a generated input space, with failures automatically minimized to a reproducible
seed." Everything in Sections C/E rides on this.

### Phase 11 — the correctness contract (spec, no rendering code)  ✅

Write down what we're actually promising, because every "Done when" below cites it. Lives as a
doc comment block (`internal/parity/contract.go`) plus this section.

- [x] **Tolerance, defined once.** State per-channel premultiplied RGBA tolerance and *why it
      is what it is*: Δ=0 is required wherever both backends run the same integer/analytic path
      (many-nodes, clips today); Δ≤1 is the floor set by final 8-bit quantization of independent
      float pipelines (f64 CPU vs f32 GPU rounding the same value); Δ≤3 is admitted **only** for
      documented division-based divergence (ColorDodge/ColorBurn `min(1,·)` clamp, Phase 10) and
      each such site must name the operation. A tolerance above the quantization floor is a
      **bug budget with an owner**, not a free parameter — new features may not silently widen it.
      Landed as three constructors — `Identical()` / `Quantized()` / `Budget(tol, why)` — with
      `Config.Validate` **rejecting** any tolerance above `QuantizationFloor` whose `Why` is empty,
      so the "name the operation" rule is enforced by the compiler-adjacent path, not by convention.
- [x] **Two comparison modes, named.** *Exact* = raw premultiplied-RGBA max-channel-delta (the
      current gate, colorspace-free). *Perceptual* = ΔE (CIE76/CIEDE2000 over sRGB→Lab) and SSIM,
      used **only** for features where exactness is provably unreachable (image resampling with
      f32 kernels, mesh-gradient interpolation) — and where used, the exact-mode failure that
      forced it is recorded. Perceptual mode never *replaces* exact mode; it's a second gate for
      a named subset. `Mode` is declared now; `Compare` **errors** on Perceptual rather than
      silently passing, so 12a implements it against a gate that already refuses to lie.
- [x] **The AA contract (explicit).** Today both backends use **analytic** signed-area coverage
      (`raster/raster.go` `coverage()`, ported verbatim to `raster.wgsl`) — one coverage value
      per pixel, no sampling. Declare analytic AA the contract. Record that MSAA and supersampling
      are **alternative** AA models that would break bit-parity with the analytic path, so they are
      out of scope for the parity gate; if ever added they get their own golden set at perceptual
      tolerance, never compared against analytic output. This closes the "analytic vs MSAA vs
      supersampling" question by *choosing analytic and saying why*.

**Result:** `internal/parity` (`contract.go` spec + types, `compare.go` `Compare`/`Assert`/`Result`,
stdlib-only so the zero-dep packages are untouched; importable from the quarantined `gpu`/`window`
modules the way `internal/sample` already is). Every parity gate in the tree now names a `Config`;
no tolerance literal survives outside the package. `Result` reports the max delta **and its
location** — the seed of Phase 22's visual-diff overlay.

**The gates got tighter, not just centralized.** Every test previously gated at a blanket `tol=2`,
which let the Δ=0 scenes regress to Δ=2 undetected and over-budgeted ColorDodge. Re-measured each
scene and pinned it to its true floor: many-nodes / rect-clip / post-resize / window-resize now gate
at **Identical (Δ=0)**; solid / gradient / many-segs / path-clips / 10 blend modes / window-gradient
at **Quantized (Δ≤1)**; only ColorDodge (**Δ≤2**, measured — was over-budgeted at 3) and ColorBurn
(**Δ≤3**) carry a named `Budget`. All pass at the tightened gates. Note the CPU quantizes via
`clamp8(v+0.5)` (round-half-up) while the GPU's f32→u8 conversion is the driver's on the
`rgba8unorm` store — the two rounding rules are the Δ≤1 floor's actual cause, and pinning them is
exactly Phase 13's job.

*Scope call:* the `>2` in `raster/fill_test.go` is a single-backend assertion against a hand-computed
blend value, and `tol` in `path`/`raster` flattening tests is a geometry tolerance — neither is a
cross-backend parity gate, so both correctly stay outside the contract.

### Phase 12 — the differential test machine  ✅ (12a–12d; 12d's coverage awaits non-Apple hardware)

Four capabilities on one shared corpus + generator. Order is cheapest-first; each is independently
useful.

#### 12a — golden image corpus with exact + perceptual modes  ✅

- [x] Promote `internal/sample` scenes into a **named corpus** (`internal/corpus`) — each entry
      `{name, build func() *scene.Scene, tol parity.Config}`. The existing `TestParity*` scenes
      seed it; feature phases in Section C each add entries. **19 entries**: solid, gradient,
      many-nodes, many-segments, clip-rect, clip-path, clip-path-nested, blend-× 12. The seven
      hand-written `TestParity*` functions collapsed into one `TestParityCorpus` loop; the rect-clip
      scene moved out of the gpu test file into `sample.ClipRectScene` so both backends share it.
- [x] Extend `internal/goldentest` beyond byte-exact PNG: add `AssertExact` (premultiplied RGBA
      delta, the current gate) and `AssertPerceptual` (ΔE + SSIM with per-entry thresholds). Golden
      images stored per corpus entry; `-update` regenerates. The GPU parity tests become
      "render corpus entry on both backends, assert per its `tol`."
- [x] **Perceptual mode implemented** (Phase 11 declared it and made `Compare` refuse it).
      `Perceptual(maxDeltaE, minSSIM, why)`; `Validate` demands the `why` exactly as `Budget` does.
      **It still has no customer.** It was built for the two phases that were expected to need it and
      both refused it by measurement: mesh interpolation (Phase 16) and bilinear image sampling
      (Phase 17) each landed at the exact floor. The one feature that genuinely cannot be gated
      exactly — nearest sampling at a texel boundary — is not a candidate either, since it is a
      discontinuity rather than a slightly-off average, and a ΔE gate would admit a whole wrong texel
      as readily as an exact one. Recorded rather than quietly shipped into.

**Two measured design decisions:**

**1. Goldens store premultiplied bytes through an NRGBA container, not a plain PNG.** Measured the
obvious approach first: encoding an `*image.RGBA` to PNG unpremultiplies, decoding re-premultiplies,
and the round-trip **loses Δ=1 on 13% of channels** (34056/262144; worst `[1 1 1 2]` → `[0 0 0 2]`).
That is the *entire* `Quantized` budget spent on the file format before the renderer is compared —
the gate would measure PNG. Carrying premultiplied bytes through an `image.NRGBA` container is
**bit-exact** (the encoder copies NRGBA verbatim) and still compresses (256×256 → 1.6 KB); opaque
frames encode as RGB and decode as `*image.RGBA`, equally exact since premul == straight at α=255.
`TestGoldenRoundTripIsLosslessForPremultiplied` guards it over all (value, alpha) pairs. Cost: a
translucent golden displays composited-over-black in a viewer — which is what the renderer produced.

**2. The golden gate is `Identical()`, not the entry's `Tol`.** They answer different questions:
`Tol` is the **cross-backend** gate (CPU vs GPU, where two float pipelines diverge), while a golden
is generated *by* the deterministic CPU reference, so re-rendering must reproduce it bit-for-bit.
Gating goldens at `Tol` would let the CPU drift by a full LSB undetected. The CPU golden test also
needs no GPU, so it catches drift in plain CI. (Cross-platform CPU determinism — Go may fuse FMA on
some architectures — is Phase 13/12d's problem, and the repo already relied on it.)

**Perceptual gate, and why it is three checks rather than ΔE alone:** ΔE is measured over the frame
composited against **opaque black** — the premultiplied RGB *is* that composite, making it a total
function with no unpremultiply division and no undefined color at α=0. But compositing over black
makes transparent-black and opaque-black indistinguishable to ΔE, so **alpha is gated separately**
by `Tol`; and **SSIM** (luma, 11×11 Gaussian σ=1.5, Wang et al.) catches structural drift a
per-pixel metric averages away. All three are asserted by test, including the alpha blind spot.
ΔE is **CIE76, not CIEDE2000**, recorded with its reason: CIE76 overestimates distance for saturated
colors, which is a defect when *ranking* arbitrary pairs but not when gating two renders that should
already be near-identical — there it is only ever *stricter* than CIEDE2000, and a stricter gate
cannot admit a divergence CIEDE2000 would have caught. Upgrade if a feature ever needs finer
discrimination. `srgb8ToLab` is validated against **published Lindbloom sRGB→Lab D65 references**
(red 53.2408/80.0925/67.2032, green, blue, mid-grey, and the exact white=100 / black=0 endpoints) —
an independent oracle, not values read back out of the implementation.

**Result:** `go test ./...` runs 19 corpus entries on the GPU at their earned gates (all pass) and
19 CPU goldens at Δ=0 (316 KB total). `AssertPNG` retired — its failure affordance (writing the
actual + red-highlighted diff image to a temp dir) folded into `assertGolden`, so it survives and
seeds Phase 22's overlay. `TestBlendEntriesBuildDistinctScenes` guards the corpus against a
loop-variable capture bug that would silently collapse all 12 blend entries into one scene while
every gate still passed; **verified it fails when the bug is deliberately injected**, since a guard
that cannot fail is worse than none.

#### 12b — property-based invariants  ✅

- [x] A `internal/parity/props` suite asserting algebraic laws the renderer must obey, checked on
      generated scenes (not fixed goldens):
      - **Affine composition:** `render(node∘(A·B)) == render((node∘A)∘B)` within tolerance —
        transform associativity through the CTM. **Δ=0** on both backends.
      - **Idempotent clips:** clipping by the same path twice equals clipping once
        (`clip(clip(x,C),C) == clip(x,C)`). **Δ=0 — but only pixel-aligned; see the finding below.**
      - **Compositing associativity** (for the associative operators only — SrcOver, and the
        Porter-Duff subset that qualifies after Phase 15): `(A over B) over C == A over (B over C)`.
      - **Premultiply round-trips:** `premul(unpremul(p)) == p` for every representable pixel;
        guards the un/re-premultiply used by every non-SrcOver blend path. **Δ=0** both backends.
- [x] Each property runs on **both** backends independently (the law must hold within each) **and**
      cross-backend (both must agree). A property failure prints the generating seed for 12c replay.

**Structure:** a `Law` carries its **own** gate (like a corpus `Entry`) — the tolerance is a property
of the law, not a global knob. Laws are parameterised by a `RenderFunc`, so the suite lives in the
root module and the **GPU test passes its own renderer in**; `props` never imports `backend/gpu` and
the cgo quarantine holds. `CheckAll` runs each law within one backend (52 CPU subtests);
`CheckAgreement` renders each law's generated scene on both and requires they agree (104 GPU
subtests) — a law holding independently on each backend does **not** imply the backends agree, so
that is a separate gate. Every failure prints `seed=0x…`, and the seed *is* the repro for 12c.

**FINDING — clip idempotence is false under antialiasing, by design.** Clip coverages compose by
**multiplication**, so applying the same path twice squares an edge coverage: 0.5 → 0.25. Measured
**Δ=52** on an AA circle clip, versus **Δ=0** for a pixel-aligned clip. This is not a rounding
artifact and not something tolerance should absorb — it is what multiplicative coverage composition
*means* (Skia/Cairo behave the same). The law is therefore **scoped to pixel-aligned clips**, where
coverage is binary, and the AA case is recorded here rather than hidden by a widened gate.
*Open decision for later:* product vs `min` for stacking clip coverage — `min` would make idempotence
hold under AA, but it is a semantic change touching both backends and is not a test-phase call.

**Two oracle corrections the measurements forced:**

1. **Premultiply round-trip:** the first oracle compared against `col.RGBA()`, which premultiplies in
   16-bit then truncates, while the renderer rounds in 8-bit — it failed at Δ=1 while the renderer was
   correct. It was testing **Go's color model**, not the round-trip. The real oracle was already in the
   tree: **SrcOver is the one path that never unpremultiplies**, so "every general mode over an empty
   backdrop == SrcOver over an empty backdrop" pins the divide/re-multiply with no external formula.
   Δ=0.
2. **Compositing associativity** cannot be exact and measurement says so: the split render quantizes
   its intermediate composite to 8 bits where the whole render keeps full precision — **14/16 seeds at
   Δ=1, 2/16 at Δ=2**, so the gate is `Budget(2, "split render quantizes its intermediate composite")`,
   the measured bound, and the suite's only budget. The **grouped** form `(A over B) over C` is not
   expressible until **Phase 18** adds isolated layers (a scene admits exactly one grouping today), so
   what is asserted is the achievable equivalent: rendering is a **pure fold over nodes** with no
   cross-node state.

**Anti-vacuity, because a property test that draws nothing passes everything:** `nonTrivial` requires
real coverage and more than one distinct pixel before any law is allowed to assert, and it **caught a
real case** (a generated scene rendering ~nothing) during bring-up; `randColor` never emits alpha 0
for the same reason (low alphas are kept — that is where the divide by alpha is worst). Both guards
were **verified by injection**: removing the pixel-alignment from the clip generator makes
clip-idempotence fail immediately, so the scoping is load-bearing rather than decorative.

#### 12c — differential fuzzing with automatic shrinking + seed replay + bisect  ✅

- [x] A scene generator seeded by a single `uint64` (deterministic; the seed *is* the repro).
      Emits random-but-valid scenes: N nodes, random paths/transforms/paints/clips/blend ops drawn
      from the currently-implemented feature set. Go's native fuzzing drives it (`FuzzDifferential`);
      the differential oracle is CPU-vs-GPU exact delta. `TestFuzzSweep` runs 64 fixed seeds on every
      `go test`, so the differential is a **standing gate**, not something that only runs when someone
      remembers to pass `-fuzz`.
- [x] **Automatic shrinking:** delta debugging over the node list (drop a chunk, keep the failure,
      halve on failure), then per-node stripping of clips/stroke/dashes/gradient/shape→bbox/
      transform/blend/fill-rule, to a fixpoint. The gate is computed **once from the original scene
      and held fixed**: recomputing it per candidate would let the shrinker re-gate its way to a
      "failure" the original never had. Fixed-gate shrinking can only under-report.
- [x] **Bisect mode:** `FirstDiverging` attributes the divergence to one node. It is a **linear scan,
      deliberately, where the plan said binary search** — prefix divergence is *not monotone*: a later
      opaque node can paint over the pixels where an earlier one diverged, so a clean prefix at k says
      nothing about k-1, and a binary search would name a node that merely sits on a monotone
      boundary. It runs on the *shrunk* scene, so correctness costs a few renders.
      `TestFirstDivergingFindsTheEarliestOfSeveral` pins exactly the case a binary search gets wrong.
- [x] **Seed replay:** `go test ./backend/gpu -run TestFuzzReplay -seed=0x...` re-materializes a seed
      headlessly; `-emit` writes the minimized scene to `internal/corpus/regress/*.json`, which
      `corpus.All()` loads as ordinary entries (cross-backend gate + CPU golden).
- [x] **A find is stored as a `Spec`, not a seed.** A seed only reproduces while the generator is
      untouched, so the first edit to `gen.go` would silently retire every past find — and this
      happened *within this phase*: excluding two blend modes shifted every seed's scene. The `Spec`
      is explicit data, JSON round-tripping **bit-exactly** (verified on the render, not just the
      struct, since regressions are gated at Δ=0).

**Result:** ~1M fuzz executions plus a 3000-seed sweep, no unexplained divergence. The machine found
two real things, and both changed the contract rather than the code.

**FINDING 1 — ColorDodge/ColorBurn are ill-conditioned, so the differential oracle does not apply to
them.** Their blend derivatives are `1/(1-cs)` and `1/cs`, *unbounded*, so they multiply any input
difference without bound. *(Phase 13 later found and fixed the difference they were amplifying here —
the numbers below are pre-fix; the worst generated divergence fell 18 → 5, and their corpus budgets
were retired. The conclusion is unchanged: 5 is not a bound, it is where a 25000-seed sample stopped.)*
Measured on the fuzzer's own find (seed `0x737`), isolated by construction:

| scene | Δ |
|---|---|
| gradient + ColorDodge over a *blended* backdrop | **17** |
| same gradient, SrcOver instead | 1 |
| **solid** + ColorDodge over the same blended backdrop | **18** |
| gradient + ColorDodge over a *plain* backdrop | 1 |

Both backends are individually correct in every row. The dodge node amplifies whatever sub-LSB
difference reaches it — a 1-LSB backdrop left by a previous blend, *or* a gradient's source colour
whose f32-vs-f64 parameter differs by well under one LSB (invisible at Δ≤1 under SrcOver, multiplied
≈54× via `dB/dcs = cb/(1-cs)²`). There is no bug to find and no bound to gate at, so the generator
**omits them**, with the measurement recorded. They stay covered exactly where the oracle *does*
apply — the corpus feeds them bit-identical inputs (solid over a plain backdrop) at Δ≤2/Δ≤3 — and
`Spec.Tol` still gates them correctly if a stored spec contains one, because `Tol` is a function of
the scene, not of the generator. **This also reframes Phase 10's dodge/burn budgets: they hold
because `BlendScene` feeds them pinned inputs, not because the divergence is bounded at 2–3.**

**FINDING 2 — the Δ≤1 floor does not compose across stacked blends, but it is worth exactly one more
bit.** *(The mechanism stated here was corrected by Phase 13: at the time, only the CPU quantized
between nodes — the shader accumulated in f32 and rounded once. Both now round per node. The
measurement below was identical before and after, because at these depths the residue is seeded by
f32-vs-f64 coverage rather than by the quantization mismatch, which only dominates past ~8 layers.)*
A second blend node reads an already-quantized backdrop that may be 1 LSB apart. Its sensitivity to
it is
`d(Co)/d(Cb) = αs·B'(cb) + (1-αs)` — **the backdrop alpha cancels** — so a mode with `B'>1` (Overlay
at `2(1-cs)`) amplifies rather than absorbs. Measured over 3000 seeds, by number of general
(non-SrcOver) blend nodes:

| general nodes | 0 | 1 | 2 | 3 | 4 |
|---|---|---|---|---|---|
| max Δ | 1 | 1 | **2** | **2** | **2** |
| max alpha Δ | 0 | 0 | 0 | 0 | 0 |

**Flat, not compounding** — which is what made this a `Budget(2)` in exact mode rather than a retreat
to perceptual mode. (The first measurement said Δ=6 and looked unbounded; that was entirely
dodge/burn contamination, and re-measuring after Finding 1 collapsed it.) Alpha never diverged at any
depth and cannot: `αo = αs + αb(1-αs)` has gain `(1-αs) ≤ 1`. **Phase 13 tried this prediction and it
failed:** pinning the rounding retired the dodge/burn budgets and removed an unbounded-with-depth
divergence, but `stackedBlend` survived unchanged — the residue is seeded by f32-vs-f64 coverage and
gradient evaluation, which no rounding rule reaches.

**The generator's guarantee is checked, not asserted.** Every scene opens with an opaque background
(without which blend modes collapse to the premultiply round-trip and clipped nodes bite on nothing),
but 0.8% of seeds still render uniformly — a node can be invisible by *correct* blending (Darken with
a source lighter than an opaque backdrop returns the backdrop exactly, for any alpha). Those seeds are
**skipped, not failed**, and the skip rate is itself gated at 3% so vacuity can never quietly eat the
suite. `parity.NonTrivial` moved out of `props` to sit with the contract, since any harness rendering
a scene it did not hand-write needs it.

**Every failure-path component is tested by injection**, because the renderers agree today and a
minimizer that quietly does nothing still reports "minimized": a fake divergence with a known culprit
proves the shrinker reaches one node, strips every feature that does not matter, keeps the one that
does, and never modifies a passing spec.

#### 12d — cross-platform reconciliation  ✅ harness; ⏳ coverage needs non-Apple hardware

- [x] **Backend selection, by name and honest about it.** `gpu.Backend` (`Any`/`Metal`/`Vulkan`/
      `DX12`), `NewDeviceOn`/`NewRendererOn`, `Device.Describe()`. `TestReconcileBackends` runs the
      whole corpus on every backend the host exposes, at each entry's earned tolerance; an absent
      backend **skips**, never fails.
- [x] **A trap worth recording: `RequestAdapterOptions.BackendType` does not work.** The obvious way
      to pick a backend is accepted, warned about (`unsupported, use WGPUInstanceExtras.backends`),
      and **silently ignored** — asking for Vulkan on this Metal-only host returns the *Metal*
      adapter. A harness built on it would have printed `vulkan: PASS` having tested Metal, which is
      worse than no harness at all. The instance-level mask (`InstanceDescriptor{Backends:…}`) does
      filter: absent backends fail to yield an adapter. `NewDeviceOn` additionally verifies the
      adapter's own reported `BackendType` matches the request — defence in depth against a library
      that has already answered a different question than the one it was asked.
- [x] **A green run states its own scope.** The test logs `reconciled 1 backend(s): [metal]` and
      `NOT reconciled here: [vulkan dx12] — the portability claim covers [metal] only`. Verified to
      fail on an injected divergence, so it is a gate and not a formality.

**Decision: no stored GPU goldens, and the plan's own instrument was the wrong one.** Three reasons,
the second of which only existed once Phase 13 landed:

- It invents a **second oracle with no authority**. If Metal records 128 and Vulkan says 129 while
  the true value is 128.5, both are correct; failing Vulkan against Metal's recording asserts
  "reproduce Metal's rounding rather than the true value" — the exact trade Phase 11 rejects.
- The **CPU reference is already machine-independent**, and that is measured, not assumed: Phase 13
  showed Go fuses FMA on arm64 yet every golden reproduces bit-for-bit, because f64 sits ~13 orders
  of magnitude from the 8-bit rounding decision. Gating each backend against the CPU *on its own
  host* therefore composes across hosts with no shared file and no canonical GPU.
- It would be **weaker than what is already gated**: a GPU golden could only be held at the
  quantization floor, while many-nodes, clip-rect and both blend-stack entries are gated at **Δ=0**
  against the CPU reference today.

A direct backend-vs-backend gate is no better: if both pass their CPU gate at tolerance T they are
within 2T of each other automatically (asserting 2T is a tautology), and asserting T would claim two
independent f32 drivers must agree more tightly than either agrees with the reference, which nothing
supports. So the CPU reference stays the only oracle and this harness's job is to make sure every
backend faces it.

**Honest status:** the harness is done and green; the *coverage* is still Metal-only, because this
host exposes exactly one adapter (`metal/integrated-gpu Apple M4`; Vulkan/DX12/GL enumerate zero).
Nothing here proves Vulkan or DX12 parity — it proves they have never been run, and says so out loud
instead of passing quietly. Closing that needs a non-Apple runner. When one exists, the first
suspects for a divergence past the floor are Phase 13's two named unknowns: FMA contraction (WGSL
cannot forbid it; f32 has the headroom to show it) and the tile-backdrop summation order at
`routeCol`.

**Done when:** a single `go test ./internal/parity/...` runs corpus goldens (exact + perceptual),
all four invariants on generated scenes, and a fuzz pass that on failure prints a minimized,
replayable, first-diverging-primitive report; cross-platform reconciliation is green on whatever
backends the runner exposes. ✅ — with the standing caveat that "whatever the runner exposes" is
Metal alone on Apple silicon, which the harness reports rather than glosses.

---

## Section B — making exactness reachable across backends

12d and any "must agree bit-for-bit" claim are impossible if the same WGSL compiles to different
arithmetic on different drivers. This section removes that variable, then provides an escape hatch
for the cases where it can't.

### Phase 13 — float determinism controls  ✅ (12d's on-device half remains; **the FMA finding was overturned by CI — see below**)

**Two budgets retired, one semantic divergence killed, and two "obvious" fixes measured and
rejected.** Every tolerance in the tree is now the quantization floor except one, which is measured.

- [x] **Rounding — pinned, and it was the whole ballgame.** `raster.wgsl` `quant8` now rounds
      `floor(v*255+0.5)/255`, matching `clamp8`'s round-half-up, instead of leaving the f32→u8
      conversion to the driver on the `rgba8unorm` store. An implementation-defined rounding rule
      cannot be half of a parity contract — Vulkan and DX12 need not round like Metal — so this is
      also 12d's precondition.
- [x] **The GPU now quantizes per node, like the reference.** The real find: the CPU composites into
      an 8-bit `image.RGBA`, re-quantizing after *every* node, while the shader accumulated `fbl` in
      f32 across all nodes in a tile and rounded **once** at the end. That is a different function,
      not a more precise one, and the gap **grows without bound with depth**:

      | stacked Overlay layers | 1 | 2 | 4 | 8 | 16 | 32 | 64 |
      |---|---|---|---|---|---|---|---|
      | accumulate, round once | 0 | 1 | 1 | 2 | 2 | 6 | **10** |
      | round per node | 0 | **0** | **0** | **0** | **0** | **0** | **0** |

      12c's generator caps at four nodes, where the effect is a single LSB — it could never have
      found this. `sample.BlendStack` + the `blend-stack-normal`/`blend-stack-overlay` corpus
      entries gate it at **Δ=0** forever (pixel-aligned solids, so nothing but rounding can move
      them); reverting the shader fails them at Δ=1/Δ=3, verified.
- [x] **Phase 10's ColorDodge/ColorBurn budgets are deleted.** Both now hold at `Quantized()` — the
      first tolerances in this tree retired by a fix rather than widened. Their stated reason was
      also **wrong**: the division never was the culprit, it only *amplified* a backdrop the two
      backends had quantized differently. Exactly 12c's Finding 1, now with the mechanism named.
- [x] ~~**FMA contraction — measured, then deliberately NOT pinned.**~~ **OVERTURNED by CI's first
      amd64 run (2026-07-16). The measurement was right; the law drawn from it was not.** Superseded
      by the entry below — kept rather than deleted, because *how* a correct measurement produced a
      wrong rule is the whole finding.

      The original claim: Go fuses `a*b+c` on arm64 and not on amd64, so the CPU reference's
      arithmetic is architecture-dependent in principle — but not in practice, because pinning every
      hot expression with explicit `float64()` conversions (`geom.Matrix.Apply`, `raster/fill.go`'s
      SrcOver) reproduced **all 21 CPU goldens bit-for-bit, ~4M channels, Δ=0**. Justified by
      headroom: 8-bit output is ~2.4 decimal digits, f64 carries ~15, so the CPU's last-bit noise
      sits ~13 orders of magnitude below the rounding decision it would have to cross.

- [x] **FMA contraction — pinned in `paint.MeshAt`, after CI found the golden it breaks.** Phase 22's
      CI landed and its first amd64 run went red: `TestGoldenCorpus/mesh`, **Δ=1 at pixel (75,96),
      1/172800 channels**, deterministic. Everything else — the other 42 goldens, the 64-seed
      differential, every property law — passed. The divergence is **`paint.MeshAt`**, which is dense
      with `x*y + z*w`.

      **The headroom argument is unsound, and this is the correction that matters.** Headroom is a
      distance to a boundary; it protects a value sitting *away* from one and offers nothing at a
      **tie**, where the distance is zero. Quantization is a threshold, not a smooth map: the
      perturbation does not have to be big enough to matter, only non-zero. Measured at that pixel,
      blue channel, true value by exact rational arithmetic:

      | | value | `v*0xffff` | 16-bit | verdict |
      |---|---|---|---|---|
      | true | 0.5 + 1.36e-17 | 32767.5+ | 32768 | — |
      | unfused (amd64, and now everywhere) | 0.5 | 32767.5 | **32768** | ✅ correct |
      | fused (arm64, what shipped) | 0.5 − 1 ULP | 32767.49… | **32767** | ❌ wrong by one |

      **And the polarity is the lesson, because it is backwards from the guess.** Fusion is the more
      accurate operation in general — one rounding instead of two — so the natural reading is that
      arm64 was right and portability would cost accuracy. It is the reverse: unfused lands **3×
      closer** to the true value (1.36e-17 vs 4.19e-17) and on the correct side of the tie. There was
      no trade to make. **The golden was recording a wrong pixel, and the fix is both deterministic
      and more correct.** That is the mesh crack (Phase 16 / `MeshEps`) in a different key — more
      precision made it worse, and the reference was the buggy one.

      **Why Phase 13 was not sloppy, which is the uncomfortable part.** Its experiment is real and
      still reproduces. `MeshAt` **did not exist** when it ran — mesh landed in `65bf558`, two phases
      later — and none of the 21 scenes it measured contained a tie pixel. So the experiment could
      not distinguish *"headroom protects us"* from *"we got lucky"*, and it was written down as the
      former. The corpus then grew **21 → 43** and nobody re-ran the question. A measurement was
      promoted to a law, and the law was never re-checked as the code grew under it.

      **`math.FMA` was measured and rejected**, not argued away: it pins the fused (less accurate)
      answer, and it costs **3.8× on amd64** (4.40 ms vs 1.16 ms, `BenchmarkMeshAt`) because Go
      emulates it in software unless `GOAMD64>=v3`, which no library can require of its callers.
      Forcing no-fusion with `float64()` costs **nothing measurable on either architecture** (arm64
      1.20 vs 1.27 ms; amd64 1.15 vs 1.16 ms) and makes the two **byte-identical**.

      **The gate is `paint.TestMeshAtDoesNotFuse`, and it fails on arm64 — deliberately.** A gate
      that only fails on CI's amd64 is invisible to everyone developing on this machine, and deleting
      a "redundant" `float64()` is a one-keystroke cleanup. It pins the blue channel's exact bits
      (`0x3fe0000000000000`); injection-verified — removing the conversions fails on **arm64** with
      `0x3fdfffffffffffff`, and passes on amd64, which cannot observe fusion at all. The two gates are
      complements, not duplicates. `TestMeshFMAFixtureStraddlesTheTie` guards the guard: it asserts
      the fixture still quantizes to exactly 32767.5, because a fixture edited off the tie would
      leave the first test green and testing nothing.

      **What is NOT pinned, and why that is now a stated risk rather than a proof.**
      `geom.Matrix.Apply` and `raster/fill.go`'s SrcOver remain unpinned. Phase 13's bit-for-bit
      result over 43 goldens on both architectures still holds for them — CI now checks it every
      push, which is the only reason the claim is worth anything. But that is evidence about the
      pixels measured, **never a proof about the pixels not yet written**. The next tie pixel in a
      new scene is the same bug, and the thing that will find it is CI on amd64, not this argument.
- [x] **Operation ordering — audited; one load-bearing divergence, kept deliberately.** Stop-table
      interpolation matches term-for-term. The signed-area sweep does **not**: `raster.go` runs one
      running total left-to-right across the whole row, while the shader collapses everything left of
      a tile into a scalar backdrop in *segment* order and seeds the tile sweep with it. Equal in
      exact arithmetic, not in floating point. Fixing it means sweeping whole rows — the exact
      serialization the 2D-tiling rewrite removed (720 → ~57,600 threads) — so it is recorded at
      `routeCol` as an accepted contributor to the Δ≤1 floor.
- [x] **Gradient 16-bit rounding — measured and rejected.** The CPU's gradient is quantized to 16
      bits (`paint.Color.RGBA()` → `/257`), so the reference never sees a continuous gradient colour.
      Mirroring it in the shader moved **nothing** (gradient stayed Δ=1; a gradient read through
      ColorDodge's unbounded derivative stayed Δ=0) — 16-bit error sits ~130× below the 8-bit
      decision. It is also an artifact of reusing Go's `color.Color` convention rather than a choice
      the reference made, and matching an artifact costs per-pixel work to enshrine an accident.
      Recorded at `gradColor` as a known semantic difference living below the floor.
- [x] **12c's prediction was half right, and the half that failed is the informative one.**
      `fuzz.stackedBlend`'s `Budget(2)` **survives**: pinning the rounding removed the unbounded-depth
      divergence and retired dodge/burn, but the residue it gates is seeded by f32-vs-f64 evaluation
      of *coverage and gradients*, which no rounding rule touches. Still flat at Δ≤2 for 2–4 stacked
      nodes over 25000 seeds. Retiring it would require both backends to compute in the same
      precision — which Phase 11 rejected on purpose.

**Result:** corpus deltas after the fix — solid Δ=0, many-nodes Δ=0, clip-rect Δ=0, blend-stack-* Δ=0,
and **every** other entry Δ=1 including both formerly-budgeted division modes. 472k fuzz executions
against the tightened gates, no failures. Perf neutral within noise.

**Done when:** 12d's cross-backend delta on the corpus is ≤ the quantization floor (Δ≤1) with every
residual Δ≥2 site named and justified. *The rounding rule is now pinned rather than driver-defined,
which is what makes that question askable; the on-device Vulkan/DX12 half is 12d's.*

### Phase 14 — per-tile CPU fallback for inexact GPU features  ✅

**The escape hatch works and is priced. The headline is the price:** a fallback tile costs ~6µs, so
the mechanism is only ever worth it for a *small* node — at full coverage it is ~14× a GPU frame and
strictly worse than just using the CPU backend. It buys exactness locally, not globally, and that
number is what keeps a future feature from reaching for it as a general answer.

- [x] **The handshake.** `scene.Node.Fallback` marks a node; `encode.go` flags the tiles its bbox
      reaches (`markFallbackTiles`, reusing the binner's own `tileRange`, so a node's fallback tiles
      and its GPU bins agree by construction); `backend/gpu/fallback.go` renders the flagged tiles
      with the CPU reference and `queue.WriteTexture`s them over the compute pass's output, before
      any readback or present, so the offscreen and (6b) windowed paths both see patched pixels.
      Runs of flagged tiles upload as one region per tile row. **No shader change**: the GPU still
      rasterizes flagged tiles and is simply overwritten. Skipping them on-device is a perf idea, not
      a correctness one, and it is not worth a tenth binding until something measures it.
- [x] **Why a tile, not a node.** The CPU pass renders the *whole scene* into the flagged tiles, not
      just the fallback node. Blending the CPU's node over the GPU's tile would composite it onto a
      backdrop the two backends had already quantized differently — exactly Phase 13's Finding 1,
      re-created one layer up. A tile is a complete composite of the scene restricted to its area, so
      replacing it wholesale cannot leave a seam.
- [x] **The exactness argument, and where it actually lives.** `raster.TileMask` gates the *write*;
      `FillPaint`'s coverage sweep still starts at each path's own left edge and accumulates through
      masked-out columns before reaching a live one. So masked pixels are bit-identical to a full
      render's, rather than merely close. This was **already true of the existing `clip` parameter** —
      the partial render is a use the rasterizer's shape allowed for, not new machinery. Narrowing the
      sweep instead would have been the obvious optimization and would have quietly broken it.
- [x] **Measured, two-sided.** `sample.FallbackScene` is a frame of pixel-aligned solids (Δ=0, the
      `BlendStack` reason) around one radial-gradient circle, marked. Fallback **off → Δ=1, on → Δ=0**,
      12 of 48 tiles. Both entries are in the corpus and `TestFallbackBuysExactness` asserts the
      *difference*, so the Δ=0 gate cannot pass vacuously on a backend that was exact anyway.
      **The first draft of that scene did exactly that** — a pixel-aligned rect with a 2-stop linear
      ramp rendered at Δ=0 on Metal with the fallback off, and the gate would have been decorative.
      The radial gradient's division and the circle's AA edges are what make the divergence reliable;
      the near-miss is why the vacuity check is a test rather than a comment.
- [x] **Mutation-tested, and it found the honest shape of the thing.** Three mutations: fallback
      disabled → caught; fingerprint ignoring the tile mask → caught; **CPU pass ignoring the tile
      mask → NOT caught.** That is not a missing test, it is the design telling the truth: containment
      is enforced at *upload* (only flagged rects are written), so the mask is a **pure optimization** —
      worth 33% at 25% coverage, 15% at 50%, growing as coverage shrinks, i.e. exactly where the
      fallback is worth using at all. Since breaking it would be silent, its contract is now pinned
      where it lives, in `backend/cpu`'s `TestTileMaskConfinesWrites`, which does catch the mutation.
- [x] **Fingerprint.** Toggling the mark leaves segments/nodes/stops byte-identical, so Phase 7's
      frame skip would have hashed the two frames alike and skipped the patch the toggle asks for.
      `FallbackTiles` is now hashed. An unsupported-paint node marked for fallback also keeps its
      segments in the buffer, unreferenced by any GPU node, purely so the fingerprint covers geometry
      only the CPU pass can see.
- [x] **The silent-drop hole is closed for marked nodes.** The encoder dropped an unsupported paint
      before computing a bbox, which was sound only because the CPU reference drops it too. A marked
      node now gets its bbox and flags its tiles regardless, so the mark cannot silently do nothing
      for the very case (Phase 17's image paint) it exists for.

**Result:** corpus gains `fallback-gradient` at `Identical()` and `fallback-gradient-off` at
`Quantized()`; the pair, not either entry, is the measurement. Edge tiles are covered at 130×100
(8.125 × 6.25 tiles — both edges partial, by different amounts) where every other test's 128×96 is
whole tiles. `BenchmarkFallback` sweeps coverage 0/25/50/100% and reports `cpu-tiles` and `%frame`
alongside ns/op:

| coverage | flagged tiles | GPU-only | with fallback | added |
|---|---|---|---|---|
| 0% | 0 | 1.42ms | 1.41ms | none |
| 25% | 960 | 1.93ms | 7.71ms | +5.8ms |
| 50% | 1840 | 2.41ms | 12.44ms | +10.0ms |
| 100% | 3600 | 1.72ms | 23.61ms | +21.9ms |

Linear at ~6µs/tile (1280×720, M4, Metal). Zero flagged tiles costs nothing measurable, so the path
is free for every scene that does not use it.

**Post-review correction (found after Phase 15).** The fallback shipped with a real corruption bug,
and its shape is worth keeping: `cpu.nodeBounds` padded a stroked node by the stroke's half-width,
which is **not** an upper bound — a miter join reaches `miterLimit*w/2`, four times that by default.
The estimate had been wrong since long before Phase 14 and was harmless, because its only caller
(`culled`) needed a node's whole bbox off-canvas before dropping it. `tileCulled` then reused it to
drop nodes against a handful of flagged tiles, where being 8.9px short is enough: a miter spike
reaching a flagged tile that the padded path bbox misses gets culled from the CPU patch, and the patch
overwrites the GPU's correct pixels with a tile that never drew it. Measured **Δ=220 over 76 channels**
— corruption, not a rounding delta. The bound now comes from `path.Stroker.MaxExtent`, which owns the
miter-limit default, and `TestFallbackKeepsMiterSpike` reproduces the old failure exactly. **The
lesson is the reuse, not the arithmetic:** an approximation is only as safe as its callers' tolerance
for error, and Phase 14 changed that tolerance without rechecking the approximation.

Also added in review: `TestFallbackWithPorterDuff`, since Phases 14 and 15 never met in a test.
A fallback tile under a backdrop-reading operator (Clear, DstOut, SrcIn, DstIn, Xor, Src/DstAtop) is
exact at Δ=0 — the patch recomputes the whole node stack from transparent black rather than
compositing over the GPU's pixels, so there is no backdrop for the two to disagree about.

**Done when:** a deliberately-inexact feature renders at exact tolerance overall by CPU-filling its
tiles, with a benchmark showing the fallback tile count and its frame-time cost. ✅ — with the caveat
that the inexact feature is a *real* one (a radial gradient at the f32-vs-f64 floor) rather than a
synthetic defect, because Phases 16/17's genuinely-inexact features do not exist yet. When they land,
they inherit a mechanism already gated, priced, and mutation-tested; what they must not inherit is
the assumption that it is cheap.

---

## Section C — rendering features (each on both backends, gated by Section A)

Ordered by independence. Each cites Section A for its gate. None is "done" GPU-only.

### Phase 15 — full Porter-Duff compositing operators  ✅

Blend modes (Phase 10) answer "how do source and backdrop *colors* combine." Porter-Duff answers
"how do their *coverages* combine" — a different axis. Before this phase only `SrcOver` existed.

**All 12 land at Δ=0 on Metal, gated at the floor. The phase's real content is two things the plan
did not anticipate: what antialiasing coverage *means* once the operator is not source-over, and a
pre-existing tolerance bug the widened fuzzer surfaced.**

- [x] **The two axes, named apart.** `paint.CompositeOp` (12 operators) alongside `paint.BlendMode`,
      per W3C `mix-blend-mode` × `composite`. `scene.Node` carries both; `Canvas.SetComposite` is the
      opt-in. `Node.Flags` packs blend in bits 0-3 and the operator in bits 4-7 (`packFlags`), which
      keeps the hand-mirrored GPU `Node` at its current size rather than inserting a field into two
      structs for two bits.
- [x] **`BlendMode.SrcOver` was renamed to `Normal`, which is what it always was.** The plan listed
      `SrcOver` among the 12 operators while `paint.SrcOver` already existed as a *blend mode* — the
      old name conflated the two axes at exactly the point they had to come apart. W3C calls that
      blend function `normal`; the enum's zero value never was a Porter-Duff operator. The compiler
      caught all 16 sites, no pixel moved, and corpus `blend-srcover` → `blend-normal`,
      `blend-stack-srcover` → `blend-stack-normal` (Phase 13's text above updated to match).
- [x] **Coverage is not source alpha — the one real trap.** `blend()` folds coverage into αs, which
      is *correct for SrcOver* and nonsense for anything else: a half-covered `Clear` has αs·cov = 0
      either way and (Fa,Fb)=(0,0) would erase the whole pixel instead of half of it. The general
      form composites at full source strength and then lerps between that and the untouched backdrop
      by coverage — `result = cov·PD(s,b) + (1-cov)·b` — which reduces to exactly the αs-scaling for
      SrcOver, which is *why* the fast path was never wrong. `TestPorterDuffCoverageIsNotSourceAlpha`
      pins it; mutating the code to fold coverage in produces the predicted all-zero pixel.
- [x] **SrcOver keeps its old arithmetic on both backends, deliberately.** `porterDuff` is
      algebraically identical for it — proven by `TestPorterDuffGeneralizesSrcOver` over
      backdrop×source×coverage×mode — but not identical float-for-float, so routing SrcOver through
      the general form would move every AA edge in the tree by an LSB and churn every golden to buy
      nothing. Neither form is more correct. The fast path also skips an unpremultiply.
- [x] **The coefficient table has an independent witness.** `raster.Coefficients` and `raster.wgsl`'s
      `pdCoeff` state the same 12 rows. An alpha check against `αs·Fa + αb·Fb` would restate the
      table rather than test it, so `alphaOracle` derives each operator's output alpha from
      Porter-Duff's *coverage geometry* (αs and αb as areas of two independent subsets; each operator
      names which parts of the Venn diagram survive). Plus `DstOver(s,b) == SrcOver(b,s)` and a
      color-source test separating the pairs alpha cannot distinguish (SrcIn/DstIn have identical
      αo and opposite colors). Mutations tried: transposed SrcIn row → caught; SrcAtop/DstAtop
      swapped → caught.
- [x] **The axes are tested crossed, and the reason is measured.** Every `composite-*` entry left
      blend at Normal and every `blend-*` entry left composite at SrcOver, so neither family could see
      the two being confused — and they share one packed word. Widening the shader's blend mask from
      `0xF` to `0xFF` lets the operator bits leak into the mode and **every single-axis entry still
      passes**: with one axis at its zero value the leaked bits land past the end of `blendCh`'s
      switch, whose default returns the source color, which is what Normal does. The four
      `composite-x-blend-*` entries are the complete set that goes red.
- [x] **Associativity: two of the twelve qualify, and the other ten are recorded, not skipped.** The
      law needs the operator to be associative *and* transparent black to be its identity. SrcOver and
      DstOver pass both — DstOver's fold reverses paint order, so it recombines as `over(P,S)` rather
      than `over(S,P)`, a nontrivial second witness reusing the same helper. Six fail the identity
      condition (their coefficients collapse against a transparent backdrop, so the fold renders
      nothing); Clear/Dst/Src hold it vacuously. **Xor is the near-miss worth naming:** T *is* its
      identity and its alpha *is* associative (a+b−2ab is symmetric in three arguments), but its color
      is not — cx's coefficient is (1−ay)(1−az) one grouping and 1−ay−az+2ay·az the other, equal only
      when ay·az=0. Alpha associativity is not associativity.
- [x] **The generator now draws composite ops, and the bias is load-bearing.** Sampling the nine
      non-degenerate operators uniformly took the trivial-scene rate from 0.8% to **4.0%**, past 12c's
      3% vacuity gate — not a bug: `SrcOut` over an opaque backdrop is *defined* to erase, so a large
      node wipes the frame and the differential has nothing to compare. Raising the gate would have
      inverted its purpose. A node keeps SrcOver at 8/10 instead (1.8% measured, ~40% of the gate in
      hand). Clear/Src/Dst are excluded from generation — each ignores an operand, so there is little
      interaction to lose — and each keeps a corpus entry where its effect is the point.

**The fuzz find, and what it says about this apparatus.** Widening the generator found a divergence in
90 seconds: seed `0xb50`, shrunk to three nodes — opaque background, radial-gradient rect, and **one**
SoftLight rect — diverging at Δ=2 against a Δ≤1 gate, one channel of 36864. It contains **no
Porter-Duff operator at all**. `Spec.Tol()` gave the stacking budget only to scenes with *two* general
blend nodes, while the budget's own stated reason already named "the 1 LSB that f32-vs-f64 coverage and
**gradient** evaluation leave" — so the rule contradicted the budget it was selecting, and "one general
mode is exact" was asserted rather than measured. A gradient leaves that LSB just as a prior blend node
does. Fixed here as `general >= 2 || (general >= 1 && gradient)` — **which was also wrong, and for the
same reason: it blamed a correlate.** See the review note below.

This was **verified pre-existing, not argued**: the minimized scene was replayed against the previous
commit's tree and reproduces Δ=2 at the same pixel (39,53) with no Porter-Duff in the tree. Phase 15
surfaced it only because adding one draw to the generator reshuffled every seed's scene — which is the
lesson worth keeping: **the seeds are not a fixed test set, and any generator change re-rolls all of
them.** That is an argument for storing finds as specs (12c already does) and against ever reading a
green fuzz run as coverage of a fixed space. Recorded as `regress/fuzz-softlight-over-gradient.json`.

With the rule corrected, **436,603 differential executions** over the widened space (composite ops
generated) found nothing further.

**Post-review: the tolerance rule, measured rather than reasoned.** Both rules above keyed on a
correlate instead of the mechanism, and the second was wrong the same way the first was. The budget's
own reason names the cause — an operation whose output moves further than its inputs are apart — so
the rule should ask only that. Investigated by measurement, and every step overturned a guess:

- **Which blend modes amplify.** `max|dB/dCb|` scanned numerically over the unit square: Overlay
  **2.0**, SoftLight **4.0**; Normal 0; Multiply, Screen, Darken, Lighten, **HardLight**, Difference,
  Exclusion all exactly **1.0**. HardLight is Overlay's transpose and looks like it should amplify —
  its `2·cs` branch only runs when `cs ≤ 0.5`, so it does not. Where `|dCo/dCb| ≤ 1` a 1-LSB input
  difference *cannot* become 2, because `floor(x+.5)` of two reals within 1 of each other cannot
  differ by 2. That is arithmetic, not luck, and it is why those modes need no budget.
- **The gradient was never the cause.** One Overlay node over **solids only** — which the shipped rule
  gated at `Quantized` — breaches at Δ=2 in **2 of 5955** generated scenes. With a gradient the same
  node breaches in **42 of 904**. The gradient is a ~40× risk multiplier, which is exactly why it was
  present in the find that prompted the rule and why blaming it looked right. **The shipped rule had a
  live hole**, of the same species as the one it fixed.
- **A small sample nearly hid it.** Stacked amplifying modes over solids first measured 0 breaches in
  155 scenes — the generator rarely makes gradient-free scenes. Forcing paints solid to get 2999
  scenes found 3 breaches (0.1%). The clean claims were then re-measured at that sample size before
  being believed: non-amplifying modes stacked over the generator's own scenes hold at Δ≤1 over
  **3000 scenes, 0 breaches**.
- **Porter-Duff operators amplify independently of blending.** With composite ops on and every blend
  mode forced non-amplifying, Δ=2 still appears (1 of 2992). `porterDuff` unpremultiplies through
  `s[0]/s[3]`, and operators like SrcOut and Xor manufacture near-transparent backdrops — so a 1-LSB
  premultiplied difference gets divided by an alpha the operator itself drove toward zero. SrcOver's
  fast path never unpremultiplies at all.

**Result:** `Tol()` is now `any amplifying blend mode || any non-SrcOver composite op`, the gradient
clause is gone, and `stackedBlend` is renamed `amplifiedBlend` — stacking was never the cause, and the
name kept the rule pointed at the wrong variable. The loose gate now covers **63.6%** of generated
scenes, down from the shipped 85% and **below even the pre-Phase-15 69.5%**: the oracle is stricter
than it has ever been *and* no longer has a hole. `amplifying` and `illConditioned` are disjoint by
test — both name `dB/dCb>1`, and the difference is whether the derivative is bounded and so whether a
budget can be fitted at all.

Validated by **531,622 differential executions** against the tightened rule — a stricter gate over more
of the space than the 436,603 that passed under the loose one.

**Result:** corpus gains 12 `composite-*` entries (all three backdrop regimes: opaque, translucent,
empty) plus 4 `composite-x-blend-*` crossed entries plus 1 regression — all green, all Δ=0 on Metal.
They are gated `Quantized` and not `Identical` on purpose: Δ=0 here is a fact about these colors on
this driver, not a property, since `porterDuff` unpremultiplies through a division and lerps by
coverage in f32 against the reference's f64. Gating on it would read luck as a guarantee.

**Done when:** all 12 operators pass exact parity (Δ≤1) per corpus entry; associativity invariant
green for the qualifying subset. ✅

### Phase 16 — conic + mesh gradients  ✅

Linear/radial are done. Two more paint kinds:

- [x] **Conic (angular):** `paint.ConicGradient{Center, Angle, Stops}`; parameter is `atan2` of the
      inverse-transformed pixel. Same stop-table machinery as linear/radial in both `backend/cpu`
      `shader.go` and `raster.wgsl`; added `PaintConic` to the encoder's gradient geometry (the
      `G0`/`G1` slots already carry per-kind geometry — endpoints for linear, centre+radius for
      radial, centre+angle here — so no struct grew). Exact-mode gate (Δ≤1), earned: corpus
      `gradient-conic` and `gradient-conic-seam` both measure **Δ=1**, the same floor linear and
      radial sit at. 2.3M differential executions over the widened space, no failures.

**The plan's one-line prediction — "it's the same interpolation, only the parameter differs" — is
true of the arithmetic and false of the correctness story. The parameter WRAPS, and that makes conic
the first paint that is ill-conditioned by construction rather than by arithmetic.**

- [x] **FINDING — an open seam is a discontinuity, and no tolerance can own it.** Where the first and
      last stops differ, crossing the ray at `Angle` takes `t` from just under 1 to just over 0 and
      the colour jumps between them. Linear and radial are continuous in the pixel position, so their
      f32-vs-f64 parameter difference stays sub-LSB and the floor absorbs it; a discontinuity
      *amplifies without bound*, and unlike ColorDodge the magnitude is set by the **stop colours**
      rather than by the arithmetic — so there is no derivative to bound and no budget to fit.
      Measured, not argued: `TestConicSeamDivergesWithoutBound` aims a seam through a pixel centre and
      gets **Δ=255 on 1 pixel of 8192**, with *both backends individually correct* — the true
      parameter there sits 1e-9 from the wrap and f32 cannot resolve which side it falls on. It is
      bounded in **extent, not magnitude**: rare rather than mild.
- [x] **The first probe measured Δ=0, and the reason is the useful part.** Aiming the seam exactly
      through a pixel centre is *not* enough: f32 `atan2(11,37)` rounds to the same f32 as
      `float32(f64 atan2(11,37))`, so both backends compute `t=0` on the nose and agree exactly. The
      hazard needs the true value inside f32's **blind spot**, not merely on the ray — hence a 1e-9
      nudge to `Angle`, below f32's ~3e-8 resolution there and far above f64's. Both cases are in the
      tree: the probe asserts Δ≥128 *at the aimed pixel*, and `TestConicSeamIsExactWhenF32CanResolveIt`
      is the control at nudge=0, without which Δ=255 could equally mean the implementation is simply
      broken.
- [x] **The generator emits CLOSED conics only** (last stop colour = first), which is the same measured
      scope decision as excluding ColorDodge/ColorBurn, for the same reason: a differential oracle
      needs a well-conditioned function. Closing the loop makes the paint continuous, which puts conic
      back where linear and radial are while still generating the whole atan2 path, the wrap, and the
      inverse transform. `TestGeneratorEmitsOnlyClosedConicGradients` pins it **in both directions** —
      an exclusion satisfied by never generating a conic at all would be vacuous (1047 conics over
      2000 seeds). Seams stay covered where the oracle *does* apply: `gradient-conic-seam` pins one
      over fixed geometry, where the ray's position is a property of the scene and not of a seed. That
      entry's Δ=1 is a fact about these centres on this driver, **not** evidence a seam is safe.
- [x] **A vacuity bug in the new generator, caught by measuring rather than by review.** `randStops`
      draws 2–4 stops; closing the loop overwrites the last colour with the first, so a **two-stop
      conic comes out a constant colour** — a solid paint wearing a gradient's name. A third of every
      conic generated would have exercised no atan2 at all while passing everything. Conic now draws
      3–4 stops via `randStopsN`.
- [x] **Semantics need an oracle the parity machine cannot supply.** Every other gate compares the two
      backends, or compares the CPU against a golden the CPU generated — all blind to a semantic error
      the two renderers *share*. A conic sweeping the wrong way or starting a quarter-turn off would
      render identically on both, match its golden byte-for-byte, and pass every corpus entry.
      `TestConicParameterMapping` asserts the mapping against hand-computed angles instead (right=0,
      down=0.25 — device y grows downward, so increasing atan2 sweeps clockwise on screen — left=0.5,
      up=0.75, plus `Angle` rotating the start), which is Phase 15's `alphaOracle` discipline applied
      to a paint.
- [x] **`atan2(0,0)` is 0 in Go and UNDEFINED in WGSL**, so the exact centre pixel is pinned to `t=0`
      on both sides rather than resting on a driver's corner case. Reachable, not theoretical — a
      `Center` on a pixel centre is an ordinary thing to write. Injection-verified: removing the guard
      turns the centre pixel from `{255,0,0}` into `{41,0,214}`.
- [x] **Trivial-scene rate 1.8% → 2.4%** against 12c's 3% gate, and conic is **not** the cause. A
      controlled A/B (identical RNG stream and scenes, conic collapsed to a solid of its first stop)
      shows conic *reduces* triviality: **1.12% vs 1.52%** on the same 1252 scenes. The rise is the
      re-roll — adding a draw to `randPaint` reshuffles every seed's scene, exactly the Phase 15
      lesson — and at n=3000 the 1.80%→2.37% difference is ~1.5σ, i.e. sampling noise. The first split
      that *looked* like evidence (scenes "without conic" at 3.26%) was confounded: conditioning on
      no-conic biases toward scenes with fewer nodes, which are likelier to be trivial. Headroom is
      now 21% of the gate rather than 40%; recorded so the next generator change starts from the real
      number.
- [ ] *Deferred:* the SVG backend drops conic nodes silently, as it already does for any paint it
      cannot express — SVG has no conic-gradient element (CSS does; SVG 1.1/2 do not). Same standing
      caveat as its ignoring path clips.
- [x] **Mesh (Gouraud triangles):** `paint.MeshGradient{Triangles}` + `paint.MeshGrid`, evaluated per
      pixel by `paint.MeshAt` (the canonical reference; `raster.wgsl`'s `meshColor` is the verbatim
      port). **Δ=1 — the exact floor**, so the plan's two hedges (perceptual mode, Phase 14 fallback)
      are both unused, and the prediction that mesh could not reach Δ≤1 was wrong *for this mesh*.

**Three predictions this phase overturned, and the middle one is the most important thing in Part II
so far.**

- [x] **Exact-vs-perceptual, decided by measurement: EXACT.** The plan expected f32-vs-f64 patch
      interpolation to miss the floor. That is true of what it was imagining — a **Coons** patch, whose
      device→parameter inverse is a per-pixel **Newton solve** — and false of Gouraud triangles, whose
      inverse is a closed-form 2×2 solve: barycentric weights, one division, no iteration. So mesh
      sits at the floor like every other paint. **Perceptual mode (12a) therefore still has no
      customer**, which is worth saying out loud rather than quietly shipping a feature into it: it
      was built for exactly this phase and this phase did not need it.
- [x] **Coons patches deliberately NOT built, and the reason is the inverse, not the interpolation.**
      A `Paint` here answers "what colour is at this pixel", so a cubic-edged patch needs Newton per
      pixel — and for parity **both backends would have to run bit-comparable iterations from the same
      initial guess with the same fixed step count**, since an early-exit predicate evaluated in f32
      on one side and f64 on the other is a *different function*, not a rounding difference. That is a
      large amount of machinery with a worse correctness story, and a caller can tessellate a Coons
      patch into triangles anyway. Recorded rather than deferred.
- [x] **FINDING — the differential caught a bug in the REFERENCE, which a golden would have enshrined
      forever.** This is the first time the CPU reference was the wrong one, and it is the clearest
      argument in the tree for why the reference is not the oracle. Two triangles sharing an edge
      compute that edge's weight by **different expressions** — an edge function over the denominator
      in one, `1 - l0 - l1` in the other. Exact arithmetic makes them complementary: one is zero
      exactly when the other is, and the two `l >= 0` tests partition the edge. Floating point makes
      them **independent roundings of the same real zero**, so both can land a hair negative and both
      tests fail. Measured on `MeshScene`'s 3×3 grid, at a pixel centre landing on a shared diagonal:
      `l = -3.5e-17` in one triangle and `-8.3e-17` in the other, so the **f64 CPU rejected both and
      painted the background through the mesh** — a 41-pixel hairline crack — while the **f32 GPU
      rounded the same real zero to +0.0, accepted, and drew correctly**. Δ=198. **More precision made
      it worse**: exact zero is the only value both triangles accept, and f64 is better at not landing
      on it. A CPU golden would have recorded the crack as expected output; the GPU had no bug to find;
      only comparing them saw it.
- [x] **`paint.MeshEps` closes it, in normalized barycentric units** — a fraction of a triangle, not a
      distance, so it means the same for a 3px triangle and a 300px one. 1e-5 is ~300× the f32 noise
      measured at that pixel (3e-8) and widens a triangle by a hundred-thousandth of itself. The slack
      does not need to pick the *right* triangle, only the **same** one on both backends; Gouraud
      continuity makes the two agree along a shared edge anyway. `TestMeshAtAcceptsAPointOnASharedEdge`
      pins the exact field coordinates that cracked, at the unit level (no GPU needed), and is
      injection-verified: with the eps removed it is the *only* test that fails.
- [x] **The mesh landed without a tenth binding, and that was a constraint rather than a preference.**
      `raster.wgsl` already holds **eight** storage buffers — exactly WebGPU's default
      `maxStorageBuffersPerShaderStage`, and `device.go` requests no raised limits. A triangle buffer
      would have been the first time this renderer demanded more than the portable default, and a new
      way for a backend 12d has never run on to fail. Instead the `Stop` record generalizes from *a
      colour at a parameter* to **a colour at a sample**: gradients use `Offset` (X/Y unused), a mesh
      uses X/Y (`Offset` unused), three consecutive records to a triangle, `StopStart/StopCount`
      addressing both. Cost is 8 bytes per stop, and gradients carry a handful per node.
- [x] **The silhouette is ill-conditioned and there is no closed-loop trick.** A mesh is transparent
      outside its triangles, so its outer boundary is the same unbounded amplifier as a conic's seam —
      but unlike the conic, a mesh has to end somewhere. The remedy is scene-level: `MeshScene` builds
      every mesh over a rect **inflated past the shape that uses it**, and `randMesh` spans
      [-64,160] against generated geometry that stays within ~[0,96] of paint space. A mesh clipped to
      its own outline would be measuring that hazard instead of the interpolation.
- [x] **Semantics get an independent oracle**, for the reason conic's did: parity compares the two
      backends and the golden compares the CPU against itself, so a transposed vertex or a barycentric
      solved for the wrong corner would render identically on both, match its golden byte-for-byte and
      pass every gate. `TestMeshAtInterpolatesBarycentrically` hand-computes each corner, the centroid
      and all three edge midpoints instead.
- [x] **The fuzzer now mixes meshes with gradients, which closes a gap no corpus scene reaches.** The
      two kinds share one table, so a mis-indexed `StopStart` would show as wrong colours rather than
      a failure — and every hand-written scene holds only one kind. 479 of 2000 seeds mix them;
      `TestGeneratorEmitsEveryPaintKind` keeps that true, and `TestEncodeInterleavesMeshAndGradientStops`
      pins the interleaving directly. Trivial-scene rate **2.3%** against the 3% gate (mesh lowered it
      slightly from conic's 2.4% — it adds colour variety).

**Done when:** conic passes exact parity ✅; mesh passes at its measured-and-documented tolerance ✅
(exact, Δ=1 — measured, not predicted), with the fallback decision recorded ✅ (**not used**: the
fallback exists for a feature that cannot be made exact, and this one is; reaching for it at ~6µs/tile
to buy nothing is exactly the misuse Phase 14 priced itself to prevent).

### Phase 17 — pattern fills + image sampling  ✅ (image paint; pattern-of-a-scene deferred to 18)

**The plan got the two filters exactly backwards, and that is the whole phase.** It expected
nearest to be exact and budgeted perceptual mode plus a Phase 14 fallback for bilinear. Bilinear
needs neither hedge and sits at the floor on its merits. **Nearest** is the one carrying an
unbounded hazard — it is the third instance of the same species as the conic seam, and the plan's
own sentence "nearest is exact" is what makes it worth writing down.

- [x] **`paint.Image`** (`paint/image.go`): premultiplied RGBA8 texels, `Filter` (Nearest/Bilinear),
      `EdgeMode` (Clamp/Repeat/Mirror). `paint.ImageAt` is the canonical evaluator and
      `raster.wgsl`'s `imageColor` is the verbatim port — the `MeshAt` discipline, for the same
      reason. `EdgeMode.Wrap` is the pinned index map.
- [x] **Pinned filter kernels, and the crux held.** `raster.wgsl` binds a `texture_2d<f32>` and reads
      it with `textureLoad` — **no sampler**, so no driver's filtering enters the contract. An
      rgba8unorm texel arrives as exactly `n/255`, which is the number the CPU computes. Nearest is
      therefore a pure texel copy on both sides (`TestNearestIsATexelCopy` pins the `b/255*255 == b`
      round-trip the claim rests on, for all 256 bytes). `lerpC` is written `a + (b-a)*t` rather than
      WGSL's `mix()`, which is `a*(1-t)+b*t` — algebraically equal, a different f32 expression, and
      exactly the substitution Phase 13's ordering audit exists to catch.
- [x] **No tenth binding, and no ninth storage buffer.** `raster.wgsl` was already at **eight**
      storage buffers — WebGPU's default `maxStorageBuffersPerShaderStage`, the wall the mesh hit in
      Phase 16. Texels go in a sampled texture (a different limit, 16 by default) and the per-node
      parameters go in slots that already existed: `G0`=atlas origin, `G1`=image size, filter and
      edge in the **flags word** at bits 8-11 and 12-15, beside blend (0-3) and composite (4-7). The
      `Node` struct did not grow.
- [x] **Corpus: 6 entries, filter × edge, all `Quantized`, all measured Δ≤1** (43 → 49 entries). The
      cross is the point: filter and edge share a word with the two compositing axes, so a wrong
      shift is only visible where two fields are non-zero — the `composite-x-blend-*` argument
      applied to a second pair. `TestImageEntriesRenderDistinctly` requires the six to render six
      *different frames*, which is stronger than the structural check the blend family gets: if
      `Edge` were ignored entirely, three entries would build distinct scenes, render identical
      pixels and pass at Δ=0 on both backends.

**FINDING — nearest is a step function, and that beats "it introduces no arithmetic".** The plan's
reasoning was that averaging four texels in f32 cannot match averaging them in f64, while picking one
texel involves no arithmetic to diverge. The arithmetic was never the question. The question is
whether the paint is **continuous in the pixel position**, which is what decides if the f32-vs-f64
difference in the sample point stays sub-LSB or gets amplified — the lesson the conic seam and the
mesh silhouette each taught once already.

Measured on one scene, one nudge, one field changed (`backend/gpu/image_test.go`):

| probe | Δ |
|---|---:|
| nearest, nudge=0 — **every pixel exactly on a texel boundary** | **0** |
| nearest, nudge=1e-9 — below f32's ulp at 0.5 | **255** |
| bilinear, same geometry, same nudge | **1** |

`floor(q)` is a threshold and a threshold has no headroom in front of it: the two backends do not
need to disagree by much, only at all, and then the colour jumps a whole texel. The magnitude is set
by the **texels**, not by the arithmetic — so, exactly like a conic's seam, there is no derivative to
bound and no budget that could be fitted. Bilinear is continuous: where the backends pick different
`i0` they also pick `fx` near 1 and near 0, and the weighted average lands in the same place.

**The nudge=0 row is the more surprising one and it is the control.** A transform that puts *every*
pixel centre precisely on a texel boundary — a translate of exactly half a texel, the most ordinary
thing a caller can ask for — is Δ=0. An exact tie is not a disagreement; both precisions floor the
same integer identically. The hazard is not "on a boundary", it is "near a boundary and not on it",
which is measure-zero-adjacent and utterly reachable: half a pixel **plus a hair** is what an
accumulated transform actually produces.

**The mechanism is asserted, not inferred.** Δ=255 alone is equally consistent with the GPU sampler
simply being broken near a boundary — a bug, wanting the opposite response.
`TestTheNudgeIsInvisibleToF32AndDecisiveForF64` asks each backend directly: the GPU's frames are
**byte-identical** with and without the nudge (its inverse matrix rounds back to exactly -0.5, so it
never received the information), and the CPU's are not. Both halves fail loudly if the analysis rots.

**Consequences, all following the conic precedent:**

- The generator emits **bilinear only** (`randImage`), for the reason it emits closed conics only. It
  still exercises the inverse transform, both floors, the wrap, the atlas fetch and all three edge
  modes; only the branch that picks one texel instead of four is out.
  `TestGeneratorEmitsOnlyBilinearImages` pins it **in both directions** — an exclusion satisfied by
  never generating an image would be vacuous (741 images over 2000 seeds; all three edge modes
  reachable at 254/250/237).
- The six nearest/bilinear corpus entries are gated `Quantized` and **not** `Identical`, though
  nearest measures Δ=0 on three of them. That Δ=0 is a fact about these transforms on this driver,
  not a property. Gating on it would read luck as a guarantee — the same call the `composite-*`
  entries make.
- **Phase 14's fallback does not answer this either**, and the phase is now 0-for-2 on its predicted
  customers. It named "hardware-filtered image sampling" and "mesh-gradient patch interpolation";
  mesh landed at Δ=1 (Phase 16) and image sampling is not hardware-filtered by construction. Nearest
  *is* inexact, but falling back at ~6µs/tile for a node that is usually the whole frame is worse
  than just using the CPU backend — the misuse that price was written down to prevent. A
  discontinuity is a scene-level fact, not a tile-level one. `fallback.go` now says so; its comment
  had been carrying both dead predictions as live motivation.
- **Perceptual mode still has no customer.** Built in 12a for exactly this phase and Phase 16, and
  refused by both. Worth restating rather than quietly shipping a feature into it.

**Other things measured rather than assumed:**

- **Premultiplied texels are not a storage convention.** Bilinear must average premultiplied values
  or an invisible colour bleeds into an alpha edge. Stored premultiplied, that colour is *not
  representable* — which is why `TestBilinearAveragesPremultiplied` can only be written one way
  round, and is the whole argument for the type.
- **The fingerprint covers the texels.** A node records where its image sits and how to sample it;
  nothing in `Segments`/`Nodes`/`Stops` records what is **in** it. A caller redrawing into `Pix` in
  place — a video frame, any animation reusing its buffer — would leave the encoding byte-identical
  and get last frame's pixels back forever from Phase 7c's skip. This is Phase 14's `FallbackTiles`
  trap one field up, and it is closed by hashing rather than by declaring `Pix` immutable.
  Injection-verified.
- **Priced, because the hash is not free.** `BenchmarkEncodeImage` sweeps image size: 8×8 26.7µs,
  64×64 29.5µs, 256×256 65.0µs, 512×512 **182µs** (1 MB atlas) — so the atlas path costs ~155µs/MB
  for build+clear+hash together. Against Phase 7c's ~2.8ms saved on a static frame it is plainly
  worth it, and on a changing frame it is ~6%. The escape hatch if that ever binds is a
  caller-supplied generation counter, which is an API burden and is not warranted by these numbers.
  **Encode stays 0 allocs/op** at every size, and `BenchmarkEncodeManyNodes` is unmoved at 0 —
  Phase 7b's property survives (the dedup map is nil for image-free scenes).
- **One image drawn N times uploads once.** The atlas dedups by (texel array, dimensions) —
  deliberately not by filter or edge, which live in the flags word, so one sprite sheet sampled two
  ways still shares a slot. Without it a scene drawing an icon twenty times would build twenty
  copies, which is a pathology rather than an inefficiency.
- **SVG reports it, and the classification is the interesting part** — see Phase 24's addendum below.

**Deferred, with the reason:**

- [ ] **`paint.Pattern` as a tiled *scene*.** An image + transform + repeat mode **is** a tiled
      pattern, and that is what shipped. A pattern whose content is a *scene* needs an
      offscreen-layer pass to rasterize the content before it can be sampled — which is precisely
      the machinery **Phase 18** adds for group opacity and masks. Building it now would duplicate
      that pass and then delete one copy. Sequenced after 18, not dropped.
- [ ] **Atlas overflow is an error, not a fallback.** An atlas exceeding
      `maxTextureDimension2D` fails at `newAtlas` with a named error rather than deep in the driver.
      That is a backend limit and not a parity break — the CPU renders the scene fine, and an error
      is a different fact from a wrong pixel. A real packer (rather than a vertical stack) is the fix
      when something needs it.

**Done:** nearest and bilinear both pass exact parity (Δ≤1) across all three edge modes; the filter
kernel and edge modes are byte-defined in shared code and read with `textureLoad`, not delegated to a
sampler. **933,938 differential executions** over the widened space, no divergence. ✅ — with the
correction that the plan's tolerance predictions were inverted, and the note that the feature it
called exact is the one no differential oracle can cover.

### Phase 18 — layer / group opacity + masks  ⏳ planned

The first feature that needs a **compositing stage beyond a single node** — a group renders to an
isolated buffer, then composites as a unit.

- [ ] `scene` gains a group construct (`Group{Nodes, Opacity, Mask, Isolated}`) or a node-range
      grouping. Group opacity ≠ per-node opacity: `(A over B) at 0.5` differs from `A@0.5 over B@0.5`.
      Requires rendering the group to an offscreen layer, scaling its alpha, then compositing.
- [ ] **Masks:** a luminance or alpha mask (a rendered path/gradient/image) multiplied into the
      group's coverage — a generalization of the existing clip-mask machinery (`Renderer.clipMask`,
      GPU `clipf[16]` sweep), extended from binary coverage to arbitrary [0,1] mask values.
- [ ] GPU: a second target texture for the layer + a composite pass; CPU: render group to a scratch
      `image.RGBA`, then composite. This is the largest structural change in Section C — it adds a
      pass to both pipelines. Sequence it after 15 (it composites *with* the PD operators).

**Done when:** group-opacity and masked-group corpus entries pass exact parity, and the
`group ≠ per-node` distinction is proven by a scene that renders differently under each.

### Phase 19 — sRGB vs linear-light compositing toggle  ⏳ planned

Merges Part I's deferred sRGB item (Phase 10 last bullet). That item was gated on "6b's sRGB
surface" and the gate turned out not to exist: 6b measured `bgra8unorm` on Metal and takes
the non-sRGB format deliberately, so nothing in the present path converts today. This phase
is therefore **not** unblocked by 6b — it stands on its own.

- [ ] A `parity.ColorSpace` toggle: composite in linear-light (decode sRGB→linear before blending,
      re-encode after) vs the current sRGB-space compositing. Applied identically on both backends.
- [ ] This phase makes the **compositing space** explicit and toggleable rather than implicit, and
      adds the decode/encode to both `raster/fill.go` and `raster.wgsl`. Verify the toggle changes
      output (blends visibly differ) and that each mode holds CPU/GPU parity independently.
- [ ] `blit.wgsl`'s `fs_main_srgb` already inverts an sRGB encode and is gated at the quantization
      floor — reuse its constants rather than writing a second transfer function, and note that the
      blit assumes the target holds sRGB-encoded bytes. If this phase makes the target *linear*,
      that assumption is what breaks, and `TestBlitToUnormSurfaceIsExact` is what will catch it.

**Done when:** both compositing spaces pass exact parity CPU-vs-GPU, and the linear-vs-sRGB
difference is demonstrated by a corpus entry that diverges between the two modes.

### Phase 20 — stroking parity audit (joins, caps, dashing, miter limits)  ⏳ planned

Largely **already implemented on CPU** — `path/stroke.go` (joins, caps, miter limit via
`paint.Stroke`), `path/dash.go` (dashing + phase). GPU consumes it by expanding strokes to fill
outlines on the CPU side pre-encode. So this is an **audit + parity-lock**, not a build.

- [ ] Enumerate the stroke parameter space (each `path.Join`, each `path.Cap`, miter-limit
      bevel-fallback boundary, dash pattern + phase, degenerate zero-length subpaths) and add a
      corpus entry per combination; assert exact parity — since GPU strokes are CPU-expanded today,
      parity should be Δ≤1 and any gap is a real bug.
- [ ] Decide the **GPU stroke-expansion story** explicitly and record it: keep CPU expansion (simple,
      already exact) vs move to GPU (Phase 9 territory, gated on encode-cost measurement). Default:
      keep CPU expansion; this phase just proves it's complete and correct, and hands the "if it's
      slow" branch to Phase 9.

**Done when:** every stroke parameter combination has a passing exact-parity corpus entry, and the
expansion-location decision is written down with its measurement.

### Phase 21 — unified scene/command encoding format  ⏳ planned

Today the CPU backend walks `scene.Scene` directly while the GPU has its own `encode.go`. After
15–20 add features, that logic is duplicated in two places and drifts — a parity risk by
construction.

- [ ] Extract a single **command-encoding format** (flattened draw commands + resource tables) that
      *both* backends consume: GPU uploads it to buffers; CPU interprets it in a scanline loop. The
      encoder becomes the one place feature semantics live; both renderers become executors of the
      same command stream.
- [ ] Do this **after** the feature phases, not before — 15–20 reveal what the format actually needs
      (PD coefficients, gradient kinds, group/layer boundaries, mask refs), so designing it now would
      guess. The parity machine (Section A) makes the refactor safe the way it made Part I's rewrites
      safe.

**Done when:** both backends render the full corpus from one shared encoded representation, parity
unchanged, with feature logic no longer duplicated across `backend/cpu` and `backend/gpu`.

### Phase 24 — SVG backend conformance audit  ✅

Found while writing the release docs (2026-07-16), by reading `svg.go` rather than trusting this
file's own account of it. The roadmap had recorded the conic drop twice (Phase 16, and the standing
path-clip caveat) as "the SVG backend drops any paint it cannot express" — which undercounted the
problem and, more importantly, **misclassified it**.

**The distinction that makes this a phase and not a caveat: dropping a node it cannot express and
mis-rendering a node it CAN are different failures.** A dropped conic is incomplete output — SVG
1.1/2 genuinely have no conic or mesh primitive, so the format is the limit and the node is visibly
absent. But `Node.Op` and `Node.Composite` are simply **never read**, so a Multiply node exports as
**Normal**: a node that is present, plausible, and **wrong**. Nothing in the output says so. That is
the same species of problem as 12d's `RequestAdapterOptions.BackendType` — an API that quietly
answers a different question than the one it was asked.

The full list, read from the code:

| Feature | Today | Class |
|---|---|---|
| Blend mode (`Node.Op`) | **ignored — exports as Normal** | **wrong output** |
| Composite op (`Node.Composite`) | **ignored — exports as SrcOver** | **wrong output** |
| Path clips (`Node.Clips`) | **ignored — node drawn unclipped** | **wrong output** |
| Conic paint | node dropped (`paintRef` default) | missing output (format limit) |
| Mesh paint | node dropped (`paintRef` default) | missing output (format limit) |
| Rect clip (`Node.Clip`) | honoured | ok |
| Solid / linear / radial paint | honoured | ok |
| Path, transform, stroke, fill rule | honoured | ok |

Note the first three are **expressible in SVG** and simply are not emitted: `mix-blend-mode` covers
the W3C separable set, `<clipPath>` takes arbitrary path data (the rect case already builds one),
and the Porter-Duff operators map to `<feComposite operator=…>`. These are omissions, not format
limits.

**↑ The last clause of that paragraph is wrong, and the audit's first result was overturning it.**
See "What the table got wrong" below. The table is kept verbatim because the correction is the
entry worth having.

- [x] **Decide the contract and write it down.** Chosen: **report**. `Encode` now returns
      `(Report, error)`; `Report.Dropped` names the node index and the feature, and `Report.Lossy()`
      is the one-line question. It does **not** error — the document is still written and the node
      still dropped, exactly as before; the difference is that the caller is now told. Erroring was
      considered and rejected (below).
- [x] **Nothing currently tests any of this.** Confirmed and fixed. The suite went from 6 tests and
      2 goldens to a conformance file with a test per row, and **7 of them were red against the
      code as it stood**: `TestBlendModeEmitsMixBlendMode`, `TestEverySeparableBlendModeHasACSSName`
      (11 subtests), `TestCompositeOpIsReported`, `TestBlendUnderNonSrcOverCompositeIsNotEmitted`,
      `TestPathClipEmitsClipPath`, `TestPathClipEvenOddEmitsClipRule`, `TestNestedPathClipsIntersect`.
- [x] **Audit exhaustively over the scene model, not over the paint switch.** Done, field by field
      over `scene.Node` against what `writeNode` reads. Result below: the five rows are confirmed,
      `Fallback` is correctly ignored, and there is **no sixth field**.

#### What the field-by-field audit found that the paint switch could not

The exhaustive pass over `scene.Node` confirmed the table's five rows and added nothing to them —
so the prediction that a sixth gap was hiding in the node model was **wrong**. What it did produce
is four **near misses**: places where the SVG backend is correct, but only because two
coordinate-space or default-value conventions happen to line up. None was verified before; all four
are now recorded because each is one refactor away from becoming a real gap.

| Checked | Why it could have been wrong | Measured |
|---|---|---|
| Gradient coordinate space | `paintRef` emits `gradientUnits="userSpaceOnUse"`, which SVG resolves **inside** the element's own `transform`. If the model's gradients were device-space, every transformed node's gradient would be doubly transformed. | **Correct.** `cpu/shader.go` does `minv.Apply(pixel)` — the device pixel is mapped *back* into local space, so gradients are pre-transform, exactly like `Node.Path`. The two conventions agree. |
| Stroke width space | SVG scales `stroke-width` by the element's `transform`. A renderer that expanded the stroke in device space would disagree under any scale. | **Correct.** `cpu.strokeOutline` strokes `n.Path` untransformed and `FillPaint` applies the matrix afterwards, so width rides the transform as SVG expects. |
| Miter limit default | `writeStroke` omits `stroke-miterlimit` when `MiterLimit == 0`. If the renderer's default were anything but SVG's, every unset miter join would differ. | **Correct.** `path.Stroker.miterLimit()` returns **4** for a non-positive value; SVG's initial value is also 4. Omission is the right encoding. |
| Dash odd-count + phase | SVG repeats an odd-length `stroke-dasharray` to even length. A renderer that cycled the raw pattern instead would invert every other dash. | **Correct.** `path.normalizeDash` does `pat = append(pat, pat...)` for odd input — SVG's own rule — and `dashStart` consumes a positive phase forward into the pattern, matching `stroke-dashoffset`. |

#### What the table got wrong

**`Node.Composite` is not expressible in SVG.** This document filed it under "expressible and simply
not emitted", mapping it to `<feComposite operator=…>`. That is wrong. `feComposite` combines two
**filter inputs** inside a filter graph; it does not composite an element against the canvas
backdrop. So `Composite` belongs beside conic and mesh as a **format limit**, not beside blend as an
omission. The row moved class:

| Feature | Class as recorded | Class as measured |
|---|---|---|
| Composite op (`Node.Composite`) | wrong output — expressible, not emitted | **wrong output — and NOT expressible.** Reported, not emitted. |

##### The first version of this correction was itself unverified, and wrong

Kept, because it is the sharpest entry in the file. The paragraph above originally closed with: "Doing
that requires `BackgroundImage` as a filter input — which SVG 1.1 defined, **no browser ever
implemented, and SVG 2 removed**." Three claims, none checked against the text, in the very commit
whose subject is *asserting from knowledge instead of measuring*. Checked now:

| Sub-claim | Verdict |
|---|---|
| `feComposite` combines filter inputs, not element-vs-backdrop | **Holds.** |
| Reaching the backdrop needs `BackgroundImage` | **Holds.** SVG 1.1 §15.7: "an image snapshot of the canvas under the filter region at the time that the filter element was invoked". |
| No browser ever implemented it | **Holds.** MDN: "`BackgroundImage` is not supported as a filter source in modern browsers" — it documents an `feImage` workaround. |
| SVG 2 removed it | **False.** |

SVG 2 removed its *entire* filters chapter, delegating to Filter Effects 1
([changes K.2.18](https://www.w3.org/TR/SVG2/changes.html#filters): "Removed this chapter (replaced by
Filter Effects specification"). And [Filter Effects 1](https://drafts.csswg.org/filter-effects-1/)
still lists `BackgroundImage` in the `in` grammar today, redefined as "the back drop defined by the
current **isolation group** behind the filter region". What was actually dropped is
`enable-background`, the SVG 1.1 property that *armed* it —
[Appendix A](https://drafts.csswg.org/filter-effects-1/#AccessBackgroundImage): "This specification
does not support the enable-background property. UAs must support the `isolation` property instead."
`BackgroundImage` was re-plumbed from `enable-background` onto `isolation` and then implemented by
nobody. Still unreachable; **different reason than the one claimed**.

The conclusion survived. The argument for it did not — and an argument that lands on the right answer
by luck is the thing this phase exists to catch.

**The load-bearing citation needs no filter at all.** SVG merges every element with its backdrop using
**source-over and nothing else**, and offers no property to change it.
[Compositing 1](https://www.w3.org/TR/compositing-1/) gives elements `mix-blend-mode`,
`background-blend-mode` and `isolation` — blend, never composite. The Porter-Duff operators appear in
CSS only as values of canvas 2D's `globalCompositeOperation`, and that is still true in the
[Compositing **Level 2**](https://drafts.csswg.org/compositing-2/) editor's draft, which states the
present model outright — "each element is rendered into its own buffer and is then merged with its
backdrop using the Porter Duff **source-over** operator" — and puts element-level Porter-Duff in the
*future* tense: "This specification **will** define a new compositing model… offering additional
Porter Duff compositing operators." A draft that may change at any moment is not a format capability.
That is why `Composite` is reported, and it is why the answer does not move if `BackgroundImage` is
ever implemented.

**And the blend row is coupled to the composite row — which a table of independent rows cannot
say.** `mix-blend-mode` **implies source-over compositing**; CSS has no way to spell "multiply, but
XOR-composited". So a node with `Op=Multiply, Composite=Xor` cannot have its blend emitted either:
`mix-blend-mode:multiply` under `Xor` renders Multiply-**over**, which is not a partial answer but a
**different wrong one**. Blend is emitted only where `Composite == SrcOver`; otherwise both axes drop
together and the report names both.

This is the finding, and it is a finding about the *shape* of the table rather than a row in it. The
table invites fixing each row on its own, and fixing the blend row alone — the obvious reading — would
have replaced a silent wrong output with a reported-but-still-wrong one and looked like progress.
`TestBlendUnderNonSrcOverCompositeIsNotEmitted` is the gate that pins it. The audit was asked to look
for a sixth *field*; the thing it actually found was a dependency between two fields, which is why
reading the scene model did not surface it and reading the format spec did.

#### Addendum (Phase 17): the audit's machinery caught the next one, which is the point

`paint.Image` arrived and `TestCorpusLossReportIsAudited` went **red on contact** — six scenes
reporting a drop "this phase did not account for". That is this phase working as designed: the gap
was found by a gate rather than by someone reading `svg.go` a year later.

And the classification was the trap again, in the same shape. An image *looks* expressible — SVG has
`<pattern>`, a pattern holds an `<image>`, CSS has `image-rendering` — so the obvious reading is
"expressible and simply not emitted", the class this phase put `mix-blend-mode` in. **Checked
against the text rather than asserted, and the reading is wrong.** The load-bearing citation is the
**filter**, and it disqualifies every image node regardless of edge mode: [CSS Images
3](https://www.w3.org/TR/css-images-3/) declines to pin a kernel, in as many words —
`image-rendering:auto` is "The scaling algorithm is UA-dependent"; `smooth` says only that "scaling
algorithms that 'smooth' colors are **acceptable, such as** bilinear interpolation"; `crisp-edges`
"**may** be scaled using nearest neighbor **or any other UA-chosen algorithm**"; and `pixelated` —
the value that sounds like Nearest — "**allows minor smoothing** as necessary". Not one names a
function. Phase 17's entire correctness content is that the kernel is pinned in shared code and not
a driver's opinion; emitting an image would hand it straight back to the user agent.

The edge modes fail independently, and the detail is worth keeping: SVG's `<pattern>` tiles "to
infinity in X and Y" with no alternative, so Clamp and Mirror have nowhere to go. `spreadMethod`
names exactly **pad/reflect/repeat** — this paint's three edge modes under other names — and it is a
*gradient* attribute that patterns do not take. The vocabulary is in the format and is unavailable
where it would be needed.

**The coupling is the same finding as blend/composite, one axis over.** Repeat alone *is* tileable,
so a per-row reading of this table would "fix" the repeat case and ship UA-defined filtering under a
node that looks correct — trading a reported gap for an unreported wrong pixel, which is the move
this phase exists to refuse. Filter and edge are not independent rows. `TestImagePaintIsReported`
pins it with `repeat-is-tileable-and-still-drops` as a named subtest.

Reported and dropped, beside conic and mesh.

#### Measured

- **SVG cannot fully express 24 of the 49 corpus scenes; 25 encode losslessly.** (Was 18 of 43
  before Phase 17 added six image scenes — every one of them a genuine format limit.) Logged per-run by
  `TestCorpusLossReportIsAudited`, which pins the list rather than merely printing it — a silently
  growing set of drops is exactly what this phase found, and a test that logs without asserting
  would let it happen again while staying green. The 18: the 11 non-`SrcOver` composite scenes, the
  4 `composite-x-blend-*` crosses, `gradient-conic`, `gradient-conic-seam`, and `mesh`.
- **11 corpus blend scenes now carry their mode onto the wire.** Every one of them exported as
  **Normal** before this phase — the wrong output was live across the whole blend family, not a
  corner case.
- The two goldens (`sample.svg`, `gradient.svg`) **did not move**, which is its own small finding:
  the existing golden scenes contain no clip, blend or composite at all. They could not have caught
  any of this.

#### Tried and rejected

- **Erroring on an inexpressible node.** The strictest reading of "silence is not an option", and
  rejected on two grounds. It breaks every existing caller whose scene contains a conic or mesh —
  including `examples/07-conic-mesh`, which exists precisely to demonstrate the paints SVG lacks —
  and it conflates "this scene did not survive intact" with "this export failed", which are
  different facts. A dropped conic still produces a useful document. The report says so without
  making the caller handle an error it usually cannot act on.
- **Emitting `mix-blend-mode` unconditionally and reporting the dropped composite separately.**
  Maximizes fidelity in the common `SrcOver` case and knowingly ships wrong pixels in the coupled
  case. Rejected: it trades a silent wrong output for a reported wrong output, and this phase's whole
  argument is that a node which is present, plausible and wrong is the failure worth removing.
- **Merging multiple clips into one `<clipPath>`.** Two children inside one `<clipPath>` **union**;
  a clip stack **intersects**. Nesting one `<g>` per clip is what intersects in SVG, and the failure
  mode of getting it wrong is *too much surviving*, which looks like a rendering bug rather than an
  encoding one. `TestNestedPathClipsIntersect` pins the nesting.

#### The gate, and why it is not a parity gate

**Not a parity question.** SVG emits vectors, not pixels, so there is nothing to diff at the channel
level and none of this belongs to the tolerance contract. That is precisely why it drifted: **the SVG
backend is the one output path the parity machine does not watch**, and it accumulated five silent
gaps while the two rasterizers were held to Δ≤1 over millions of executions. The apparatus only
protects what it is pointed at — so fixing the five gaps without pointing something at it would have
left the diagnosis true and merely reset the clock.

Three gates now watch it, and each was verified by injection:

- **`TestEverySceneNodeFieldHasAnSVGContract`** — reflects over `scene.Node` and fails if any field
  has no recorded answer in `svgFieldContract`, or if the map names a field the struct no longer has.
  This is the structural answer to "the conic drop was found because someone added conic": a field
  added tomorrow with no SVG decision is now a **build failure rather than a discovery**. Deciding to
  ignore a field is a valid answer — `Fallback` is one — but it must be *decided*.
  *Injection:* adding `Opacity float64` to `scene.Node` turns it red naming the field (and `Opacity`
  is Phase 18's own planned field, so this gate will fire for real when group opacity lands);
  deleting the `Clips` row turns it red from the other direction. Restored, green.
- **`TestCorpusScenesEncodeAsWellFormedXML`** — the same 43 scenes the differential runs, through
  this encoder, asserted to parse. A far weaker claim than parity, and stated as the weaker claim it
  is: well-formed XML is not correct SVG. Its value is breadth — before this phase, zero corpus
  scenes had ever been through the SVG backend.
- **`TestCorpusLossReportIsAudited`** — pins which scenes are lossy and why. *Injection:* removing
  the `rep.drop` call in `Encode` turns `TestConicPaintIsReported` and `TestMeshPaintIsReported` red;
  restored, green. Worth recording that those two were **green from the moment they compiled**,
  because the reporting arrived with the `Report` type itself — so unlike the other seven they were
  never observed red, and the injection is the only evidence they gate anything.

**Done:** every `scene.Node` field is either honoured or reported; the contract is written down in
`svgFieldContract` and enforced by reflection; 43 corpus scenes and a per-row conformance suite watch
the backend that nothing watched before. `Encode`'s signature changed to `(Report, error)` — a
breaking change, taken deliberately **before** the v0.1.0 tag rather than after.

---

## Section D — performance & observability (build alongside Section C)

### Phase 22 — instrumentation: timing, occupancy, visual diff  ⏳ planned

- [ ] **Benchmark harness at equal correctness.** Extend the existing `bench_test.go` so every
      benchmark is paired with the parity assertion for the same scene — a speedup is only reported
      if the output still passes its `tol`. "Faster" that isn't "still correct" is not a result.
- [ ] **Per-stage timing.** Break the frame into encode / upload / dispatch / readback / blit /
      present with GPU timestamp queries where available; log per-stage ms so a regression is
      attributed to a stage, not the whole frame. Extends Part I's `logTiming`. 6b's
      `BenchmarkPresentVia*` pair is the crude version — whole-path wall clock, differenced against
      dispatch-only to price readback vs blit — and it works because a benchmark can be pinned to a
      scene and a size. Note what it cannot do, which is why this item stands: it cannot attribute
      cost *inside* a stage, and it cannot be read off a live window at all (vsync pins the frame
      rate to the display and macOS throttles unfocused ones — see 6b).
- [ ] **Tile occupancy metrics.** Per-tile segment-count / node-count / clip-count histogram from the
      encoder (the data already exists in `TileSegOff`/`TileNodes`); surfaces load imbalance (the
      "one dense tile stalls the dispatch" failure mode). Fallback coverage is already counted —
      Phase 14 landed `gpu.Stats{Tiles, FallbackTiles, Dispatches}` — so what is left here is
      *logging* it per frame alongside the per-stage timings, not measuring it.
- [ ] **Visual diff overlay tool.** A CLI/debug mode that renders a corpus (or replayed fuzz) scene
      on both backends and writes a heat-map PNG of per-pixel delta (amplified), plus side-by-side
      CPU|GPU|diff. This is the human end of 12c's automatic bisect — when a delta is subtle, *look*
      at where it is. Reuses `internal/goldentest`'s diff-image writing.

**Done when:** `go test -bench` reports frame time only for scenes that pass parity; per-stage +
occupancy numbers print for the corpus; and the diff tool renders a heat-map for any corpus/seed on
demand.

---

## Section E — scope decisions (recorded, not deferred)

### Phase 23 — text / glyph rasterization parity: **out of core scope (for now)**

- [ ] **Decision:** glyph rasterization is **out** of the initial parity scope, and the reason is
      recorded here rather than left open. A glyph is, after flattening, just filled paths — which
      the parity machine *already* covers — so text adds no new *rasterization* correctness question;
      what it adds is font loading, hinting, and subpixel positioning, none of which is a renderer
      concern and all of which would balloon scope. If added later, glyphs enter as ordinary filled
      `scene.Node` paths through the existing pipeline and are gated by the same corpus/fuzz machine —
      no special-case path. Hinting/subpixel AA, if ever wanted, would be a *new AA model* and fall
      under Phase 11's "alternative AA model gets its own perceptual golden set" clause.
- [ ] **Revisit trigger:** a real target that renders text as a first-class primitive (not
      pre-outlined paths). Until then, text is representable (as outlined paths) without being a
      feature.

### Tolerance, restated as a scope decision

Why tolerance is **not zero** everywhere: two independent float pipelines (f64 CPU, f32 GPU) that
round the *same* value to 8 bits can differ by 1 — that Δ≤1 is quantization, not error, and driving
it to 0 would mean making one backend match the *other's rounding* rather than the true value, which
is worse. Δ=0 **is** required and achieved wherever both backends run the same integer/analytic path
(coverage winding, clip intersection). Tolerance above Δ≤1 is only ever a *named, owned* exception
(Phase 11). This is the whole philosophy of the project stated as a number: correctness is parity,
parity is measured, and the measurement has a budget with an owner.
