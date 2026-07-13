package render

import (
	"testing"

	"github.com/stohirov/sukho/geom"
	"github.com/stohirov/sukho/paint"
	"github.com/stohirov/sukho/path"
)

func approx(a, b float64) bool {
	d := a - b
	return d < 1e-9 && d > -1e-9
}

func unitSquare() path.Path {
	return path.Rect(geom.RectXYWH(0, 0, 1, 1))
}

func TestCanvasBakesCTM(t *testing.T) {
	c := NewCanvas()
	c.Translate(10, 20)
	c.FillColor(unitSquare(), paint.RGB(1, 0, 0))

	nodes := c.Scene().Nodes
	if len(nodes) != 1 {
		t.Fatalf("got %d nodes, want 1", len(nodes))
	}
	m := nodes[0].Transform
	if !approx(m.E, 10) || !approx(m.F, 20) {
		t.Errorf("CTM not baked: E=%v F=%v", m.E, m.F)
	}
	if !nodes[0].Filled() {
		t.Error("node should be a fill")
	}
}

func TestCanvasSaveRestore(t *testing.T) {
	c := NewCanvas()
	c.Translate(5, 0)
	c.Save()
	c.Translate(5, 0)
	if !approx(c.CTM().E, 10) {
		t.Fatalf("inside save: E=%v want 10", c.CTM().E)
	}
	c.Restore()
	if !approx(c.CTM().E, 5) {
		t.Fatalf("after restore: E=%v want 5", c.CTM().E)
	}
	c.Restore()
	if !approx(c.CTM().E, 5) {
		t.Fatalf("extra restore changed CTM: E=%v", c.CTM().E)
	}
}

func TestCanvasStrokeNode(t *testing.T) {
	c := NewCanvas()
	c.StrokeColor(unitSquare(), paint.RGB(0, 0, 1), paint.Stroke{Width: 2})
	n := c.Scene().Nodes[0]
	if n.Filled() {
		t.Fatal("node should be a stroke")
	}
	if n.Stroke.Width != 2 {
		t.Errorf("stroke width = %v, want 2", n.Stroke.Width)
	}
}

func TestCanvasComposesTransforms(t *testing.T) {
	c := NewCanvas()
	c.Translate(10, 0)
	c.Scale(2, 2)
	p := c.CTM().Apply(geom.Pt(1, 0))
	if !approx(p.X, 12) || !approx(p.Y, 0) {
		t.Errorf("composed transform gave %+v, want (12,0)", p)
	}
}

func TestCanvasReset(t *testing.T) {
	c := NewCanvas()
	c.Translate(3, 3)
	c.FillColor(unitSquare(), paint.RGB(1, 1, 1))
	c.Reset()
	if len(c.Scene().Nodes) != 0 {
		t.Error("Reset should clear nodes")
	}
	if c.CTM() != geom.Identity() {
		t.Error("Reset should restore identity CTM")
	}
}
