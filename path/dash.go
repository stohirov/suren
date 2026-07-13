package path

import (
	"math"

	"github.com/stohirov/sukho/geom"
)

type Dash struct {
	Pattern []float64
	Phase   float64
}

func (d Dash) Apply(p Path, tol float64) Path {
	pat, total := normalizeDash(d.Pattern)
	if pat == nil {
		return p.Clone()
	}
	if tol <= 0 {
		tol = DefaultTolerance
	}
	var out Path
	p.Flatten(tol, geom.Identity(), func(pts []geom.Point, closed bool) {
		d.dashPolyline(&out, pts, closed, pat, total)
	})
	return out
}

func normalizeDash(pattern []float64) ([]float64, float64) {
	if len(pattern) == 0 {
		return nil, 0
	}
	pat := make([]float64, len(pattern))
	total := 0.0
	for i, v := range pattern {
		if v < 0 {
			v = 0
		}
		pat[i] = v
		total += v
	}
	if total <= 0 {
		return nil, 0
	}
	if len(pat)%2 == 1 {
		pat = append(pat, pat...)
		total *= 2
	}
	return pat, total
}

func (d Dash) dashPolyline(out *Path, pts []geom.Point, closed bool, pat []float64, total float64) {
	verts := pts
	if closed {
		verts = append(append(make([]geom.Point, 0, len(pts)+1), pts...), pts[0])
	}
	if len(verts) < 2 {
		return
	}
	idx, on, rem := dashStart(pat, total, d.Phase)
	open := false
	for i := 0; i+1 < len(verts); i++ {
		a, b := verts[i], verts[i+1]
		delta := b.Sub(a)
		segLen := delta.Len()
		if segLen < 1e-12 {
			continue
		}
		dir := delta.Mul(1 / segLen)
		pos := 0.0
		for pos < segLen-1e-12 {
			if rem <= 1e-12 {
				idx = (idx + 1) % len(pat)
				on = idx%2 == 0
				rem = pat[idx]
				open = false
				continue
			}
			step := math.Min(rem, segLen-pos)
			if on {
				if !open {
					out.MoveTo(a.Add(dir.Mul(pos)))
					open = true
				}
				out.LineTo(a.Add(dir.Mul(pos + step)))
			}
			pos += step
			rem -= step
		}
	}
}

func dashStart(pat []float64, total, phase float64) (idx int, on bool, rem float64) {
	ph := math.Mod(phase, total)
	if ph < 0 {
		ph += total
	}
	for n := 0; n < 2*len(pat)+1; n++ {
		if pat[idx] > ph {
			break
		}
		ph -= pat[idx]
		idx = (idx + 1) % len(pat)
	}
	return idx, idx%2 == 0, pat[idx] - ph
}
