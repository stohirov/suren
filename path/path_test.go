package path

import (
	"math"
	"testing"

	"github.com/stohirov/suren/geom"
)

func TestIteratorRoundTrip(t *testing.T) {
	var p Path
	p.MoveTo(geom.Pt(1, 2))
	p.LineTo(geom.Pt(3, 4))
	p.QuadTo(geom.Pt(5, 6), geom.Pt(7, 8))
	p.CubicTo(geom.Pt(9, 10), geom.Pt(11, 12), geom.Pt(13, 14))
	p.Close()

	type step struct {
		v   Verb
		pts []geom.Point
	}
	want := []step{
		{MoveTo, []geom.Point{geom.Pt(1, 2)}},
		{LineTo, []geom.Point{geom.Pt(3, 4)}},
		{QuadTo, []geom.Point{geom.Pt(5, 6), geom.Pt(7, 8)}},
		{CubicTo, []geom.Point{geom.Pt(9, 10), geom.Pt(11, 12), geom.Pt(13, 14)}},
		{Close, nil},
	}
	it := p.Iter()
	for i, w := range want {
		v, pts, ok := it.Next()
		if !ok {
			t.Fatalf("iterator exhausted at step %d", i)
		}
		if v != w.v {
			t.Fatalf("step %d: verb = %v, want %v", i, v, w.v)
		}
		for j, wp := range w.pts {
			if pts[j] != wp {
				t.Errorf("step %d point %d = %v, want %v", i, j, pts[j], wp)
			}
		}
	}
	if _, _, ok := it.Next(); ok {
		t.Error("iterator should be exhausted")
	}
}

func TestImplicitMoveTo(t *testing.T) {
	var p Path
	p.LineTo(geom.Pt(5, 5))
	pit := p.Iter()
	v, pts, ok := pit.Next()
	if !ok || v != MoveTo || pts[0] != geom.Pt(0, 0) {
		t.Errorf("first verb = %v %v, want MoveTo (0,0)", v, pts[0])
	}

	var q Path
	q.MoveTo(geom.Pt(10, 10))
	q.LineTo(geom.Pt(20, 10))
	q.Close()
	q.LineTo(geom.Pt(30, 30))
	verbs := []Verb{}
	it := q.Iter()
	var reopened geom.Point
	for {
		v, pts, ok := it.Next()
		if !ok {
			break
		}
		if len(verbs) == 3 && v == MoveTo {
			reopened = pts[0]
		}
		verbs = append(verbs, v)
	}
	wantVerbs := []Verb{MoveTo, LineTo, Close, MoveTo, LineTo}
	if len(verbs) != len(wantVerbs) {
		t.Fatalf("verbs = %v, want %v", verbs, wantVerbs)
	}
	for i := range verbs {
		if verbs[i] != wantVerbs[i] {
			t.Fatalf("verbs = %v, want %v", verbs, wantVerbs)
		}
	}
	if reopened != geom.Pt(10, 10) {
		t.Errorf("reopened subpath at %v, want (10,10)", reopened)
	}
}

func TestFlattenCircleWithinTolerance(t *testing.T) {
	const r, tol = 100.0, 0.25
	p := Circle(geom.Pt(0, 0), r)

	subpaths := 0
	p.Flatten(tol, geom.Identity(), func(pts []geom.Point, closed bool) {
		subpaths++
		if !closed {
			t.Error("circle subpath should be closed")
		}
		if len(pts) < 8 {
			t.Errorf("suspiciously coarse circle: %d vertices", len(pts))
		}

		slack := tol + 0.0002*r
		for i, pt := range pts {
			if err := math.Abs(pt.Len() - r); err > slack {
				t.Errorf("vertex %d is %g off the circle (max %g)", i, err, slack)
			}
		}

		for i := range pts {
			m := mid(pts[i], pts[(i+1)%len(pts)])
			if err := math.Abs(m.Len() - r); err > slack {
				t.Errorf("chord %d midpoint is %g off the circle (max %g)", i, err, slack)
			}
		}
	})
	if subpaths != 1 {
		t.Errorf("subpaths = %d, want 1", subpaths)
	}
}

func TestFlattenStraightCubic(t *testing.T) {
	var p Path
	p.MoveTo(geom.Pt(0, 0))
	p.CubicTo(geom.Pt(10, 10), geom.Pt(20, 20), geom.Pt(30, 30))
	p.Flatten(0.25, geom.Identity(), func(pts []geom.Point, closed bool) {
		if len(pts) != 2 {
			t.Errorf("straight cubic flattened to %d vertices, want 2", len(pts))
		}
		if closed {
			t.Error("open subpath reported closed")
		}
	})
}

func TestFlattenRespectsTransformScale(t *testing.T) {
	p := Circle(geom.Pt(0, 0), 10)
	count := func(m geom.Matrix) (n int) {
		p.Flatten(0.25, m, func(pts []geom.Point, _ bool) { n = len(pts) })
		return n
	}
	small, big := count(geom.Identity()), count(geom.Scale(10, 10))
	if big <= small {
		t.Errorf("scaled flattening has %d vertices, unscaled %d; want more when scaled", big, small)
	}
}

func TestBoundsContainsFlattenedPoints(t *testing.T) {
	var p Path
	p.MoveTo(geom.Pt(10, 10))
	p.CubicTo(geom.Pt(200, -50), geom.Pt(-100, 150), geom.Pt(90, 80))
	p.QuadTo(geom.Pt(300, 300), geom.Pt(20, 250))
	p.Close()

	b := p.Bounds()
	p.Flatten(0.1, geom.Identity(), func(pts []geom.Point, _ bool) {
		for i, pt := range pts {
			if !b.Contains(pt) {
				t.Errorf("flattened vertex %d %v outside Bounds %+v", i, pt, b)
			}
		}
	})
}

func TestTransform(t *testing.T) {
	p := Rect(geom.RectXYWH(0, 0, 10, 10))
	q := p.Transform(geom.Translate(5, 7))
	want := geom.Rect{Min: geom.Pt(5, 7), Max: geom.Pt(15, 17)}
	if got := q.Bounds(); got != want {
		t.Errorf("transformed bounds = %+v, want %+v", got, want)
	}

	if got := p.Bounds(); got != (geom.Rect{Min: geom.Pt(0, 0), Max: geom.Pt(10, 10)}) {
		t.Errorf("original bounds changed: %+v", got)
	}
}

func TestRoundedRectClamping(t *testing.T) {
	r := geom.RectXYWH(0, 0, 40, 20)
	p := RoundedRect(r, 100, 100)
	if got := p.Bounds(); got != r {
		t.Errorf("clamped rounded rect bounds = %+v, want %+v", got, r)
	}

	if got := RoundedRect(r, 0, 5).Bounds(); got != r {
		t.Errorf("zero-radius rounded rect bounds = %+v, want %+v", got, r)
	}
}
