package raster

import (
	"math"

	"github.com/stohirov/sukho/geom"
)

type FillRule uint8

const (
	NonZero FillRule = iota
	EvenOdd
)

// BlendMode and CompositeOp mirror paint's two axes. They are duplicated rather
// than imported because raster is the arithmetic layer and does not depend on
// paint; the backends cast across. See paint.BlendMode / paint.CompositeOp for
// what the axes mean.
type BlendMode uint8

const (
	Normal BlendMode = iota
	Multiply
	Screen
	Overlay
	Darken
	Lighten
	ColorDodge
	ColorBurn
	HardLight
	SoftLight
	Difference
	Exclusion
)

type CompositeOp uint8

const (
	SrcOver CompositeOp = iota
	Clear
	Src
	Dst
	DstOver
	SrcIn
	DstIn
	SrcOut
	DstOut
	SrcAtop
	DstAtop
	Xor
)

// Coefficients returns Porter-Duff's (Fa, Fb) for an operator at source alpha as
// and backdrop alpha ab, weighting the source and backdrop contributions:
//
//	co = as·Fa·Cs + ab·Fb·Cb        ao = as·Fa + ab·Fb
//
// This is the whole table, stated once. raster.wgsl's pdCoeff is a verbatim port
// and the two must not drift — a divergence here is not a rounding artifact but
// two renderers computing different functions, which no tolerance should absorb.
func Coefficients(op CompositeOp, as, ab float64) (fa, fb float64) {
	switch op {
	case Clear:
		return 0, 0
	case Src:
		return 1, 0
	case Dst:
		return 0, 1
	case DstOver:
		return 1 - ab, 1
	case SrcIn:
		return ab, 0
	case DstIn:
		return 0, as
	case SrcOut:
		return 1 - ab, 0
	case DstOut:
		return 0, 1 - as
	case SrcAtop:
		return ab, 1 - as
	case DstAtop:
		return 1 - ab, as
	case Xor:
		return 1 - ab, 1 - as
	}
	return 1, 1 - as // SrcOver
}

type Rasterizer struct {
	w, h   int
	cover  []float64
	area   []float64
	Binary bool
}

func NewRasterizer(w, h int) *Rasterizer {
	w, h = max(w, 0), max(h, 0)
	return &Rasterizer{w: w, h: h, cover: make([]float64, w*h), area: make([]float64, w*h)}
}

func (r *Rasterizer) Size() (w, h int) { return r.w, r.h }

func (r *Rasterizer) Reset() {
	clear(r.cover)
	clear(r.area)
}

func (r *Rasterizer) Resize(w, h int) {
	w, h = max(w, 0), max(h, 0)
	n := w * h
	if cap(r.cover) < n {
		r.cover = make([]float64, n)
		r.area = make([]float64, n)
	} else {
		r.cover = r.cover[:n]
		r.area = r.area[:n]
	}
	r.w, r.h = w, h
}

func (r *Rasterizer) resetRegion(x0, x1, y0, y1 int) {
	for y := y0; y < y1; y++ {
		row := y * r.w
		clear(r.cover[row+x0 : row+x1])
		clear(r.area[row+x0 : row+x1])
	}
}

func (r *Rasterizer) Line(p0, p1 geom.Point) {
	if r.w == 0 || r.h == 0 {
		return
	}

	dir := 1.0
	if p0.Y > p1.Y {
		p0, p1, dir = p1, p0, -1.0
	}
	dy := p1.Y - p0.Y
	if dy < 1e-12 {
		return
	}
	dxdy := (p1.X - p0.X) / dy

	top := math.Max(p0.Y, 0)
	bot := math.Min(p1.Y, float64(r.h))
	if top >= bot {
		return
	}

	for yi := int(math.Floor(top)); float64(yi) < bot; yi++ {
		ytop := math.Max(top, float64(yi))
		ybot := math.Min(bot, float64(yi+1))
		if ytop >= ybot {
			continue
		}
		xa := p0.X + (ytop-p0.Y)*dxdy
		xb := p0.X + (ybot-p0.Y)*dxdy
		r.accumulate(yi, xa, xb, (ybot-ytop)*dir)
	}
}

func (r *Rasterizer) accumulate(yi int, xa, xb, dy float64) {
	x0, x1 := xa, xb
	if x0 > x1 {
		x0, x1 = x1, x0
	}
	if x0 >= float64(r.w) {
		return
	}
	row := yi * r.w

	if x1-x0 < 1e-12 {
		col := int(math.Floor(x0))
		fx := x0 - math.Floor(x0)
		if col < 0 {
			col, fx = 0, 0
		}
		r.cover[row+col] += dy
		r.area[row+col] += dy * 2 * fx
		return
	}

	inv := 1 / (x1 - x0)

	if x0 < 0 {
		r.cover[row] += dy * (math.Min(x1, 0) - x0) * inv
		x0 = 0
	}

	for col := int(math.Floor(x0)); float64(col) < x1 && col < r.w; col++ {
		xl := math.Max(x0, float64(col))
		xr := math.Min(x1, float64(col+1))
		if xl >= xr {
			continue
		}
		dseg := dy * (xr - xl) * inv
		r.cover[row+col] += dseg
		r.area[row+col] += dseg * (xl - float64(col) + xr - float64(col))
	}
}

func (r *Rasterizer) Sweep(rule FillRule, emit func(x, y int, alpha float64)) {
	for y := 0; y < r.h; y++ {
		row := y * r.w
		acc := 0.0
		for x := 0; x < r.w; x++ {
			acc += r.cover[row+x]
			alpha := coverage(acc-r.area[row+x]/2, rule)
			if r.Binary {
				if alpha >= 0.5 {
					alpha = 1
				} else {
					alpha = 0
				}
			}
			if alpha > 0 {
				emit(x, y, alpha)
			}
		}
	}
}

func coverage(w float64, rule FillRule) float64 {
	a := math.Abs(w)
	if rule == EvenOdd {
		a = math.Mod(a, 2)
		if a > 1 {
			a = 2 - a
		}
		return a
	}
	return math.Min(a, 1)
}
