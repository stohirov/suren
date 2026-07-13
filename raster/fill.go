package raster

import (
	"image"
	"image/color"
	"math"

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

type Shader interface {
	RGBA(px, py int) (r, g, b, a float64)
}

type solidShader struct{ r, g, b, a float64 }

func NewSolidShader(c color.Color) Shader {
	cr, cg, cb, ca := c.RGBA()
	return solidShader{float64(cr) / 257, float64(cg) / 257, float64(cb) / 257, float64(ca) / 257}
}

func (s solidShader) RGBA(int, int) (r, g, b, a float64) { return s.r, s.g, s.b, s.a }

func (r *Rasterizer) Paint(dst *image.RGBA, c color.Color, rule FillRule) {
	r.PaintShader(dst, NewSolidShader(c), rule)
}

func (r *Rasterizer) PaintShader(dst *image.RGBA, sh Shader, rule FillRule) {
	r.paintShaderClip(dst, sh, rule, dst.Bounds())
}

func (r *Rasterizer) paintShaderClip(dst *image.RGBA, sh Shader, rule FillRule, clip image.Rectangle) {
	b := dst.Bounds()
	clip = clip.Intersect(b)
	r.Sweep(rule, func(x, y int, cov float64) {
		px, py := b.Min.X+x, b.Min.Y+y
		if !(image.Point{X: px, Y: py}).In(clip) {
			return
		}
		blend(dst, px, py, sh, cov)
	})
}

func blend(dst *image.RGBA, px, py int, sh Shader, cov float64) {
	sr, sg, sb, sa := sh.RGBA(px, py)
	fa := sa * cov
	inv := 1 - fa/255
	i := dst.PixOffset(px, py)
	s := dst.Pix[i : i+4 : i+4]
	s[0] = clamp8(sr*cov + float64(s[0])*inv)
	s[1] = clamp8(sg*cov + float64(s[1])*inv)
	s[2] = clamp8(sb*cov + float64(s[2])*inv)
	s[3] = clamp8(fa + float64(s[3])*inv)
}

func (r *Rasterizer) FillPaint(dst *image.RGBA, p path.Path, m geom.Matrix, sh Shader, rule FillRule, clip image.Rectangle) {
	if r.w == 0 || r.h == 0 {
		return
	}
	b := dst.Bounds()
	shift := geom.Translate(float64(-b.Min.X), float64(-b.Min.Y)).Mul(m)

	pb := p.TransformedBounds(shift)
	ax0 := clampInt(int(math.Floor(pb.Min.X)), 0, r.w)
	ax1 := clampInt(int(math.Floor(pb.Max.X))+1, 0, r.w)
	ay0 := clampInt(int(math.Floor(pb.Min.Y)), 0, r.h)
	ay1 := clampInt(int(math.Ceil(pb.Max.Y)), 0, r.h)
	if ax0 >= ax1 || ay0 >= ay1 {
		return
	}

	r.FillPath(p, path.DefaultTolerance, shift)

	clip = clip.Intersect(b)
	px0 := max(ax0, clip.Min.X-b.Min.X)
	px1 := min(ax1, clip.Max.X-b.Min.X)
	py0 := max(ay0, clip.Min.Y-b.Min.Y)
	py1 := min(ay1, clip.Max.Y-b.Min.Y)

	for y := py0; y < py1; y++ {
		row := y * r.w
		acc := 0.0
		for x := ax0; x < px1; x++ {
			acc += r.cover[row+x]
			if x < px0 {
				continue
			}
			alpha := coverage(acc-r.area[row+x]/2, rule)
			if r.Binary {
				if alpha >= 0.5 {
					alpha = 1
				} else {
					alpha = 0
				}
			}
			if alpha > 0 {
				blend(dst, b.Min.X+x, b.Min.Y+y, sh, alpha)
			}
		}
	}

	r.resetRegion(ax0, ax1, ay0, ay1)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
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
	fillShader(dst, p, m, NewSolidShader(c), rule, false, dst.Bounds())
}

func FillBinary(dst *image.RGBA, p path.Path, m geom.Matrix, c color.Color, rule FillRule) {
	fillShader(dst, p, m, NewSolidShader(c), rule, true, dst.Bounds())
}

func FillShader(dst *image.RGBA, p path.Path, m geom.Matrix, sh Shader, rule FillRule) {
	fillShader(dst, p, m, sh, rule, false, dst.Bounds())
}

func FillClip(dst *image.RGBA, p path.Path, m geom.Matrix, c color.Color, rule FillRule, clip image.Rectangle) {
	fillShader(dst, p, m, NewSolidShader(c), rule, false, clip)
}

func FillShaderClip(dst *image.RGBA, p path.Path, m geom.Matrix, sh Shader, rule FillRule, clip image.Rectangle) {
	fillShader(dst, p, m, sh, rule, false, clip)
}

func fillShader(dst *image.RGBA, p path.Path, m geom.Matrix, sh Shader, rule FillRule, binary bool, clip image.Rectangle) {
	b := dst.Bounds()
	r := NewRasterizer(b.Dx(), b.Dy())
	r.Binary = binary

	shift := geom.Translate(float64(-b.Min.X), float64(-b.Min.Y)).Mul(m)
	r.FillPath(p, path.DefaultTolerance, shift)
	r.paintShaderClip(dst, sh, rule, clip)
}
