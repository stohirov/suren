package fuzz

import (
	"math"
	"math/rand/v2"

	"github.com/stohirov/sukho/geom"
	"github.com/stohirov/sukho/paint"
	"github.com/stohirov/sukho/path"
)

const (
	W = 96
	H = 96
)

// blendModes is the implemented set MINUS ColorDodge and ColorBurn, and the
// omission is a measured scope decision rather than a convenience.
//
// Those two are ill-conditioned: their blend derivatives are 1/(1-cs) and 1/cs,
// unbounded as the divisor approaches zero, so they multiply ANY difference in
// their inputs without bound. A differential oracle needs a well-conditioned
// function — otherwise it measures the conditioning of the operator, not the
// agreement of the renderers, and both backends can be individually correct
// while disagreeing by any amount.
//
// The exclusion survived Phase 13, which shrank the effect without bounding it.
// Pinning the shader's rounding removed the 1-LSB backdrop mismatch these modes
// were amplifying, and the worst generated divergence fell from Δ=18 to Δ=5 —
// but what is left is the *sub*-LSB difference in antialiased coverage and
// gradient parameters, and an unbounded derivative amplifies that too. The
// measurement says so directly: the worst delta over scenes containing dodge or
// burn keeps climbing with sample size (Δ=3 at 3k seeds, Δ=5 at 25k) while every
// other mode stays flat at Δ=2 across both. A budget fitted to any sample of an
// unbounded distribution is a flake waiting for a longer fuzz run.
//
// They stay covered where the oracle DOES apply: the corpus feeds them
// bit-identical inputs (a solid source over a plain backdrop) and — since
// Phase 13 — gates them at the ordinary quantization floor, no budget needed.
// Spec.Tol still gates them correctly if a stored spec contains one, because Tol
// is a function of the scene, not of this generator.
var blendModes = []paint.BlendMode{
	paint.Normal, paint.Multiply, paint.Screen, paint.Overlay, paint.Darken, paint.Lighten,
	paint.HardLight, paint.SoftLight, paint.Difference, paint.Exclusion,
}

// compositeOps is the full Porter-Duff set MINUS Clear, Src and Dst, and unlike
// blendModes the omission is about the ORACLE'S REACH, not the operators'
// conditioning. All twelve are well-conditioned; these three are excluded
// because of what they do to the generated scene:
//
//   - Clear and Src discard the backdrop wholesale. A scene ending in one is a
//     scene whose earlier nodes never mattered, so the generator would spend its
//     budget rendering work it then erased, and the shrinker would find every
//     bug reduced to a single node.
//   - Dst is the identity on the framebuffer. It contributes nothing to render
//     and nothing to diverge.
//
// None of the three is left untested: each has a corpus entry over all three
// backdrop regimes, where its effect is the point rather than an obstacle. What
// is lost here is only their INTERACTION with generated geometry, and since all
// three ignore at least one operand entirely, there is little interaction to
// lose. The nine that read both operands are the ones worth generating.
var compositeOps = []paint.CompositeOp{
	paint.SrcOver, paint.DstOver, paint.SrcIn, paint.DstIn, paint.SrcOut,
	paint.DstOut, paint.SrcAtop, paint.DstAtop, paint.Xor,
}

// randComposite biases hard toward SrcOver, and the bias is what keeps the
// oracle alive rather than a hedge against the operators.
//
// Sampling the nine uniformly took the generator's trivial-scene rate from 0.8%
// to 4.0%, past the 3% gate. The cause is not a bug: an operator like SrcOut
// over an opaque backdrop is DEFINED to erase, so a large node carrying one
// wipes the frame to uniform transparency and the differential has nothing left
// to compare. Raising the gate to fit would have inverted its purpose — it
// exists to fail exactly when a generator change starts drawing nothing.
//
// So a node keeps SrcOver unless it draws below the threshold. Measured trivial
// rate over 1000 seeds against the 3% gate: 0.8% with no composite ops at all,
// 1.7% at 1/10, 1.8% at 2/10, 2.4% at 3/10, 4.0% sampling all nine uniformly.
// 2/10 is the pick — it doubles the baseline rate while keeping better than a
// third of the gate in hand for seed variation, and still lands a non-SrcOver
// operator in roughly a fifth of all nodes.
func randComposite(rng *rand.Rand) paint.CompositeOp {
	if rng.IntN(10) < 2 {
		return compositeOps[1+rng.IntN(len(compositeOps)-1)]
	}
	return paint.SrcOver
}

func newRNG(seed uint64) *rand.Rand {
	return rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
}

// Generate builds a random but VALID scene from a seed. Valid is the operative
// word: this is a differential oracle, so a scene that renders nothing proves
// nothing — every scene opens with an opaque background covering the canvas.
// That guarantees a non-empty backdrop (without which every blend mode collapses
// to the same premultiply round-trip) and keeps clipped nodes biting on
// something. Anti-vacuity is still asserted per scene, because a guarantee that
// is not checked is a comment.
func Generate(seed uint64) Spec {
	rng := newRNG(seed)
	s := Spec{Seed: seed, W: W, H: H}
	s.Nodes = append(s.Nodes, background(rng))
	for range 1 + rng.IntN(4) {
		s.Nodes = append(s.Nodes, randNode(rng))
	}
	return s
}

func background(rng *rand.Rand) NodeSpec {
	return NodeSpec{
		Shape:     ShapeSpec{Kind: ShapeRect, Rect: geom.RectXYWH(0, 0, W, H)},
		Transform: geom.Identity(),
		Paint:     PaintSpec{Kind: PaintSolid, Color: randOpaque(rng)},
	}
}

func randNode(rng *rand.Rand) NodeSpec {
	n := NodeSpec{
		Shape:     randShape(rng),
		Transform: randTransform(rng),
		Paint:     randPaint(rng),
		Op:        blendModes[rng.IntN(len(blendModes))],
		Composite: randComposite(rng),
		Rule:      randRule(rng),
	}
	if rng.IntN(10) < 2 {
		n.Stroke = randStroke(rng)
	}
	for range rng.IntN(3) {
		n.Clips = append(n.Clips, randClip(rng))
	}
	return n
}

// Geometry stays near the canvas centre so a random transform does not push it
// off-canvas, which would render nothing and prove nothing.
func randShape(rng *rand.Rand) ShapeSpec {
	cx, cy := W/2.0, H/2.0
	switch rng.IntN(5) {
	case 0:
		return ShapeSpec{Kind: ShapeRect, Rect: randCenteredRect(rng)}
	case 1:
		r := randCenteredRect(rng)
		return ShapeSpec{Kind: ShapeRoundRect, Rect: r, RX: 2 + rng.Float64()*8, RY: 2 + rng.Float64()*8}
	case 2:
		return ShapeSpec{Kind: ShapeCircle, Rect: randCenteredRect(rng)}
	case 3:
		n := 3 + rng.IntN(6)
		pts := make([]geom.Point, 0, n)
		r0 := 14 + rng.Float64()*20
		for i := range n {
			ang := 2*math.Pi*float64(i)/float64(n) + rng.Float64()*0.4
			rad := r0 * (0.5 + rng.Float64()*0.7)
			pts = append(pts, geom.Pt(cx+rad*math.Cos(ang), cy+rad*math.Sin(ang)))
		}
		return ShapeSpec{Kind: ShapePolygon, Pts: pts}
	default:
		k := 1 + rng.IntN(3)
		pts := make([]geom.Point, 0, 1+3*k)
		near := func() geom.Point {
			return geom.Pt(cx+(rng.Float64()*2-1)*34, cy+(rng.Float64()*2-1)*34)
		}
		pts = append(pts, near())
		for range 3 * k {
			pts = append(pts, near())
		}
		return ShapeSpec{Kind: ShapeCurve, Pts: pts}
	}
}

func randCenteredRect(rng *rand.Rand) geom.Rect {
	cx, cy := W/2.0, H/2.0
	w := 18 + rng.Float64()*44
	h := 18 + rng.Float64()*44
	dx := (rng.Float64()*2 - 1) * 10
	dy := (rng.Float64()*2 - 1) * 10
	return geom.RectXYWH(cx-w/2+dx, cy-h/2+dy, w, h)
}

func randTransform(rng *rand.Rand) geom.Matrix {
	if rng.IntN(4) == 0 {
		return geom.Identity()
	}
	cx, cy := W/2.0, H/2.0
	m := geom.Translate(cx, cy)
	m = m.Mul(geom.Rotate((rng.Float64()*2 - 1) * 0.7))
	s := 0.7 + rng.Float64()*0.6
	m = m.Mul(geom.Scale(s, s))
	if rng.IntN(4) == 0 {
		m = m.Mul(geom.Shear((rng.Float64()*2-1)*0.3, (rng.Float64()*2-1)*0.3))
	}
	m = m.Mul(geom.Translate((rng.Float64()*2-1)*10, (rng.Float64()*2-1)*10))
	return m.Mul(geom.Translate(-cx, -cy))
}

func randPaint(rng *rand.Rand) PaintSpec {
	switch rng.IntN(4) {
	case 0:
		return PaintSpec{
			Kind:  PaintLinear,
			P0:    geom.Pt(rng.Float64()*W, rng.Float64()*H),
			P1:    geom.Pt(rng.Float64()*W, rng.Float64()*H),
			Stops: randStops(rng),
		}
	case 1:
		return PaintSpec{
			Kind:   PaintRadial,
			Center: geom.Pt(W/2+(rng.Float64()*2-1)*16, H/2+(rng.Float64()*2-1)*16),
			Radius: 12 + rng.Float64()*36,
			Stops:  randStops(rng),
		}
	default:
		return PaintSpec{Kind: PaintSolid, Color: randColor(rng)}
	}
}

func randStops(rng *rand.Rand) []paint.Stop {
	n := 2 + rng.IntN(3)
	stops := make([]paint.Stop, n)
	for i := range stops {
		stops[i] = paint.Stop{Offset: float64(i) / float64(n-1), Color: randColor(rng)}
	}
	return stops
}

// Alpha is never 0: a fully transparent color renders nothing. Low alphas are
// kept — that is where the blend path's divide by alpha is worst behaved.
func randColor(rng *rand.Rand) paint.Color {
	return paint.FromRGBA8(uint8(rng.IntN(256)), uint8(rng.IntN(256)), uint8(rng.IntN(256)), uint8(1+rng.IntN(255)))
}

func randOpaque(rng *rand.Rand) paint.Color {
	return paint.FromRGBA8(uint8(rng.IntN(256)), uint8(rng.IntN(256)), uint8(rng.IntN(256)), 255)
}

func randRule(rng *rand.Rand) paint.FillRule {
	if rng.IntN(2) == 0 {
		return paint.NonZero
	}
	return paint.EvenOdd
}

func randStroke(rng *rand.Rand) *StrokeSpec {
	s := &StrokeSpec{
		Width:      1 + rng.Float64()*6,
		MiterLimit: 1 + rng.Float64()*8,
		Cap:        []path.Cap{path.ButtCap, path.RoundCap, path.SquareCap}[rng.IntN(3)],
		Join:       []path.Join{path.MiterJoin, path.RoundJoin, path.BevelJoin}[rng.IntN(3)],
	}
	if rng.IntN(3) == 0 {
		s.Dashes = []float64{1 + rng.Float64()*6, 1 + rng.Float64()*6}
		s.DashOffset = rng.Float64() * 4
	}
	return s
}

func randClip(rng *rand.Rand) ClipSpec {
	return ClipSpec{
		Shape: randShape(rng),
		Rule:  randRule(rng),
		Rect:  rng.IntN(3) == 0,
	}
}
