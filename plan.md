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
  Phase 10).
- **Encoder unit tests** (pure Go, no GPU): node/segment/stop counts, kinds, bbox,
  clip flags, tile-bin structure; extend to per-tile segment ranges + backdrops in Phase 8.
- **Headless GPU roundtrip** (`TestDeviceInit`, offscreen render + readback) so the
  full pipeline is exercised without a display. Stays the CI gate — windowed present (6b)
  is validated manually on-device only.
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

## Critical files

- `render/render.go` — `Renderer` interface, the CPU/GPU seam
- `backend/cpu/cpu.go` + `raster/fill.go` — CPU reference the GPU must match
- `backend/gpu/encode.go` — scene → GPU buffers + tile bins (Phase 7 `EncodeInto`, Phase 8 coarse lists)
- `backend/gpu/raster.wgsl` — the fine rasterizer (Phase 8 per-tile lists, Phase 10 blend/clip)
- `backend/gpu/renderer.go` — encode → upload → dispatch (Phase 6 `Resize`, Phase 7 buffer reuse)
- `backend/gpu/target.go` — offscreen texture + readback (Phase 6a `resize`)
- `backend/gpu/present.go` — glfw surface + blit present (Phase 6b, new; `gpupresent` build tag)
- `backend/window/window.go` — window loop; gains a GPU-backed variant (Phase 6a)
