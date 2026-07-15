package render

import (
	"github.com/stohirov/sukho/geom"
	"github.com/stohirov/sukho/paint"
	"github.com/stohirov/sukho/path"
	"github.com/stohirov/sukho/scene"
)

type Renderer interface {
	Render(*scene.Scene) error
}

type state struct {
	ctm       geom.Matrix
	clip      *geom.Rect
	clipPaths []scene.ClipPath
	blend     paint.BlendMode
	fallback  bool
}

type Canvas struct {
	sc    scene.Scene
	st    state
	stack []state
}

func NewCanvas() *Canvas {
	return &Canvas{st: state{ctm: geom.Identity()}}
}

func (c *Canvas) Save() { c.stack = append(c.stack, c.st) }

func (c *Canvas) Restore() {
	if n := len(c.stack); n > 0 {
		c.st = c.stack[n-1]
		c.stack = c.stack[:n-1]
	}
}

func (c *Canvas) Transform(m geom.Matrix) { c.st.ctm = c.st.ctm.Mul(m) }

func (c *Canvas) Translate(tx, ty float64) { c.Transform(geom.Translate(tx, ty)) }
func (c *Canvas) Scale(sx, sy float64)     { c.Transform(geom.Scale(sx, sy)) }
func (c *Canvas) Rotate(theta float64)     { c.Transform(geom.Rotate(theta)) }

func (c *Canvas) CTM() geom.Matrix { return c.st.ctm }

func (c *Canvas) SetBlend(mode paint.BlendMode) { c.st.blend = mode }

// SetFallback routes subsequent nodes through the CPU reference on backends
// that cannot render them exactly. See scene.Node.Fallback; it is saved and
// restored with the rest of the canvas state.
func (c *Canvas) SetFallback(on bool) { c.st.fallback = on }

func (c *Canvas) ClipRect(r geom.Rect) {
	d := deviceBBox(c.st.ctm, r)
	if c.st.clip != nil {
		d = d.Intersect(*c.st.clip)
	}
	c.st.clip = &d
}

func (c *Canvas) ClipPath(p path.Path, rule paint.FillRule) {
	dp := p.Transform(c.st.ctm)
	d := dp.Bounds()
	if c.st.clip != nil {
		d = d.Intersect(*c.st.clip)
	}
	c.st.clip = &d
	next := make([]scene.ClipPath, len(c.st.clipPaths)+1)
	copy(next, c.st.clipPaths)
	next[len(c.st.clipPaths)] = scene.ClipPath{Path: dp, Rule: rule}
	c.st.clipPaths = next
}

func deviceBBox(m geom.Matrix, r geom.Rect) geom.Rect {
	corners := [4]geom.Point{
		m.Apply(r.Min),
		m.Apply(geom.Pt(r.Max.X, r.Min.Y)),
		m.Apply(r.Max),
		m.Apply(geom.Pt(r.Min.X, r.Max.Y)),
	}
	out := geom.Rect{Min: corners[0], Max: corners[0]}
	for _, p := range corners[1:] {
		out = out.ExpandToInclude(p)
	}
	return out
}

func (c *Canvas) Fill(p path.Path, pt paint.Paint, rule paint.FillRule) {
	c.sc.Add(scene.Node{
		Path:      p,
		Transform: c.st.ctm,
		Paint:     pt,
		Op:        c.st.blend,
		FillRule:  rule,
		Clip:      c.st.clip,
		Clips:     c.st.clipPaths,
		Fallback:  c.st.fallback,
	})
}

func (c *Canvas) FillColor(p path.Path, col paint.Color) {
	c.Fill(p, paint.Solid{Color: col}, paint.NonZero)
}

func (c *Canvas) Stroke(p path.Path, pt paint.Paint, s paint.Stroke) {
	sc := s
	c.sc.Add(scene.Node{
		Path:      p,
		Transform: c.st.ctm,
		Paint:     pt,
		Op:        c.st.blend,
		Stroke:    &sc,
		Clip:      c.st.clip,
		Clips:     c.st.clipPaths,
		Fallback:  c.st.fallback,
	})
}

func (c *Canvas) StrokeColor(p path.Path, col paint.Color, s paint.Stroke) {
	c.Stroke(p, paint.Solid{Color: col}, s)
}

func (c *Canvas) Scene() *scene.Scene { return &c.sc }

func (c *Canvas) Reset() {
	c.sc.Reset()
	c.st = state{ctm: geom.Identity()}
	c.stack = c.stack[:0]
}
