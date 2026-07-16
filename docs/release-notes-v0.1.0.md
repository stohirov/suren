# DRAFT — v0.1.0 release notes. Not tagged. See "Before tagging" at the bottom.

# suren v0.1.0

A 2D vector graphics library in Go with two independent renderers — a pure-Go CPU
rasterizer and a WebGPU compute backend — held to a measured, budgeted tolerance
contract.

**This is v0.1.0, not v1.0, and the version number is the honest part.** Phase 6b —
presenting from a native wgpu surface — landed days before this tag, and the API has
never been driven against a real swapchain by anyone but its author. `Resize` under
surface reconfigure may well be the wrong shape. v0.x is installable, citable and
linkable without an API promise this project cannot yet make.

## What this is

Not that it renders. That two independently written implementations — an f64 CPU
rasterizer and an f32 WGSL compute shader — are held to each other as the **primary
correctness gate**, and that the tolerance between them is a measured quantity with a
stated owner rather than a knob turned until the tests went green.

- **Δ=0** where both backends run the same analytic path.
- **Δ≤1** as the 8-bit quantization floor between an f64 and an f32 pipeline.
- Anything above that is a **named budget** whose `Why` must identify the operation —
  `parity.Config.Validate` rejects an empty one.
- **The corpus carries 43 entries and zero budgets.** The two that once existed
  (ColorDodge Δ≤2, ColorBurn Δ≤3) were **retired by a fix, not widened** — and their
  stated reason had been wrong as well.

## Verified

- **Metal, darwin/arm64 (Apple M4).** Offscreen and windowed.
- **43 corpus scenes** at the tolerance each earned by measurement.
- **The differential fuzzer**, with shrinking, first-diverging-node bisect and
  JSON-spec replay. The roadmap records ~1M executions in Phase 12c, 531,622 against
  Phase 15's corrected tolerance rule, and 2.3M over Phase 16's widened space, with no
  unexplained divergence.
- **Four property laws**, per-backend (52 CPU subtests) and cross-backend (104 GPU
  subtests) — because a law holding on each backend independently does not imply the
  two agree.
- **The blit gated headlessly at Δ=0** against a texture of the surface's own format,
  plus an sRGB path gated at the floor and a guard
  (`TestSRGBSurfaceWouldWashOutWithoutLinearize`) that asserts the un-linearized
  version **fails** by 73/255 over 30,830 channels. A passing test proves nothing if
  the bug it guards is invisible.

**Performance** (Apple M4, Metal, `many-nodes`: 961 nodes / 16,324 segments, 1280×720):
GPU dispatch **2.4 ms** vs CPU full frame **12.4 ms** — **5.2×**, at the **Δ≤1** gate
verified at that resolution. An unchanged scene costs **0.76 ms and 0 allocations**.
The windowed blit drops **~0.4 ms and 3.69 MB/frame** versus the readback bridge; the
allocation is the real prize, and the time is modest only because Apple silicon has no
bus to cross.

## Not verified — read this before depending on it

- **Vulkan and DX12 have never run.** The reconciliation harness exists and enumerates
  zero adapters on this host. The portability claim covers **Metal only**, and a green
  test run prints exactly that rather than passing quietly. If a non-Metal backend
  diverges, the first suspects are named: FMA contraction (WGSL cannot forbid it) and
  the `routeCol` tile-backdrop summation order.
- **No discrete GPU has run any of this.** The present-path benchmark is a
  unified-memory answer and is scoped no further.
- **Text is out of scope by decision**, not by omission. After flattening a glyph is
  just filled paths, which the parity machine already covers; what text adds is font
  loading, hinting and subpixel positioning, none of which is a renderer concern.
- **Live resize on macOS draws nothing.** The drag runs a nested Cocoa run loop, so
  `PollEvents` does not return until it ends. The window snaps to the new size on
  release. The documented answer is a window-refresh callback, which this does not
  register.
- **A resized window does not rescale the scene** under `RunPresent` — the callback is
  handed a canvas and never learns the new size. Drive a `Presenter` directly and read
  `Size()` to follow the window.
- **A failed surface acquire cannot be detected** — upstream bug, see below.
- **The blit rebuilds its bind group every frame** (~57 allocs/op). Not on the measured
  critical path; Phase 7's argument for caching applies whenever it is worth making.
- **CI runs the pure-Go core only.** The GPU suite has no runner and is not faked.

## The upstream bug you should know about

`cogentcore/webgpu` v0.23.0's `Surface.GetCurrentTexture` **discards
`WGPUSurfaceTexture.status`** and returns a NULL texture with a **nil error** on a
failed acquire (`Timeout`/`Outdated`/`Lost`). `Texture.ref` is unexported, so the NULL
is unreachable from Go, and the documented next call — `CreateView` — aborts the
process. The error scope the shim does install catches `Validation` errors, which an
outdated swapchain is not.

`Presenter.Frame` refuses to acquire from a minimised window, which removes the only
trigger this project can reach. A slow acquire under heavy load remains exposed, and
`Outdated` cannot be handled at all. The fix is upstream; the analysis and a suggested
patch are in `docs/upstream-issue-draft.md`.

## API changes in this release

- **`svg.Encode` now returns `(Report, error)`** instead of `error` — a breaking
  change, taken deliberately before the tag rather than after. The SVG backend had
  **five silent gaps**, three of which were *wrong output* rather than missing output
  (a Multiply node exported as Normal; a clipped node exported unclipped). It now
  emits `mix-blend-mode` and `<clipPath>` with path data, and **reports** what SVG
  genuinely cannot express: conic paints, mesh paints, and non-`SrcOver` composites.
  Measured: **SVG cannot fully express 18 of the 43 corpus scenes; 25 encode
  losslessly.** See Phase 24.

## Known-deferred, tracked and not started

Phases 17–22: pattern fills / image sampling, group opacity and masks, an
sRGB-vs-linear toggle, a stroking parity audit, a unified encoding format, and
instrumentation. Each is planned in `docs/roadmap.md` with its correctness crux
stated. **⏳ in that document means not done**, and it is used honestly.

## Install

```sh
go get github.com/stohirov/suren           # core: zero deps, empty go.sum
go get github.com/stohirov/suren/backend/gpu   # WebGPU: own module, cgo
```

The core — `geom`, `path`, `paint`, `raster`, `scene`, `render`, `backend/cpu`,
`backend/png`, `backend/svg` — pulls **zero third-party code** and cross-compiles
anywhere Go does. That is not an aesthetic preference; it is why the module boundary
exists, and as of this release CI gates it rather than trusting it.

---

## Before tagging — open items

1. **CI has never run.** Every gate in `.github/workflows/ci.yml` passes on
   darwin/arm64, but the workflow has not executed on GitHub. The amd64 golden run is
   the entire point of it and its result is **unknown**. If a golden diverges on amd64,
   Phase 13's FMA claim is wrong and that is a finding to record, not a flake to
   retry. **Tag only after a green run.**
2. **Decide whether the upstream issue is filed first**, so the notes can link it.
3. The "43 entries / zero budgets" and performance figures above are read from the
   current tree and README; re-confirm after any further change.
