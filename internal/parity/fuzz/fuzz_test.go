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

	for _, tc := range []struct {
		name string
		spec Spec
		want parity.Config
	}{
		{"no blending is exact", spec(paint.Normal, paint.Normal), parity.Quantized()},
		{"one general mode over solids is exact", spec(paint.Normal, paint.Overlay), parity.Quantized()},
		{"one ill-conditioned mode is still only the floor", spec(paint.Normal, paint.ColorDodge), parity.Quantized()},
		{"two general modes earn the stacking budget", spec(paint.Overlay, paint.Multiply), stackedBlend},
		{"three general modes earn the same", spec(paint.Overlay, paint.Multiply, paint.Screen), stackedBlend},
		// The Phase 15 fuzz find, as a unit case. A gradient anywhere in the scene
		// leaves a backdrop the two backends already disagree about by an LSB, so
		// ONE blend node is enough to amplify it past the floor — the case the old
		// rule called exact and seed 0xb50 disproved.
		{"one general mode over a gradient earns the budget", gradSpec(paint.Overlay), stackedBlend},
		{"a gradient with no blending stays at the floor", gradSpec(paint.Normal), parity.Quantized()},
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
func TestGeneratorOmitsIllConditionedBlendModes(t *testing.T) {
	for op := range illConditioned {
		for _, m := range blendModes {
			if m == op {
				t.Errorf("generator emits %v, whose derivative is unbounded; it cannot be gated by a differential oracle", op)
			}
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
