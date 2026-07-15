package scene

import (
	"github.com/stohirov/sukho/geom"
	"github.com/stohirov/sukho/paint"
	"github.com/stohirov/sukho/path"
)

// Fallback marks a node whose GPU rendering is not trusted to be exact. Every
// tile the node touches is rasterized by the CPU reference instead, and those
// pixels replace the GPU's (backend/gpu, Phase 14). It is a property of the
// node rather than of a renderer so that a scene carries its own exactness
// requirement: the CPU reference ignores the field (it is already the
// reference), and a backend that cannot honor it renders the node normally.
//
// Setting it is opt-in per feature, not a global switch, and it is not free —
// it moves whole tiles onto the CPU. It buys exactness only where the GPU is
// genuinely inexact.
type Node struct {
	Path      path.Path
	Transform geom.Matrix
	Paint     paint.Paint
	Op        paint.BlendMode
	FillRule  paint.FillRule
	Stroke    *paint.Stroke
	Clip      *geom.Rect
	Clips     []ClipPath
	Fallback  bool
}

type ClipPath struct {
	Path path.Path
	Rule paint.FillRule
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
