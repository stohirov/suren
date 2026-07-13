package gpu

import (
	"math"
	"unsafe"

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
	TileSegOff    []uint32
	TileSegIdx    []uint32
	Fingerprint   uint64

	flatScratch []geom.Point
	tileCursor  []uint32
	rankCursor  []uint32
	entryOf     []uint32
	segCursor   []uint32
}

func Encode(s *scene.Scene, w, h int) *Encoded {
	e := &Encoded{}
	EncodeInto(e, s, w, h)
	return e
}

func EncodeInto(e *Encoded, s *scene.Scene, w, h int) {
	e.Width, e.Height = w, h
	e.Segments = e.Segments[:0]
	e.Nodes = e.Nodes[:0]
	e.Stops = e.Stops[:0]
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
		e.appendSegments(geo, n.Transform)
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
	e.buildTiles()
	e.Fingerprint = e.fingerprint()
}

func (e *Encoded) fingerprint() uint64 {
	fp := uint64(14695981039346656037)
	dims := [2]uint32{uint32(e.Width), uint32(e.Height)}
	fp = hashWords(fp, unsafe.Pointer(&dims[0]), 8)
	if len(e.Segments) > 0 {
		fp = hashWords(fp, unsafe.Pointer(&e.Segments[0]), len(e.Segments)*int(unsafe.Sizeof(e.Segments[0])))
	}
	if len(e.Nodes) > 0 {
		fp = hashWords(fp, unsafe.Pointer(&e.Nodes[0]), len(e.Nodes)*int(unsafe.Sizeof(e.Nodes[0])))
	}
	if len(e.Stops) > 0 {
		fp = hashWords(fp, unsafe.Pointer(&e.Stops[0]), len(e.Stops)*int(unsafe.Sizeof(e.Stops[0])))
	}
	return fp
}

func hashWords(fp uint64, p unsafe.Pointer, n int) uint64 {
	const prime = 1099511628211
	for _, w := range unsafe.Slice((*uint64)(p), n/8) {
		fp = (fp ^ w) * prime
	}
	for _, c := range unsafe.Slice((*byte)(unsafe.Add(p, n&^7)), n&7) {
		fp = (fp ^ uint64(c)) * prime
	}
	return fp
}

func (e *Encoded) buildTiles() {
	nx, ny := e.NTilesX, e.NTilesY
	nt := nx * ny

	e.TileOffsets = resetU32(e.TileOffsets, nt+1)
	for ni := range e.Nodes {
		tx0, tx1, ty0, ty1 := tileRange(e.Nodes[ni].BBox, nx, ny)
		for ty := ty0; ty < ty1; ty++ {
			for tx := tx0; tx < tx1; tx++ {
				e.TileOffsets[ty*nx+tx+1]++
			}
		}
	}
	for i := 1; i <= nt; i++ {
		e.TileOffsets[i] += e.TileOffsets[i-1]
	}
	numEntries := int(e.TileOffsets[nt])

	e.tileCursor = growU32(e.tileCursor, nt)
	copy(e.tileCursor, e.TileOffsets[:nt])
	e.TileNodes = growU32(e.TileNodes, numEntries)
	for ni := range e.Nodes {
		tx0, tx1, ty0, ty1 := tileRange(e.Nodes[ni].BBox, nx, ny)
		for ty := ty0; ty < ty1; ty++ {
			for tx := tx0; tx < tx1; tx++ {
				t := ty*nx + tx
				e.TileNodes[e.tileCursor[t]] = uint32(ni)
				e.tileCursor[t]++
			}
		}
	}

	e.TileSegOff = resetU32(e.TileSegOff, numEntries+1)
	e.entryOf = growU32(e.entryOf, nt)
	e.rankCursor = resetU32(e.rankCursor, nt)
	for ni := range e.Nodes {
		nd := &e.Nodes[ni]
		ntx0, ntx1, nty0, nty1 := tileRange(nd.BBox, nx, ny)
		e.assignEntries(nx, ntx0, ntx1, nty0, nty1)
		s1 := nd.SegStart + nd.SegCount
		for si := nd.SegStart; si < s1; si++ {
			stx0, stx1, sty0, sty1 := segTiles(e.Segments[si], ntx0, ntx1, nty0, nty1)
			for ty := sty0; ty < sty1; ty++ {
				for tx := stx0; tx < stx1; tx++ {
					e.TileSegOff[e.entryOf[ty*nx+tx]+1]++
				}
			}
		}
	}
	for i := 1; i <= numEntries; i++ {
		e.TileSegOff[i] += e.TileSegOff[i-1]
	}

	e.TileSegIdx = growU32(e.TileSegIdx, int(e.TileSegOff[numEntries]))
	e.segCursor = growU32(e.segCursor, numEntries)
	copy(e.segCursor, e.TileSegOff[:numEntries])
	e.rankCursor = resetU32(e.rankCursor, nt)
	for ni := range e.Nodes {
		nd := &e.Nodes[ni]
		ntx0, ntx1, nty0, nty1 := tileRange(nd.BBox, nx, ny)
		e.assignEntries(nx, ntx0, ntx1, nty0, nty1)
		s1 := nd.SegStart + nd.SegCount
		for si := nd.SegStart; si < s1; si++ {
			stx0, stx1, sty0, sty1 := segTiles(e.Segments[si], ntx0, ntx1, nty0, nty1)
			for ty := sty0; ty < sty1; ty++ {
				for tx := stx0; tx < stx1; tx++ {
					k := e.entryOf[ty*nx+tx]
					e.TileSegIdx[e.segCursor[k]] = si
					e.segCursor[k]++
				}
			}
		}
	}
}

func (e *Encoded) assignEntries(nx, tx0, tx1, ty0, ty1 int) {
	for ty := ty0; ty < ty1; ty++ {
		for tx := tx0; tx < tx1; tx++ {
			t := ty*nx + tx
			e.entryOf[t] = e.TileOffsets[t] + e.rankCursor[t]
			e.rankCursor[t]++
		}
	}
}

func tileRange(bb [4]float32, nx, ny int) (tx0, tx1, ty0, ty1 int) {
	tx0 = clampInt(int(math.Floor(float64(bb[0])))/tileSize, 0, nx)
	tx1 = clampInt(int(math.Floor(float64(bb[2])))/tileSize+1, 0, nx)
	ty0 = clampInt(int(math.Floor(float64(bb[1])))/tileSize, 0, ny)
	ty1 = clampInt(int(math.Floor(float64(bb[3])))/tileSize+1, 0, ny)
	return
}

func segTiles(s Segment, ntx0, ntx1, nty0, nty1 int) (tx0, tx1, ty0, ty1 int) {
	minx := math.Min(float64(s.X0), float64(s.X1))
	miny := math.Min(float64(s.Y0), float64(s.Y1))
	maxy := math.Max(float64(s.Y0), float64(s.Y1))
	tx0 = clampInt(int(math.Floor(minx))/tileSize, ntx0, ntx1)
	tx1 = ntx1
	ty0 = clampInt(int(math.Floor(miny))/tileSize, nty0, nty1)
	ty1 = clampInt(int(math.Floor(maxy))/tileSize+1, nty0, nty1)
	return
}

func resetU32(s []uint32, n int) []uint32 {
	s = growU32(s, n)
	for i := range s {
		s[i] = 0
	}
	return s
}

func growU32(s []uint32, n int) []uint32 {
	if cap(s) < n {
		return make([]uint32, n)
	}
	return s[:n]
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

func (e *Encoded) appendSegments(geo path.Path, m geom.Matrix) {
	e.flatScratch = geo.FlattenInto(e.flatScratch, path.DefaultTolerance, m, func(pts []geom.Point, closed bool) {
		if len(pts) < 2 {
			return
		}
		for i := 0; i+1 < len(pts); i++ {
			e.Segments = append(e.Segments, seg(pts[i], pts[i+1]))
		}
		e.Segments = append(e.Segments, seg(pts[len(pts)-1], pts[0]))
	})
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
