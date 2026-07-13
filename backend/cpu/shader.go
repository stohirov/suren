package cpu

import (
	"github.com/stohirov/sukho/geom"
	"github.com/stohirov/sukho/paint"
	"github.com/stohirov/sukho/raster"
)

type gradShader struct {
	minv  geom.Matrix
	ok    bool
	stops []paint.Stop
	param func(q geom.Point) float64
}

func (g gradShader) RGBA(px, py int) (r, gg, b, a float64) {
	if !g.ok {
		return 0, 0, 0, 0
	}
	q := g.minv.Apply(geom.Pt(float64(px)+0.5, float64(py)+0.5))
	c := paint.Interp(g.stops, g.param(q))
	cr, cgg, cb, ca := c.RGBA()
	return float64(cr) / 257, float64(cgg) / 257, float64(cb) / 257, float64(ca) / 257
}

func shader(p paint.Paint, m geom.Matrix) (raster.Shader, bool) {
	minv, ok := m.Invert()
	switch g := p.(type) {
	case paint.LinearGradient:
		d := g.P1.Sub(g.P0)
		len2 := d.Dot(d)
		p0 := g.P0
		return gradShader{minv, ok, g.Stops, func(q geom.Point) float64 {
			if len2 <= 0 {
				return 0
			}
			return q.Sub(p0).Dot(d) / len2
		}}, true
	case paint.RadialGradient:
		center, radius := g.Center, g.Radius
		return gradShader{minv, ok, g.Stops, func(q geom.Point) float64 {
			if radius <= 0 {
				return 0
			}
			return q.Sub(center).Len() / radius
		}}, true
	default:
		return nil, false
	}
}
