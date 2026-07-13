package path

import "github.com/stohirov/sukho/geom"

type Verb uint8

const (
	MoveTo Verb = iota
	LineTo
	QuadTo
	CubicTo
	Close
)

func (v Verb) pointCount() int {
	switch v {
	case MoveTo, LineTo:
		return 1
	case QuadTo:
		return 2
	case CubicTo:
		return 3
	default:
		return 0
	}
}

type Path struct {
	verbs  []Verb
	points []geom.Point

	start geom.Point
	cur   geom.Point
	open  bool
}

func (p *Path) MoveTo(pt geom.Point) {
	p.verbs = append(p.verbs, MoveTo)
	p.points = append(p.points, pt)
	p.start, p.cur, p.open = pt, pt, true
}

func (p *Path) LineTo(pt geom.Point) {
	p.ensureOpen()
	p.verbs = append(p.verbs, LineTo)
	p.points = append(p.points, pt)
	p.cur = pt
}

func (p *Path) QuadTo(c, pt geom.Point) {
	p.ensureOpen()
	p.verbs = append(p.verbs, QuadTo)
	p.points = append(p.points, c, pt)
	p.cur = pt
}

func (p *Path) CubicTo(c1, c2, pt geom.Point) {
	p.ensureOpen()
	p.verbs = append(p.verbs, CubicTo)
	p.points = append(p.points, c1, c2, pt)
	p.cur = pt
}

func (p *Path) Close() {
	if !p.open {
		return
	}
	p.verbs = append(p.verbs, Close)
	p.cur, p.open = p.start, false
}

func (p *Path) ensureOpen() {
	if !p.open {
		p.MoveTo(p.start)
	}
}

func (p Path) Empty() bool { return len(p.verbs) == 0 }

func (p Path) Clone() Path {
	q := p
	q.verbs = append([]Verb(nil), p.verbs...)
	q.points = append([]geom.Point(nil), p.points...)
	return q
}

func (p Path) Transform(m geom.Matrix) Path {
	q := p.Clone()
	for i, pt := range q.points {
		q.points[i] = m.Apply(pt)
	}
	q.start = m.Apply(q.start)
	q.cur = m.Apply(q.cur)
	return q
}

func (p Path) Bounds() geom.Rect {
	var r geom.Rect
	for _, pt := range p.points {
		r = r.ExpandToInclude(pt)
	}
	return r
}

func (p Path) TransformedBounds(m geom.Matrix) geom.Rect {
	var r geom.Rect
	for _, pt := range p.points {
		r = r.ExpandToInclude(m.Apply(pt))
	}
	return r
}

type Iterator struct {
	verbs  []Verb
	points []geom.Point
	vi, pi int
}

func (p Path) Iter() Iterator {
	return Iterator{verbs: p.verbs, points: p.points}
}

func (it *Iterator) Next() (v Verb, pts [3]geom.Point, ok bool) {
	if it.vi >= len(it.verbs) {
		return 0, pts, false
	}
	v = it.verbs[it.vi]
	it.vi++
	n := v.pointCount()
	copy(pts[:n], it.points[it.pi:it.pi+n])
	it.pi += n
	return v, pts, true
}
