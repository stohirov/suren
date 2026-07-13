package png

import (
	"image"
	stdpng "image/png"
	"io"

	"github.com/stohirov/sukho/geom"
	"github.com/stohirov/sukho/paint"
	"github.com/stohirov/sukho/path"
	"github.com/stohirov/sukho/raster"
	"github.com/stohirov/sukho/scene"
)

type Renderer struct {
	Img *image.RGBA
}

func (r *Renderer) Render(s *scene.Scene) error {
	view := viewRect(r.Img.Bounds())
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
		if col, ok := solidColor(n.Paint); ok {
			raster.Fill(r.Img, geo, n.Transform, col, rule)
			continue
		}
		if sh, ok := shader(n.Paint, n.Transform); ok {
			raster.FillShader(r.Img, geo, n.Transform, sh, rule)
		}
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

func Encode(w io.Writer, s *scene.Scene, pxW, pxH int) error {
	return stdpng.Encode(w, Render(s, pxW, pxH))
}

func viewRect(b image.Rectangle) geom.Rect {
	return geom.Rect{
		Min: geom.Pt(float64(b.Min.X), float64(b.Min.Y)),
		Max: geom.Pt(float64(b.Max.X), float64(b.Max.Y)),
	}
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
