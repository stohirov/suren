package png

import (
	"image"
	stdpng "image/png"
	"io"

	"github.com/stohirov/sukho/paint"
	"github.com/stohirov/sukho/raster"
	"github.com/stohirov/sukho/scene"
)

type Renderer struct {
	Img *image.RGBA
}

func (r *Renderer) Render(s *scene.Scene) error {
	for _, n := range s.Nodes {
		col, ok := solidColor(n.Paint)
		if !ok {
			continue
		}
		if n.Stroke != nil {
			if d, dashed := n.Stroke.Dash(); dashed {
				raster.StrokeDashed(r.Img, n.Path, n.Transform, n.Stroke.Stroker(), d, col)
			} else {
				raster.Stroke(r.Img, n.Path, n.Transform, n.Stroke.Stroker(), col)
			}
			continue
		}
		raster.Fill(r.Img, n.Path, n.Transform, col, fillRule(n.FillRule))
	}
	return nil
}

func Render(s *scene.Scene, pxW, pxH int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, pxW, pxH))
	(&Renderer{Img: img}).Render(s)
	return img
}

func Encode(w io.Writer, s *scene.Scene, pxW, pxH int) error {
	return stdpng.Encode(w, Render(s, pxW, pxH))
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
