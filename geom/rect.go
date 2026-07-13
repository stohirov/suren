package geom

type Rect struct {
	Min, Max Point
}

func RectXYWH(x, y, w, h float64) Rect {
	return Rect{Point{x, y}, Point{x + w, y + h}}
}

func (r Rect) Empty() bool { return r.Min.X >= r.Max.X || r.Min.Y >= r.Max.Y }

func (r Rect) Width() float64 { return r.Max.X - r.Min.X }

func (r Rect) Height() float64 { return r.Max.Y - r.Min.Y }

func (r Rect) Union(s Rect) Rect {
	if r.Empty() {
		return s
	}
	if s.Empty() {
		return r
	}
	return Rect{
		Min: Point{min(r.Min.X, s.Min.X), min(r.Min.Y, s.Min.Y)},
		Max: Point{max(r.Max.X, s.Max.X), max(r.Max.Y, s.Max.Y)},
	}
}

func (r Rect) Intersect(s Rect) Rect {
	i := Rect{
		Min: Point{max(r.Min.X, s.Min.X), max(r.Min.Y, s.Min.Y)},
		Max: Point{min(r.Max.X, s.Max.X), min(r.Max.Y, s.Max.Y)},
	}
	if i.Empty() {
		return Rect{}
	}
	return i
}

func (r Rect) Contains(p Point) bool {
	return p.X >= r.Min.X && p.X <= r.Max.X && p.Y >= r.Min.Y && p.Y <= r.Max.Y
}

func (r Rect) ExpandToInclude(p Point) Rect {
	if r == (Rect{}) {
		return Rect{p, p}
	}
	return Rect{
		Min: Point{min(r.Min.X, p.X), min(r.Min.Y, p.Y)},
		Max: Point{max(r.Max.X, p.X), max(r.Max.Y, p.Y)},
	}
}
