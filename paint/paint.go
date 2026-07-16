package paint

import "github.com/stohirov/suren/geom"
import "github.com/stohirov/suren/path"

type FillRule uint8

const (
	NonZero FillRule = iota
	EvenOdd
)

// BlendMode answers how a source and backdrop COLOR combine — the W3C
// mix-blend-mode axis. It is orthogonal to CompositeOp, which answers how their
// COVERAGE combines; a node carries one of each.
//
// Normal is the W3C name for "take the source color", and it is what this enum's
// zero value has always meant. It was called SrcOver until Phase 15, which is
// the name of a Porter-Duff OPERATOR and not of a blend function: the old name
// conflated the two axes at exactly the point they had to come apart. Every
// scene that said SrcOver meant Normal blending under a source-over composite,
// which is now spelled with the two fields it was always two facts about.
type BlendMode uint8

const (
	Normal BlendMode = iota
	Multiply
	Screen
	Overlay
	Darken
	Lighten
	ColorDodge
	ColorBurn
	HardLight
	SoftLight
	Difference
	Exclusion
)

// CompositeOp answers how a source and backdrop COVERAGE combine — the twelve
// Porter-Duff operators, the W3C composite axis. Orthogonal to BlendMode.
//
// SrcOver is the zero value, so a node that names neither axis composites the
// way every node did before Phase 15.
//
// The operators are a pair of coefficients (Fa, Fb) applied to the source and
// backdrop contributions:
//
//	co = αs·Fa·Cs + αb·Fb·Cb        αo = αs·Fa + αb·Fb
//
// where Cs is the source color AFTER blending with the backdrop. See
// raster.Coefficients for the table; it is stated once and ported verbatim to
// raster.wgsl.
type CompositeOp uint8

const (
	SrcOver CompositeOp = iota
	Clear
	Src
	Dst
	DstOver
	SrcIn
	DstIn
	SrcOut
	DstOut
	SrcAtop
	DstAtop
	Xor
)

type Color struct {
	R, G, B, A float64
}

func RGB(r, g, b float64) Color { return Color{r, g, b, 1} }

func RGBA(r, g, b, a float64) Color { return Color{r, g, b, a} }

func Gray(v float64) Color { return Color{v, v, v, 1} }

func FromRGBA8(r, g, b, a uint8) Color {
	return Color{float64(r) / 255, float64(g) / 255, float64(b) / 255, float64(a) / 255}
}

func clamp01(v float64) float64 {
	if v <= 0 {
		return 0
	}
	if v >= 1 {
		return 1
	}
	return v
}

func (c Color) RGBA() (r, g, b, a uint32) {
	ca := clamp01(c.A)
	to16 := func(v float64) uint32 { return uint32(clamp01(v)*ca*0xffff + 0.5) }
	return to16(c.R), to16(c.G), to16(c.B), uint32(ca*0xffff + 0.5)
}

type Paint interface {
	isPaint()
}

type Solid struct {
	Color Color
}

func (Solid) isPaint() {}

type Stop struct {
	Offset float64
	Color  Color
}

type LinearGradient struct {
	P0, P1 geom.Point
	Stops  []Stop
}

func (LinearGradient) isPaint() {}

type RadialGradient struct {
	Center geom.Point
	Radius float64
	Stops  []Stop
}

func (RadialGradient) isPaint() {}

// ConicGradient sweeps its stops around Center. The parameter is the angle from
// Angle, measured in the direction of increasing atan2 — clockwise on screen,
// since device y grows downward — wrapped into [0,1):
//
//	t = frac((atan2(qy-cy, qx-cx) - Angle) / 2π)
//
// # The seam is a discontinuity, and it is the whole correctness story
//
// Linear and radial gradients are continuous in the pixel position, so the
// sub-LSB difference between the CPU's f64 parameter and the GPU's f32 one
// produces a sub-LSB difference in colour, which the Δ≤1 floor absorbs. A conic
// gradient wraps: crossing the ray at Angle takes t from just under 1 to just
// over 0, so the colour jumps from the last stop to the first. Where those two
// stops differ, the paint is DISCONTINUOUS along that ray, and a discontinuity
// amplifies any input difference without bound — the same species of problem as
// ColorDodge's unbounded derivative (internal/parity/fuzz), not a rounding
// artifact a tolerance can own.
//
// It is bounded in EXTENT rather than in magnitude: only a pixel whose centre
// falls within f32's ~1e-7 of the seam ray can land on opposite sides in the two
// backends, so it is rare rather than mild. Give the gradient matching first and
// last stops and the paint is continuous everywhere, the seam vanishes, and the
// ordinary floor holds — which is what internal/parity/fuzz generates and what
// the gradient-conic corpus entry pins.
type ConicGradient struct {
	Center geom.Point
	Angle  float64
	Stops  []Stop
}

func (ConicGradient) isPaint() {}

// MeshVertex is a colour at a point in paint space.
type MeshVertex struct {
	P     geom.Point
	Color Color
}

// MeshTriangle is a Gouraud triangle: three coloured corners, the colour of any
// interior point being their barycentric combination.
type MeshTriangle struct {
	V [3]MeshVertex
}

// MeshGradient is a Gouraud triangle mesh — PDF shading types 4/5 — evaluated per
// pixel by MeshAt. A point outside every triangle is transparent.
//
// # Why triangles rather than Coons patches
//
// The plan named "Coons-patch / Gouraud" and predicted per-patch interpolation
// would miss the exact floor. Triangles are the half that fits this renderer's
// shape, and the reason is the INVERSE map, not the interpolation.
//
// A Paint here answers "what colour is at this pixel", so shading needs device
// space → patch parameter. For a Gouraud triangle that inverse is a closed-form
// 2x2 solve: barycentric coordinates, one division, no iteration. For a Coons
// patch with cubic edges it is a 2D nonlinear solve — Newton, per pixel — and to
// keep parity BOTH backends would have to run bit-comparable iterations from the
// same initial guess with the same fixed step count, since an early-exit whose
// predicate is evaluated in f32 on one side and f64 on the other is a different
// function, not a rounding difference. That is a large amount of machinery whose
// correctness story is worse, and a caller can tessellate a Coons patch into
// triangles anyway.
//
// # Where it is ill-conditioned
//
// The mesh's outer SILHOUETTE is not safe, for the same reason a conic's seam is
// not: colour drops to transparent across it, so the paint is discontinuous and a
// pixel centre within f32's reach of a boundary edge can be opaque on one backend
// and transparent on the other. Unlike the conic there is no closed-loop trick —
// a mesh has to end somewhere. The remedy is scene-level and is what MeshScene
// does: extend the mesh past the filled path so no boundary pixel is ever
// sampled. See internal/sample.
//
// Interior edges are safe only because MeshEps makes them so. See MeshEps: the
// first draft asserted they were safe by construction and was wrong, in a way
// that drew a visible crack.
type MeshGradient struct {
	Triangles []MeshTriangle
}

func (MeshGradient) isPaint() {}

// MeshGrid tessellates r into a cols x rows quad grid, splitting each quad into
// two Gouraud triangles along its top-left/bottom-right diagonal. colors are the
// (rows+1)*(cols+1) grid-vertex colours in row-major order.
//
// It exists so the triangulation ORDER is stated once. MeshAt resolves overlap by
// first match, so the order in which quads and their two halves are emitted is
// part of the mesh's meaning, not an implementation detail — two callers building
// "the same" grid differently would disagree along every shared diagonal. Sharing
// vertices between neighbouring quads is the other half: a vertex colour is read
// from one place, so adjacent triangles agree along their shared edge by
// construction and the mesh is continuous.
func MeshGrid(r geom.Rect, cols, rows int, colors []Color) MeshGradient {
	at := func(i, j int) MeshVertex {
		u, v := float64(i)/float64(cols), float64(j)/float64(rows)
		return MeshVertex{
			P:     geom.Pt(r.Min.X+u*r.Width(), r.Min.Y+v*r.Height()),
			Color: colors[j*(cols+1)+i],
		}
	}
	m := MeshGradient{Triangles: make([]MeshTriangle, 0, 2*cols*rows)}
	for j := range rows {
		for i := range cols {
			a, b, c, d := at(i, j), at(i+1, j), at(i+1, j+1), at(i, j+1)
			m.Triangles = append(m.Triangles,
				MeshTriangle{V: [3]MeshVertex{a, b, c}},
				MeshTriangle{V: [3]MeshVertex{a, c, d}})
		}
	}
	return m
}

// MeshEps is the slack in the barycentric inside test, in NORMALIZED barycentric
// units — the weights sum to 1, so it is a fraction of a triangle rather than a
// distance, and it means the same thing for a 3px triangle and a 300px one.
//
// It is not a fudge factor. Without it, two triangles sharing an edge each reject
// a point ON that edge and the mesh draws a HAIRLINE CRACK along every interior
// diagonal, because the shared edge's weight is computed by two DIFFERENT
// expressions: it is an edge function over the denominator in one triangle and
// `1 - l0 - l1` in the other. In exact arithmetic those are complementary — one
// is zero exactly when the other is, and the two sign tests partition the edge.
// In floating point they are independent roundings of the same real zero, so both
// can land a hair negative and both `l >= 0` tests fail.
//
// Measured, and the polarity is the lesson: on a 3x3 quad grid a pixel centre
// landing exactly on a shared diagonal came out l=-3.5e-17 in one triangle and
// l=-8.3e-17 in the other — so the f64 CPU reference rejected BOTH and painted
// the background through the mesh, 41 pixels of visible crack, while the f32 GPU
// rounded the same real zero to +0.0, accepted, and drew correctly (Δ=198). MORE
// precision made it worse: exact zero is the only value both triangles accept, and
// f64 is better at not landing on it.
//
// This was a bug in BOTH renderers that only the DIFFERENTIAL could see. A CPU
// golden would have recorded the crack as the expected output and gated it there
// forever; the GPU had no bug to find. It is the clearest case in this tree for
// why the reference is not the oracle — see internal/parity.
//
// 1e-5 is ~300x the f32 noise measured at that pixel (3e-8) and still far below
// any visible effect: it widens each triangle by a hundred-thousandth of itself,
// so the silhouette moves by a small fraction of a pixel and a point near an
// interior edge is accepted by the FIRST of the two triangles on both backends —
// which is the point. The slack does not need to pick the right triangle, only
// the SAME one, and Gouraud continuity makes the two agree along the edge anyway.
const MeshEps = 1e-5

// MeshAt is the canonical mesh evaluator: raster.wgsl's meshColor is a verbatim
// port, term for term and in the same order, and the two must not drift. It is
// stated once here for the same reason raster.Coefficients states the Porter-Duff
// table once.
//
// First match wins, in triangle order. Overlapping triangles are therefore
// resolved by the scene's own ordering rather than by depth, and — see the type's
// comment — a shared interior edge makes the choice immaterial to the colour.
//
// A degenerate triangle (zero area, so the barycentric denominator vanishes) is
// SKIPPED rather than divided by. That case is reachable from any caller and the
// division would otherwise produce infinities that the inside test would then
// compare, which is undefined behaviour dressed up as a colour.
//
// # The float64() conversions are load-bearing. Do not remove them.
//
// Every `float64(x*y)` below exists to ROUND that product before it is added,
// which is what the Go spec gives you to forbid the compiler from contracting
// `x*y + z` into a fused multiply-add. Go fuses on arm64 and does not on amd64,
// so without them this function computes a DIFFERENT VALUE per architecture and
// the mesh golden is portable only by luck. They look redundant. They are not,
// and TestMeshAtDoesNotFuse fails on every architecture if they are deleted.
//
// Phase 13 predicted this was unobservable: f64 carries ~13 orders of magnitude
// more precision than 8-bit output needs, so a 1-ULP difference cannot survive
// quantization. That reasoning is wrong, and the mesh scene is where it broke.
// Headroom is irrelevant AT A TIE. Quantization is a threshold, not a smooth map,
// and a threshold has no headroom in front of it — the perturbation does not have
// to be big enough to matter, only non-zero.
//
// Measured at pixel (75,96) of internal/sample.MeshScene, blue channel, with the
// true value computed in exact rational arithmetic:
//
//	true    0.5 + 1.36e-17   (just ABOVE 1/2)  -> x*0xffff = 32767.5+ -> 32768
//	unfused 0.5              (this code)       -> exactly  32767.5    -> 32768  ✓
//	fused   0.5 - 1 ULP      (arm64 today)     ->          32767.49…  -> 32767  ✗
//
// And the polarity is the lesson, because the obvious guess is backwards. Fusion
// is the MORE accurate operation in general — one rounding instead of two — so
// the natural assumption is that the fused answer is right and portability costs
// accuracy. It is the other way around here: unfused lands 3x closer to the true
// value (1.36e-17 vs 4.19e-17) and on the correct side of the tie. There is no
// trade. This code is both deterministic AND more correct, and the golden that
// recorded the fused value recorded a WRONG PIXEL — which is the mesh crack's
// lesson again, in a different key: see MeshEps above, where more precision also
// made things worse and only the differential could see it. A golden generated on
// one architecture cannot see this at all.
//
// math.FMA would pin the fused answer everywhere and was rejected on both counts:
// it is the less accurate value here, and it costs 3.8x on amd64 because Go
// emulates it in software unless GOAMD64>=v3, which no library can require of its
// callers. Measured, both variants, both architectures — see Phase 13 in
// docs/roadmap.md.
//
// raster.wgsl's meshColor stays a verbatim port of the TERM ORDER above. It
// cannot port these conversions — WGSL has no way to forbid contraction — and it
// does not need to: the GPU is f32 and gated at Δ≤1, which absorbs a 1-ULP flip.
// This function is the CPU reference, and the reference is what must not move.
func MeshAt(tris []MeshTriangle, q geom.Point) (Color, bool) {
	for _, t := range tris {
		a, b, c := t.V[0], t.V[1], t.V[2]
		d := float64((b.P.Y-c.P.Y)*(a.P.X-c.P.X)) + float64((c.P.X-b.P.X)*(a.P.Y-c.P.Y))
		if d == 0 {
			continue
		}
		l0 := (float64((b.P.Y-c.P.Y)*(q.X-c.P.X)) + float64((c.P.X-b.P.X)*(q.Y-c.P.Y))) / d
		l1 := (float64((c.P.Y-a.P.Y)*(q.X-c.P.X)) + float64((a.P.X-c.P.X)*(q.Y-c.P.Y))) / d
		l2 := 1 - l0 - l1
		if l0 < -MeshEps || l1 < -MeshEps || l2 < -MeshEps {
			continue
		}
		return Color{
			R: float64(float64(l0*a.Color.R)+float64(l1*b.Color.R)) + float64(l2*c.Color.R),
			G: float64(float64(l0*a.Color.G)+float64(l1*b.Color.G)) + float64(l2*c.Color.G),
			B: float64(float64(l0*a.Color.B)+float64(l1*b.Color.B)) + float64(l2*c.Color.B),
			A: float64(float64(l0*a.Color.A)+float64(l1*b.Color.A)) + float64(l2*c.Color.A),
		}, true
	}
	return Color{}, false
}

func Interp(stops []Stop, t float64) Color {
	if len(stops) == 0 {
		return Color{}
	}
	if t <= stops[0].Offset {
		return stops[0].Color
	}
	last := stops[len(stops)-1]
	if t >= last.Offset {
		return last.Color
	}
	for i := 1; i < len(stops); i++ {
		hi := stops[i]
		if t <= hi.Offset {
			lo := stops[i-1]
			span := hi.Offset - lo.Offset
			if span <= 0 {
				return hi.Color
			}
			return lerp(lo.Color, hi.Color, (t-lo.Offset)/span)
		}
	}
	return last.Color
}

func lerp(a, b Color, t float64) Color {
	return Color{
		R: a.R + (b.R-a.R)*t,
		G: a.G + (b.G-a.G)*t,
		B: a.B + (b.B-a.B)*t,
		A: a.A + (b.A-a.A)*t,
	}
}

type Stroke struct {
	Width      float64
	MiterLimit float64
	Cap        path.Cap
	Join       path.Join
	Dashes     []float64
	DashOffset float64
}

func (s Stroke) Stroker() path.Stroker {
	return path.Stroker{
		Width:      s.Width,
		Cap:        s.Cap,
		Join:       s.Join,
		MiterLimit: s.MiterLimit,
	}
}

func (s Stroke) Dash() (path.Dash, bool) {
	if len(s.Dashes) == 0 {
		return path.Dash{}, false
	}
	return path.Dash{Pattern: s.Dashes, Phase: s.DashOffset}, true
}
