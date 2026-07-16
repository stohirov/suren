# sukho — native-GPU renderer plan

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
                   └─ 6b native surface (on-device only)
Phase 7 (frame cost) ── independent of 6, but makes 6 worth watching (fast frames)
Phase 8 (coarse lists) ── needs Phase 7's EncodeInto scratch reuse to stay cheap
Phase 9 (GPU flatten/stroke) ── gated on Phase 8 diagnosis: only if CPU encode dominates
Phase 10 (feature parity: blend modes, path clips) ── touches BOTH cpu + gpu + wgsl
```

## Phase 6 — windowed present (real-time on screen)  ⏳ in progress

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
one GPU→CPU→GPU roundtrip — removed in 6b.

### 6b — native wgpu surface present (`present.go`, on-device)

- [ ] glfw window + `wgpu.Surface`; configure a swapchain (surface format is typically
      `BGRA8UnormSrgb` — record it, don't assume RGBA). Quarantine glfw behind a build tag
      (`//go:build gpupresent`) so the default/headless build and CI test binary never link
      a display library.
- [ ] The compute shader writes a `storage<rgba8unorm, write>` texture; swapchain images
      are `RenderAttachment`, **not** storage-writable. So: compute into the offscreen
      target as today, then a tiny fullscreen-triangle **blit pipeline** (sample offscreen
      tex → surface, handling the RGBA→BGRA/sRGB format difference) writes the frame. This
      blit is the new bit; the raster path is unchanged.
- [ ] Present loop: acquire next surface texture → dispatch compute → blit render pass →
      present; on resize, reconfigure the surface **and** call `target.resize` from 6a.
- [ ] Manual on-device validation (sandbox/CI have no display): render the sample scene to
      a real window, screenshot, eyeball against the offscreen PNG.

**Done when:** the sample scene presents directly from GPU memory with no readback, on
darwin/arm64 (Metal). CI still gates on the offscreen parity tests, not the window.

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
- [ ] **Colorspace / sRGB correctness** once 6b introduces an sRGB surface: confirm the
      linear-premultiplied compositing still matches the CPU `image.RGBA` path end to end.

**Done when:** each feature renders identically (within tolerance) on CPU and GPU via a
dedicated parity scene. (Blend modes ✅; path clips ✅; sRGB remains, gated on 6b.)

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
- **Encoder unit tests** (pure Go, no GPU): node/segment/stop counts, kinds, bbox,
  clip flags, tile-bin structure; extend to per-tile segment ranges + backdrops in Phase 8.
- **Headless GPU roundtrip** (`TestDeviceInit`, offscreen render + readback) so the
  full pipeline is exercised without a display. Stays the CI gate — windowed present (6b)
  is validated manually on-device only.
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
| No display in CI/sandbox | validate offscreen (readback); windowed present is on-device only |
| Tiling breaks winding across tile edges | backdrop routing + clip-across-tiles parity test |
| Per-frame encode becomes the bottleneck | measured (~22%); Phase 7 buffer/scratch reuse, Phase 9 GPU flattening |
| Huge many-segment path × many tiles (backdrop re-iteration) | acceptable now; Phase 8 coarse per-tile segment lists |
| Swapchain image not storage-writable (can't compute into surface) | Phase 6b computes offscreen then blits via a fullscreen render pass |
| Surface format is BGRA/sRGB, not linear RGBA | record the surface format; the blit shader handles the conversion; verify colorspace parity (Phase 10) |
| glfw drags a display dep into headless builds/CI | quarantine behind `//go:build gpupresent`; default build and test binary never link it |
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
| A paint whose colour is DISCONTINUOUS in the pixel position amplifies the f32-vs-f64 floor to the full distance between two stops, with both backends correct | Phase 16: conic's seam. Unlike dodge/burn there is no derivative to bound — the magnitude is the stop colours' — so no budget can be fitted. The generator emits closed loops only (`randConic`), and the hazard is measured rather than asserted (`TestConicSeamDivergesWithoutBound`: Δ=255) so the restriction has evidence behind it |
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
- `backend/gpu/fallback.go` — per-tile CPU fallback + `Stats` (Phase 14)
- `backend/gpu/encode.go` — scene → GPU buffers + tile bins (Phase 7 `EncodeInto`, Phase 8 coarse lists)
- `backend/gpu/raster.wgsl` — the fine rasterizer (Phase 8 per-tile lists, Phase 10 blend/clip)
- `backend/gpu/renderer.go` — encode → upload → dispatch (Phase 6 `Resize`, Phase 7 buffer reuse)
- `backend/gpu/target.go` — offscreen texture + readback (Phase 6a `resize`)
- `backend/gpu/present.go` — glfw surface + blit present (Phase 6b, new; `gpupresent` build tag)
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
  17 patterns + image sampling ┤ 17 needs 11's AA/filter contract
  18 group opacity + masks ──┘ 18 needs an offscreen-layer pass (new compositing stage)
  19 sRGB vs linear toggle ── merges Part I's deferred sRGB item; interacts with 6b surface
  20 stroking parity audit ── mostly CPU-done; question is the GPU expansion story
  21 unified scene/command encoding ── refactor; 15–20 make the duplication expensive first

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

### Phase 13 — float determinism controls  ✅ (12d's on-device half remains)

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
- [x] **FMA contraction — measured, then deliberately NOT pinned.** Go's spec permits fusing `a*b+c`
      and **it does on arm64** (verified against `math.FMA`), so the CPU reference's arithmetic is
      architecture-dependent in principle. It is not observable in practice: pinning every hot
      expression with explicit `float64()` conversions (`geom.Matrix.Apply`, `raster/fill.go`'s
      SrcOver) reproduced **all 21 CPU goldens bit-for-bit, ~4M channels, Δ=0**. The reason is
      quantitative and now recorded in `contract.go`: 8-bit output is ~2.4 decimal digits, f64 carries
      ~15, so the CPU's last-bit noise sits ~13 orders of magnitude below the rounding decision it
      would have to cross. **f32 has only ~4–5 orders of headroom — which is why the GPU diverges and
      the CPU cannot.** Pinning the CPU would cost FMA throughput to buy nothing. The same latitude on
      the GPU is *not* safe by that argument reversed, WGSL offers no way to forbid contraction, and
      that is now 12d's named first suspect.
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

### Phase 16 — conic + mesh gradients  ⏳ conic ✅; mesh planned

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
- [ ] **Mesh (Coons-patch / Gouraud):** the hard one. Per-patch bilinear/bicubic color
      interpolation over a grid of control points. Likely **perceptual-mode** (12a) because f32-vs-f64
      patch interpolation won't hit Δ≤1, and a candidate for Phase 14 CPU fallback if GPU divergence
      is large. Decide exact-vs-perceptual by measurement, then record the forcing operation per
      Phase 11.

**Done when:** conic passes exact parity ✅; mesh passes at its measured-and-documented tolerance,
with the fallback decision recorded.

### Phase 17 — pattern fills + image sampling  ⏳ planned

- [ ] `paint.Pattern` (a tiled scene/image with its own transform + repeat mode) and `paint.Image`
      (sample a source RGBA). New `Paint` implementors; encoder uploads image data as a texture
      (GPU) / holds the source (CPU).
- [ ] **Pinned filter kernels** (this is the correctness crux, ties to Phase 11's AA/filter
      contract): nearest and bilinear implemented **identically** on both sides — the CPU does the
      same weight math the shader does, rather than leaning on a hardware sampler whose filtering is
      driver-defined. Nearest is exact; bilinear is the first real candidate for perceptual mode +
      Phase 14 fallback — but note 14's price (~6µs/tile): falling back a full-frame image costs more
      than rendering the whole scene on the CPU, so the escape hatch only answers for a *small* node.
      A large bilinear image wants pinned kernels or perceptual mode instead.
      Repeat/clamp/mirror edge modes pinned identically.
- [ ] Corpus entries: nearest (exact), bilinear (perceptual or fallback), each repeat mode, non-axis
      -aligned transforms.

**Done when:** nearest-sampled patterns/images pass exact parity; bilinear passes at documented
tolerance; filter kernel + edge modes are byte-defined in shared code, not delegated to a sampler.

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

Merges Part I's deferred sRGB item (Phase 10 last bullet, gated on 6b's sRGB surface).

- [ ] A `parity.ColorSpace` toggle: composite in linear-light (decode sRGB→linear before blending,
      re-encode after) vs the current sRGB-space compositing. Applied identically on both backends.
- [ ] The 6b blit shader already must handle RGBA→BGRA/sRGB surface conversion; this phase makes the
      **compositing space** explicit and toggleable rather than implicit, and adds the decode/encode
      to both `raster/fill.go` and `raster.wgsl`. Verify the toggle changes output (blends visibly
      differ) and that each mode holds CPU/GPU parity independently.

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

---

## Section D — performance & observability (build alongside Section C)

### Phase 22 — instrumentation: timing, occupancy, visual diff  ⏳ planned

- [ ] **Benchmark harness at equal correctness.** Extend the existing `bench_test.go` so every
      benchmark is paired with the parity assertion for the same scene — a speedup is only reported
      if the output still passes its `tol`. "Faster" that isn't "still correct" is not a result.
- [ ] **Per-stage timing.** Break the frame into encode / upload / dispatch / readback (and, post-6b,
      blit / present) with GPU timestamp queries where available; log per-stage ms so a regression is
      attributed to a stage, not the whole frame. Extends Part I's `logTiming`.
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
