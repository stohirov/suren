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

type Canvas struct {
	sc    scene.Scene
	ctm   geom.Matrix
	stack []geom.Matrix
}

func NewCanvas() *Canvas {
	return &Canvas{ctm: geom.Identity()}
}

func (c *Canvas) Save() { c.stack = append(c.stack, c.ctm) }

func (c *Canvas) Restore() {
	if n := len(c.stack); n > 0 {
		c.ctm = c.stack[n-1]
		c.stack = c.stack[:n-1]
	}
}

func (c *Canvas) Transform(m geom.Matrix) { c.ctm = c.ctm.Mul(m) }

func (c *Canvas) Translate(tx, ty float64) { c.Transform(geom.Translate(tx, ty)) }
func (c *Canvas) Scale(sx, sy float64)     { c.Transform(geom.Scale(sx, sy)) }
func (c *Canvas) Rotate(theta float64)     { c.Transform(geom.Rotate(theta)) }

func (c *Canvas) CTM() geom.Matrix { return c.ctm }

func (c *Canvas) Fill(p path.Path, pt paint.Paint, rule paint.FillRule) {
	c.sc.Add(scene.Node{
		Path:      p,
		Transform: c.ctm,
		Paint:     pt,
		FillRule:  rule,
	})
}

func (c *Canvas) FillColor(p path.Path, col paint.Color) {
	c.Fill(p, paint.Solid{Color: col}, paint.NonZero)
}

func (c *Canvas) Stroke(p path.Path, pt paint.Paint, s paint.Stroke) {
	sc := s
	c.sc.Add(scene.Node{
		Path:      p,
		Transform: c.ctm,
		Paint:     pt,
		Stroke:    &sc,
	})
}

func (c *Canvas) StrokeColor(p path.Path, col paint.Color, s paint.Stroke) {
	c.Stroke(p, paint.Solid{Color: col}, s)
}

func (c *Canvas) Scene() *scene.Scene { return &c.sc }

func (c *Canvas) Reset() {
	c.sc.Reset()
	c.ctm = geom.Identity()
	c.stack = c.stack[:0]
}
