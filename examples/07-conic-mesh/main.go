// Command 07-conic-mesh renders the two paints that carry their own geometry:
// a conic (angular) gradient and a Gouraud triangle mesh.
//
// Both are exact — CPU and GPU agree to Δ=1, the same 8-bit quantization floor
// that linear and radial sit at — but only when used the way this file uses
// them. Each has one usage rule that is a correctness requirement rather than a
// style preference, and each is called out below where it applies.
//
// Unlike 05-scene and 06-gradient, this example writes PNG only, and that is
// deliberate: the SVG backend silently drops both of these paints (SVG 1.1/2
// have no conic-gradient element or mesh primitive), so an SVG here would be a
// file containing the background and nothing else. See the SVG conformance
// audit in docs/roadmap.md.
package main

import (
	"log"
	"math"
	"os"

	"github.com/stohirov/suren/backend/png"
	"github.com/stohirov/suren/geom"
	"github.com/stohirov/suren/paint"
	"github.com/stohirov/suren/path"
	"github.com/stohirov/suren/render"
)

const (
	w = 640
	h = 400
)

// wheelStops sweeps the spectrum once and RETURNS TO ITS FIRST COLOUR.
//
// That last stop is the rule. A conic's parameter wraps: crossing the ray at
// Angle takes t from just under 1 to just over 0. Where the first and last
// stops differ, the colour jumps between them, so the paint is DISCONTINUOUS
// along that ray — and a discontinuity amplifies the sub-LSB difference between
// the CPU's f64 parameter and the GPU's f32 one without bound. Unlike a
// division-based blend there is no derivative to bound, because the magnitude
// is set by the stop colours themselves, so no tolerance can own it: a pixel
// centre landing within f32's reach of the seam is measurably Δ=255 with both
// backends individually correct.
//
// Closing the loop makes the paint continuous everywhere and the seam vanishes.
// An open seam is not forbidden — it is a legitimate look — but it is the one
// place this renderer cannot promise the two backends agree.
func wheelStops() []paint.Stop {
	first := paint.FromRGBA8(232, 72, 92, 255)
	return []paint.Stop{
		{Offset: 0, Color: first},
		{Offset: 1.0 / 6, Color: paint.FromRGBA8(233, 160, 44, 255)},
		{Offset: 2.0 / 6, Color: paint.FromRGBA8(206, 220, 62, 255)},
		{Offset: 3.0 / 6, Color: paint.FromRGBA8(76, 214, 142, 255)},
		{Offset: 4.0 / 6, Color: paint.FromRGBA8(72, 196, 232, 255)},
		{Offset: 5.0 / 6, Color: paint.FromRGBA8(142, 112, 230, 255)},
		{Offset: 1, Color: first},
	}
}

// meshGrid tessellates r into a cols x rows quad grid and colours every grid
// vertex from f(u, v). paint.MeshGrid fixes the triangulation order and shares
// vertices between neighbouring quads, so adjacent triangles agree along a
// shared edge by construction.
func meshGrid(r geom.Rect, cols, rows int, f func(u, v float64) paint.Color) paint.MeshGradient {
	colors := make([]paint.Color, 0, (cols+1)*(rows+1))
	for j := range rows + 1 {
		for i := range cols + 1 {
			colors = append(colors, f(float64(i)/float64(cols), float64(j)/float64(rows)))
		}
	}
	return paint.MeshGrid(r, cols, rows, colors)
}

func build() *render.Canvas {
	c := render.NewCanvas()

	c.Fill(path.Rect(geom.RectXYWH(0, 0, w, h)), paint.LinearGradient{
		P0: geom.Pt(0, 0),
		P1: geom.Pt(0, h),
		Stops: []paint.Stop{
			{Offset: 0, Color: paint.FromRGBA8(20, 22, 28, 255)},
			{Offset: 1, Color: paint.FromRGBA8(34, 38, 52, 255)},
		},
	}, paint.NonZero)

	// Conic: the parameter is atan2 of the pixel about Center, so the disc reads
	// as a colour wheel. Angle rotates where t=0 sits; increasing atan2 sweeps
	// CLOCKWISE on screen, because device y grows downward.
	c.Fill(path.Circle(geom.Pt(170, 200), 118), paint.ConicGradient{
		Center: geom.Pt(170, 200),
		Angle:  -math.Pi / 2,
		Stops:  wheelStops(),
	}, paint.NonZero)

	// Mesh: four-corner Gouraud interpolation across a 3x3 quad grid.
	//
	// The mesh rect is INFLATED 20px past the rounded rect it fills. That is the
	// second rule. A mesh is transparent outside its triangles, so its outer
	// silhouette is a colour discontinuity for exactly the reason an open conic
	// seam is — and unlike the conic there is no closing trick, since a mesh has
	// to end somewhere. Extending it past the filled path means no boundary
	// pixel is ever sampled, so the shape's own edge (ordinary analytic AA) is
	// what bounds the paint.
	const bleed = 20
	shape := geom.RectXYWH(370, 90, 210, 220)
	field := geom.RectXYWH(
		shape.Min.X-bleed, shape.Min.Y-bleed,
		shape.Width()+2*bleed, shape.Height()+2*bleed,
	)
	c.Fill(path.RoundedRect(shape, 28, 28),
		meshGrid(field, 3, 3, func(u, v float64) paint.Color {
			return paint.RGBA(0.30+0.65*u, 0.85-0.45*v, 0.55+0.40*v, 1)
		}), paint.NonZero)

	// Paints are not fill-only: the same conic drives a stroke, and the stroke is
	// expanded to a fill outline before it ever reaches a rasterizer.
	c.Stroke(path.Circle(geom.Pt(475, 200), 68), paint.ConicGradient{
		Center: geom.Pt(475, 200),
		Angle:  0.6,
		Stops:  wheelStops(),
	}, paint.Stroke{Width: 12, Join: path.RoundJoin})

	return c
}

func main() {
	c := build()

	f, err := os.Create("07-conic-mesh.png")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, c.Scene(), w, h); err != nil {
		log.Fatal(err)
	}

	log.Println("wrote 07-conic-mesh.png")
}
