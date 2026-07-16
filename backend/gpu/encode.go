package gpu

import (
	"math"
	"unsafe"

	"github.com/stohirov/suren/geom"
	"github.com/stohirov/suren/paint"
	"github.com/stohirov/suren/path"
	"github.com/stohirov/suren/scene"
)

type PaintKind uint32

const (
	PaintSolid PaintKind = iota
	PaintLinear
	PaintRadial
	PaintConic
	PaintMesh
	PaintImage
)

type Segment struct {
	X0, Y0, X1, Y1 float32
}

// Stop is a colour at a sample, which is the generalization that let the mesh
// land without a tenth binding.
//
// For a gradient the sample is a parameter (Offset) and X/Y are unused; for a
// mesh it is a point (X, Y) and Offset is unused, with three consecutive records
// forming one Gouraud triangle. The shader keys on Node.Kind, so one table serves
// both and StopStart/StopCount address it either way.
//
// The alternative was a tenth binding for a triangle buffer, and it was not
// available: raster.wgsl already holds EIGHT storage buffers, which is exactly
// WebGPU's default maxStorageBuffersPerShaderStage. A ninth would mean requesting
// a raised limit at device creation — the first time this renderer demanded more
// than the portable default, and a new way for a backend 12d has never run on to
// fail. Two f32 per stop is the cheaper price by far: gradients carry a handful
// of stops per node, so the waste is bytes, not frames.
type Stop struct {
	Offset     float32
	R, G, B, A float32
	X, Y       float32
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
	ClipStart uint32
	ClipCount uint32
}

type ClipRec struct {
	SegStart uint32
	SegCount uint32
	Rule     uint32
	Pad      uint32
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
	Clips         []ClipRec

	// FallbackTiles flags, per tile (ty*NTilesX+tx), the tiles touched by a node
	// with scene.Node.Fallback set. The GPU still rasterizes them; the renderer
	// overwrites them with CPU reference pixels afterwards. NFallback is how many
	// are set.
	//
	// The flag follows the node's bbox, which is the exact extent a fill can
	// write, so a flagged region is never smaller than the inexact one. Marking
	// whole tiles rather than pixels is what makes the patch sound: a tile is a
	// complete composite of the scene restricted to its area, so replacing it
	// wholesale cannot leave a seam.
	FallbackTiles []bool
	NFallback     int

	// Atlas is every image paint's texels in one rgba8unorm texture image,
	// AtlasW x AtlasH, tight rows. Images are stacked vertically at x=0, so a
	// node's atlas origin is (0, sum of the heights before it) and AtlasW is the
	// widest image; the slack to the right of a narrower one is never sampled,
	// because paint.EdgeMode.Wrap keeps every index inside the image's own extent
	// before the origin is added.
	//
	// # Why one texture rather than the obvious storage buffer
	//
	// Uploading texels as u32 in a storage buffer would be simpler and it is not
	// available: raster.wgsl already binds EIGHT storage buffers, which is exactly
	// WebGPU's default maxStorageBuffersPerShaderStage, and device.go requests no
	// raised limits. That is the same wall the mesh hit — see Stop, which
	// generalized rather than take a ninth. A sampled texture is a different limit
	// (maxSampledTexturesPerShaderStage, 16 by default), so it costs nothing that
	// is scarce here.
	//
	// It is read with textureLoad and NO sampler, which is the correctness point
	// and not an optimization: a hardware sampler's filtering is driver-defined, so
	// binding one would put an implementation-defined rule inside the parity
	// contract. See paint.Filter.
	//
	// AtlasFP is a hash of the texels alone, so a frame that changes geometry but
	// not images can skip re-uploading them.
	Atlas   []uint8
	AtlasW  int
	AtlasH  int
	AtlasFP uint64

	Fingerprint uint64

	fallbackBBox [][4]float32
	atlasSrc     []paint.Image
	atlasSlots   map[atlasKey]int
	flatScratch  []geom.Point
	tileCursor   []uint32
	rankCursor   []uint32
	entryOf      []uint32
	segCursor    []uint32
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
	e.Clips = e.Clips[:0]
	e.fallbackBBox = e.fallbackBBox[:0]
	e.atlasSrc = e.atlasSrc[:0]
	clear(e.atlasSlots)
	e.AtlasW, e.AtlasH = 0, 0
	for _, n := range s.Nodes {
		kind, ok := paintKind(n.Paint)
		// An unsupported paint is dropped, which is only sound because the CPU
		// reference drops it too. A fallback-marked one is different: it is
		// dropped from the GPU pass but still needs its bbox, or flagging its
		// tiles — the whole point of the mark — would silently do nothing.
		if !ok && !n.Fallback {
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
		bbox := segBounds(e.Segments[start:])
		if n.Fallback {
			e.fallbackBBox = append(e.fallbackBBox, bbox)
		}
		if !ok {
			// No GPU node references these segments; they stay in the buffer only
			// so the fingerprint covers this node's geometry. Dropping them would
			// let two unsupported-paint nodes with the same bbox but different
			// paths hash alike, and a scene change that only the CPU pass can see
			// would be skipped as unchanged.
			continue
		}
		nd := Node{
			SegStart: start,
			SegCount: uint32(len(e.Segments)) - start,
			Rule:     rule,
			Kind:     uint32(kind),
			Flags:    packFlags(n.Op, n.Composite),
			BBox:     bbox,
		}
		e.fillPaint(&nd, kind, n)
		setClip(&nd, n.Clip, w, h)
		e.appendClips(&nd, n.Clips)
		e.Nodes = append(e.Nodes, nd)
	}
	e.NTilesX = (w + tileSize - 1) / tileSize
	e.NTilesY = (h + tileSize - 1) / tileSize
	e.buildTiles()
	e.markFallbackTiles()
	e.buildAtlas()
	e.Fingerprint = e.fingerprint()
}

// buildAtlas copies each image node's texels into one texture image. The nodes
// already carry their atlas origins — fillPaint assigned them as it walked, since
// a vertical stack's origin is just the running height — so this only moves bytes.
func (e *Encoded) buildAtlas() {
	e.AtlasFP = 0
	if e.AtlasW <= 0 || e.AtlasH <= 0 {
		e.Atlas = e.Atlas[:0]
		return
	}
	n := e.AtlasW * e.AtlasH * 4
	if cap(e.Atlas) < n {
		e.Atlas = make([]uint8, n)
	}
	e.Atlas = e.Atlas[:n]
	// The slack right of a narrow image is never sampled, so its contents cannot
	// change a pixel — but they can change the FINGERPRINT, and stale bytes left in
	// reused capacity would make an unchanged scene hash as changed and re-upload
	// every frame. Clearing is what makes the hash a function of the scene.
	clear(e.Atlas)
	y := 0
	for _, im := range e.atlasSrc {
		row := im.W * 4
		for j := range im.H {
			dst := ((y+j)*e.AtlasW + 0) * 4
			copy(e.Atlas[dst:dst+row], im.Pix[j*row:(j+1)*row])
		}
		y += im.H
	}
	e.AtlasFP = hashWords(offsetBasis, unsafe.Pointer(&e.Atlas[0]), len(e.Atlas))
}

// markFallbackTiles flags every tile a fallback node's bbox reaches, using the
// same tileRange the binner uses for node bins, so a node's fallback tiles and
// its GPU bins agree by construction.
func (e *Encoded) markFallbackTiles() {
	nt := e.NTilesX * e.NTilesY
	if cap(e.FallbackTiles) < nt {
		e.FallbackTiles = make([]bool, nt)
	}
	e.FallbackTiles = e.FallbackTiles[:nt]
	clear(e.FallbackTiles)
	e.NFallback = 0
	for _, bb := range e.fallbackBBox {
		tx0, tx1, ty0, ty1 := tileRange(bb, e.NTilesX, e.NTilesY)
		for ty := ty0; ty < ty1; ty++ {
			for tx := tx0; tx < tx1; tx++ {
				if !e.FallbackTiles[ty*e.NTilesX+tx] {
					e.FallbackTiles[ty*e.NTilesX+tx] = true
					e.NFallback++
				}
			}
		}
	}
}

const offsetBasis = uint64(14695981039346656037)

func (e *Encoded) fingerprint() uint64 {
	fp := offsetBasis
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
	// Without this, toggling Fallback on a node leaves Segments/Nodes/Stops
	// untouched, so the frame would hash unchanged and Render would skip the
	// dispatch AND the CPU patch that the toggle exists to schedule.
	if len(e.FallbackTiles) > 0 {
		fp = hashWords(fp, unsafe.Pointer(&e.FallbackTiles[0]), len(e.FallbackTiles))
	}
	// The texels, not just the atlas geometry. A node records where its image sits
	// and how to sample it; nothing in Segments/Nodes/Stops records what is IN it.
	// A caller that redraws into its image's Pix in place and re-renders would
	// otherwise hash the frame identical and get last frame's pixels back — the
	// same trap FallbackTiles closed one field up, and the reason this is folded in
	// rather than assumed away by declaring Pix immutable.
	fp = (fp ^ e.AtlasFP) * 1099511628211
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
	// G0/G1 are the gradient's geometry slots, read per kind: the two endpoints
	// for linear, centre and radius for radial, centre and start angle here. The
	// shader keys on Kind, so the slots carry whatever that kind needs rather than
	// growing a field per gradient type.
	case paint.ConicGradient:
		nd.G0 = pt(g.Center)
		nd.G1 = [2]float32{float32(g.Angle), 0}
		nd.StopStart = uint32(len(e.Stops))
		e.Stops = appendStops(e.Stops, g.Stops)
		nd.StopCount = uint32(len(e.Stops)) - nd.StopStart
		nd.Minv = invMatrix(n.Transform)
	case paint.MeshGradient:
		nd.StopStart = uint32(len(e.Stops))
		e.Stops = appendMeshVertices(e.Stops, g.Triangles)
		nd.StopCount = uint32(len(e.Stops)) - nd.StopStart
		nd.Minv = invMatrix(n.Transform)
	// The image reuses the gradient geometry slots the same way conic did: G0 is
	// the atlas origin, G1 the image's own size. Filter and edge mode ride in the
	// flags word alongside the two compositing axes (see packFlags). So a paint
	// with a texture behind it costs the Node struct nothing — which matters
	// because that struct is hand-mirrored in WGSL and every field is a field in
	// two places.
	case paint.Image:
		nd.G0 = [2]float32{0, float32(e.atlasSlot(g))}
		nd.G1 = [2]float32{float32(g.W), float32(g.H)}
		nd.Flags |= packSample(g.Filter, g.Edge)
		nd.Minv = invMatrix(n.Transform)
	}
}

// atlasKey identifies texels by the array holding them, not by their contents:
// hashing the pixels to dedup would cost more than the copy it saves.
type atlasKey struct {
	data *uint8
	w, h int
}

// atlasSlot returns the atlas row this image starts at, reusing a slot when the
// same texels have already been placed this frame.
//
// Sharing one image across many nodes is the ORDINARY case — a sprite sheet, an
// icon drawn in twenty places — and without this each node would get its own copy,
// so a scene drawing one image N times would build and upload N times the texels.
// That is a pathology rather than an inefficiency, which is why it is worth a map
// here instead of a note in the roadmap.
//
// Two facts make the key sound. Within one EncodeInto the same pointer and
// dimensions can only be the same bytes, so a shared slot can never be wrong. And
// the key deliberately omits Filter and Edge: those ride in the node's flags word,
// not in the atlas, so the same image sampled two different ways still shares one
// slot. Distinct arrays holding identical texels get two slots, which costs memory
// and cannot cost correctness.
//
// The map is retained and cleared per frame rather than rebuilt, keeping Phase 7b's
// zero-allocation encode: clear() empties it without releasing its buckets.
func (e *Encoded) atlasSlot(im paint.Image) int {
	k := atlasKey{unsafe.SliceData(im.Pix), im.W, im.H}
	if y, ok := e.atlasSlots[k]; ok {
		return y
	}
	if e.atlasSlots == nil {
		e.atlasSlots = make(map[atlasKey]int)
	}
	y := e.AtlasH
	e.atlasSlots[k] = y
	e.atlasSrc = append(e.atlasSrc, im)
	e.AtlasW = max(e.AtlasW, im.W)
	e.AtlasH += im.H
	return y
}

func (e *Encoded) appendClips(nd *Node, clips []scene.ClipPath) {
	nd.ClipStart = uint32(len(e.Clips))
	for _, cl := range clips {
		start := uint32(len(e.Segments))
		e.appendSegments(cl.Path, geom.Identity())
		if uint32(len(e.Segments)) == start {
			continue
		}
		rule := uint32(0)
		if cl.Rule == paint.EvenOdd {
			rule = 1
		}
		e.Clips = append(e.Clips, ClipRec{SegStart: start, SegCount: uint32(len(e.Segments)) - start, Rule: rule})
	}
	nd.ClipCount = uint32(len(e.Clips)) - nd.ClipStart
}

func paintKind(p paint.Paint) (PaintKind, bool) {
	switch v := p.(type) {
	case paint.Solid:
		_ = v
		return PaintSolid, true
	case paint.LinearGradient:
		return PaintLinear, true
	case paint.RadialGradient:
		return PaintRadial, true
	case paint.ConicGradient:
		return PaintConic, true
	case paint.MeshGradient:
		return PaintMesh, true
	case paint.Image:
		// An image whose Pix does not hold the texels W and H claim is dropped, and
		// that agrees with the CPU reference by construction rather than by
		// coincidence: backend/cpu's shader() drops the same node for the same
		// reason. The encoder could not upload the missing texels in any case.
		return PaintImage, v.Valid()
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

// appendMeshVertices flattens each triangle to three consecutive Stop records.
// raster.wgsl's meshColor reads them back in threes, so the grouping is the
// contract: a partial trailing triple would be read as a triangle with garbage
// corners, which is why StopCount is always a multiple of three here.
func appendMeshVertices(dst []Stop, tris []paint.MeshTriangle) []Stop {
	for _, t := range tris {
		for _, v := range t.V {
			dst = append(dst, Stop{
				R: float32(v.Color.R),
				G: float32(v.Color.G),
				B: float32(v.Color.B),
				A: float32(v.Color.A),
				X: float32(v.P.X),
				Y: float32(v.P.Y),
			})
		}
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

// The flags word's layout, stated once. Every field is 4 bits and every reader in
// raster.wgsl unpacks with the same shift and the same 0xF mask:
//
//	bits 0-3    paint.BlendMode      composite()
//	bits 4-7    paint.CompositeOp    composite()
//	bits 8-11   paint.Filter         imageColor()
//	bits 12-15  paint.EdgeMode       imageColor()
//
// Packing rather than adding fields keeps the GPU Node at its current size and
// layout, which is hand-mirrored in WGSL — a new field would have to be inserted
// in both, in order, for two bits of payload.
//
// The masks are load-bearing and that is measured, not assumed: Phase 15 widened
// the shader's blend mask from 0xF to 0xFF and every single-axis corpus entry
// still passed, because a leaked bit lands past the end of a switch whose default
// happens to be the zero value's behaviour. Only a scene exercising two axes at
// once goes red. The image entries cross filter and edge with each other for the
// same reason the composite-x-blend-* entries exist.
const (
	flagBlendShift  = 0
	flagCompShift   = 4
	flagFilterShift = 8
	flagEdgeShift   = 12
)

func packFlags(op paint.BlendMode, comp paint.CompositeOp) uint32 {
	return ((uint32(op) & 0xF) << flagBlendShift) | ((uint32(comp) & 0xF) << flagCompShift)
}

func packSample(f paint.Filter, e paint.EdgeMode) uint32 {
	return ((uint32(f) & 0xF) << flagFilterShift) | ((uint32(e) & 0xF) << flagEdgeShift)
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
