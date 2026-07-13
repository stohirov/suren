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
		blend(dst, px, py, sh, cov, SrcOver)
	})
}

func blend(dst *image.RGBA, px, py int, sh Shader, cov float64, mode BlendMode) {
	sr, sg, sb, sa := sh.RGBA(px, py)
	fa := sa * cov
	if fa <= 0 {
		return
	}
	i := dst.PixOffset(px, py)
	s := dst.Pix[i : i+4 : i+4]
	if mode == SrcOver {
		inv := 1 - fa/255
		s[0] = clamp8(sr*cov + float64(s[0])*inv)
		s[1] = clamp8(sg*cov + float64(s[1])*inv)
		s[2] = clamp8(sb*cov + float64(s[2])*inv)
		s[3] = clamp8(fa + float64(s[3])*inv)
		return
	}

	as := fa / 255
	ab := float64(s[3]) / 255
	csr, csg, csb := sr/sa, sg/sa, sb/sa
	var cbr, cbg, cbb float64
	if s[3] > 0 {
		cbr = float64(s[0]) / float64(s[3])
		cbg = float64(s[1]) / float64(s[3])
		cbb = float64(s[2]) / float64(s[3])
	}
	w1, w2, w3 := as*(1-ab), as*ab, (1-as)*ab
	s[0] = clamp8((w1*csr + w2*blendCh(mode, cbr, csr) + w3*cbr) * 255)
	s[1] = clamp8((w1*csg + w2*blendCh(mode, cbg, csg) + w3*cbg) * 255)
	s[2] = clamp8((w1*csb + w2*blendCh(mode, cbb, csb) + w3*cbb) * 255)
	s[3] = clamp8((as + ab*(1-as)) * 255)
}

func blendCh(mode BlendMode, cb, cs float64) float64 {
	switch mode {
	case Multiply:
		return cb * cs
	case Screen:
		return cb + cs - cb*cs
	case Overlay:
		return hardLight(cs, cb)
	case Darken:
		return math.Min(cb, cs)
	case Lighten:
		return math.Max(cb, cs)
	case ColorDodge:
		if cb <= 0 {
			return 0
		}
		if cs >= 1 {
			return 1
		}
		return math.Min(1, cb/(1-cs))
	case ColorBurn:
		if cb >= 1 {
			return 1
		}
		if cs <= 0 {
			return 0
		}
		return 1 - math.Min(1, (1-cb)/cs)
	case HardLight:
		return hardLight(cb, cs)
	case SoftLight:
		return softLight(cb, cs)
	case Difference:
		return math.Abs(cb - cs)
	case Exclusion:
		return cb + cs - 2*cb*cs
	}
	return cs
}

func hardLight(cb, cs float64) float64 {
	if cs <= 0.5 {
		return 2 * cb * cs
	}
	return cb + (2*cs - 1) - cb*(2*cs-1)
}

func softLight(cb, cs float64) float64 {
	if cs <= 0.5 {
		return cb - (1-2*cs)*cb*(1-cb)
	}
	var d float64
	if cb <= 0.25 {
		d = ((16*cb-12)*cb + 4) * cb
	} else {
		d = math.Sqrt(cb)
	}
	return cb + (2*cs-1)*(d-cb)
}

func (r *Rasterizer) FillPaint(dst *image.RGBA, p path.Path, m geom.Matrix, sh Shader, rule FillRule, clip image.Rectangle, mode BlendMode, mask []float64) {
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
			if mask != nil {
				alpha *= mask[row+x]
			}
			if alpha > 0 {
				blend(dst, b.Min.X+x, b.Min.Y+y, sh, alpha, mode)
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
