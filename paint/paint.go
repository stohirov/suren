package paint

import "github.com/stohirov/sukho/path"

type FillRule uint8

const (
	NonZero FillRule = iota
	EvenOdd
)

type BlendMode uint8

const (
	SrcOver BlendMode = iota
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
