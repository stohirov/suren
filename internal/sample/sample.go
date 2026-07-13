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
