package geom

import "math"

type Point struct {
	X, Y float64
}

func Pt(x, y float64) Point { return Point{x, y} }

func (p Point) Add(q Point) Point { return Point{p.X + q.X, p.Y + q.Y} }

func (p Point) Sub(q Point) Point { return Point{p.X - q.X, p.Y - q.Y} }

func (p Point) Mul(s float64) Point { return Point{p.X * s, p.Y * s} }

func (p Point) Dot(q Point) float64 { return p.X*q.X + p.Y*q.Y }

func (p Point) Cross(q Point) float64 { return p.X*q.Y - p.Y*q.X }

func (p Point) Len() float64 { return math.Hypot(p.X, p.Y) }

func (p Point) Normalize() (n Point, ok bool) {
	l := p.Len()
	if l < 1e-12 {
		return Point{}, false
	}
	return Point{p.X / l, p.Y / l}, true
}
