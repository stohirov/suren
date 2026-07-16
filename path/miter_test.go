package path_test

import (
	"testing"

	"github.com/stohirov/suren/geom"
	"github.com/stohirov/suren/path"
)

func lShape() path.Path {
	var p path.Path
	p.MoveTo(geom.Pt(0, 10))
	p.LineTo(geom.Pt(0, 0))
	p.LineTo(geom.Pt(10, 0))
	return p
}

func hasVertexNear(p path.Path, target geom.Point, eps float64) bool {
	it := p.Iter()
	for {
		v, pts, ok := it.Next()
		if !ok {
			return false
		}
		if v == path.MoveTo || v == path.LineTo {
			if pts[0].Sub(target).Len() <= eps {
				return true
			}
		}
	}
}

func TestMiterApex90(t *testing.T) {
	s := path.Stroker{Width: 4, Join: path.MiterJoin, MiterLimit: 10}
	out := s.Stroke(lShape(), 0.1)

	apex := geom.Pt(-2, -2)
	if !hasVertexNear(out, apex, 1e-6) {
		t.Fatalf("expected miter apex at %+v in stroke outline", apex)
	}
}

func TestMiterLimitTriggers(t *testing.T) {
	apex := geom.Pt(-2, -2)

	beveled := path.Stroker{Width: 4, Join: path.MiterJoin, MiterLimit: 1.3}.Stroke(lShape(), 0.1)
	if hasVertexNear(beveled, apex, 1e-6) {
		t.Errorf("miter limit 1.3 < sqrt(2) should bevel, but apex is present")
	}

	mitered := path.Stroker{Width: 4, Join: path.MiterJoin, MiterLimit: 1.5}.Stroke(lShape(), 0.1)
	if !hasVertexNear(mitered, apex, 1e-6) {
		t.Errorf("miter limit 1.5 > sqrt(2) should keep the miter apex")
	}
}
