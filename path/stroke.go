package path

import (
	"math"

	"github.com/stohirov/sukho/geom"
)

type Cap uint8

const (
	ButtCap Cap = iota
	RoundCap
	SquareCap
)

type Join uint8

const (
	MiterJoin Join = iota
	RoundJoin
	BevelJoin
)

type Stroker struct {
	Width      float64
	Cap        Cap
	Join       Join
	MiterLimit float64
}

func (s Stroker) miterLimit() float64 {
	if s.MiterLimit > 0 {
		return s.MiterLimit
	}
	return 4
}

// MaxExtent is the furthest the outline can lie from the source path. A miter
// reaches miterLimit*w/2, well past the w/2 a caller might assume.
func (s Stroker) MaxExtent() float64 {
	h := s.Width / 2
	e := h
	if s.Join == MiterJoin {
		e = h * s.miterLimit()
	}
	if s.Cap == SquareCap {
		e = math.Max(e, h*math.Sqrt2)
	}
	return e
}

func (s Stroker) Stroke(p Path, tol float64) Path {
	var out Path
	h := s.Width / 2
	if h <= 0 {
		return out
	}
	if tol <= 0 {
		tol = DefaultTolerance
	}
	p.Flatten(tol, geom.Identity(), func(pts []geom.Point, closed bool) {
		s.strokePolyline(&out, dedup(pts, closed), closed, h, tol)
	})
	return out
}

func (s Stroker) strokePolyline(out *Path, pts []geom.Point, closed bool, h, tol float64) {
	n := len(pts)
	if n < 2 {
		return
	}
	segCount := n - 1
	if closed {
		segCount = n
	}

	dir := func(i int) geom.Point { return unit(pts[(i+1)%n].Sub(pts[i])) }

	for i := 0; i < segCount; i++ {
		a, b := pts[i], pts[(i+1)%n]
		nrm := perp(dir(i)).Mul(h)
		addConvex(out, []geom.Point{a.Add(nrm), b.Add(nrm), b.Sub(nrm), a.Sub(nrm)})
	}

	if closed {
		for i := 0; i < n; i++ {
			s.addJoin(out, pts[i], dir((i-1+n)%n), dir(i), h, tol)
		}
		return
	}

	for i := 1; i < n-1; i++ {
		s.addJoin(out, pts[i], dir(i-1), dir(i), h, tol)
	}
	s.addCap(out, pts[0], dir(0).Mul(-1), h, tol)
	s.addCap(out, pts[n-1], dir(n-2), h, tol)
}

func (s Stroker) addJoin(out *Path, v, d0, d1 geom.Point, h, tol float64) {
	if s.Join == RoundJoin {
		addDisk(out, v, h, tol)
		return
	}
	cross := d0.Cross(d1)
	if math.Abs(cross) < 1e-12 {
		return
	}
	n0 := outerNormal(d0, cross).Mul(h)
	n1 := outerNormal(d1, cross).Mul(h)

	if s.Join == BevelJoin {
		addConvex(out, []geom.Point{v, v.Add(n0), v.Add(n1)})
		return
	}

	c := outerNormal(d0, cross).Dot(outerNormal(d1, cross))
	if 1+c < 1e-9 || math.Sqrt(2/(1+c)) > s.miterLimit() {
		addConvex(out, []geom.Point{v, v.Add(n0), v.Add(n1)})
		return
	}
	apex := v.Add(n0.Add(n1).Mul(1 / (1 + c)))
	addConvex(out, []geom.Point{v, v.Add(n0), apex, v.Add(n1)})
}

func (s Stroker) addCap(out *Path, p, o geom.Point, h, tol float64) {
	switch s.Cap {
	case RoundCap:
		addDisk(out, p, h, tol)
	case SquareCap:
		side := perp(o).Mul(h)
		ext := o.Mul(h)
		a, b := p.Add(side), p.Sub(side)
		addConvex(out, []geom.Point{a, a.Add(ext), b.Add(ext), b})
	}
}

func addDisk(out *Path, c geom.Point, r, tol float64) {
	if r <= 0 {
		return
	}
	steps := 8
	if tol > 0 && tol < r {
		steps = int(math.Ceil(math.Pi / math.Acos(1-tol/r)))
		steps = min(max(steps, 8), 256)
	}
	pts := make([]geom.Point, steps)
	for i := range pts {
		sin, cos := math.Sincos(2 * math.Pi * float64(i) / float64(steps))
		pts[i] = geom.Pt(c.X+r*cos, c.Y+r*sin)
	}
	addConvex(out, pts)
}

func addConvex(out *Path, pts []geom.Point) {
	if len(pts) < 3 {
		return
	}
	if signedArea(pts) < 0 {
		for i, j := 0, len(pts)-1; i < j; i, j = i+1, j-1 {
			pts[i], pts[j] = pts[j], pts[i]
		}
	}
	out.MoveTo(pts[0])
	for _, p := range pts[1:] {
		out.LineTo(p)
	}
	out.Close()
}

func outerNormal(d geom.Point, cross float64) geom.Point {
	nrm := perp(d)
	if cross > 0 {
		return nrm.Mul(-1)
	}
	return nrm
}

func perp(d geom.Point) geom.Point { return geom.Pt(-d.Y, d.X) }

func unit(v geom.Point) geom.Point {
	n, _ := v.Normalize()
	return n
}

func signedArea(pts []geom.Point) float64 {
	a := 0.0
	for i := range pts {
		j := (i + 1) % len(pts)
		a += pts[i].X*pts[j].Y - pts[j].X*pts[i].Y
	}
	return a
}

func dedup(pts []geom.Point, closed bool) []geom.Point {
	if len(pts) == 0 {
		return nil
	}
	out := make([]geom.Point, 0, len(pts))
	out = append(out, pts[0])
	for _, p := range pts[1:] {
		if p.Sub(out[len(out)-1]).Len() > 1e-9 {
			out = append(out, p)
		}
	}
	if closed && len(out) > 1 && out[len(out)-1].Sub(out[0]).Len() <= 1e-9 {
		out = out[:len(out)-1]
	}
	return out
}
