package gpu

import (
	"testing"
	"unsafe"

	"github.com/stohirov/sukho/geom"
	"github.com/stohirov/sukho/internal/sample"
	"github.com/stohirov/sukho/paint"
	"github.com/stohirov/sukho/path"
	"github.com/stohirov/sukho/render"
)

// The mesh rides the stops buffer as three consecutive records per triangle, so
// StopCount being a multiple of three is the contract meshColor reads back. A
// partial trailing triple would be silently dropped by the shader's `i + 3u > end`
// guard, which is the safe failure but still the wrong picture.
func TestEncodeMeshScene(t *testing.T) {
	e := Encode(sample.MeshScene(), sample.W, sample.H)

	meshes := 0
	for i, n := range e.Nodes {
		if n.Kind != uint32(PaintMesh) {
			continue
		}
		meshes++
		if n.StopCount == 0 || n.StopCount%3 != 0 {
			t.Errorf("mesh node %d has StopCount %d, want a non-zero multiple of 3", i, n.StopCount)
		}
		if int(n.StopStart+n.StopCount) > len(e.Stops) {
			t.Errorf("mesh node %d stop range [%d,+%d) overruns the %d-record table", i, n.StopStart, n.StopCount, len(e.Stops))
		}
	}
	if meshes != 3 {
		t.Fatalf("mesh nodes = %d, want 3", meshes)
	}
}

// Two paint kinds now share one table, and each node addresses it by
// StopStart/StopCount. This is the case no hand-written corpus scene covers —
// gradient scenes hold only gradients and MeshScene only meshes — and a
// mis-indexed offset would render the wrong colours rather than fail.
//
// The vertex data is checked against the mesh it came from, not merely for
// self-consistency: a gradient's stops sitting where a mesh's vertices should be
// would pass a bounds check and fail this.
func TestEncodeInterleavesMeshAndGradientStops(t *testing.T) {
	tris := paint.MeshGrid(geom.RectXYWH(0, 0, 40, 40), 1, 1, []paint.Color{
		paint.RGBA(1, 0, 0, 1), paint.RGBA(0, 1, 0, 1),
		paint.RGBA(0, 0, 1, 1), paint.RGBA(1, 1, 0, 1),
	})

	c := render.NewCanvas()
	c.Fill(path.Rect(geom.RectXYWH(0, 0, 40, 40)), paint.LinearGradient{
		P0: geom.Pt(0, 0), P1: geom.Pt(40, 0),
		Stops: []paint.Stop{
			{Offset: 0, Color: paint.RGBA(1, 1, 1, 1)},
			{Offset: 1, Color: paint.RGBA(0, 0, 0, 1)},
		},
	}, paint.NonZero)
	c.Fill(path.Rect(geom.RectXYWH(0, 0, 40, 40)), tris, paint.NonZero)
	c.Fill(path.Rect(geom.RectXYWH(0, 0, 40, 40)), paint.RadialGradient{
		Center: geom.Pt(20, 20), Radius: 20,
		Stops: []paint.Stop{
			{Offset: 0, Color: paint.RGBA(0, 1, 1, 1)},
			{Offset: 1, Color: paint.RGBA(1, 0, 1, 1)},
		},
	}, paint.NonZero)

	e := Encode(c.Scene(), 40, 40)
	if len(e.Nodes) != 3 {
		t.Fatalf("nodes = %d, want 3", len(e.Nodes))
	}
	lin, mesh, rad := e.Nodes[0], e.Nodes[1], e.Nodes[2]

	if lin.StopCount != 2 || rad.StopCount != 2 {
		t.Errorf("gradient stop counts = %d and %d, want 2 each", lin.StopCount, rad.StopCount)
	}
	if want := uint32(6); mesh.StopCount != want {
		t.Fatalf("mesh StopCount = %d, want %d (two triangles)", mesh.StopCount, want)
	}
	// The mesh must sit AFTER the first gradient and the second gradient after the
	// mesh: appended in scene order, with no overlap.
	if mesh.StopStart != lin.StopStart+lin.StopCount {
		t.Errorf("mesh starts at %d, want %d (immediately after the linear gradient)", mesh.StopStart, lin.StopStart+lin.StopCount)
	}
	if rad.StopStart != mesh.StopStart+mesh.StopCount {
		t.Errorf("radial starts at %d, want %d (immediately after the mesh)", rad.StopStart, mesh.StopStart+mesh.StopCount)
	}

	// The mesh's first vertex is the grid's top-left: red at (0,0).
	v := e.Stops[mesh.StopStart]
	if v.R != 1 || v.G != 0 || v.B != 0 || v.A != 1 || v.X != 0 || v.Y != 0 {
		t.Errorf("mesh vertex 0 = %+v, want red at (0,0); the mesh is reading a gradient's records", v)
	}
	// A gradient's records carry Offset and no position, and the mesh's carry the
	// reverse. Confirming both directions is what proves the two kinds are not
	// simply overwriting one another.
	if s := e.Stops[lin.StopStart+1]; s.Offset != 1 || s.X != 0 || s.Y != 0 {
		t.Errorf("linear stop 1 = %+v, want Offset=1 with unused X/Y", s)
	}
}

// The upload is a raw byte copy of the Go slice, so the WGSL struct must agree on
// size and field order. Nothing else checks this: a mismatch shifts every field
// and renders garbage, and the only symptom is a parity failure with no pointer
// at the cause.
func TestStopRecordMatchesTheShaderLayout(t *testing.T) {
	// off, r, g, b, a, x, y — seven f32, tightly packed, no padding.
	if got, want := unsafe.Sizeof(Stop{}), uintptr(28); got != want {
		t.Errorf("sizeof(Stop) = %d, want %d; raster.wgsl declares seven f32", got, want)
	}
	var s Stop
	base := uintptr(unsafe.Pointer(&s))
	for _, f := range []struct {
		name string
		off  uintptr
		want uintptr
	}{
		{"Offset", uintptr(unsafe.Pointer(&s.Offset)) - base, 0},
		{"R", uintptr(unsafe.Pointer(&s.R)) - base, 4},
		{"A", uintptr(unsafe.Pointer(&s.A)) - base, 16},
		{"X", uintptr(unsafe.Pointer(&s.X)) - base, 20},
		{"Y", uintptr(unsafe.Pointer(&s.Y)) - base, 24},
	} {
		if f.off != f.want {
			t.Errorf("Stop.%s at offset %d, want %d", f.name, f.off, f.want)
		}
	}
}
