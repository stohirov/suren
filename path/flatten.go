package path

import (
	"math"

	"github.com/stohirov/sukho/geom"
)

const DefaultTolerance = 0.25

const maxDepth = 16

func (p Path) Flatten(tol float64, m geom.Matrix, emit func(pts []geom.Point, closed bool)) {
	p.FlattenInto(nil, tol, m, emit)
}

func (p Path) FlattenInto(scratch []geom.Point, tol float64, m geom.Matrix, emit func(pts []geom.Point, closed bool)) []geom.Point {
	buf := scratch[:0]
	var cur geom.Point
	flush := func(closed bool) {
		if len(buf) >= 2 {
			emit(buf, closed)
		}
		buf = buf[:0]
	}
	it := p.Iter()
	for {
		v, pts, ok := it.Next()
		if !ok {
			break
		}
		switch v {
		case MoveTo:
			flush(false)
			cur = m.Apply(pts[0])
			buf = append(buf, cur)
		case LineTo:
			cur = m.Apply(pts[0])
			buf = append(buf, cur)
		case QuadTo:
			c, end := m.Apply(pts[0]), m.Apply(pts[1])
			buf = flattenQuad(buf, cur, c, end, tol, 0)
			cur = end
		case CubicTo:
			c1, c2, end := m.Apply(pts[0]), m.Apply(pts[1]), m.Apply(pts[2])
			buf = flattenCubic(buf, cur, c1, c2, end, tol, 0)
			cur = end
		case Close:
			flush(true)
		}
	}
	flush(false)
	return buf
}

func chordDist(a, b, q geom.Point) float64 {
	ab := b.Sub(a)
	l := ab.Len()
	if l < 1e-12 {
		return q.Sub(a).Len()
	}
	return math.Abs(ab.Cross(q.Sub(a))) / l
}

func flattenQuad(dst []geom.Point, p0, c, p1 geom.Point, tol float64, depth int) []geom.Point {
	if depth >= maxDepth || chordDist(p0, p1, c)/2 <= tol {
		return append(dst, p1)
	}

	c0 := mid(p0, c)
	c1 := mid(c, p1)
	m := mid(c0, c1)
	dst = flattenQuad(dst, p0, c0, m, tol, depth+1)
	return flattenQuad(dst, m, c1, p1, tol, depth+1)
}

func flattenCubic(dst []geom.Point, p0, c1, c2, p1 geom.Point, tol float64, depth int) []geom.Point {
	d := math.Max(chordDist(p0, p1, c1), chordDist(p0, p1, c2))
	if depth >= maxDepth || 0.75*d <= tol {
		return append(dst, p1)
	}

	c11 := mid(p0, c1)
	cm := mid(c1, c2)
	c22 := mid(c2, p1)
	c12 := mid(c11, cm)
	c21 := mid(cm, c22)
	m := mid(c12, c21)
	dst = flattenCubic(dst, p0, c11, c12, m, tol, depth+1)
	return flattenCubic(dst, m, c21, c22, p1, tol, depth+1)
}

func mid(a, b geom.Point) geom.Point {
	return geom.Point{X: (a.X + b.X) / 2, Y: (a.Y + b.Y) / 2}
}
