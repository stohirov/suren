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
// agreement of the renderers. Measured on the scene the fuzzer produced (seed
// 0x737): a ColorDodge node reading a backdrop that a previous blend left
// differing by the legal 1 LSB diverged by Δ=18, and the same node fed a
// gradient — whose parameter differs by well under one LSB, invisible at Δ≤1
// under SrcOver — diverged by Δ=17 via dB/dcs = cb/(1-cs)² ≈ 54. Both backends
// are individually correct in both cases; there is no bug to find and no bound
// to gate at.
//
// They stay covered where the oracle DOES apply: the corpus feeds them
// bit-identical inputs (a solid source over a plain backdrop) and gates them
// exactly, at the Δ≤2/Δ≤3 budgets Phase 10 measured — which this generator
// independently re-derived for the single-node case before the exclusion.
// Spec.Tol still gates them correctly if a stored spec contains one, because Tol
// is a function of the scene, not of this generator.
var blendModes = []paint.BlendMode{
	paint.SrcOver, paint.Multiply, paint.Screen, paint.Overlay, paint.Darken, paint.Lighten,
	paint.HardLight, paint.SoftLight, paint.Difference, paint.Exclusion,
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
		Rule:      randRule(rng),
	}
	if rng.IntN(10) < 3 {
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
