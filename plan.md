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

## Future work (post-parity)

- **Windowed present:** glfw + wgpu surface (`present.go`), verified on-device (needs a
  display). Interim bridge: feed `ReadRGBA()` into the existing Ebiten window.
- **Buffer reuse for static scenes:** encode is ~22% of the GPU frame (~0.65 ms);
  cache/diff encoded buffers when the scene is unchanged.
- **Coarse segment lists:** each tile currently re-iterates a node's *full* segment
  list for the backdrop — fine for typical scenes, poor for one huge many-segment path
  spanning many tiles. A coarse pass with precomputed per-tile segment lists +
  backdrops (the Vello answer) removes the redundancy.
- **GPU-side stroke expansion / flattening** if CPU encode becomes the bottleneck.

---

## Testing strategy

- **GPU/CPU parity** is the primary correctness gate: same scene both renderers, raw
  premultiplied RGBA diff within tolerance (solid, many-nodes, clip; gradients next).
- **Encoder unit tests** (pure Go, no GPU): node/segment/stop counts, kinds, bbox,
  clip flags, tile-bin structure.
- **Headless GPU roundtrip** (`TestDeviceInit`, offscreen render + readback) so the
  full pipeline is exercised without a display.
- **GPU-vs-CPU benchmark** on a shared many-nodes scene; encode measured separately.

## Risk register

| Risk | Mitigation |
|---|---|
| Exact coverage on GPU diverges from CPU | port the algorithm verbatim; per-tile backdrop; raw-RGBA parity test on every stage |
| cgo/native dep won't build or run headless | spiked before committing — Metal device confirmed headless; dep quarantined in its own module |
| No display in CI/sandbox | validate offscreen (readback); windowed present is on-device only |
| Tiling breaks winding across tile edges | backdrop routing + clip-across-tiles parity test |
| Per-frame encode becomes the bottleneck | measured (~22%); buffer reuse / GPU flattening planned |
| Huge many-segment path × many tiles (backdrop re-iteration) | acceptable now; coarse per-tile segment lists planned |

## Critical files

- `render/render.go` — `Renderer` interface, the CPU/GPU seam
- `backend/cpu/cpu.go` + `raster/fill.go` — CPU reference the GPU must match
- `backend/gpu/encode.go` — scene → GPU buffers + tile bins
- `backend/gpu/raster.wgsl` — the fine rasterizer (ported signed-area coverage + backdrop)
- `backend/gpu/renderer.go` — encode → upload → dispatch, `render.Renderer` impl
