package scene

import (
	"github.com/stohirov/sukho/geom"
	"github.com/stohirov/sukho/paint"
	"github.com/stohirov/sukho/path"
)

type Node struct {
	Path      path.Path
	Transform geom.Matrix
	Paint     paint.Paint
	Op        paint.BlendMode
	FillRule  paint.FillRule
	Stroke    *paint.Stroke
	Clip      *geom.Rect
}

func (n Node) Filled() bool { return n.Stroke == nil }

type Scene struct {
	Nodes []Node
}

func (s *Scene) Add(n Node) { s.Nodes = append(s.Nodes, n) }

func (s *Scene) Reset() { s.Nodes = s.Nodes[:0] }

func (s *Scene) Bounds() geom.Rect {
	var r geom.Rect
	first := true
	for _, n := range s.Nodes {
		b := n.Path.Transform(n.Transform).Bounds()
		if b.Empty() {
			continue
		}
		if n.Stroke != nil {
			pad := n.Stroke.Width / 2 * n.Transform.MaxScale()
			b.Min = b.Min.Sub(geom.Pt(pad, pad))
			b.Max = b.Max.Add(geom.Pt(pad, pad))
		}
		if first {
			r, first = b, false
		} else {
			r = r.Union(b)
		}
	}
	return r
}
