package raster

import (
	"image"
	"image/color"

	"github.com/stohirov/sukho/geom"
	"github.com/stohirov/sukho/path"
)

func (r *Rasterizer) FillPath(p path.Path, tol float64, m geom.Matrix) {
	p.Flatten(tol, m, func(pts []geom.Point, closed bool) {
		for i := 0; i+1 < len(pts); i++ {
			r.Line(pts[i], pts[i+1])
		}
		r.Line(pts[len(pts)-1], pts[0])
	})
}

func (r *Rasterizer) Paint(dst *image.RGBA, c color.Color, rule FillRule) {
	cr, cg, cb, ca := c.RGBA()

	sr, sg, sb, sa := float64(cr)/257, float64(cg)/257, float64(cb)/257, float64(ca)/257
	b := dst.Bounds()
	r.Sweep(rule, func(x, y int, cov float64) {
		px, py := b.Min.X+x, b.Min.Y+y
		if px >= b.Max.X || py >= b.Max.Y {
			return
		}

		fa := sa * cov
		inv := 1 - fa/255
		i := dst.PixOffset(px, py)
		s := dst.Pix[i : i+4 : i+4]
		s[0] = clamp8(sr*cov + float64(s[0])*inv)
		s[1] = clamp8(sg*cov + float64(s[1])*inv)
		s[2] = clamp8(sb*cov + float64(s[2])*inv)
		s[3] = clamp8(fa + float64(s[3])*inv)
	})
}

func clamp8(v float64) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(v + 0.5)
}

func Fill(dst *image.RGBA, p path.Path, m geom.Matrix, c color.Color, rule FillRule) {
	b := dst.Bounds()
	r := NewRasterizer(b.Dx(), b.Dy())

	shift := geom.Translate(float64(-b.Min.X), float64(-b.Min.Y)).Mul(m)
	r.FillPath(p, path.DefaultTolerance, shift)
	r.Paint(dst, c, rule)
}
