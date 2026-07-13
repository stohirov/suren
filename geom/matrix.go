package geom

import "math"

type Matrix struct {
	A, B, C, D, E, F float64
}

func Identity() Matrix { return Matrix{A: 1, D: 1} }

func Translate(tx, ty float64) Matrix { return Matrix{A: 1, D: 1, E: tx, F: ty} }

func Scale(sx, sy float64) Matrix { return Matrix{A: sx, D: sy} }

func Rotate(theta float64) Matrix {
	s, c := math.Sincos(theta)
	return Matrix{A: c, B: s, C: -s, D: c}
}

func Shear(sx, sy float64) Matrix { return Matrix{A: 1, B: sy, C: sx, D: 1} }

func (m Matrix) Mul(n Matrix) Matrix {
	return Matrix{
		A: m.A*n.A + m.C*n.B,
		B: m.B*n.A + m.D*n.B,
		C: m.A*n.C + m.C*n.D,
		D: m.B*n.C + m.D*n.D,
		E: m.A*n.E + m.C*n.F + m.E,
		F: m.B*n.E + m.D*n.F + m.F,
	}
}

func (m Matrix) Apply(p Point) Point {
	return Point{
		X: m.A*p.X + m.C*p.Y + m.E,
		Y: m.B*p.X + m.D*p.Y + m.F,
	}
}

func (m Matrix) ApplyVector(p Point) Point {
	return Point{
		X: m.A*p.X + m.C*p.Y,
		Y: m.B*p.X + m.D*p.Y,
	}
}

func (m Matrix) Det() float64 { return m.A*m.D - m.B*m.C }

func (m Matrix) Invert() (inv Matrix, ok bool) {
	det := m.Det()
	if math.Abs(det) < 1e-12 {
		return Matrix{}, false
	}
	return Matrix{
		A: m.D / det,
		B: -m.B / det,
		C: -m.C / det,
		D: m.A / det,
		E: (m.C*m.F - m.D*m.E) / det,
		F: (m.B*m.E - m.A*m.F) / det,
	}, true
}

func (m Matrix) MaxScale() float64 {
	s1 := m.A*m.A + m.B*m.B + m.C*m.C + m.D*m.D
	h := m.A*m.A + m.C*m.C - m.B*m.B - m.D*m.D
	x := m.A*m.B + m.C*m.D
	s2 := math.Sqrt(h*h + 4*x*x)
	return math.Sqrt((s1 + s2) / 2)
}
