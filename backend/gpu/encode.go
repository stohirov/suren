package gpu

import (
	"math"

	"github.com/stohirov/sukho/geom"
	"github.com/stohirov/sukho/paint"
	"github.com/stohirov/sukho/path"
	"github.com/stohirov/sukho/scene"
)

type PaintKind uint32

const (
	PaintSolid PaintKind = iota
	PaintLinear
	PaintRadial
)

type Segment struct {
	X0, Y0, X1, Y1 float32
}

type Stop struct {
	Offset     float32
	R, G, B, A float32
}

type Node struct {
	SegStart  uint32
	SegCount  uint32
	Rule      uint32
	Kind      uint32
	Color     [4]float32
	G0        [2]float32
	G1        [2]float32
	StopStart uint32
	StopCount uint32
	HasClip   uint32
	Flags     uint32
	Clip      [4]float32
	BBox      [4]float32
	Minv      [6]float32
	Pad       [2]float32
}

const tileSize = 16

type Encoded struct {
	Width, Height int
	NTilesX       int
	NTilesY       int
	Segments      []Segment
	Nodes         []Node
	Stops         []Stop
	TileOffsets   []uint32
	TileNodes     []uint32
}

func Encode(s *scene.Scene, w, h int) *Encoded {
	e := &Encoded{Width: w, Height: h}
	for _, n := range s.Nodes {
		kind, ok := paintKind(n.Paint)
		if !ok {
			continue
		}
		geo := n.Path
		rule := uint32(0)
		if n.FillRule == paint.EvenOdd {
			rule = 1
		}
		if n.Stroke != nil {
			geo = strokeOutline(n)
			rule = 0
		}
		start := uint32(len(e.Segments))
		e.Segments = appendSegments(e.Segments, geo, n.Transform)
		if uint32(len(e.Segments)) == start {
			continue
		}
		nd := Node{
			SegStart: start,
			SegCount: uint32(len(e.Segments)) - start,
			Rule:     rule,
			Kind:     uint32(kind),
			BBox:     segBounds(e.Segments[start:]),
		}
		e.fillPaint(&nd, kind, n)
		setClip(&nd, n.Clip, w, h)
		e.Nodes = append(e.Nodes, nd)
	}
	e.NTilesX = (w + tileSize - 1) / tileSize
	e.NTilesY = (h + tileSize - 1) / tileSize
	e.TileOffsets, e.TileNodes = buildTileBins(e.Nodes, e.NTilesX, e.NTilesY)
	return e
}

func buildTileBins(nodes []Node, nx, ny int) ([]uint32, []uint32) {
	tiles := make([][]uint32, nx*ny)
	for ni := range nodes {
		bb := nodes[ni].BBox
		tx0 := clampInt(int(math.Floor(float64(bb[0])))/tileSize, 0, nx)
		tx1 := clampInt(int(math.Floor(float64(bb[2])))/tileSize+1, 0, nx)
		ty0 := clampInt(int(math.Floor(float64(bb[1])))/tileSize, 0, ny)
		ty1 := clampInt(int(math.Floor(float64(bb[3])))/tileSize+1, 0, ny)
		for ty := ty0; ty < ty1; ty++ {
			for tx := tx0; tx < tx1; tx++ {
				tiles[ty*nx+tx] = append(tiles[ty*nx+tx], uint32(ni))
			}
		}
	}
	offsets := make([]uint32, nx*ny+1)
	var flat []uint32
	for i, t := range tiles {
		offsets[i] = uint32(len(flat))
		flat = append(flat, t...)
	}
	offsets[nx*ny] = uint32(len(flat))
	return offsets, flat
}

func (e *Encoded) fillPaint(nd *Node, kind PaintKind, n scene.Node) {
	switch g := n.Paint.(type) {
	case paint.Solid:
		nd.Color = premul(g.Color)
	case paint.LinearGradient:
		nd.G0 = pt(g.P0)
		nd.G1 = pt(g.P1)
		nd.StopStart = uint32(len(e.Stops))
		e.Stops = appendStops(e.Stops, g.Stops)
		nd.StopCount = uint32(len(e.Stops)) - nd.StopStart
		nd.Minv = invMatrix(n.Transform)
	case paint.RadialGradient:
		nd.G0 = pt(g.Center)
		nd.G1 = [2]float32{float32(g.Radius), 0}
		nd.StopStart = uint32(len(e.Stops))
		e.Stops = appendStops(e.Stops, g.Stops)
		nd.StopCount = uint32(len(e.Stops)) - nd.StopStart
		nd.Minv = invMatrix(n.Transform)
	}
}

func paintKind(p paint.Paint) (PaintKind, bool) {
	switch p.(type) {
	case paint.Solid:
		return PaintSolid, true
	case paint.LinearGradient:
		return PaintLinear, true
	case paint.RadialGradient:
		return PaintRadial, true
	default:
		return 0, false
	}
}

func strokeOutline(n scene.Node) path.Path {
	tol := path.DefaultTolerance
	if k := n.Transform.MaxScale(); k > 0 {
		tol /= k
	}
	src := n.Path
	if d, ok := n.Stroke.Dash(); ok {
		src = d.Apply(src, tol)
	}
	return n.Stroke.Stroker().Stroke(src, tol)
}

func appendSegments(dst []Segment, geo path.Path, m geom.Matrix) []Segment {
	geo.Flatten(path.DefaultTolerance, m, func(pts []geom.Point, closed bool) {
		if len(pts) < 2 {
			return
		}
		for i := 0; i+1 < len(pts); i++ {
			dst = append(dst, seg(pts[i], pts[i+1]))
		}
		dst = append(dst, seg(pts[len(pts)-1], pts[0]))
	})
	return dst
}

func appendStops(dst []Stop, stops []paint.Stop) []Stop {
	for _, s := range stops {
		dst = append(dst, Stop{
			Offset: float32(s.Offset),
			R:      float32(s.Color.R),
			G:      float32(s.Color.G),
			B:      float32(s.Color.B),
			A:      float32(s.Color.A),
		})
	}
	return dst
}

func segBounds(segs []Segment) [4]float32 {
	minx, miny := segs[0].X0, segs[0].Y0
	maxx, maxy := minx, miny
	upd := func(x, y float32) {
		minx, miny = min(minx, x), min(miny, y)
		maxx, maxy = max(maxx, x), max(maxy, y)
	}
	for _, s := range segs {
		upd(s.X0, s.Y0)
		upd(s.X1, s.Y1)
	}
	return [4]float32{minx, miny, maxx, maxy}
}

func setClip(nd *Node, r *geom.Rect, w, h int) {
	if r == nil {
		nd.Clip = [4]float32{0, 0, float32(w), float32(h)}
		nd.HasClip = 0
		return
	}
	nd.Clip = [4]float32{float32(r.Min.X), float32(r.Min.Y), float32(r.Max.X), float32(r.Max.Y)}
	nd.HasClip = 1
}

func seg(a, b geom.Point) Segment {
	return Segment{float32(a.X), float32(a.Y), float32(b.X), float32(b.Y)}
}

func pt(p geom.Point) [2]float32 { return [2]float32{float32(p.X), float32(p.Y)} }

func premul(c paint.Color) [4]float32 {
	r, g, b, a := c.RGBA()
	return [4]float32{float32(r) / 65535, float32(g) / 65535, float32(b) / 65535, float32(a) / 65535}
}

func invMatrix(m geom.Matrix) [6]float32 {
	inv, _ := m.Invert()
	return [6]float32{float32(inv.A), float32(inv.B), float32(inv.C), float32(inv.D), float32(inv.E), float32(inv.F)}
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
