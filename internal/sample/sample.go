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
	solid(path.Rect(geom.RectXYWH(0, 0, W*0.7, H)), paint.RGBA(0.12, 0.16, 0.28, 0.85), paint.SrcOver)
	solid(path.Circle(geom.Pt(W*0.35, H*0.5), H*0.32), paint.FromRGBA8(240, 200, 60, 255), paint.SrcOver)
	solid(path.Circle(geom.Pt(W*0.62, H*0.5), H*0.36), paint.RGBA(0.20, 0.72, 0.90, 0.7), op)
	return s
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
