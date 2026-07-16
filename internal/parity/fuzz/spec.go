// Package fuzz drives differential testing: a seed generates a scene, both
// backends render it, and any divergence beyond the gate the scene has earned is
// minimized to the smallest scene that still diverges and attributed to a single
// node.
//
// A Spec is fully explicit data, not a seed plus a replay procedure. That is the
// design decision the rest of the package rests on: shrinking must be able to
// strip one field of one node and re-render, which is impossible if the scene
// only exists as "whatever this RNG produces". It also makes a find storable as
// JSON, so a minimized failure becomes a permanent corpus entry (Phase 12a)
// rather than a seed that only reproduces while the generator is untouched —
// changing the generator would otherwise silently retire every past find.
//
// The seed is still the repro for a fresh find; the Spec is the repro for a
// recorded one.
package fuzz

import (
	"fmt"

	"github.com/stohirov/sukho/geom"
	"github.com/stohirov/sukho/internal/parity"
	"github.com/stohirov/sukho/paint"
	"github.com/stohirov/sukho/path"
	"github.com/stohirov/sukho/render"
	"github.com/stohirov/sukho/scene"
)

type ShapeKind uint8

const (
	ShapeRect ShapeKind = iota
	ShapeRoundRect
	ShapeCircle
	ShapePolygon
	ShapeCurve
)

// ShapeSpec carries its geometry explicitly rather than as generator
// parameters, so shrinking can replace any shape with its bounding rect — the
// simplest shape that still covers the same pixels.
type ShapeSpec struct {
	Kind ShapeKind    `json:"kind"`
	Rect geom.Rect    `json:"rect,omitzero"`
	RX   float64      `json:"rx,omitempty"`
	RY   float64      `json:"ry,omitempty"`
	Pts  []geom.Point `json:"pts,omitempty"`
}

func (s ShapeSpec) Path() path.Path {
	switch s.Kind {
	case ShapeRect:
		return path.Rect(s.Rect)
	case ShapeRoundRect:
		return path.RoundedRect(s.Rect, s.RX, s.RY)
	case ShapeCircle:
		c := geom.Pt((s.Rect.Min.X+s.Rect.Max.X)/2, (s.Rect.Min.Y+s.Rect.Max.Y)/2)
		return path.Circle(c, min(s.Rect.Width(), s.Rect.Height())/2)
	case ShapePolygon:
		var p path.Path
		p.MoveTo(s.Pts[0])
		for _, pt := range s.Pts[1:] {
			p.LineTo(pt)
		}
		p.Close()
		return p
	case ShapeCurve:
		var p path.Path
		p.MoveTo(s.Pts[0])
		for i := 1; i+2 < len(s.Pts); i += 3 {
			p.CubicTo(s.Pts[i], s.Pts[i+1], s.Pts[i+2])
		}
		p.Close()
		return p
	}
	return path.Path{}
}

func (s ShapeSpec) validate() error {
	switch s.Kind {
	case ShapeRect, ShapeRoundRect, ShapeCircle:
		if s.Rect.Empty() {
			return fmt.Errorf("shape kind %d has an empty rect %v", s.Kind, s.Rect)
		}
	case ShapePolygon:
		if len(s.Pts) < 3 {
			return fmt.Errorf("polygon has %d points, need at least 3", len(s.Pts))
		}
	case ShapeCurve:
		if len(s.Pts) < 4 || (len(s.Pts)-1)%3 != 0 {
			return fmt.Errorf("curve has %d points, need 1+3k with k>=1", len(s.Pts))
		}
	default:
		return fmt.Errorf("unknown shape kind %d", s.Kind)
	}
	return nil
}

// bboxRect is the shrink target for any shape: same footprint, simplest form.
func (s ShapeSpec) bboxRect() ShapeSpec {
	return ShapeSpec{Kind: ShapeRect, Rect: s.Path().Bounds()}
}

type PaintKind uint8

const (
	PaintSolid PaintKind = iota
	PaintLinear
	PaintRadial
	PaintConic
	PaintMesh
)

// PaintSpec carries every paint kind's parameters in one flat record. Stops does
// double duty for PaintMesh, holding the grid's vertex COLOURS in row-major order
// with Offset unused — the same generalization the GPU's own Stop record makes
// (see backend/gpu's encode.go), and it keeps solid() and size() working for
// meshes without a special case.
type PaintSpec struct {
	Kind   PaintKind    `json:"kind"`
	Color  paint.Color  `json:"color,omitzero"`
	P0     geom.Point   `json:"p0,omitzero"`
	P1     geom.Point   `json:"p1,omitzero"`
	Center geom.Point   `json:"center,omitzero"`
	Radius float64      `json:"radius,omitempty"`
	Angle  float64      `json:"angle,omitempty"`
	Rect   geom.Rect    `json:"rect,omitzero"`
	Cols   int          `json:"cols,omitempty"`
	Rows   int          `json:"rows,omitempty"`
	Stops  []paint.Stop `json:"stops,omitempty"`
}

func (p PaintSpec) Paint() paint.Paint {
	switch p.Kind {
	case PaintLinear:
		return paint.LinearGradient{P0: p.P0, P1: p.P1, Stops: p.Stops}
	case PaintRadial:
		return paint.RadialGradient{Center: p.Center, Radius: p.Radius, Stops: p.Stops}
	case PaintConic:
		return paint.ConicGradient{Center: p.Center, Angle: p.Angle, Stops: p.Stops}
	case PaintMesh:
		colors := make([]paint.Color, len(p.Stops))
		for i, s := range p.Stops {
			colors[i] = s.Color
		}
		return paint.MeshGrid(p.Rect, p.Cols, p.Rows, colors)
	}
	return paint.Solid{Color: p.Color}
}

// closed reports whether a conic gradient's first and last stops carry the same
// colour, which is what makes its paint continuous across the seam ray — and so
// whether a differential oracle applies to it at all. See gen.go's randConic.
func (p PaintSpec) closed() bool {
	return p.Stops[0].Color == p.Stops[len(p.Stops)-1].Color
}

func (p PaintSpec) validate() error {
	switch p.Kind {
	case PaintSolid:
	case PaintLinear, PaintRadial, PaintConic:
		if len(p.Stops) < 2 {
			return fmt.Errorf("gradient has %d stops, need at least 2", len(p.Stops))
		}
		if p.Kind == PaintRadial && p.Radius <= 0 {
			return fmt.Errorf("radial gradient has radius %v", p.Radius)
		}
	case PaintMesh:
		if p.Cols < 1 || p.Rows < 1 {
			return fmt.Errorf("mesh grid is %dx%d, need at least 1x1", p.Cols, p.Rows)
		}
		if p.Rect.Empty() {
			return fmt.Errorf("mesh has an empty rect %v", p.Rect)
		}
		// paint.MeshGrid indexes colors[j*(cols+1)+i] with no bounds story of its
		// own, so a short slice is a panic rather than a bad render. The shrinker
		// rewrites specs and Load accepts them from disk; both reach this.
		if want := (p.Cols + 1) * (p.Rows + 1); len(p.Stops) != want {
			return fmt.Errorf("mesh %dx%d has %d vertex colours, need exactly %d", p.Cols, p.Rows, len(p.Stops), want)
		}
	default:
		return fmt.Errorf("unknown paint kind %d", p.Kind)
	}
	return nil
}

// solid collapses a gradient to one of its stops, so shrinking can remove the
// gradient without removing the node.
func (p PaintSpec) solid() PaintSpec {
	if p.Kind == PaintSolid {
		return p
	}
	return PaintSpec{Kind: PaintSolid, Color: p.Stops[0].Color}
}

type StrokeSpec struct {
	Width      float64   `json:"width"`
	MiterLimit float64   `json:"miterLimit,omitempty"`
	Cap        path.Cap  `json:"cap,omitempty"`
	Join       path.Join `json:"join,omitempty"`
	Dashes     []float64 `json:"dashes,omitempty"`
	DashOffset float64   `json:"dashOffset,omitempty"`
}

func (s StrokeSpec) Stroke() paint.Stroke {
	return paint.Stroke{
		Width:      s.Width,
		MiterLimit: s.MiterLimit,
		Cap:        s.Cap,
		Join:       s.Join,
		Dashes:     s.Dashes,
		DashOffset: s.DashOffset,
	}
}

// ClipSpec is applied under the node's CTM, so it exercises Canvas.ClipPath's
// transform rather than only device-space clips. Rect selects the cheap bbox
// pre-filter path (Canvas.ClipRect) instead of a path clip.
type ClipSpec struct {
	Shape ShapeSpec      `json:"shape"`
	Rule  paint.FillRule `json:"rule,omitempty"`
	Rect  bool           `json:"rect,omitempty"`
}

type NodeSpec struct {
	Shape     ShapeSpec         `json:"shape"`
	Transform geom.Matrix       `json:"transform"`
	Paint     PaintSpec         `json:"paint"`
	Op        paint.BlendMode   `json:"op,omitempty"`
	Composite paint.CompositeOp `json:"composite,omitempty"`
	Rule      paint.FillRule    `json:"rule,omitempty"`
	Stroke    *StrokeSpec       `json:"stroke,omitempty"`
	Clips     []ClipSpec        `json:"clips,omitempty"`
}

// Spec carries no JSON tags: codec.go marshals it through specJSON so the
// canvas size is stored explicitly.
type Spec struct {
	Seed  uint64
	W, H  int
	Nodes []NodeSpec
}

// specJSON spells out Width and Height so a stored regression carries its own
// canvas size rather than silently inheriting whatever the generator's constants
// say at load time.
type specJSON struct {
	Seed  uint64     `json:"seed"`
	W     int        `json:"w"`
	H     int        `json:"h"`
	Nodes []NodeSpec `json:"nodes"`
}

// Scene builds through render.Canvas rather than assembling scene.Nodes
// directly, so the fuzzer exercises the API a caller actually uses — clip
// stacking, Save/Restore scoping and blend state included — instead of a
// hand-built node the Canvas could never produce.
func (s Spec) Scene() *scene.Scene {
	c := render.NewCanvas()
	for _, n := range s.Nodes {
		c.Save()
		c.Transform(n.Transform)
		for _, cl := range n.Clips {
			if cl.Rect {
				c.ClipRect(cl.Shape.Path().Bounds())
			} else {
				c.ClipPath(cl.Shape.Path(), cl.Rule)
			}
		}
		c.SetBlend(n.Op)
		c.SetComposite(n.Composite)
		if n.Stroke != nil {
			c.Stroke(n.Shape.Path(), n.Paint.Paint(), n.Stroke.Stroke())
		} else {
			c.Fill(n.Shape.Path(), n.Paint.Paint(), n.Rule)
		}
		c.Restore()
	}
	return c.Scene()
}

func (s Spec) Validate() error {
	if s.W <= 0 || s.H <= 0 {
		return fmt.Errorf("bad canvas size %dx%d", s.W, s.H)
	}
	if len(s.Nodes) == 0 {
		return fmt.Errorf("spec has no nodes")
	}
	for i, n := range s.Nodes {
		if err := n.Shape.validate(); err != nil {
			return fmt.Errorf("node %d shape: %w", i, err)
		}
		if err := n.Paint.validate(); err != nil {
			return fmt.Errorf("node %d paint: %w", i, err)
		}
		if n.Stroke != nil && n.Stroke.Width <= 0 {
			return fmt.Errorf("node %d stroke width %v", i, n.Stroke.Width)
		}
		for j, cl := range n.Clips {
			if err := cl.Shape.validate(); err != nil {
				return fmt.Errorf("node %d clip %d: %w", i, j, err)
			}
		}
	}
	return nil
}

// Tol is the gate this scene has EARNED, derived from what it contains rather
// than fixed globally — the same discipline a corpus Entry follows. A generated
// scene cannot be gated by a global knob precisely because nobody chose what is
// in it.
//
// The budget is earned by an operation that AMPLIFIES: one whose output moves by
// more than the 1 LSB its inputs are apart, so the two backends' f32-vs-f64
// difference clears the rounding decision instead of vanishing under it.
// Everything else is held to the exact floor. Every gate is exact: nothing here
// is unreachable enough to need perceptual mode.
//
// Two amplifiers exist among what the generator emits, and they are the whole
// rule:
//
//   - A blend mode with dB/dCb > 1. Measured numerically over the unit square:
//     Overlay is 2.0 and SoftLight is 4.0; Normal is 0 and Multiply, Screen,
//     Darken, Lighten, HardLight, Difference and Exclusion are all exactly 1.0.
//     (HardLight's 2*cs branch only runs when cs <= 0.5, which is why it lands at
//     1 rather than 2 like its transpose Overlay.)
//   - Any non-SrcOver Porter-Duff operator. porterDuff unpremultiplies through
//     s[0]/s[3], and operators like SrcOut and Xor manufacture near-transparent
//     backdrops, so a 1-LSB difference in a premultiplied channel is divided by a
//     tiny alpha. SrcOver's fast path never unpremultiplies at all.
//
// A gate of exactly 1 LSB is safe for everything else, and that is arithmetic
// rather than luck: if |dCo/dCb| <= 1 then a 1-LSB input difference gives at most
// a 1-LSB output difference, and floor(x+.5) of two reals within 1 of each other
// cannot differ by 2.
//
// # This rule is measured, and its two predecessors were not (Phase 15 review)
//
// Both earlier rules asserted exactness for cases that are not exact, and both
// were wrong in the same way: they keyed on a correlate instead of the mechanism.
//
//   - `general >= 2` (through Phase 14) called one blend node exact. Seed 0xb50
//     disproved it — an opaque background, a radial-gradient rect, and ONE
//     SoftLight rect diverging at Δ=2. Recorded as
//     regress/fuzz-softlight-over-gradient.json.
//   - `general >= 2 || (general >= 1 && gradient)` (Phase 15's fix) blamed the
//     gradient. The gradient is a risk MULTIPLIER, not the cause: one Overlay
//     node over solids only, which that rule gated at Quantized, still breaches
//     at Δ=2 in 2 of 5955 generated scenes. With a gradient the same node breaches
//     in 42 of 904 — 40x more often, which is exactly why a gradient was present
//     in the find that prompted the rule and why blaming it looked right.
//
// The complement is measured at the sample size that matters: non-amplifying
// blends stacked over the generator's own scenes with all-SrcOver composites hold
// at Δ<=1 over 3000 scenes, 0 breaches — checked at a size that had already caught
// a 3-in-2999 event, so "never" here is not a claim a small sample was too coarse
// to refute.
func (s Spec) Tol() parity.Config {
	for _, n := range s.Nodes {
		if _, amp := amplifying[n.Op]; amp || n.Composite != paint.SrcOver {
			return amplifiedBlend
		}
	}
	return parity.Quantized()
}

// amplifying names the blend modes whose backdrop derivative exceeds 1 while
// staying bounded, so a budget can be fitted to them.
//
// ColorDodge and ColorBurn amplify too and are deliberately NOT here: their
// derivatives are unbounded, so no budget fits (see illConditioned). They are
// excluded from the generator instead, and a stored spec containing one keeps the
// ordinary floor — which is sound only because the corpus feeds them
// bit-identical inputs. A generated dodge scene would need this set to grow a
// third case with no number to put in it, which is the reason gen.go refuses to
// emit them.
var amplifying = map[paint.BlendMode]struct{}{
	paint.Overlay:   {},
	paint.SoftLight: {},
}

// illConditioned names the modes whose blend derivative is unbounded — 1/(1-cs)
// for ColorDodge, 1/cs for ColorBurn — so they amplify any difference in their
// inputs without bound.
//
// It is a SET, not a map of budgets. These modes once carried Budget(2)/Budget(3)
// here and in the corpus; Phase 13 retired those by pinning the rounding, and
// re-deriving a budget for them would be worse than no budget at all: an
// unbounded distribution has no worst case to measure, so any number fitted to a
// sample is a flake waiting for a longer run (Δ=3 at 3k generated seeds, Δ=5 at
// 25k, still climbing). The set's job is to keep gen.go honest — see
// TestGeneratorOmitsIllConditionedBlendModes — not to price a divergence.
//
// A stored spec containing one is therefore gated at the ordinary floor, which is
// where the corpus now holds them when their inputs are pinned. If such a spec
// ever fails, that is a signal to look, not a knob to turn.
var illConditioned = map[paint.BlendMode]bool{
	paint.ColorDodge: true,
	paint.ColorBurn:  true,
}

// amplifiedBlend is the one tolerance this package adds to the tree, and it is
// worth exactly one bit above the floor.
//
// Both backends composite into an 8-bit buffer per node — the CPU because
// image.RGBA is its framebuffer, the GPU because Phase 13 made raster.wgsl round
// per node to match. So a second blend node reads a backdrop already quantized,
// which the two may have rounded to values 1 LSB apart wherever antialiased
// coverage or a gradient parameter differs sub-LSB between f64 and f32. A blend's
// sensitivity to its backdrop is d(Co)/d(Cb) = αs·B'(cb) + (1-αs) — the backdrop
// alpha cancels — so a mode with B' > 1 (Overlay at 2(1-cs)) multiplies that
// 1 LSB instead of absorbing it.
//
// The amplification is real but mild and does not compound with depth, which is
// what made this a Budget rather than a retreat to perceptual mode. Measured over
// 3000 generated seeds: Δ≤1 at zero or one general node, Δ≤2 at two, three AND
// four — flat, because the bounded-gain modes have B' ≤ 2 and the αs in the gain
// formula keeps the per-node factor near 1. Still flat at Δ≤2 over 25000 seeds.
// Alpha never diverged at any depth, as it cannot: αo = αs + αb(1-αs) has gain
// (1-αs) ≤ 1.
//
// Phase 13 tried and failed to retire this one. Pinning the rounding DID retire
// the dodge/burn budgets and did remove an unbounded-with-depth divergence
// (Δ=10 at 64 stacked layers → Δ=0), but the residue here is seeded by f32-vs-f64
// evaluation of coverage and gradients, not by rounding, and no rounding rule
// removes that. Retiring this budget needs the two backends to compute in the
// same precision — which Phase 11 rejected on purpose, since matching the other
// backend's rounding rather than the true value is the worse trade.
// Renamed from amplifiedBlend in the Phase 15 review: stacking was never the
// cause, and the name kept the rule pointed at the wrong variable. One node is
// enough if it amplifies. See Tol.
var amplifiedBlend = parity.Budget(2,
	"an amplifying operation clears the rounding decision that its 1-LSB f32-vs-f64 input difference would otherwise vanish under: a blend mode with dB/dCb>1 (Overlay 2.0, SoftLight 4.0) or a non-SrcOver Porter-Duff operator, whose unpremultiply divides a premultiplied LSB by a backdrop alpha the operator itself can drive toward zero; Δ≤2 held flat over 531622 differential executions")

// clone copies the node slice so a caller may replace fields of one node without
// mutating the original. Fields holding slices (Pts, Stops, Dashes, Clips) are
// only ever REPLACED wholesale by the shrinker, never written through, so
// sharing their backing arrays is safe and keeps shrinking allocation-light.
func (s Spec) clone() Spec {
	out := s
	out.Nodes = append([]NodeSpec(nil), s.Nodes...)
	return out
}

// size orders specs by complexity so the shrinker can tell whether a round made
// progress. It counts what a reader of the failure report would have to hold in
// their head, not bytes.
func (s Spec) size() int {
	n := 0
	for _, nd := range s.Nodes {
		n += 1 + len(nd.Clips) + len(nd.Shape.Pts)
		if nd.Stroke != nil {
			n++
		}
		if nd.Paint.Kind != PaintSolid {
			n += 1 + len(nd.Paint.Stops)
		}
		if nd.Transform != geom.Identity() {
			n++
		}
		if nd.Op != paint.Normal {
			n++
		}
		if nd.Composite != paint.SrcOver {
			n++
		}
	}
	return n
}
