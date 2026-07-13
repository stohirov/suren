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
	ras *raster.Rasterizer
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
		if culled(n, view) {
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
		r.ras.FillPaint(r.Img, geo, n.Transform, sh, rule, clip)
	}
	return nil
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

func culled(n scene.Node, view geom.Rect) bool {
	b := n.Path.Transform(n.Transform).Bounds()
	pad := 1.0
	if n.Stroke != nil {
		pad += n.Stroke.Width / 2 * n.Transform.MaxScale()
	}
	b.Min = b.Min.Sub(geom.Pt(pad, pad))
	b.Max = b.Max.Add(geom.Pt(pad, pad))
	return b.Intersect(view).Empty()
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
