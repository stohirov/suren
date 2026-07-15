package paint

import "github.com/stohirov/sukho/geom"
import "github.com/stohirov/sukho/path"

type FillRule uint8

const (
	NonZero FillRule = iota
	EvenOdd
)

// BlendMode answers how a source and backdrop COLOR combine — the W3C
// mix-blend-mode axis. It is orthogonal to CompositeOp, which answers how their
// COVERAGE combines; a node carries one of each.
//
// Normal is the W3C name for "take the source color", and it is what this enum's
// zero value has always meant. It was called SrcOver until Phase 15, which is
// the name of a Porter-Duff OPERATOR and not of a blend function: the old name
// conflated the two axes at exactly the point they had to come apart. Every
// scene that said SrcOver meant Normal blending under a source-over composite,
// which is now spelled with the two fields it was always two facts about.
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

// CompositeOp answers how a source and backdrop COVERAGE combine — the twelve
// Porter-Duff operators, the W3C composite axis. Orthogonal to BlendMode.
//
// SrcOver is the zero value, so a node that names neither axis composites the
// way every node did before Phase 15.
//
// The operators are a pair of coefficients (Fa, Fb) applied to the source and
// backdrop contributions:
//
//	co = αs·Fa·Cs + αb·Fb·Cb        αo = αs·Fa + αb·Fb
//
// where Cs is the source color AFTER blending with the backdrop. See
// raster.Coefficients for the table; it is stated once and ported verbatim to
// raster.wgsl.
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

type Color struct {
	R, G, B, A float64
}

func RGB(r, g, b float64) Color { return Color{r, g, b, 1} }

func RGBA(r, g, b, a float64) Color { return Color{r, g, b, a} }

func Gray(v float64) Color { return Color{v, v, v, 1} }

func FromRGBA8(r, g, b, a uint8) Color {
	return Color{float64(r) / 255, float64(g) / 255, float64(b) / 255, float64(a) / 255}
}

func clamp01(v float64) float64 {
	if v <= 0 {
		return 0
	}
	if v >= 1 {
		return 1
	}
	return v
}

func (c Color) RGBA() (r, g, b, a uint32) {
	ca := clamp01(c.A)
	to16 := func(v float64) uint32 { return uint32(clamp01(v)*ca*0xffff + 0.5) }
	return to16(c.R), to16(c.G), to16(c.B), uint32(ca*0xffff + 0.5)
}

type Paint interface {
	isPaint()
}

type Solid struct {
	Color Color
}

func (Solid) isPaint() {}

type Stop struct {
	Offset float64
	Color  Color
}

type LinearGradient struct {
	P0, P1 geom.Point
	Stops  []Stop
}

func (LinearGradient) isPaint() {}

type RadialGradient struct {
	Center geom.Point
	Radius float64
	Stops  []Stop
}

func (RadialGradient) isPaint() {}

func Interp(stops []Stop, t float64) Color {
	if len(stops) == 0 {
		return Color{}
	}
	if t <= stops[0].Offset {
		return stops[0].Color
	}
	last := stops[len(stops)-1]
	if t >= last.Offset {
		return last.Color
	}
	for i := 1; i < len(stops); i++ {
		hi := stops[i]
		if t <= hi.Offset {
			lo := stops[i-1]
			span := hi.Offset - lo.Offset
			if span <= 0 {
				return hi.Color
			}
			return lerp(lo.Color, hi.Color, (t-lo.Offset)/span)
		}
	}
	return last.Color
}

func lerp(a, b Color, t float64) Color {
	return Color{
		R: a.R + (b.R-a.R)*t,
		G: a.G + (b.G-a.G)*t,
		B: a.B + (b.B-a.B)*t,
		A: a.A + (b.A-a.A)*t,
	}
}

type Stroke struct {
	Width      float64
	MiterLimit float64
	Cap        path.Cap
	Join       path.Join
	Dashes     []float64
	DashOffset float64
}

func (s Stroke) Stroker() path.Stroker {
	return path.Stroker{
		Width:      s.Width,
		Cap:        s.Cap,
		Join:       s.Join,
		MiterLimit: s.MiterLimit,
	}
}

func (s Stroke) Dash() (path.Dash, bool) {
	if len(s.Dashes) == 0 {
		return path.Dash{}, false
	}
	return path.Dash{Pattern: s.Dashes, Phase: s.DashOffset}, true
}
