package path_test

import (
	"math"
	"testing"

	"github.com/stohirov/suren/geom"
	"github.com/stohirov/suren/path"
)

func dashStats(p path.Path) (spans int, length float64) {
	it := p.Iter()
	var cur geom.Point
	for {
		v, pts, ok := it.Next()
		if !ok {
			break
		}
		switch v {
		case path.MoveTo:
			spans++
			cur = pts[0]
		case path.LineTo:
			length += pts[0].Sub(cur).Len()
			cur = pts[0]
		}
	}
	return spans, length
}

func TestDashEvenSplit(t *testing.T) {
	d := path.Dash{Pattern: []float64{10, 10}}
	spans, length := dashStats(d.Apply(line(geom.Pt(0, 0), geom.Pt(100, 0)), path.DefaultTolerance))
	if spans != 5 {
		t.Fatalf("spans = %d, want 5", spans)
	}
	near(t, "on-length", length, 50, 1e-9)
}

func TestDashPhase(t *testing.T) {
	d := path.Dash{Pattern: []float64{10, 10}, Phase: 5}
	spans, length := dashStats(d.Apply(line(geom.Pt(0, 0), geom.Pt(100, 0)), path.DefaultTolerance))
	if spans != 6 {
		t.Fatalf("phased spans = %d, want 6", spans)
	}
	near(t, "phased on-length", length, 50, 1e-9)
}

func TestDashOddPatternDoubles(t *testing.T) {
	odd := path.Dash{Pattern: []float64{10}}
	even := path.Dash{Pattern: []float64{10, 10}}
	so, lo := dashStats(odd.Apply(line(geom.Pt(0, 0), geom.Pt(100, 0)), path.DefaultTolerance))
	se, le := dashStats(even.Apply(line(geom.Pt(0, 0), geom.Pt(100, 0)), path.DefaultTolerance))
	if so != se || math.Abs(lo-le) > 1e-9 {
		t.Fatalf("odd pattern (%d,%v) != doubled even (%d,%v)", so, lo, se, le)
	}
}

func TestDashInvalidReturnsClone(t *testing.T) {
	d := path.Dash{Pattern: nil}
	spans, length := dashStats(d.Apply(line(geom.Pt(0, 0), geom.Pt(100, 0)), path.DefaultTolerance))
	if spans != 1 || math.Abs(length-100) > 1e-9 {
		t.Fatalf("empty pattern altered the path: spans=%d length=%v", spans, length)
	}
}

func TestDashClosedSeam(t *testing.T) {
	sq := path.Rect(geom.RectXYWH(20, 20, 20, 20))
	d := path.Dash{Pattern: []float64{10, 10}}
	spans, length := dashStats(d.Apply(sq, path.DefaultTolerance))
	if spans != 4 {
		t.Fatalf("closed spans = %d, want 4", spans)
	}
	near(t, "closed on-length", length, 40, 1e-9)
}

func TestDashSpansCrossVertices(t *testing.T) {
	var p path.Path
	p.MoveTo(geom.Pt(0, 0))
	p.LineTo(geom.Pt(10, 0))
	p.LineTo(geom.Pt(20, 0))
	d := path.Dash{Pattern: []float64{15, 5}}
	spans, length := dashStats(d.Apply(p, path.DefaultTolerance))
	if spans != 1 {
		t.Fatalf("a 15-on dash over two 10-long segments should be one span, got %d", spans)
	}
	near(t, "cross-vertex length", length, 15, 1e-9)
}
