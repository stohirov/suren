package props

import (
	"math"
	"math/rand/v2"

	"github.com/stohirov/suren/geom"
	"github.com/stohirov/suren/paint"
	"github.com/stohirov/suren/path"
)

const (
	W = 96
	H = 96
)

func newRNG(seed uint64) *rand.Rand {
	return rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
}

// Alpha is never 0: a fully transparent color renders nothing, which would make
// every law pass vacuously. Low alphas are kept — that is where the blend path's
// divide by alpha is worst behaved.
func randColor(rng *rand.Rand) paint.Color {
	return paint.FromRGBA8(uint8(rng.IntN(256)), uint8(rng.IntN(256)), uint8(rng.IntN(256)), uint8(1+rng.IntN(255)))
}

// randBlendGeneral never returns SrcOver, which takes a different (fast) path.
func randBlendGeneral(rng *rand.Rand) paint.BlendMode {
	modes := []paint.BlendMode{
		paint.Multiply, paint.Screen, paint.Overlay, paint.Darken, paint.Lighten,
		paint.ColorDodge, paint.ColorBurn, paint.HardLight, paint.SoftLight, paint.Difference, paint.Exclusion,
	}
	return modes[rng.IntN(len(modes))]
}

// randPath keeps geometry near the canvas centre so a random transform does not
// push it off-canvas, which would render nothing and pass every law vacuously.
func randPath(rng *rand.Rand) path.Path {
	cx, cy := W/2.0, H/2.0
	switch rng.IntN(3) {
	case 0:
		w := 20 + rng.Float64()*30
		h := 20 + rng.Float64()*30
		return path.Rect(geom.RectXYWH(cx-w/2, cy-h/2, w, h))
	case 1:
		return path.Circle(geom.Pt(cx, cy), 12+rng.Float64()*22)
	default:
		var p path.Path
		n := 3 + rng.IntN(5)
		r0 := 14 + rng.Float64()*20
		for i := range n {
			ang := 2*math.Pi*float64(i)/float64(n) + rng.Float64()*0.3
			rad := r0 * (0.6 + rng.Float64()*0.6)
			pt := geom.Pt(cx+rad*math.Cos(ang), cy+rad*math.Sin(ang))
			if i == 0 {
				p.MoveTo(pt)
			} else {
				p.LineTo(pt)
			}
		}
		p.Close()
		return p
	}
}

// randAboutCenter builds transforms anchored at the canvas centre so that a
// composition of two of them still lands on-canvas.
func randAboutCenter(rng *rand.Rand) geom.Matrix {
	cx, cy := W/2.0, H/2.0
	m := geom.Translate(cx, cy)
	m = m.Mul(geom.Rotate((rng.Float64()*2 - 1) * 0.6))
	s := 0.8 + rng.Float64()*0.4
	m = m.Mul(geom.Scale(s, s))
	m = m.Mul(geom.Translate(rng.Float64()*12-6, rng.Float64()*12-6))
	return m.Mul(geom.Translate(-cx, -cy))
}

// randAlignedRect returns a pixel-aligned rect, whose coverage is exactly 0 or 1.
func randAlignedRect(rng *rand.Rand) geom.Rect {
	x := float64(rng.IntN(W / 3))
	y := float64(rng.IntN(H / 3))
	w := float64(10 + rng.IntN(W/2))
	h := float64(10 + rng.IntN(H/2))
	return geom.RectXYWH(x, y, w, h)
}
