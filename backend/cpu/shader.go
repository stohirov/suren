package cpu

import (
	"math"

	"github.com/stohirov/suren/geom"
	"github.com/stohirov/suren/paint"
	"github.com/stohirov/suren/raster"
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

// meshShader is its own Shader rather than a gradShader param func: a gradient
// maps a point to a scalar t and looks the colour up in a stop table, while a
// mesh produces a colour directly and has no parameter to return. Both quantize
// through paint.Color.RGBA() the same way, so the 16-bit rounding raster.wgsl
// deliberately does not mirror (see gradColor) lives at the same depth here.
type meshShader struct {
	minv geom.Matrix
	ok   bool
	tris []paint.MeshTriangle
}

func (m meshShader) RGBA(px, py int) (r, g, b, a float64) {
	if !m.ok {
		return 0, 0, 0, 0
	}
	q := m.minv.Apply(geom.Pt(float64(px)+0.5, float64(py)+0.5))
	c, in := paint.MeshAt(m.tris, q)
	if !in {
		return 0, 0, 0, 0
	}
	cr, cg, cb, ca := c.RGBA()
	return float64(cr) / 257, float64(cg) / 257, float64(cb) / 257, float64(ca) / 257
}

// imgShader samples an image paint. Unlike gradShader and meshShader it does NOT
// go through paint.Color.RGBA(), and the reason is a bug it would otherwise
// introduce rather than a rounding rule it declines: RGBA() premultiplies, and
// ImageAt has already returned premultiplied values (see paint.Image), so routing
// them through it would multiply by alpha twice. There is also nothing to
// quantize — a texel is 8-bit at the source, so the 16-bit round trip the
// gradients inherit from Go's color.Color convention has no meaning here.
//
// The *255 is the Shader contract's scale (premultiplied, 0..255), and for Nearest
// it is exact: float64(b)/255*255 == float64(b) for every byte, which
// TestNearestIsATexelCopy pins because the claim that nearest introduces no
// arithmetic of its own rests on it.
type imgShader struct {
	minv geom.Matrix
	ok   bool
	im   paint.Image
}

func (s imgShader) RGBA(px, py int) (r, g, b, a float64) {
	if !s.ok {
		return 0, 0, 0, 0
	}
	q := s.minv.Apply(geom.Pt(float64(px)+0.5, float64(py)+0.5))
	c := paint.ImageAt(s.im, q)
	return c.R * 255, c.G * 255, c.B * 255, c.A * 255
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
	case paint.ConicGradient:
		center, angle := g.Center, g.Angle
		return gradShader{minv, ok, g.Stops, func(q geom.Point) float64 {
			d := q.Sub(center)
			// atan2(0,0) is 0 in Go and UNDEFINED in WGSL, so the exact centre
			// pixel is pinned to t=0 on both sides rather than left to two
			// languages' corner cases. It is reachable: a Center on a pixel
			// centre is an ordinary thing to write. Every term below is mirrored
			// in raster.wgsl's gradColor, division included — f32 multiply by a
			// reciprocal is not f32 division.
			if d.X == 0 && d.Y == 0 {
				return 0
			}
			t := (math.Atan2(d.Y, d.X) - angle) / (2 * math.Pi)
			return t - math.Floor(t)
		}}, true
	case paint.MeshGradient:
		return meshShader{minv, ok, g.Triangles}, true
	case paint.Image:
		// An invalid Image is dropped here rather than shaded transparent, so that
		// the GPU encoder — which cannot upload texels that are not there — drops the
		// node for the same reason and the two agree by construction. paint.ImageAt
		// returns transparent for one anyway; this is the encoder's contract, not a
		// second opinion about the colour.
		if !g.Valid() {
			return nil, false
		}
		return imgShader{minv, ok, g}, true
	default:
		return nil, false
	}
}
