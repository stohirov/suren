package geom

import (
	"math"
	"testing"
)

const eps = 1e-9

func approxEq(a, b float64) bool { return math.Abs(a-b) <= eps }

func pointApproxEq(p, q Point) bool { return approxEq(p.X, q.X) && approxEq(p.Y, q.Y) }

func matrixApproxEq(m, n Matrix) bool {
	return approxEq(m.A, n.A) && approxEq(m.B, n.B) && approxEq(m.C, n.C) &&
		approxEq(m.D, n.D) && approxEq(m.E, n.E) && approxEq(m.F, n.F)
}

func TestPointOps(t *testing.T) {
	p, q := Pt(3, 4), Pt(1, -2)
	if got := p.Add(q); !pointApproxEq(got, Pt(4, 2)) {
		t.Errorf("Add = %v", got)
	}
	if got := p.Sub(q); !pointApproxEq(got, Pt(2, 6)) {
		t.Errorf("Sub = %v", got)
	}
	if got := p.Mul(2); !pointApproxEq(got, Pt(6, 8)) {
		t.Errorf("Mul = %v", got)
	}
	if got := p.Dot(q); !approxEq(got, 3-8) {
		t.Errorf("Dot = %v", got)
	}
	if got := p.Cross(q); !approxEq(got, -6-4) {
		t.Errorf("Cross = %v", got)
	}
	if got := p.Len(); !approxEq(got, 5) {
		t.Errorf("Len = %v", got)
	}
}

func TestNormalize(t *testing.T) {
	n, ok := Pt(3, 4).Normalize()
	if !ok || !pointApproxEq(n, Pt(0.6, 0.8)) {
		t.Errorf("Normalize(3,4) = %v, %v", n, ok)
	}
	if _, ok := Pt(0, 0).Normalize(); ok {
		t.Error("Normalize(0,0) should report ok=false")
	}
}

func TestRotateBasis(t *testing.T) {
	r := Rotate(math.Pi / 2)
	if got := r.Apply(Pt(1, 0)); !pointApproxEq(got, Pt(0, 1)) {
		t.Errorf("Rotate(π/2)(1,0) = %v, want (0,1)", got)
	}
	if got := r.Apply(Pt(0, 1)); !pointApproxEq(got, Pt(-1, 0)) {
		t.Errorf("Rotate(π/2)(0,1) = %v, want (-1,0)", got)
	}
}

func TestMulCompositionOrder(t *testing.T) {
	m := Translate(10, 0)
	n := Scale(2, 2)
	p := Pt(1, 1)
	got := m.Mul(n).Apply(p)
	want := m.Apply(n.Apply(p))
	if !pointApproxEq(got, want) || !pointApproxEq(got, Pt(12, 2)) {
		t.Errorf("m.Mul(n).Apply = %v, m(n(p)) = %v, want (12,2)", got, want)
	}

	if got := n.Mul(m).Apply(p); !pointApproxEq(got, Pt(22, 2)) {
		t.Errorf("n.Mul(m).Apply = %v, want (22,2)", got)
	}
}

func TestMulAssociative(t *testing.T) {
	a := Rotate(0.3)
	b := Translate(5, -7)
	c := Scale(2, 0.5)
	if !matrixApproxEq(a.Mul(b).Mul(c), a.Mul(b.Mul(c))) {
		t.Error("Mul is not associative")
	}
}

func TestInvert(t *testing.T) {
	ms := []Matrix{
		Identity(),
		Translate(3, -4),
		Scale(2, 5),
		Rotate(1.234),
		Shear(0.5, 0.25),
		Rotate(0.7).Mul(Translate(10, 20)).Mul(Scale(3, 0.5)),
	}
	for _, m := range ms {
		inv, ok := m.Invert()
		if !ok {
			t.Errorf("Invert(%+v) reported singular", m)
			continue
		}
		if got := inv.Mul(m); !matrixApproxEq(got, Identity()) {
			t.Errorf("Invert(%+v).Mul(m) = %+v, want identity", m, got)
		}
		p := Pt(2.5, -1.5)
		if got := inv.Apply(m.Apply(p)); !pointApproxEq(got, p) {
			t.Errorf("inv(m(%v)) = %v, want %v", p, got, p)
		}
	}
}

func TestInvertSingular(t *testing.T) {
	if _, ok := Scale(0, 1).Invert(); ok {
		t.Error("Invert of a singular matrix should report ok=false")
	}
}

func TestApplyVectorIgnoresTranslation(t *testing.T) {
	m := Translate(100, 200).Mul(Scale(2, 3))
	if got := m.ApplyVector(Pt(1, 1)); !pointApproxEq(got, Pt(2, 3)) {
		t.Errorf("ApplyVector = %v, want (2,3)", got)
	}
}

func TestMaxScale(t *testing.T) {
	tests := []struct {
		name string
		m    Matrix
		want float64
	}{
		{"identity", Identity(), 1},
		{"rotation", Rotate(0.9), 1},
		{"scale", Scale(2, 3), 3},
		{"rotated scale", Rotate(0.5).Mul(Scale(2, 3)), 3},
		{"translate only", Translate(1000, 1000), 1},
	}
	for _, tt := range tests {
		if got := tt.m.MaxScale(); !approxEq(got, tt.want) {
			t.Errorf("%s: MaxScale = %v, want %v", tt.name, got, tt.want)
		}
	}

	if got := Shear(1, 0).MaxScale(); got <= 1 {
		t.Errorf("Shear(1,0).MaxScale = %v, want > 1", got)
	}
}

func TestDet(t *testing.T) {
	if got := Scale(2, 3).Det(); !approxEq(got, 6) {
		t.Errorf("Det(Scale(2,3)) = %v, want 6", got)
	}

	if got := Scale(-1, 1).Det(); !approxEq(got, -1) {
		t.Errorf("Det(Scale(-1,1)) = %v, want -1", got)
	}
}

func TestRect(t *testing.T) {
	r := RectXYWH(0, 0, 10, 10)
	s := RectXYWH(5, 5, 10, 10)

	if got := r.Union(s); got != (Rect{Pt(0, 0), Pt(15, 15)}) {
		t.Errorf("Union = %+v", got)
	}
	if got := r.Intersect(s); got != (Rect{Pt(5, 5), Pt(10, 10)}) {
		t.Errorf("Intersect = %+v", got)
	}
	if got := r.Intersect(RectXYWH(20, 20, 5, 5)); !got.Empty() {
		t.Errorf("Intersect of disjoint rects = %+v, want empty", got)
	}

	if !r.Contains(Pt(10, 10)) {
		t.Error("Contains should include the boundary")
	}
	if r.Contains(Pt(10.001, 5)) {
		t.Error("Contains(outside) = true")
	}

	if got := (Rect{}).Union(s); got != s {
		t.Errorf("empty.Union(s) = %+v, want s", got)
	}
	if got := (Rect{}).ExpandToInclude(Pt(3, 4)); got != (Rect{Pt(3, 4), Pt(3, 4)}) {
		t.Errorf("zero.ExpandToInclude = %+v", got)
	}
	if got := r.ExpandToInclude(Pt(-5, 20)); got != (Rect{Pt(-5, 0), Pt(10, 20)}) {
		t.Errorf("ExpandToInclude = %+v", got)
	}
	if r.Width() != 10 || r.Height() != 10 {
		t.Errorf("Width/Height = %v/%v", r.Width(), r.Height())
	}
}
