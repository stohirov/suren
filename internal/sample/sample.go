package sample

import (
	"math"

	"github.com/stohirov/sukho/geom"
	"github.com/stohirov/sukho/paint"
	"github.com/stohirov/sukho/path"
	"github.com/stohirov/sukho/render"
	"github.com/stohirov/sukho/scene"
)

const (
	W = 240
	H = 180
)

func Scene() *scene.Scene {
	c := render.NewCanvas()

	c.FillColor(path.Rect(geom.RectXYWH(0, 0, W, H)), paint.FromRGBA8(250, 249, 246, 255))

	donut := path.Rect(geom.RectXYWH(20, 30, 70, 70))
	appendPath(&donut, path.Rect(geom.RectXYWH(40, 50, 30, 30)))
	c.Fill(donut, paint.Solid{Color: paint.FromRGBA8(40, 120, 220, 255)}, paint.EvenOdd)

	c.Save()
	c.Translate(160, 70)
	c.Rotate(math.Pi / 6)
	c.FillColor(path.Rect(geom.RectXYWH(-35, -35, 70, 70)), paint.RGBA(214/255.0, 69/255.0, 65/255.0, 0.6))
	c.Restore()

	c.StrokeColor(path.Circle(geom.Pt(120, 130), 35), paint.FromRGBA8(230, 160, 40, 255), paint.Stroke{
		Width:  8,
		Join:   path.RoundJoin,
		Dashes: []float64{18, 10},
	})

	return c.Scene()
}

func GradientScene() *scene.Scene {
	c := render.NewCanvas()

	c.Fill(path.Rect(geom.RectXYWH(0, 0, W, H)), paint.LinearGradient{
		P0: geom.Pt(0, 0),
		P1: geom.Pt(W, H),
		Stops: []paint.Stop{
			{Offset: 0, Color: paint.FromRGBA8(40, 44, 52, 255)},
			{Offset: 1, Color: paint.FromRGBA8(40, 120, 220, 255)},
		},
	}, paint.NonZero)

	c.Fill(path.Circle(geom.Pt(90, 90), 60), paint.RadialGradient{
		Center: geom.Pt(75, 75),
		Radius: 70,
		Stops: []paint.Stop{
			{Offset: 0, Color: paint.RGBA(1, 1, 1, 1)},
			{Offset: 0.6, Color: paint.FromRGBA8(230, 160, 40, 255)},
			{Offset: 1, Color: paint.RGBA(230/255.0, 69/255.0, 65/255.0, 0)},
		},
	}, paint.NonZero)

	c.Stroke(path.Circle(geom.Pt(175, 120), 40), paint.LinearGradient{
		P0: geom.Pt(135, 120),
		P1: geom.Pt(215, 120),
		Stops: []paint.Stop{
			{Offset: 0, Color: paint.RGB(1, 1, 1)},
			{Offset: 1, Color: paint.FromRGBA8(214, 69, 65, 255)},
		},
	}, paint.Stroke{Width: 10, Join: path.RoundJoin})

	return c.Scene()
}

// ConicScene sweeps a conic gradient around a filled disc, a rotated-and-scaled
// rect (so the node's inverse transform is exercised, not just the identity), and
// a stroke.
//
// seam picks whether the first and last stops MATCH. That is the whole parameter
// space worth having, because it decides whether the paint is continuous:
//
//   - closed (seam=false) wraps colour-to-colour, so the paint is continuous
//     everywhere and the f32-vs-f64 difference in the atan2 parameter stays
//     sub-LSB, exactly as it does for linear and radial. This is the case the
//     Δ≤1 floor covers on its merits.
//   - seam=true jumps from the last stop to the first across the ray at Angle.
//     The paint is discontinuous there, so a pixel centre landing within f32's
//     ~1e-7 of that ray can take opposite branches on the two backends and
//     diverge by the full colour distance. See paint.ConicGradient.
//
// The seam variant renders at the floor anyway, and that is a fact about these
// centres on this driver rather than a property — no pixel centre here falls that
// close to the ray. It is in the corpus to hold the arithmetic that makes the
// seam land in the same PLACE on both backends, which is a real thing to
// regress; it is not evidence that the seam is safe in general.
func ConicScene(seam bool) *scene.Scene {
	c := render.NewCanvas()
	first := paint.FromRGBA8(40, 44, 52, 255)
	last := first
	if seam {
		last = paint.FromRGBA8(240, 200, 60, 255)
	}
	stops := []paint.Stop{
		{Offset: 0, Color: first},
		{Offset: 0.35, Color: paint.FromRGBA8(230, 120, 60, 255)},
		{Offset: 0.7, Color: paint.RGBA(0.20, 0.72, 0.90, 0.75)},
		{Offset: 1, Color: last},
	}
	c.FillColor(path.Rect(geom.RectXYWH(0, 0, W, H)), paint.FromRGBA8(250, 249, 246, 255))

	c.Fill(path.Circle(geom.Pt(70, 90), 55), paint.ConicGradient{
		Center: geom.Pt(70, 90),
		Angle:  0.4,
		Stops:  stops,
	}, paint.NonZero)

	c.Save()
	c.Translate(170, 60)
	c.Rotate(math.Pi / 5)
	c.Scale(1.3, 0.8)
	c.Fill(path.Rect(geom.RectXYWH(-30, -30, 60, 60)), paint.ConicGradient{
		Center: geom.Pt(0, 0),
		Angle:  -1.2,
		Stops:  stops,
	}, paint.NonZero)
	c.Restore()

	c.Stroke(path.Circle(geom.Pt(175, 135), 32), paint.ConicGradient{
		Center: geom.Pt(175, 135),
		Angle:  2.5,
		Stops:  stops,
	}, paint.Stroke{Width: 9, Join: path.RoundJoin})

	return c.Scene()
}

func ManyNodes(w, h, cols, rows int) *scene.Scene {
	c := render.NewCanvas()
	c.FillColor(path.Rect(geom.RectXYWH(0, 0, float64(w), float64(h))), paint.FromRGBA8(20, 22, 28, 255))
	cw, ch := float64(w)/float64(cols), float64(h)/float64(rows)
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			cx, cy := (float64(x)+0.5)*cw, (float64(y)+0.5)*ch
			col := paint.FromRGBA8(uint8(x*6), uint8(y*10), 200, 255)
			c.FillColor(path.Circle(geom.Pt(cx, cy), ch*0.35), col)
		}
	}
	return c.Scene()
}

func ManySegments(w, h, spikes int) *scene.Scene {
	c := render.NewCanvas()
	c.FillColor(path.Rect(geom.RectXYWH(0, 0, float64(w), float64(h))), paint.FromRGBA8(20, 22, 28, 255))
	cx, cy := float64(w)/2, float64(h)/2
	outer := math.Min(float64(w), float64(h)) * 0.48
	inner := outer * 0.55
	var p path.Path
	n := spikes * 2
	for i := 0; i < n; i++ {
		ang := 2 * math.Pi * float64(i) / float64(n)
		rad := outer
		if i%2 == 1 {
			rad = inner
		}
		pt := geom.Pt(cx+rad*math.Cos(ang), cy+rad*math.Sin(ang))
		if i == 0 {
			p.MoveTo(pt)
		} else {
			p.LineTo(pt)
		}
	}
	p.Close()
	c.FillColor(p, paint.FromRGBA8(220, 120, 60, 255))
	return c.Scene()
}

func ClipRectScene() *scene.Scene {
	c := render.NewCanvas()
	c.FillColor(path.Rect(geom.RectXYWH(0, 0, 96, 96)), paint.FromRGBA8(30, 30, 40, 255))
	c.ClipRect(geom.RectXYWH(13, 13, 61, 47))
	c.FillColor(path.Circle(geom.Pt(48, 48), 40), paint.FromRGBA8(220, 80, 60, 255))
	return c.Scene()
}

func ClipPathScene(nested bool) *scene.Scene {
	c := render.NewCanvas()
	c.FillColor(path.Rect(geom.RectXYWH(0, 0, W, H)), paint.FromRGBA8(28, 30, 38, 255))
	c.Save()
	c.ClipPath(path.Circle(geom.Pt(W*0.42, H*0.5), H*0.42), paint.NonZero)
	if nested {
		c.ClipPath(path.Circle(geom.Pt(W*0.6, H*0.5), H*0.42), paint.NonZero)
	}
	c.FillColor(path.Rect(geom.RectXYWH(0, 0, W, H)), paint.FromRGBA8(230, 120, 60, 255))
	c.Fill(path.Circle(geom.Pt(W*0.5, H*0.5), H*0.62), paint.Solid{Color: paint.RGBA(0.2, 0.72, 0.9, 0.8)}, paint.NonZero)
	c.StrokeColor(path.Rect(geom.RectXYWH(W*0.2, H*0.2, W*0.55, H*0.55)), paint.FromRGBA8(255, 240, 180, 255), paint.Stroke{Width: 10})
	c.Restore()
	c.FillColor(path.Circle(geom.Pt(W*0.85, H*0.2), 12), paint.RGB(1, 1, 1))
	return c.Scene()
}

func BlendScene(op paint.BlendMode) *scene.Scene {
	s := &scene.Scene{}
	solid := func(p path.Path, c paint.Color, blend paint.BlendMode) {
		s.Add(scene.Node{Path: p, Transform: geom.Identity(), Paint: paint.Solid{Color: c}, Op: blend, FillRule: paint.NonZero})
	}
	solid(path.Rect(geom.RectXYWH(0, 0, W*0.7, H)), paint.RGBA(0.12, 0.16, 0.28, 0.85), paint.Normal)
	solid(path.Circle(geom.Pt(W*0.35, H*0.5), H*0.32), paint.FromRGBA8(240, 200, 60, 255), paint.Normal)
	solid(path.Circle(geom.Pt(W*0.62, H*0.5), H*0.36), paint.RGBA(0.20, 0.72, 0.90, 0.7), op)
	return s
}

// CompositeScene exercises one Porter-Duff operator against every backdrop
// regime it can distinguish. The three coefficient tables that differ only in
// how they treat αb — SrcIn vs SrcOut, SrcAtop vs Src — are indistinguishable
// over a single backdrop, so the frame is banded into thirds: opaque (αb=1),
// translucent (αb=0.5), and empty (αb=0). An operator that got Fa or Fb wrong
// for one regime would still match over the other two.
//
// The source is an antialiased circle, deliberately: its edge is where coverage
// is strictly between 0 and 1, which is the only place the coverage lerp in
// raster.porterDuff is observable. A pixel-aligned source would render every
// operator correctly even with coverage folded into αs, and would gate nothing
// this phase got wrong.
//
// The source is also translucent (αs=0.75), so DstIn/DstOut/DstAtop — whose
// coefficients read αs and ignore the source COLOR entirely — cannot pass by
// accident with an opaque source that makes their Fb collapse to 0 or 1.
// mode is the other axis, and passing anything but Normal is what makes the two
// axes CROSS. That matters because the GPU packs both into one flags word
// (encode.go packFlags): a shift or mask off by one bit would let an operator
// leak into the blend mode's bits, which no scene that leaves one axis at its
// default can see — the leaked value would land on the identity.
func CompositeScene(op paint.CompositeOp, mode paint.BlendMode) *scene.Scene {
	c := render.NewCanvas()
	third := float64(W) / 3
	c.FillColor(path.Rect(geom.RectXYWH(0, 0, third, H)), paint.FromRGBA8(230, 120, 60, 255))
	c.Fill(path.Rect(geom.RectXYWH(third, 0, third, H)),
		paint.Solid{Color: paint.RGBA(0.20, 0.72, 0.90, 0.5)}, paint.NonZero)
	// The final third is left untouched: αb=0, where DstOver and SrcOut show the
	// source and SrcIn and DstIn must show nothing.

	c.SetComposite(op)
	c.SetBlend(mode)
	c.Fill(path.Circle(geom.Pt(W*0.5, H*0.5), H*0.38),
		paint.Solid{Color: paint.RGBA(0.95, 0.85, 0.20, 0.75)}, paint.NonZero)
	c.SetComposite(paint.SrcOver)
	c.SetBlend(paint.Normal)
	return c.Scene()
}

// BlendStack layers n translucent quads with the same blend op.
//
// It exists to hold Phase 13's per-node rounding in place. The CPU reference
// composites into an 8-bit image.RGBA, so it re-quantizes after every node; a GPU
// that instead accumulated its tile in f32 and rounded once at the end would be
// computing a different function, and the gap grows with depth — measured Δ=10 at
// 64 Overlay layers before raster.wgsl rounded per node, Δ=0 after.
//
// The geometry is deliberately pixel-aligned and the paint solid, so coverage is
// exactly 0 or 1 and no antialiasing or gradient can seed a sub-LSB difference.
// That is what lets this scene be gated at Δ=0 and makes it a clean probe of the
// rounding alone: any delta here is a rounding divergence, not a coverage one.
// Depth is the whole point — at four layers the effect is a single LSB and the
// generated scenes in internal/parity/fuzz cannot see it.
func BlendStack(n int, op paint.BlendMode) *scene.Scene {
	c := render.NewCanvas()
	c.FillColor(path.Rect(geom.RectXYWH(0, 0, 96, 96)), paint.FromRGBA8(40, 90, 160, 255))
	for i := range n {
		c.SetBlend(op)
		x := float64(4 + i%8)
		y := float64(4 + i%5)
		col := paint.FromRGBA8(uint8(30+7*i%200), uint8(200-5*i%180), uint8(60+11*i%190), 90)
		c.FillColor(path.Rect(geom.RectXYWH(x, y, 80, 80)), col)
	}
	return c.Scene()
}

// FallbackW/H bound FallbackScene. It is 128x96 = 8x6 tiles at the GPU's tile
// size of 16, small enough that a test can enumerate the flagged tiles by hand.
const (
	FallbackW = 128
	FallbackH = 96
)

// FallbackScene probes the per-tile CPU fallback (backend/gpu, Phase 14): every
// node but one is a pixel-aligned solid, which the GPU renders at Δ=0 for the
// reason BlendStack documents, and the one exception is an antialiased circle
// filled with a radial gradient and marked for fallback.
//
// That node is inexact on GPU by measurement, not by assumption — it renders at
// Δ=1 with the mark off (see the fallback-gradient-off corpus entry). Both of
// its ingredients are things Phase 13 named as living at the floor and not
// removable by any rounding rule: the gradient parameter and the coverage sweep
// are evaluated in f32 against the reference's f64, so the same real value can
// land on either side of a .5 boundary. An earlier draft of this scene used a
// pixel-aligned rect with a 2-stop linear ramp and rendered at Δ=0 on Metal with
// the fallback off, which would have made the gate below vacuous; the radial
// gradient's division and sqrt, and the circle's AA edges, are what make the
// divergence reliable.
//
// So the pair is a two-sided measurement. With the mark ON the whole frame is
// Δ=0, which can only hold if the CPU patch is bit-exact AND lands on exactly
// the right tiles: a patch that missed a flagged tile would leave the Δ=1 there,
// and one that strayed outside would have to reproduce the GPU's pixels exactly
// to escape notice. With it OFF the same scene sits at Δ=1. Neither entry proves
// much alone; the difference between them is the result.
//
// The circle deliberately covers only part of the frame (12 of 48 tiles). A
// fallback that flagged every tile would be indistinguishable from "render the
// whole thing on the CPU", which proves nothing about the tile handshake.
func FallbackScene(fallback bool) *scene.Scene {
	c := render.NewCanvas()
	c.FillColor(path.Rect(geom.RectXYWH(0, 0, FallbackW, FallbackH)), paint.FromRGBA8(20, 22, 28, 255))
	c.FillColor(path.Rect(geom.RectXYWH(0, 0, 32, 32)), paint.FromRGBA8(40, 90, 160, 255))
	c.FillColor(path.Rect(geom.RectXYWH(96, 64, 32, 32)), paint.FromRGBA8(160, 90, 40, 255))

	c.SetFallback(fallback)
	c.Fill(path.Circle(geom.Pt(72, 48), 22), paint.RadialGradient{
		Center: geom.Pt(66, 42),
		Radius: 30,
		Stops: []paint.Stop{
			{Offset: 0, Color: paint.RGBA(1, 1, 1, 1)},
			{Offset: 0.55, Color: paint.FromRGBA8(230, 160, 40, 255)},
			{Offset: 1, Color: paint.RGBA(230/255.0, 69/255.0, 65/255.0, 0.35)},
		},
	}, paint.NonZero)
	c.SetFallback(false)

	return c.Scene()
}

// FallbackTileRect is the half-open tile rect {tx0, ty0, tx1, ty1} that
// FallbackScene's marked node covers: the circle spans x=50..94, y=26..70, which
// at tile size 16 is tiles [3,6)x[1,5) — 12 of the frame's 48. Tests assert the
// encoder flags exactly these, so a change to the tiling or to the scene has to
// be acknowledged here rather than silently shrinking what falls back.
var FallbackTileRect = [4]int{3, 1, 6, 5}

// FallbackBand is FallbackScene's shape without its exactness claim: a solid
// background under a gradient band covering the top frac of the frame, marked
// for fallback or not. It exists to sweep fallback coverage from 0 to 1 in a
// benchmark, which is the only way "how much does falling back cost" has an
// answer rather than an anecdote — the cost is per flagged tile, so a single
// coverage figure would say nothing about the slope.
func FallbackBand(w, h int, frac float64, fallback bool) *scene.Scene {
	c := render.NewCanvas()
	c.FillColor(path.Rect(geom.RectXYWH(0, 0, float64(w), float64(h))), paint.FromRGBA8(20, 22, 28, 255))
	if frac <= 0 {
		return c.Scene()
	}
	band := math.Min(float64(h)*frac, float64(h))
	c.SetFallback(fallback)
	c.Fill(path.Rect(geom.RectXYWH(0, 0, float64(w), band)), paint.LinearGradient{
		P0: geom.Pt(0, 0),
		P1: geom.Pt(float64(w), band),
		Stops: []paint.Stop{
			{Offset: 0, Color: paint.FromRGBA8(230, 160, 40, 255)},
			{Offset: 1, Color: paint.FromRGBA8(40, 120, 220, 255)},
		},
	}, paint.NonZero)
	c.SetFallback(false)
	return c.Scene()
}

func appendPath(dst *path.Path, src path.Path) {
	it := src.Iter()
	for {
		v, pts, ok := it.Next()
		if !ok {
			return
		}
		switch v {
		case path.MoveTo:
			dst.MoveTo(pts[0])
		case path.LineTo:
			dst.LineTo(pts[0])
		case path.QuadTo:
			dst.QuadTo(pts[0], pts[1])
		case path.CubicTo:
			dst.CubicTo(pts[0], pts[1], pts[2])
		case path.Close:
			dst.Close()
		}
	}
}
