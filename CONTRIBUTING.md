# Contributing

The rules below are not style preferences. They are the rules that actually
govern this codebase, and most of them exist because a measurement forced them.
Where a rule has a story, [docs/correctness.md](docs/correctness.md) tells it and
[docs/roadmap.md](docs/roadmap.md) has the numbers.

Read [docs/architecture.md](docs/architecture.md) first if you are looking for
where something lives.

---

## The rule

**Every feature lands on BOTH backends with a parity test, in the same commit.**

GPU-only is not "done". CPU-only is not "done". A feature that renders on one
backend is a feature whose correctness nothing has checked, because the only
primary gate this project has is the two backends disagreeing.

This is not negotiable and it is not a matter of sequencing — "GPU next week" means
the CPU half lands with no gate on it and the parity test that would have caught
the divergence does not exist yet.

---

## Tolerance is not a knob

Use the constructors in `internal/parity`. Never write a tolerance as a bare
literal — no gate in this tree does, and that is enforced by review.

```go
parity.Identical()          // Δ=0   — both backends run the same integer/analytic path
parity.Quantized()          // Δ≤1   — the 8-bit quantization floor between f64 and f32
parity.Budget(tol, why)     // Δ>1   — a bug budget with an owner. `why` MUST name the operation.
parity.Perceptual(dE, ssim, why)
```

**A budget above the quantization floor requires a stated reason naming the
operation.** This is enforced by `Config.Validate`, which rejects an empty `Why`:

```go
if c.Tol > QuantizationFloor && c.Why == "" {
	return fmt.Errorf("tolerance %d exceeds the quantization floor (%d) without "+
		"naming the operation responsible; use Budget(tol, why)", c.Tol, QuantizationFloor)
}
```

### Never widen a gate to make a test pass

If a test fails at `Quantized()` and passes at `Budget(2, ...)`, you have not
fixed anything — you have recorded that you stopped looking. **Measure first, then
record the measurement and its mechanism.**

The evidence that this is the right instinct is in the history: Phase 13 **retired**
the only two corpus budgets that existed (ColorDodge Δ≤2, ColorBurn Δ≤3) by finding
and fixing the divergence they were absorbing. They were the first tolerances in
this tree retired by a fix rather than widened. Their original stated reason was
also **wrong** — the division was never the culprit, it only amplified a backdrop the
two backends had quantized differently.

A budget whose `Why` names a correlate instead of a cause is a budget with a hole in
it. This has happened twice; see [the tolerance rule that took three
tries](docs/correctness.md#the-tolerance-rule-that-took-three-tries).

**The corpus currently carries zero budgets.** All 43 entries are `Identical()` or
`Quantized()`. Keep it that way if you can.

---

## New features add corpus entries

Add them to `internal/corpus`, **at the tolerance they earn by measurement, not the
one that is convenient.**

Do not reach for `Quantized()` because it is the common case. Measure. If both
backends run the same analytic path, the entry earns `Identical()` and gating it at
`Quantized()` would let a real regression through undetected — that is exactly what
Phase 11 found when it replaced a blanket `tol=2` with per-scene measured floors.

Equally: do not gate at `Identical()` because it looks rigorous. The 12
`composite-*` entries are gated `Quantized()` **on purpose** even though all 12
measure Δ=0 on Metal, because `porterDuff` unpremultiplies through a division and
lerps by coverage in f32 against the reference's f64. Δ=0 there is a fact about
those colours on that driver, not a property. **Gating on it would read luck as a
guarantee.**

Every corpus entry automatically gets a cross-backend gate at its `Tol` **and** a
CPU golden at `Identical()`. Those answer different questions — see
[correctness.md](docs/correctness.md#1-the-corpus).

---

## Independent oracles

**Parity compares the backends to each other. Goldens compare the CPU to itself.
Both are blind to a semantic error the two renderers share, and to a bug in the
reference.**

That second blind spot is not theoretical — it happened. The mesh crack: the f64 CPU
reference was **wrong**, the f32 GPU was **right**, and a golden would have recorded
the crack as expected output forever. Only the differential could see it, and only
because a second implementation disagreed. ([The full
story](docs/correctness.md#1-the-mesh-crack--the-reference-was-the-buggy-one).)

**So a new feature needs a test that hand-computes what it MEANS**, asking neither
renderer. A conic sweeping the wrong way, or a barycentric solved for the wrong
corner, renders identically on both backends, matches its golden byte-for-byte, and
passes every corpus entry.

Follow the pattern:

| Oracle | What it does |
|---|---|
| `raster.alphaOracle` (`raster/composite_test.go`) | Derives each operator's output alpha from Porter-Duff's **coverage geometry** — αs and αb as areas of two independent subsets — rather than from the coefficient table, which would merely restate it. |
| `TestConicParameterMapping` (`backend/cpu/conic_test.go`) | Hand-computed angles: right=0, down=0.25, left=0.5, up=0.75. |
| `TestMeshAtInterpolatesBarycentrically` (`paint/mesh_test.go`) | Each corner, the centroid, all three edge midpoints. |

The test that restates the implementation in a second syntax is not an oracle.

---

## Anti-vacuity

**A test that cannot fail is worse than no test**, because it reports coverage it
does not have. This project has nearly shipped several.

- **Verify guards by injection.** If you add a guard, break the thing it guards and
  confirm the guard goes red. `TestBlendEntriesBuildDistinctScenes` was verified this
  way; so was the clip-idempotence scoping; so was `MeshEps`. If you cannot make your
  new test fail on purpose, you have not tested anything.
- **Assert the difference, not the result, when the mechanism is the point.**
  Phase 14's fallback scene nearly shipped as a gate that measured nothing: the first
  draft rendered at Δ=0 with the fallback *off*, so the Δ=0 "with fallback" assertion
  was decorative. `TestFallbackBuysExactness` now asserts the off/on difference.
- **Generated scenes go through `parity.NonTrivial`.** Any harness rendering a scene
  it did not hand-write needs it. The trivial-scene skip rate is itself gated at 3%,
  so vacuity cannot quietly eat the suite. **Do not raise that gate to accommodate a
  change** — Phase 15 found that uniform composite sampling pushed the rate to 4.0%
  and the correct fix was biasing the generator, not moving the gate. Raising it
  inverts its purpose. Current headroom: 2.3% against 3%.

---

## Generator changes re-roll every seed

**The seeds are not a fixed test set.**

Adding one draw to `randPaint`, or excluding one blend mode, reshuffles every seed's
scene. This is not a hypothetical: excluding two blend modes shifted every seed
*within the phase that introduced the exclusion*, and Phase 15's SoftLight find
surfaced a **pre-existing** bug only because adding a draw reshuffled the space.

Consequences you must respect:

- **A fuzz find is stored as `Spec` JSON in `internal/corpus/regress/`, never as a
  seed.** A seed only reproduces while the generator is untouched, so storing seeds
  would silently retire every past find on the first edit to `gen.go`. The `Spec` is
  explicit data and round-trips bit-exactly.
- **Never read a green fuzz run as coverage of a fixed space.** It is coverage of the
  space *your current generator produces*.
- If you change the generator, re-measure the trivial-scene rate. Do not assume the
  old number holds.

---

## Running things

Pulled from the test files. These are the real flags.

### Unit tests and the standing gates

```sh
go test ./...
```

The root module: unit tests, 43 CPU goldens at Δ=0, property laws on generated
scenes (52 CPU subtests). No GPU needed — this is the gate that runs anywhere.

```sh
cd backend/gpu && go test ./...
```

The quarantined GPU module: cross-backend parity over the corpus at each entry's
earned tolerance, property agreement (104 subtests), and the 64-seed differential
sweep. **Needs a GPU adapter.**

### The corpus

```sh
cd backend/gpu && go test -run TestParityCorpus -v ./...     # 43 entries, cross-backend
go test -run TestGoldenCorpus -v ./backend/cpu               # CPU goldens, no GPU
go test ./backend/cpu -update                                # regenerate goldens
```

`-update` regenerates golden files. Look at the diff before committing it — that flag
exists to record an intended change, not to make a failure go away.

### The fuzzer

```sh
cd backend/gpu && go test -run TestFuzzSweep ./...           # 64 fixed seeds; runs on every `go test`
cd backend/gpu && go test -fuzz FuzzDifferential ./...       # the unbounded search
```

`TestFuzzSweep` is a **standing gate** — the differential runs on every `go test`,
not only when someone remembers `-fuzz`.

### Seed replay and emitting a find

```sh
cd backend/gpu && go test -run TestFuzzReplay -seed=0x2a ./...
cd backend/gpu && go test -run TestFuzzReplay -seed=0x2a -emit ./...
```

`-seed` accepts hex or decimal, and re-materializes one seed headlessly. Without it
the test skips (`no -seed given; pass -seed=0x... to replay one differential seed`).

`-emit` writes the **minimized** scene to `internal/corpus/regress/*.json`, which
`corpus.All()` then loads as an ordinary entry — cross-backend gate plus CPU golden.
That is what turns a fuzz find into a regression outliving the generator that
produced it.

### Reconciliation

```sh
cd backend/gpu && go test -run TestReconcileBackends -v ./...
```

Runs the whole corpus on every backend the host exposes. An absent backend skips,
never fails. Read the log — it states its own scope:

```
reconciled 1 backend(s): [metal]
NOT reconciled here (host does not expose them): [vulkan dx12] — the portability claim covers [metal] only
```

### Benchmarks

```sh
cd backend/gpu && go test -bench . -benchmem ./...
go test -bench . -benchmem ./backend/cpu
```

**`-benchmem` is not optional.** Phase 7's whole result is allocs/op → 0, and a
regression there is invisible without it. `BenchmarkFallback` additionally reports
`cpu-tiles` and `%frame` so a feature cannot quietly fall back on everything.

Never report a speedup without the correctness gate it passed at.

---

## The cgo quarantine

Three modules. The split is load-bearing. See [the module
graph](docs/architecture.md#module-graph).

**Do not add a dependency to the zero-dep modules.** `geom`, `path`, `paint`,
`raster`, `scene`, `render`, `backend/cpu`, `backend/png` and `backend/svg` import
nothing outside the standard library. A user who wants CPU rendering gets an empty
`go.sum`, and that is a real property people choose this library for — not an
aesthetic.

If you find yourself wanting a dependency in the core, the answer is almost always
that the code belongs in `backend/gpu` instead.

**Do not link a display library outside the `gpupresent` build tag.** Phase 6b's
native surface present will link glfw; it must stay behind `//go:build gpupresent`
so the default build, the headless test binary, and any CI never link a display
library. The offscreen parity tests are the gate; windowed present is validated
on-device by hand.

Note that `internal/parity` and `internal/sample` are stdlib-only on purpose, so the
quarantined modules can import them without dragging anything back the other way.
`internal/parity/props` takes a `RenderFunc` parameter rather than importing
`backend/gpu`, for the same reason. Keep both properties.

---

## Before you open a PR

- [ ] The feature renders on **both** backends.
- [ ] There is a parity test, in **this** commit.
- [ ] There is a corpus entry, at a tolerance you **measured**.
- [ ] There is an independent oracle for the feature's **semantics**.
- [ ] You broke your new test on purpose and watched it fail.
- [ ] No gate was widened. If one was, its `Why` names the **operation**, not a correlate.
- [ ] `go test ./...` and `cd backend/gpu && go test ./...` are green.
- [ ] If you touched the generator: trivial-scene rate re-measured, finds still stored as specs.
- [ ] If you learned something the measurement contradicted, it went in
      [docs/roadmap.md](docs/roadmap.md). **The reverted work and the wrong predictions are
      the most valuable entries in that file.**
