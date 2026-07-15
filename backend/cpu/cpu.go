package cpu

import (
	"image"
	"math"

	"github.com/stohirov/sukho/geom"
	"github.com/stohirov/sukho/paint"
	"github.com/stohirov/sukho/path"
	"github.com/stohirov/sukho/raster"
	"github.com/stohirov/sukho/scene"
)

type Renderer struct {
	Img *image.RGBA

	// Tiles restricts rendering to the flagged tiles, leaving every other pixel
	// of Img untouched. The pixels it does write are bit-identical to those of a
	// full render of the same scene — see raster.TileMask — which is what lets
	// the GPU backend patch inexact tiles with reference pixels (Phase 14). A
	// nil Tiles renders the whole image.
	//
	// It does not make the render proportionally cheaper: coverage still sweeps
	// from each path's left edge, so the saving is the blending and the nodes
	// that miss every flagged tile, not the rasterization.
	Tiles *raster.TileMask

	ras     *raster.Rasterizer
	clipRas *raster.Rasterizer
	mask    []float64
	tmp     []float64
}

func (r *Renderer) Render(s *scene.Scene) error {
	b := r.Img.Bounds()
	if r.ras == nil {
		r.ras = raster.NewRasterizer(b.Dx(), b.Dy())
	} else {
		r.ras.Resize(b.Dx(), b.Dy())
	}
	view := viewRect(b)
	for _, n := range s.Nodes {
		if culled(n, view) || r.tileCulled(n, b) {
			continue
		}
		geo := n.Path
		rule := fillRule(n.FillRule)
		if n.Stroke != nil {
			geo = strokeOutline(n)
			rule = raster.NonZero
		}
		var sh raster.Shader
		if col, ok := solidColor(n.Paint); ok {
			sh = raster.NewSolidShader(col)
		} else if g, ok := shader(n.Paint, n.Transform); ok {
			sh = g
		} else {
			continue
		}
		clip := b
		if c, ok := clipRect(n.Clip); ok {
			clip = c
		}
		mask := r.clipMask(n.Clips, b)
		r.ras.FillPaint(r.Img, geo, n.Transform, sh, rule, clip, raster.BlendMode(n.Op), mask, r.Tiles)
	}
	return nil
}

// tileCulled skips a node that cannot write to any flagged tile. Its bbox is
// padded and floor/ceil'd outward, so it never culls a node whose antialiased
// edge reaches a live tile.
func (r *Renderer) tileCulled(n scene.Node, b image.Rectangle) bool {
	if r.Tiles == nil {
		return false
	}
	nb := nodeBounds(n)
	return !r.Tiles.Overlaps(
		int(math.Floor(nb.Min.X))-b.Min.X, int(math.Floor(nb.Min.Y))-b.Min.Y,
		int(math.Ceil(nb.Max.X))-b.Min.X, int(math.Ceil(nb.Max.Y))-b.Min.Y,
	)
}

func (r *Renderer) clipMask(clips []scene.ClipPath, b image.Rectangle) []float64 {
	if len(clips) == 0 {
		return nil
	}
	w, h := b.Dx(), b.Dy()
	n := w * h
	if cap(r.mask) < n {
		r.mask = make([]float64, n)
		r.tmp = make([]float64, n)
	}
	r.mask = r.mask[:n]
	r.tmp = r.tmp[:n]
	for i := range r.mask {
		r.mask[i] = 1
	}
	if r.clipRas == nil {
		r.clipRas = raster.NewRasterizer(w, h)
	} else {
		r.clipRas.Resize(w, h)
	}
	shift := geom.Translate(float64(-b.Min.X), float64(-b.Min.Y))
	for _, cl := range clips {
		for i := range r.tmp {
			r.tmp[i] = 0
		}
		r.clipRas.Reset()
		r.clipRas.FillPath(cl.Path, path.DefaultTolerance, shift)
		r.clipRas.Sweep(fillRule(cl.Rule), func(x, y int, a float64) {
			r.tmp[y*w+x] = a
		})
		for i := range r.mask {
			r.mask[i] *= r.tmp[i]
		}
	}
	return r.mask
}

func strokeOutline(n scene.Node) path.Path {
	tol := path.DefaultTolerance
	if k := n.Transform.MaxScale(); k > 0 {
		tol /= k
	}
	src := n.Path
	if d, ok := n.Stroke.Dash(); ok {
		src = d.Apply(src, tol)
	}
	return n.Stroke.Stroker().Stroke(src, tol)
}

func Render(s *scene.Scene, pxW, pxH int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, pxW, pxH))
	(&Renderer{Img: img}).Render(s)
	return img
}

func viewRect(b image.Rectangle) geom.Rect {
	return geom.Rect{
		Min: geom.Pt(float64(b.Min.X), float64(b.Min.Y)),
		Max: geom.Pt(float64(b.Max.X), float64(b.Max.Y)),
	}
}

func clipRect(r *geom.Rect) (image.Rectangle, bool) {
	if r == nil {
		return image.Rectangle{}, false
	}
	return image.Rect(
		int(math.Floor(r.Min.X)), int(math.Floor(r.Min.Y)),
		int(math.Ceil(r.Max.X)), int(math.Ceil(r.Max.Y)),
	), true
}

// nodeBounds is the node's device-space extent, padded by a pixel (and by the
// stroke's half-width) so it covers every pixel the node's antialiased edge can
// reach.
func nodeBounds(n scene.Node) geom.Rect {
	b := n.Path.Transform(n.Transform).Bounds()
	pad := 1.0
	if n.Stroke != nil {
		pad += n.Stroke.Width / 2 * n.Transform.MaxScale()
	}
	b.Min = b.Min.Sub(geom.Pt(pad, pad))
	b.Max = b.Max.Add(geom.Pt(pad, pad))
	return b
}

func culled(n scene.Node, view geom.Rect) bool {
	return nodeBounds(n).Intersect(view).Empty()
}

func solidColor(p paint.Paint) (paint.Color, bool) {
	if s, ok := p.(paint.Solid); ok {
		return s.Color, true
	}
	return paint.Color{}, false
}

func fillRule(r paint.FillRule) raster.FillRule {
	if r == paint.EvenOdd {
		return raster.EvenOdd
	}
	return raster.NonZero
}
