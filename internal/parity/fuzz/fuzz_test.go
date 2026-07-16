package fuzz

import (
	"image"
	"reflect"
	"testing"

	"github.com/stohirov/sukho/backend/cpu"
	"github.com/stohirov/sukho/geom"
	"github.com/stohirov/sukho/internal/parity"
	"github.com/stohirov/sukho/paint"
	"github.com/stohirov/sukho/scene"
)

const cases = 32

func TestGeneratedScenesAreValid(t *testing.T) {
	for i := range cases {
		seed := uint64(i) + 1
		spec := Generate(seed)
		if err := spec.Validate(); err != nil {
			t.Fatalf("seed=0x%x: %v", seed, err)
		}
		sc := spec.Scene()
		if len(sc.Nodes) != len(spec.Nodes) {
			t.Fatalf("seed=0x%x: spec has %d nodes, scene has %d", seed, len(spec.Nodes), len(sc.Nodes))
		}
	}
}

// A uniformly-rendered scene is skipped by Check, which makes the skip rate the
// thing that decides whether the fuzzer is testing anything at all. Measured at
// 0.8% (17/2000 seeds); the gate is 3%, loose enough to survive seed variation
// and tight enough that a generator change which starts emitting blank scenes
// fails here instead of turning the suite green by drawing nothing.
//
// The cause is not a bug: an invisible node is usually correct blending. Darken
// with a source lighter than an opaque backdrop returns the backdrop exactly for
// any alpha (B(cb,cs)=min(cb,cs)=cb, so Co=αs·cb+(1-αs)·cb=cb), and Lighten is
// the mirror image. Every trivial seed measured was exactly this: a scene whose
// render is byte-identical to its background alone.
func TestGeneratedScenesAreRarelyTrivial(t *testing.T) {
	const n = 1000
	const maxRate = 0.03

	trivial := 0
	for i := range n {
		spec := Generate(uint64(i) + 1)
		if !parity.NonTrivial(cpu.Render(spec.Scene(), spec.W, spec.H)) {
			trivial++
		}
	}
	rate := float64(trivial) / n
	t.Logf("trivial scenes: %d/%d (%.1f%%)", trivial, n, 100*rate)
	if rate > maxRate {
		t.Fatalf("%.1f%% of seeds render uniformly (max %.1f%%); the generator is drawing nothing and the differential would prove nothing",
			100*rate, 100*maxRate)
	}
}

// The seed must be the whole repro: same seed, same scene, forever.
func TestGenerateIsDeterministic(t *testing.T) {
	for i := range cases {
		seed := uint64(i) + 1
		a, b := Generate(seed), Generate(seed)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("seed=0x%x: Generate is not deterministic", seed)
		}
	}
	if reflect.DeepEqual(Generate(1), Generate(2)) {
		t.Fatal("different seeds produced the same spec")
	}
}

// A stored regression is gated at Δ=0 against its golden, so an inexact
// round-trip would not merely lose fidelity — it would make the recorded scene a
// different scene, and the golden would encode the wrong bug. Asserting on the
// RENDER as well as the struct is the point: struct equality could hold while a
// float that only round-trips to 6 digits moves an edge by a hair.
func TestSpecJSONRoundTripIsExact(t *testing.T) {
	for i := range cases {
		spec := Generate(uint64(i) + 1)
		data, err := spec.Encode()
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		back, err := Load(data)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if !reflect.DeepEqual(spec, back) {
			t.Fatalf("seed=0x%x: spec changed across a JSON round-trip", spec.Seed)
		}
		parity.Assert(t, cpu.Render(back.Scene(), back.W, back.H), cpu.Render(spec.Scene(), spec.W, spec.H), parity.Identical())
	}
}

// The gate a scene earns must follow from what is in it, and every gate the
// generator can produce must satisfy the contract.
func TestTolIsEarnedFromSceneContent(t *testing.T) {
	rect := ShapeSpec{Kind: ShapeRect, Rect: geom.RectXYWH(0, 0, W, H)}
	node := func(op paint.BlendMode) NodeSpec {
		return NodeSpec{Shape: rect, Transform: geom.Identity(), Op: op}
	}
	spec := func(ops ...paint.BlendMode) Spec {
		s := Spec{W: W, H: H}
		for _, op := range ops {
			s.Nodes = append(s.Nodes, node(op))
		}
		return s
	}
	// A solid background under one gradient-painted node carrying op.
	gradSpec := func(op paint.BlendMode) Spec {
		grad := node(op)
		grad.Paint = PaintSpec{Kind: PaintRadial, Radius: 20, Stops: []paint.Stop{
			{Offset: 0, Color: paint.RGB(1, 0, 0)},
			{Offset: 1, Color: paint.RGB(0, 0, 1)},
		}}
		return Spec{W: W, H: H, Nodes: []NodeSpec{node(paint.Normal), grad}}
	}
	// Two solid nodes, no blending, the second carrying a composite op.
	compSpec := func(op paint.CompositeOp) Spec {
		n := node(paint.Normal)
		n.Composite = op
		return Spec{W: W, H: H, Nodes: []NodeSpec{node(paint.Normal), n}}
	}

	for _, tc := range []struct {
		name string
		spec Spec
		want parity.Config
	}{
		{"no blending is exact", spec(paint.Normal, paint.Normal), parity.Quantized()},
		{"a gradient with no blending stays at the floor", gradSpec(paint.Normal), parity.Quantized()},

		// The whole rule: dB/dCb decides, and nothing else does. These four modes
		// are measured at exactly 1.0, so a 1-LSB input difference cannot become a
		// 2-LSB output one no matter how many of them stack or what they read.
		{"one non-amplifying mode is exact", spec(paint.Normal, paint.Multiply), parity.Quantized()},
		{"stacked non-amplifying modes stay exact", spec(paint.Multiply, paint.Screen, paint.Darken), parity.Quantized()},
		{"non-amplifying over a gradient stays exact", gradSpec(paint.Difference), parity.Quantized()},
		// HardLight is Overlay's transpose and the one that looks like it should
		// amplify. Its 2*cs branch only runs when cs<=0.5, so it measures 1.0.
		{"HardLight is not an amplifier", gradSpec(paint.HardLight), parity.Quantized()},

		// ONE amplifying node is enough, with or without a gradient. Measured: 2 of
		// 5955 solid-only scenes breach Δ=1, 42 of 904 with a gradient. The gradient
		// multiplies the risk ~40x; it does not create it. The rule this replaced
		// gated the solid-only case at Quantized and was wrong.
		{"one amplifying mode over solids earns the budget", spec(paint.Normal, paint.Overlay), amplifiedBlend},
		{"one amplifying mode over a gradient earns it too", gradSpec(paint.SoftLight), amplifiedBlend},
		{"an amplifying mode among non-amplifying ones earns it", spec(paint.Multiply, paint.Overlay, paint.Screen), amplifiedBlend},

		// A non-SrcOver operator earns it on its own: porterDuff unpremultiplies,
		// and these operators drive the backdrop alpha it divides by toward zero.
		{"a composite op earns the budget with no blending at all", compSpec(paint.Xor), amplifiedBlend},
		{"SrcOver composite earns nothing by itself", compSpec(paint.SrcOver), parity.Quantized()},

		// Dodge and burn are unbounded, so no budget fits. They are excluded from
		// the generator rather than priced; a stored spec holding one keeps the
		// floor, which is sound only because the corpus feeds them exact inputs.
		{"one ill-conditioned mode is still only the floor", spec(paint.Normal, paint.ColorDodge), parity.Quantized()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.spec.Tol()
			if got != tc.want {
				t.Fatalf("Tol() = %v, want %v", got, tc.want)
			}
			if err := got.Validate(); err != nil {
				t.Fatalf("earned gate violates the contract: %v", err)
			}
		})
	}
}

// Every gate the generator can produce must satisfy the contract's rule that a
// tolerance past the floor names the operation responsible. A budget with an
// empty Why is exactly what Validate exists to reject.
func TestEarnedGatesNameTheirReason(t *testing.T) {
	for i := range cases {
		cfg := Generate(uint64(i) + 1).Tol()
		if err := cfg.Validate(); err != nil {
			t.Fatalf("seed=0x%x earned an invalid gate: %v", i+1, err)
		}
	}
}

// The generator must not emit the ill-conditioned modes. Their unbounded blend
// derivative amplifies sub-LSB input differences without bound, so a generated
// scene containing one measures the operator's conditioning rather than the
// renderers' agreement: the worst delta over such scenes keeps climbing with
// sample size (Δ=3 at 3k seeds, Δ=5 at 25k) while every other mode stays flat at
// Δ=2. This is a scope decision, and a scope decision that only lives in a
// comment is one edit from being undone silently.
//
// It also pins that illConditioned and amplifying stay disjoint. Both name modes
// with dB/dCb>1; the difference is whether the derivative is bounded, and so
// whether a budget can be fitted at all. A mode in both sets would be claiming
// both answers.
func TestGeneratorOmitsIllConditionedModes(t *testing.T) {
	for op := range illConditioned {
		for _, m := range blendModes {
			if m == op {
				t.Errorf("generator emits %v, whose derivative is unbounded; it cannot be gated by a differential oracle", op)
			}
		}
		if _, both := amplifying[op]; both {
			t.Errorf("%v is in both amplifying and illConditioned; a mode either has a fittable budget or it does not", op)
		}
	}
	for i := range 2000 {
		for _, n := range Generate(uint64(i) + 1).Nodes {
			if _, bad := illConditioned[n.Op]; bad {
				t.Fatalf("seed=0x%x emitted the ill-conditioned mode %v", i+1, n.Op)
			}
		}
	}
}

// Every paint kind must be REACHABLE, for the reason TestGeneratorEmitsEveryCompositeOp
// spells out: a kind that quietly stops being generated takes its whole code path
// out of the differential while the fuzzer keeps reporting clean executions over
// scenes that are still perfectly valid. Nothing else notices — a scene made only
// of solids is a scene.
//
// Mesh matters most here. It is the only paint whose vertex data shares the stops
// buffer with the gradients (encode.go's Stop record), so a scene mixing the two
// is the only thing that would catch StopStart/StopCount being mis-indexed
// between kinds — and no hand-written corpus scene mixes them. That coverage
// exists only as long as the generator emits both.
func TestGeneratorEmitsEveryPaintKind(t *testing.T) {
	const seeds = 2000
	seen := map[PaintKind]int{}
	mixed := 0
	for i := range seeds {
		kinds := map[PaintKind]bool{}
		for _, n := range Generate(uint64(i) + 1).Nodes {
			seen[n.Paint.Kind]++
			kinds[n.Paint.Kind] = true
		}
		if kinds[PaintMesh] && (kinds[PaintLinear] || kinds[PaintRadial] || kinds[PaintConic]) {
			mixed++
		}
	}
	for kind, name := range map[PaintKind]string{
		PaintSolid: "solid", PaintLinear: "linear", PaintRadial: "radial",
		PaintConic: "conic", PaintMesh: "mesh",
	} {
		if seen[kind] == 0 {
			t.Errorf("paint kind %s never generated over %d seeds; its path is outside the differential", name, seeds)
		}
	}
	if mixed == 0 {
		t.Errorf("no seed mixes a mesh with a gradient over %d seeds; nothing exercises two kinds sharing the stops buffer", seeds)
	}
	t.Logf("paint kinds over %d seeds: %v; mesh+gradient scenes: %d", seeds, seen, mixed)
}

// The paint axis's counterpart to the ill-conditioned check above, and it fails
// in the same silent way if left out.
//
// randConic closes every loop it emits, because an open one is discontinuous
// across its seam ray and so amplifies the f32-vs-f64 atan2 difference without
// bound. A generator that started emitting open conics would not crash or look
// wrong — it would produce perfectly valid scenes that pass thousands of seeds
// and then fail one, at a magnitude set by two random stop colours rather than by
// any arithmetic, with no budget that could honestly absorb it. That is a flake
// with no fix, so the restriction is pinned here rather than trusted to gen.go's
// comment.
//
// Both directions are checked: conic must be REACHABLE too, or the exclusion
// would be trivially satisfied by never generating one at all.
func TestGeneratorEmitsOnlyClosedConicGradients(t *testing.T) {
	const seeds = 2000
	conics := 0
	for i := range seeds {
		for j, n := range Generate(uint64(i) + 1).Nodes {
			if n.Paint.Kind != PaintConic {
				continue
			}
			conics++
			if !n.Paint.closed() {
				t.Fatalf("seed=0x%x node %d emitted an open conic gradient (stops %v..%v); its seam is a discontinuity no differential gate can own",
					i+1, j, n.Paint.Stops[0].Color, n.Paint.Stops[len(n.Paint.Stops)-1].Color)
			}
		}
	}
	if conics == 0 {
		t.Fatalf("no conic gradient over %d seeds; randConic is unreachable and the closed-loop rule above is vacuous", seeds)
	}
	t.Logf("conic gradients generated: %d over %d seeds", conics, seeds)
}

// The composite axis's counterpart to the ill-conditioned check above: it holds
// gen.go to what it claims to generate, in both directions.
//
// randComposite keeps SrcOver 8 times in 10, and that bias is the danger. If it
// regressed to always returning SrcOver, every Porter-Duff operator would quietly
// leave the generated space — the fuzzer would still run, still pass, and still
// report hundreds of thousands of clean executions while testing none of Phase
// 15. Nothing else in the tree would notice, because a scene that composites
// SrcOver is a perfectly valid scene.
//
// The exclusions are pinned for the same reason: Clear, Src and Dst are left out
// deliberately (each ignores an operand, and the first two erase the frame past
// the vacuity gate), so a later change that starts emitting them should have to
// say so here rather than silently push the trivial-scene rate up.
func TestGeneratorEmitsEveryCompositeOp(t *testing.T) {
	excluded := map[paint.CompositeOp]string{
		paint.Clear: "Clear", paint.Src: "Src", paint.Dst: "Dst",
	}
	seen := map[paint.CompositeOp]int{}
	const seeds = 2000
	for i := range seeds {
		for _, n := range Generate(uint64(i) + 1).Nodes {
			seen[n.Composite]++
			if name, bad := excluded[n.Composite]; bad {
				t.Fatalf("seed=0x%x emitted %s, which gen.go excludes on purpose", i+1, name)
			}
		}
	}
	for _, op := range compositeOps {
		if seen[op] == 0 {
			t.Errorf("composite op %d never generated over %d seeds; it is in compositeOps but unreachable", op, seeds)
		}
	}
	if n := len(compositeOps); n != 9 {
		t.Errorf("compositeOps has %d entries, want 9 (twelve operators minus Clear, Src and Dst)", n)
	}
	// The bias must leave SrcOver dominant but not total: at neither extreme is
	// the generator doing what randComposite documents.
	total := 0
	for _, c := range seen {
		total += c
	}
	frac := float64(seen[paint.SrcOver]) / float64(total)
	if frac < 0.5 || frac > 0.95 {
		t.Errorf("SrcOver is %.1f%% of generated nodes; randComposite documents a ~80%% bias", frac*100)
	}
}

func TestLoadRejectsMalformedSpecs(t *testing.T) {
	for _, tc := range []struct{ name, data string }{
		{"not json", `{`},
		{"no nodes", `{"seed":1,"w":96,"h":96,"nodes":[]}`},
		{"no size", `{"seed":1,"nodes":[{"shape":{"kind":0,"rect":{"Min":{"X":0,"Y":0},"Max":{"X":9,"Y":9}}},"transform":{"A":1,"B":0,"C":0,"D":1,"E":0,"F":0},"paint":{"kind":0}}]}`},
		{"unknown field", `{"seed":1,"w":96,"h":96,"nodes":[],"colour":"red"}`},
		{"empty rect", `{"seed":1,"w":96,"h":96,"nodes":[{"shape":{"kind":0},"transform":{"A":1,"B":0,"C":0,"D":1,"E":0,"F":0},"paint":{"kind":0}}]}`},
		{"gradient without stops", `{"seed":1,"w":96,"h":96,"nodes":[{"shape":{"kind":0,"rect":{"Min":{"X":0,"Y":0},"Max":{"X":9,"Y":9}}},"transform":{"A":1,"B":0,"C":0,"D":1,"E":0,"F":0},"paint":{"kind":1}}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Load([]byte(tc.data)); err == nil {
				t.Fatal("Load accepted a malformed spec; a rotted regression would decode into the wrong scene")
			}
		})
	}
}

// injected is a fake divergence: the "GPU" is the CPU renderer, except that any
// scene still containing the marked node is declared to diverge. It lets the
// shrinker and the bisect be tested against a KNOWN answer, which a real
// divergence cannot provide — there is none to find today, and a minimizer that
// quietly does nothing still reports "minimized".
func injected(mark func(NodeSpec) bool) Fails {
	return func(s Spec) bool {
		for _, n := range s.Nodes {
			if mark(n) {
				return true
			}
		}
		return false
	}
}

func TestShrinkReducesToTheOffendingNode(t *testing.T) {
	spec := Generate(7)
	if len(spec.Nodes) < 3 {
		t.Fatalf("seed produced %d nodes; need at least 3 to shrink", len(spec.Nodes))
	}
	culprit := spec.Nodes[len(spec.Nodes)-1]
	culprit.Op = paint.Difference
	spec.Nodes[len(spec.Nodes)-1] = culprit

	got := Shrink(spec, injected(func(n NodeSpec) bool { return n.Op == paint.Difference }))

	if len(got.Nodes) != 1 {
		t.Fatalf("shrank to %d nodes, want 1", len(got.Nodes))
	}
	if got.Nodes[0].Op != paint.Difference {
		t.Fatalf("shrank to the wrong node: op=%v", got.Nodes[0].Op)
	}
}

// Only the marked FEATURE is load-bearing, so every other feature of the
// surviving node must be stripped. This is what separates a shrinker from a
// node filter.
func TestShrinkStripsFeaturesThatDoNotMatter(t *testing.T) {
	spec := Spec{Seed: 1, W: W, H: H}
	spec.Nodes = append(spec.Nodes, background(newRNG(1)))
	n := randNode(newRNG(3))
	n.Op = paint.Difference
	n.Stroke = randStroke(newRNG(4))
	n.Clips = []ClipSpec{randClip(newRNG(5)), randClip(newRNG(6))}
	n.Paint = randPaint(newRNG(11))
	n.Paint.Kind = PaintLinear
	n.Paint.Stops = randStops(newRNG(12))
	n.Rule = paint.EvenOdd
	spec.Nodes = append(spec.Nodes, n)

	got := Shrink(spec, injected(func(n NodeSpec) bool { return n.Op == paint.Difference }))

	if len(got.Nodes) != 1 {
		t.Fatalf("shrank to %d nodes, want 1", len(got.Nodes))
	}
	g := got.Nodes[0]
	if g.Clips != nil {
		t.Errorf("clips survived: %d", len(g.Clips))
	}
	if g.Stroke != nil {
		t.Error("stroke survived")
	}
	if g.Paint.Kind != PaintSolid {
		t.Errorf("gradient survived: %v", g.Paint.Kind)
	}
	if g.Shape.Kind != ShapeRect {
		t.Errorf("shape did not reduce to its bbox rect: %v", g.Shape.Kind)
	}
	if g.Rule != paint.NonZero {
		t.Error("even-odd rule survived")
	}
	if g.Op != paint.Difference {
		t.Errorf("the load-bearing feature was stripped: op=%v", g.Op)
	}
}

// The shrinker must never turn a passing scene into a "failure". If nothing
// diverges, there is nothing to remove.
func TestShrinkKeepsSpecWhenNothingFails(t *testing.T) {
	spec := Generate(9)
	got := Shrink(spec, func(Spec) bool { return false })
	if !reflect.DeepEqual(got, spec) {
		t.Fatal("Shrink modified a spec that never failed")
	}
}

func TestFirstDivergingAttributesToOneNode(t *testing.T) {
	spec := Generate(11)
	if len(spec.Nodes) < 3 {
		t.Fatalf("seed produced %d nodes; need at least 3", len(spec.Nodes))
	}
	want := 1
	marked := spec.Nodes[want]
	marked.Op = paint.Difference
	spec.Nodes[want] = marked

	got := FirstDiverging(spec, injected(func(n NodeSpec) bool { return n.Op == paint.Difference }))
	if got != want {
		t.Fatalf("first diverging node = %d, want %d", got, want)
	}
}

// The scan must find the EARLIEST diverging node even when a later node also
// diverges — this is the case a binary search over a non-monotone predicate gets
// wrong, and the reason FirstDiverging scans.
func TestFirstDivergingFindsTheEarliestOfSeveral(t *testing.T) {
	spec := Generate(11)
	if len(spec.Nodes) < 4 {
		spec.Nodes = append(spec.Nodes, randNode(newRNG(21)), randNode(newRNG(22)))
	}
	for _, i := range []int{1, len(spec.Nodes) - 1} {
		n := spec.Nodes[i]
		n.Op = paint.Difference
		spec.Nodes[i] = n
	}
	if got := FirstDiverging(spec, injected(func(n NodeSpec) bool { return n.Op == paint.Difference })); got != 1 {
		t.Fatalf("first diverging node = %d, want 1", got)
	}
}

// Diff must report cleanly when both backends are the same renderer: no
// divergence, no shrinking, and the scene recognised as non-trivial.
func TestDiffAgreesWithItself(t *testing.T) {
	r := RenderFunc(cpu.Render)
	for i := range 8 {
		spec := Generate(uint64(i) + 1)
		rep, diverged, err := Diff(spec, r, r)
		if err != nil {
			t.Fatalf("seed=0x%x: %v", spec.Seed, err)
		}
		if diverged {
			t.Fatalf("seed=0x%x: a renderer diverged from itself:\n%s", spec.Seed, rep)
		}
		if rep.Renders != 2 {
			t.Errorf("seed=0x%x: spent %d renders on a passing scene, want 2", spec.Seed, rep.Renders)
		}
	}
}

// A divergence must be found, minimized and attributed — verified by injecting a
// renderer that is wrong in one known place. Without this, every part of the
// failure path is untested precisely because the renderers agree.
func TestDiffMinimizesAnInjectedDivergence(t *testing.T) {
	spec := Generate(5)
	bad := RenderFunc(func(sc *scene.Scene, w, h int) *image.RGBA {
		img := cpu.Render(sc, w, h)
		if len(sc.Nodes) > 0 {
			corrupt(img)
		}
		return img
	})

	rep, diverged, err := Diff(spec, bad, cpu.Render)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if !diverged {
		t.Fatal("Diff missed an injected divergence")
	}
	if len(rep.Spec.Nodes) != 1 {
		t.Errorf("minimized to %d nodes, want 1", len(rep.Spec.Nodes))
	}
	if rep.FirstNode != 0 {
		t.Errorf("first diverging node = %d, want 0", rep.FirstNode)
	}
	if rep.Box.Empty() {
		t.Error("no diverging pixel box reported")
	}
	if rep.Result.MaxDelta <= rep.Gate.Tol {
		t.Errorf("reported max delta %d does not exceed the gate %s", rep.Result.MaxDelta, rep.Gate)
	}
	t.Logf("report:\n%s", rep)
}

// corrupt shifts one pixel far enough that no tolerance absorbs it.
func corrupt(img *image.RGBA) {
	i := img.PixOffset(img.Rect.Min.X+1, img.Rect.Min.Y+1)
	img.Pix[i] ^= 0xff
}
