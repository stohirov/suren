package path_test

import (
	"math"
	"testing"

	"github.com/stohirov/suren/geom"
	"github.com/stohirov/suren/path"
	"github.com/stohirov/suren/raster"
)

func strokeArea(outline path.Path, w, h int) float64 {
	r := raster.NewRasterizer(w, h)
	r.FillPath(outline, path.DefaultTolerance, geom.Identity())
	sum := 0.0
	r.Sweep(raster.NonZero, func(x, y int, a float64) { sum += a })
	return sum
}

func line(a, b geom.Point) path.Path {
	var p path.Path
	p.MoveTo(a)
	p.LineTo(b)
	return p
}

func near(t *testing.T, name string, got, want, eps float64) {
	t.Helper()
	if math.Abs(got-want) > eps {
		t.Fatalf("%s = %v, want %v±%v", name, got, want, eps)
	}
}

func TestStrokeButtLine(t *testing.T) {
	s := path.Stroker{Width: 10, Cap: path.ButtCap}
	got := strokeArea(s.Stroke(line(geom.Pt(10, 30), geom.Pt(60, 30)), path.DefaultTolerance), 80, 60)
	near(t, "butt area", got, 500, 0.5)
}

func TestStrokeRoundCap(t *testing.T) {
	s := path.Stroker{Width: 10, Cap: path.RoundCap}
	got := strokeArea(s.Stroke(line(geom.Pt(20, 30), geom.Pt(60, 30)), 0.01), 90, 60)
	near(t, "round-cap area", got, 40*10+math.Pi*25, 0.6)
}

func TestStrokeSquareCap(t *testing.T) {
	s := path.Stroker{Width: 10, Cap: path.SquareCap}
	got := strokeArea(s.Stroke(line(geom.Pt(20, 30), geom.Pt(60, 30)), path.DefaultTolerance), 90, 60)
	near(t, "square-cap area", got, 40*10+100, 0.5)
}

func TestStrokeClosedRing(t *testing.T) {
	sq := path.Rect(geom.RectXYWH(20, 20, 20, 20))
	s := path.Stroker{Width: 4, Join: path.MiterJoin}
	outline := s.Stroke(sq, path.DefaultTolerance)
	near(t, "ring area", strokeArea(outline, 80, 80), 320, 1.0)

	r := raster.NewRasterizer(80, 80)
	r.FillPath(outline, path.DefaultTolerance, geom.Identity())
	hole := 0.0
	r.Sweep(raster.NonZero, func(x, y int, a float64) {
		if x == 30 && y == 30 {
			hole = a
		}
	})
	if hole != 0 {
		t.Fatalf("ring center coverage = %v, want 0 (hole)", hole)
	}
}

func TestStrokeMiterVsBevel(t *testing.T) {
	var corner path.Path
	corner.MoveTo(geom.Pt(20, 20))
	corner.LineTo(geom.Pt(60, 20))
	corner.LineTo(geom.Pt(60, 60))

	miter := path.Stroker{Width: 10, Join: path.MiterJoin}
	bevel := path.Stroker{Width: 10, Join: path.BevelJoin}
	am := strokeArea(miter.Stroke(corner, path.DefaultTolerance), 90, 90)
	ab := strokeArea(bevel.Stroke(corner, path.DefaultTolerance), 90, 90)
	if am <= ab {
		t.Fatalf("miter area %v should exceed bevel area %v", am, ab)
	}

	near(t, "miter-bevel delta", am-ab, 12.5, 0.5)
}

func TestStrokeJoinFillsCorner(t *testing.T) {
	var corner path.Path
	corner.MoveTo(geom.Pt(20, 20))
	corner.LineTo(geom.Pt(60, 20))
	corner.LineTo(geom.Pt(60, 60))
	s := path.Stroker{Width: 10, Join: path.RoundJoin}
	outline := s.Stroke(corner, path.DefaultTolerance)

	r := raster.NewRasterizer(90, 90)
	r.FillPath(outline, path.DefaultTolerance, geom.Identity())
	cov := 0.0
	r.Sweep(raster.NonZero, func(x, y int, a float64) {
		if x == 63 && y == 18 {
			cov = a
		}
	})
	if cov <= 0 {
		t.Fatalf("round join left the outer corner uncovered (cov=%v)", cov)
	}
}

func TestStrokeEmptyWidth(t *testing.T) {
	s := path.Stroker{Width: 0}
	if !s.Stroke(line(geom.Pt(0, 0), geom.Pt(10, 0)), path.DefaultTolerance).Empty() {
		t.Fatal("zero-width stroke should be empty")
	}
}
