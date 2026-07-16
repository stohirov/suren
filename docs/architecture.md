# Architecture

A map for finding things. What each package is for, how the modules depend on
each other and why the split is shaped the way it is, and what happens to a scene
between `Render` and pixels.

For *why* correctness is built the way it is, see [correctness.md](correctness.md).
For the measurements behind every number here, see [roadmap.md](roadmap.md).

## The seam

Everything hangs off one interface, in `render/render.go`:

```go
type Renderer interface {
	Render(*scene.Scene) error
}
```

`scene.Scene` is retained: a whole frame of geometry, resolved to device space,
before anything is drawn. That is what makes a second backend possible at all —
a GPU pipeline wants the whole frame to batch, and an immediate-mode API would
have to guess. `Canvas` (also `render/render.go`) is the authoring layer that
builds one: it owns the CTM, the clip stack, and the blend/composite/fallback
state, with `Save`/`Restore` scoping. It flattens all of that into each
`scene.Node` as it is added, so a `Node` is self-contained and a renderer never
walks a state stack.

The CPU and GPU renderers are two independent implementors. Neither is privileged
in the type system — but the CPU one *is* the reference the GPU is measured
against, which is a correctness relationship, not an architectural one.

## Package layout

### Core — pure Go, zero dependencies

| Package | Files | Purpose |
|---|---|---|
| `geom` | `matrix.go` `point.go` `rect.go` | Affine matrix, point, rect. `Matrix.Apply` is on the hot path. |
| `path` | `path.go` `shapes.go` `flatten.go` `stroke.go` `dash.go` | Path building (`MoveTo`/`LineTo`/curves/`Close`), shape constructors, curve→line flattening (`FlattenInto` takes reusable scratch), stroke expansion (joins/caps/miter), dashing. |
| `paint` | `paint.go` | Paint types and their canonical evaluators. `Color`, `Solid`, `Linear`/`Radial`/`Conic`/`Mesh` gradients, `Stroke` params, `BlendMode`, `CompositeOp`, `FillRule`. Also `MeshAt`/`MeshEps` — the reference mesh evaluator `raster.wgsl` ports verbatim. |
| `raster` | `raster.go` `fill.go` `stroke.go` `tile.go` | The analytic signed-area rasterizer. `coverage()` is the algorithm ported verbatim to WGSL; `fill.go` holds `blend`, `porterDuff`, `Coefficients`, `clamp8`; `tile.go`'s `TileMask` gates a fill's writes without touching its coverage sweep. |
| `scene` | `scene.go` | `Scene`, `Node`, `ClipPath`. The retained frame. |
| `render` | `render.go` | The `Renderer` interface and `Canvas`. |
| `backend/cpu` | `cpu.go` `shader.go` | The reference renderer: scene → `*image.RGBA`. `shader.go` evaluates paints per pixel. |
| `backend/png` | `png.go` | `Encode` over `cpu.Render`. Thin. |
| `backend/svg` | `svg.go` | Scene → SVG. Has known gaps; see below. |

These import nothing outside the standard library. That is enforced by the module
split, not by convention.

### Quarantined — cgo, own `go.mod`

| Package | Files | Purpose |
|---|---|---|
| `backend/gpu` | `device.go` `encode.go` `renderer.go` `raster.go` `raster.wgsl` `target.go` `fallback.go` `blit.go` `blit.wgsl` `present.go` | The WebGPU compute renderer, plus native windowed present. |
| `backend/window` | `window.go` | `Run` (CPU) / `RunGPU` (GPU via readback bridge). |

Inside `backend/gpu`:

- `device.go` — wgpu instance/adapter/device/queue lifecycle. `NewDeviceOn(Backend)` selects a named backend and **verifies the adapter's reported backend matches the request** (see correctness.md — the obvious API silently lies).
- `blit.go` + `blit.wgsl` — moves the finished target onto a surface-format texture, and picks the surface format and present mode. Deliberately **outside** the `gpupresent` tag: a swapchain image is just a texture, so pointing the real blitter at an offscreen one of the same format tests the whole pixel path headlessly (Δ=0). Only `present.go`'s window plumbing needs a display.
- `encode.go` — scene → flat GPU buffers + tile bins. Pure Go, unit-testable without a GPU. The largest file and the one that holds the tile model.
- `renderer.go` — the frame: encode → fingerprint → upload → dispatch → fallback. Owns buffer reuse.
- `raster.go` — compute pipeline, bind groups, dispatch.
- `raster.wgsl` — the fine rasterizer. The ported `coverage()`, blend/composite, gradient and mesh evaluation, `quant8`.
- `target.go` — offscreen `rgba8unorm` storage texture, `resize`, readback (`readRGBA`, `align256`).
- `fallback.go` — per-tile CPU patch + `Stats`.

### Test infrastructure — `internal/`

Internal because it is apparatus, not API. `internal/parity` and `internal/sample`
are importable from the quarantined modules the same way any internal package is
within a repo.

| Package | Files | Purpose |
|---|---|---|
| `internal/parity` | `contract.go` `compare.go` `color.go` `ssim.go` `vacuity.go` | The tolerance contract. `contract.go` is the spec every gate cites; `Identical`/`Quantized`/`Budget`/`Perceptual` + `Config.Validate`. `vacuity.go`'s `NonTrivial` guards generated scenes. |
| `internal/corpus` | `corpus.go` `regress.go` | The named scene corpus. Each `Entry` carries the tolerance it earned. `regress.go` loads fuzz finds from `regress/*.json`. |
| `internal/parity/fuzz` | `gen.go` `spec.go` `codec.go` `shrink.go` `fuzz.go` | Scene generator, the `Spec` a find is stored as, JSON codec, delta-debugging shrinker, `FirstDiverging` bisect. |
| `internal/parity/props` | `gen.go` `props.go` | Property laws over generated scenes. Parameterised by a `RenderFunc`, so it never imports `backend/gpu`. |
| `internal/goldentest` | `goldentest.go` | CPU golden storage/compare. `AssertExact`, `AssertPerceptual`, `-update`. |
| `internal/sample` | `sample.go` | Shared scenes both backends and the benchmarks use. |

## Module graph

Three modules. The split is load-bearing and it exists for exactly one reason:
**a user who only wants CPU rendering should link zero third-party code.**

```
                  github.com/stohirov/suren                (module 1: zero-dep, pure Go)
                  ├── geom  path  paint  raster
                  ├── scene  render
                  └── backend/cpu  backend/png  backend/svg
                            ▲                 ▲
              replace ../.. │                 │ replace ../..
                            │                 │
   github.com/stohirov/suren/backend/gpu      │            (module 2: cgo)
   └── requires cogentcore/webgpu ──► wgpu-native (static .a, cgo)
                            ▲
        replace ../gpu      │
                            │
   github.com/stohirov/suren/backend/window                (module 3: cgo)
   └── requires ebiten + backend/gpu
```

Dependencies point **inward only**. `backend/gpu` imports the core; nothing in the
core imports `backend/gpu`. The core cannot acquire a cgo dependency by accident
because it is a separate module that does not require one.

The `replace` directives are all local path redirects:

| File | Directive | Why |
|---|---|---|
| `backend/gpu/go.mod` | `replace github.com/stohirov/suren => ../..` | Build the gpu module against the working tree's core, not a published version. |
| `backend/window/go.mod` | `replace github.com/stohirov/suren => ../..` | Same. |
| `backend/window/go.mod` | `replace github.com/stohirov/suren/backend/gpu => ../gpu` | `RunGPU` needs the GPU renderer; same reason. |

They exist because the modules are developed together in one repo. The versions
next to them (`v0.0.0`, `v0.0.0-00010101000000-000000000000`) are placeholders the
replace makes irrelevant.

**What this buys, concretely:** `go get github.com/stohirov/suren` and render to
PNG, and your `go.sum` gains nothing. No cgo, no C toolchain, cross-compiles
anywhere Go does. The cost is paid only by `import ".../backend/gpu"`, which is a
separate `go get` and an explicit choice. Everything in [NOTICE](../NOTICE) is
reachable only through modules 2 and 3.

## The frame pipeline

What `gpu.Renderer.Render(scene)` actually does (`backend/gpu/renderer.go:83`):

```
scene.Scene
    │
    │  EncodeInto(r.enc, s, w, h)          encode.go — pure Go, 0 allocs/op
    │    ├─ flatten paths to line segments (FlattenInto, reused scratch)
    │    ├─ expand strokes to fill outlines (path.Stroker)
    │    ├─ emit Segments / Nodes / Stops / Clips
    │    ├─ buildTileBins        counting sort → TileOffsets / TileNodes
    │    ├─ segment scatter      → TileSegOff / TileSegIdx
    │    ├─ markFallbackTiles    → FallbackTiles
    │    └─ fingerprint()        FNV-1a over dims + segs + nodes + stops + mask
    ▼
 Fingerprint == lastFP && haveFrame ?
    │
    ├── YES ──► return. No upload, no dispatch. The retained target texture
    │           still holds the last frame, patch included — which is only
    │           sound because the fingerprint covers FallbackTiles.
    │           Steady state: 0.49 ms, 0 allocs/op.
    │
    └── NO
         │
         │  upload(e)                       renderer.go — queue.WriteBuffer
         │    reuse the buffer if the new length fits; grow ×1.5 if not.
         │    Slack past the written data is never read: the shader indexes
         │    by node/tile records and never calls arrayLength.
         ▼
         │  ras.run(...)                    raster.go — 2D dispatch
         │    one invocation per (tile-column, scanline); private cover/area/fb
         ▼
         │  fallback(s)                     fallback.go
         │    for tiles flagged by a Node.Fallback: render the WHOLE SCENE
         │    restricted to that tile on the CPU reference, then
         │    queue.WriteTexture over the compute output. Runs of flagged
         │    tiles upload as one region per tile row.
         │    Before any readback or present, so offscreen and windowed
         │    paths both see patched pixels. ~6µs/tile — see correctness.md.
         ▼
    target texture (rgba8unorm storage + TextureBinding)
         │
         ├─ ReadRGBA()   → align256 → *image.RGBA     (offscreen, tests, PNG)
         ├─ Phase 6a bridge: readback → screen.WritePixels    (window.RunGPU)
         └─ Phase 6b present: blit render pass → swapchain image  (gpu.RunPresent)
              blit.go + blit.wgsl — fullscreen triangle, textureLoad 1:1.
              Swapchain images are RenderAttachment, never storage-writable,
              so the frame cannot be computed into the surface directly.
```

Two notes on the shape:

- **The fallback replaces a tile, it does not blend into one.** Compositing a CPU
  node over the GPU's tile would put it on a backdrop the two backends had already
  quantized differently. A tile is a complete composite of the scene restricted to
  its area, so wholesale replacement cannot leave a seam. Why it is a tile and not
  a node.
- **The readback is Ebiten's price, and only `window.RunGPU` still pays it.** That path
  hands pixels to a loop that wants CPU memory, so the frame makes a GPU → CPU → GPU
  round trip. Phase 6b's `gpu.RunPresent` keeps the frame on the GPU and blits it to a
  surface: **~0.4 ms and 3.69 MB per frame** cheaper (1280×720, `BenchmarkPresentVia*`).
  The allocation is the headline, not the time — on Apple silicon the readback crosses no
  bus, only unified memory, so "GPU → CPU → GPU" costs far less than it sounds. 6a is kept
  because it is the CPU-vs-GPU comparison harness, not because it is how to put pixels on
  screen.

## The tile model

The canvas is cut into **16×16 pixel tiles** (`encode.go:74`, `tileSize = 16`).
One compute invocation owns a 16-wide span of one scanline within one tile, with
**private** `cover`/`area`/`fb` arrays — no global scratch, no float atomics, no
inter-thread coordination. At 1280×720 that is ~57,600 threads against the
row-serial design's 720.

Each tile holds, from the encoder:

- **a node list** (`TileOffsets` / `TileNodes`) — which nodes touch this tile, in scene order
- **a per-(tile,node) segment list** (`TileSegOff` / `TileSegIdx`) — which of that node's segments matter here
- **a fallback flag** (`FallbackTiles`)

### Why the backdrop is the load-bearing idea

Exact-coverage rasterization is a running sum across a scanline. Cut the scanline
into tiles and each tile starts mid-sum — it does not know the winding it inherits
from everything to its left. The naive fix is to bin every node whose geometry
lies to the left, which defeats binning.

The **per-tile backdrop** is what makes plain bbox binning sufficient instead. As
each binned node's segments are routed, each one goes to exactly one of three
places by x:

```
   segment is...
   ├── left of the tile   ──► accumulate into a scalar backdrop
   ├── inside the tile    ──► the private cover/area arrays
   └── right of the tile  ──► ignored

   then: seed the sweep's accumulator with the backdrop and sweep the 16 columns.
```

The backdrop is the winding the tile inherits, collapsed to one number. And
because **closed paths wind to zero**, a node whose bbox misses this tile
contributes exactly zero to that number — so it can be skipped entirely. That is
the whole argument for why 2D bbox binning is correct here rather than merely
fast. `routeSeg`/`routeCol` in `raster.wgsl` are where it lives.

The per-tile segment lists (Phase 8) then cut the *scanning*: a tile iterates
`tileSegOff[k]..tileSegOff[k+1]` for node-entry `k` instead of the node's full
segment list. Rule: a segment is listed in a tile iff its bbox y-band overlaps the
tile **and** `minx < tile.right` — left-of-tile segments stay listed, because the
backdrop still needs them. A cap on that list would corrupt the backdrop, so the
memory is bounded but not capped.

`routeCol`'s summation order is a **known, deliberate divergence** from the CPU's
single left-to-right running total. They are equal in exact arithmetic and not in
float. Fixing it means sweeping whole rows — undoing the parallelism above — so it
is recorded as an accepted contributor to the Δ≤1 floor, and named as a first
suspect if a non-Metal backend ever diverges.

### Binding budget

`raster.wgsl` uses 10 bindings, of which **8 are `var<storage>`** — exactly
WebGPU's default `maxStorageBuffersPerShaderStage`, and `device.go` requests no
raised limits.

```
0  out_tex      texture_storage_2d<rgba8unorm, write>
1  segs         storage    5  tileNodes    storage    8  tileSegIdx   storage
2  nodes        storage    6  stops        storage    9  clips        storage
3  dims         uniform    7  tileSegOff   storage
4  tileOffsets  storage
```

This is a real constraint, not a stylistic one: it is why the mesh gradient reuses
the `Stop` record (a colour at a *sample* rather than at a parameter) instead of
adding a triangle buffer. A ninth storage buffer would be the first time this
renderer demanded more than the portable default — and a new way for a backend
that has never been run on to fail.

## Backends

| Backend | Entry point | Notes |
|---|---|---|
| `backend/cpu` | `cpu.Render(s, w, h) *image.RGBA` | The reference. f64 throughout. |
| `backend/png` | `png.Encode(w, s, pxW, pxH) error` | `cpu.Render` + stdlib PNG. |
| `backend/svg` | `svg.Encode(w, s, pxW, pxH) error` | Vector output. **Silently drops several features** — see below. |
| `backend/gpu` | `gpu.NewRenderer(w, h)`, `gpu.NewRendererOn(backend, w, h)` | WebGPU compute. `Render` / `Sync` / `ReadRGBA` / `Resize` / `Release`. |
| `backend/gpu` (present) | `gpu.RunPresent(title, w, h, frame)`, `gpu.NewPresenterWith(...)` | Native surface, no readback. `-tags gpupresent` only. Read `Format` / `PresentMode` rather than assuming either. |
| `backend/window` | `window.Run(title, w, h, frame)`, `window.RunGPU(...)` | Ebiten loop. `RunGPU` goes through the readback bridge. |

**The SVG backend's gaps are silent, and that is a known wart.** Read from
`svg.go` rather than from the roadmap, the full list of what a scene loses on the
way to SVG is:

| Feature | SVG output | Where |
|---|---|---|
| Conic paint | node **dropped entirely** | `paintRef` default case |
| Mesh paint | node **dropped entirely** | `paintRef` default case |
| Path clips (`Node.Clips`) | **ignored**; node still drawn, unclipped | `clipRef` reads only `n.Clip` |
| Blend mode (`Node.Op`) | **ignored**; composites as Normal | never emitted |
| Composite op (`Node.Composite`) | **ignored**; composites as SrcOver | never emitted |
| Rect clip (`Node.Clip`) | honoured, as a `<clipPath><rect>` | `clipRef` |
| Solid / linear / radial paint | honoured | `paintRef` |
| Path, transform, stroke, fill rule | honoured | `writeNode` |

None of these error. `paintRef` returns `(fillRef{}, false)` for a paint it cannot
express and the caller skips the node; the clip and blend fields are simply never
read.

**The rows are not the same kind of failure**, which is the part worth internalizing:

- The **conic and mesh** drops are *missing output* — SVG 1.1/2 genuinely have no
  conic-gradient element (CSS does) or mesh primitive, so the format is the limit and
  the node is visibly absent.
- The **blend, composite and path-clip** rows are *wrong output* — a Multiply node
  exports as Normal, a clipped node exports unclipped. The node is present, plausible
  and incorrect. All three are expressible in SVG (`mix-blend-mode`, `<clipPath>` with
  path data, `<feComposite operator=…>`) and are simply not emitted.

The SVG backend is **not part of the parity contract**: it emits vectors, not
pixels, so there is nothing to diff at the channel level. It is gated by its own
goldens (`backend/svg/testdata/golden` — two of them, neither exercising a clip, a
blend mode or a conic). That is precisely why it drifted: it is the one output path
the parity machine does not watch, and it accumulated five silent gaps while the two
rasterizers were held to Δ≤1 over millions of executions. **The apparatus only
protects what it is pointed at.** Tracked as [Phase 24](roadmap.md). Treat it as a
convenience exporter for the honoured subset, not as a third renderer.

## Critical files

The roadmap's own list, which is the fastest orientation for a change:

| File | Why it matters |
|---|---|
| `render/render.go` | The `Renderer` interface — the CPU/GPU seam. |
| `internal/parity/contract.go` | The tolerance contract every gate in the tree cites. |
| `internal/corpus/corpus.go` | The named scene corpus (+ `regress/` for fuzz finds). |
| `internal/parity/fuzz/` | Scene generator, shrinker, bisect. |
| `backend/cpu/cpu.go` + `raster/fill.go` | The CPU reference the GPU must match. |
| `raster/tile.go` | `TileMask` — gates a fill's writes without touching its coverage sweep. |
| `paint/paint.go` | Paint types + `MeshAt`/`MeshEps`: the canonical mesh evaluator `raster.wgsl` ports. |
| `backend/gpu/fallback.go` | Per-tile CPU fallback + `Stats`. |
| `backend/gpu/encode.go` | Scene → GPU buffers + tile bins (`EncodeInto`, coarse lists). |
| `backend/gpu/raster.wgsl` | The fine rasterizer (per-tile lists, blend/clip/paint). |
| `backend/gpu/renderer.go` | Encode → upload → dispatch (`Resize`, buffer reuse). |
| `backend/gpu/target.go` | Offscreen texture + readback (`resize`). |
| `backend/gpu/blit.go` + `blit.wgsl` | Target → surface blit; format and present-mode choice. Untagged, so CI gates it. |
| `backend/gpu/present.go` | Phase 6b — glfw window + surface + present loop, `gpupresent` build tag. |
| `backend/window/window.go` | Window loop; `Run` (CPU) and `RunGPU` (readback bridge). |
