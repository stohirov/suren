package raster

import (
	"math"
	"testing"

	"github.com/stohirov/suren/geom"
)

func fillPoly(w, h int, rule FillRule, polys ...[]geom.Point) [][]float64 {
	r := NewRasterizer(w, h)
	for _, poly := range polys {
		for i := range poly {
			r.Line(poly[i], poly[(i+1)%len(poly)])
		}
	}
	grid := make([][]float64, h)
	for y := range grid {
		grid[y] = make([]float64, w)
	}
	r.Sweep(rule, func(x, y int, a float64) { grid[y][x] = a })
	return grid
}

func rectPoly(x0, y0, x1, y1 float64) []geom.Point {
	return []geom.Point{{X: x0, Y: y0}, {X: x1, Y: y0}, {X: x1, Y: y1}, {X: x0, Y: y1}}
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestFullFrame(t *testing.T) {
	g := fillPoly(4, 3, NonZero, rectPoly(0, 0, 4, 3))
	for y := range g {
		for x := range g[y] {
			if !approx(g[y][x], 1) {
				t.Fatalf("pixel (%d,%d) = %v, want 1", x, y, g[y][x])
			}
		}
	}
}

func TestFractionalColumn(t *testing.T) {
	g := fillPoly(4, 1, NonZero, rectPoly(0, 0, 1.5, 1))
	want := []float64{1, 0.5, 0, 0}
	for x, w := range want {
		if !approx(g[0][x], w) {
			t.Fatalf("column %d = %v, want %v", x, g[0][x], w)
		}
	}
}

func TestQuarterPixel(t *testing.T) {
	g := fillPoly(1, 1, NonZero, rectPoly(0, 0, 0.5, 0.5))
	if !approx(g[0][0], 0.25) {
		t.Fatalf("quarter pixel = %v, want 0.25", g[0][0])
	}
}

func TestOrientationAgnostic(t *testing.T) {
	cw := fillPoly(3, 3, NonZero, rectPoly(0, 0, 3, 3))
	ccwPoly := []geom.Point{{X: 0, Y: 0}, {X: 0, Y: 3}, {X: 3, Y: 3}, {X: 3, Y: 0}}
	ccw := fillPoly(3, 3, NonZero, ccwPoly)
	for y := range cw {
		for x := range cw[y] {
			if cw[y][x] != ccw[y][x] {
				t.Fatalf("winding changed coverage at (%d,%d): %v vs %v", x, y, cw[y][x], ccw[y][x])
			}
		}
	}
}

func TestCoverageEqualsArea(t *testing.T) {
	tri := []geom.Point{{X: 1, Y: 1}, {X: 7, Y: 1}, {X: 1, Y: 7}}
	g := fillPoly(8, 8, NonZero, tri)
	sum := 0.0
	for _, row := range g {
		for _, a := range row {
			sum += a
		}
	}
	if math.Abs(sum-18) > 1e-6 {
		t.Fatalf("coverage sum = %v, want 18", sum)
	}
}

func TestEvenOddHole(t *testing.T) {
	outer := rectPoly(0, 0, 6, 6)
	inner := rectPoly(2, 2, 4, 4)
	eo := fillPoly(6, 6, EvenOdd, outer, inner)
	if !approx(eo[3][3], 0) {
		t.Fatalf("even-odd interior = %v, want 0 (hole)", eo[3][3])
	}
	if !approx(eo[0][0], 1) {
		t.Fatalf("even-odd border = %v, want 1", eo[0][0])
	}

	nz := fillPoly(6, 6, NonZero, outer, inner)
	if !approx(nz[3][3], 1) {
		t.Fatalf("non-zero interior = %v, want 1 (filled)", nz[3][3])
	}
}

func TestClipsOutsideFrame(t *testing.T) {
	g := fillPoly(2, 1, NonZero, rectPoly(-5, 0, 1, 1))
	if !approx(g[0][0], 1) || !approx(g[0][1], 0) {
		t.Fatalf("clip left = %v, want [1 0]", g[0])
	}
}

func TestReset(t *testing.T) {
	r := NewRasterizer(2, 2)
	for _, e := range rectEdges(rectPoly(0, 0, 2, 2)) {
		r.Line(e[0], e[1])
	}
	r.Reset()
	got := false
	r.Sweep(NonZero, func(x, y int, a float64) { got = true })
	if got {
		t.Fatal("Sweep emitted coverage after Reset")
	}
}

func rectEdges(poly []geom.Point) [][2]geom.Point {
	edges := make([][2]geom.Point, len(poly))
	for i := range poly {
		edges[i] = [2]geom.Point{poly[i], poly[(i+1)%len(poly)]}
	}
	return edges
}
