package paint

import (
	"math"
	"testing"

	"github.com/stohirov/sukho/geom"
)

func tri(p0, p1, p2 geom.Point, c0, c1, c2 Color) MeshTriangle {
	return MeshTriangle{V: [3]MeshVertex{{p0, c0}, {p1, c1}, {p2, c2}}}
}

var (
	red   = RGBA(1, 0, 0, 1)
	green = RGBA(0, 1, 0, 1)
	blue  = RGBA(0, 0, 1, 1)
)

func near(a, b Color) bool {
	const tol = 1e-9
	return math.Abs(a.R-b.R) < tol && math.Abs(a.G-b.G) < tol &&
		math.Abs(a.B-b.B) < tol && math.Abs(a.A-b.A) < tol
}

// The independent witness for what a Gouraud triangle MEANS. Parity compares the
// two backends against each other and the golden compares the CPU against itself,
// so both are blind to an interpolation that is wrong the same way on both sides
// — a transposed vertex or a barycentric solved for the wrong corner would render
// identically everywhere and pass every gate in the tree. The expectations here
// are hand-computed instead.
func TestMeshAtInterpolatesBarycentrically(t *testing.T) {
	m := []MeshTriangle{tri(geom.Pt(0, 0), geom.Pt(12, 0), geom.Pt(0, 12), red, green, blue)}

	for _, tc := range []struct {
		name string
		q    geom.Point
		want Color
	}{
		// Each corner must reproduce its OWN colour exactly. This is what catches a
		// transposition: swap two vertices and one of these three fails.
		{"corner 0", geom.Pt(0, 0), red},
		{"corner 1", geom.Pt(12, 0), green},
		{"corner 2", geom.Pt(0, 12), blue},
		// The centroid weights all three equally, by definition.
		{"centroid", geom.Pt(4, 4), RGBA(1.0/3, 1.0/3, 1.0/3, 1)},
		// Edge midpoints: the vertex opposite the edge drops out entirely.
		{"midpoint of edge 0-1", geom.Pt(6, 0), RGBA(0.5, 0.5, 0, 1)},
		{"midpoint of edge 0-2", geom.Pt(0, 6), RGBA(0.5, 0, 0.5, 1)},
		{"midpoint of edge 1-2", geom.Pt(6, 6), RGBA(0, 0.5, 0.5, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, in := MeshAt(m, tc.q)
			if !in {
				t.Fatalf("MeshAt(%v) reported outside the triangle", tc.q)
			}
			if !near(got, tc.want) {
				t.Errorf("MeshAt(%v) = %v, want %v", tc.q, got, tc.want)
			}
		})
	}
}

func TestMeshAtIsTransparentOutsideEveryTriangle(t *testing.T) {
	m := []MeshTriangle{tri(geom.Pt(0, 0), geom.Pt(12, 0), geom.Pt(0, 12), red, green, blue)}
	// Well clear of the hypotenuse, so MeshEps cannot reach it.
	if c, in := MeshAt(m, geom.Pt(11, 11)); in {
		t.Errorf("MeshAt outside the mesh = %v, in=%v; want transparent and outside", c, in)
	}
}

// A zero-area triangle makes the barycentric denominator vanish. Skipping it is
// the contract: dividing would produce infinities that the inside test then
// compares, which is undefined behaviour dressed as a colour.
func TestMeshAtSkipsDegenerateTriangles(t *testing.T) {
	// Three collinear points, followed by a real triangle covering the same area.
	m := []MeshTriangle{
		tri(geom.Pt(0, 0), geom.Pt(6, 6), geom.Pt(12, 12), red, red, red),
		tri(geom.Pt(0, 0), geom.Pt(12, 0), geom.Pt(0, 12), green, green, green),
	}
	got, in := MeshAt(m, geom.Pt(3, 3))
	if !in {
		t.Fatalf("MeshAt fell through the degenerate triangle and missed the real one behind it")
	}
	if !near(got, green) {
		t.Errorf("MeshAt = %v, want the second triangle's %v; the degenerate one was not skipped", got, green)
	}
}

// TestMeshAtAcceptsAPointOnASharedEdge is the crack regression, and it is the
// exact case measured in the field rather than a constructed analogue.
//
// These are the real coordinates from sample.MeshScene's first mesh — a 3x3 grid
// over rect (8,14) 98x98 — and q is the real pixel centre (14.5,20.5) that lands
// on the shared diagonal of quad 0. With MeshEps removed, this point's weight
// rounds to -3.5e-17 in one triangle and -8.3e-17 in the other, BOTH tests fail,
// MeshAt returns transparent, and the renderer draws a 41-pixel hairline of
// background through the mesh (Δ=198 against the GPU, which rounds the same real
// zero to +0.0 and gets it right).
//
// The assertion is deliberately at this level rather than on rendered pixels: it
// needs no GPU, and it names the invariant — a point on a shared interior edge
// belongs to SOME triangle — instead of a colour that a later scene edit could
// change.
func TestMeshAtAcceptsAPointOnASharedEdge(t *testing.T) {
	const step = 98.0 / 3
	a := geom.Pt(8, 14)
	b := geom.Pt(8+step, 14)
	c := geom.Pt(8+step, 14+step)
	d := geom.Pt(8, 14+step)
	// The two triangles sharing diagonal a-c, in the order meshGrid emits them.
	m := []MeshTriangle{tri(a, b, c, red, red, red), tri(a, c, d, green, green, green)}

	q := geom.Pt(14.5, 20.5)
	if _, in := MeshAt(m, q); !in {
		t.Fatalf("MeshAt(%v) is outside BOTH triangles sharing the edge it lies on: the mesh cracks along every interior diagonal. MeshEps is what closes this.", q)
	}
}

// The slack must not swallow the mesh's actual boundary: an epsilon large enough
// to close the crack but small enough to leave the silhouette where it belongs is
// the whole balance MeshEps strikes, and a later "just make it bigger" would break
// this rather than the test above.
func TestMeshEpsDoesNotWidenTheMeshVisibly(t *testing.T) {
	m := []MeshTriangle{tri(geom.Pt(0, 0), geom.Pt(100, 0), geom.Pt(0, 100), red, green, blue)}
	// A hundredth of a pixel outside the hypotenuse of a 100px triangle. If this
	// reads as inside, the slack has grown into something a viewer could see.
	if _, in := MeshAt(m, geom.Pt(50.005, 50.005)); in {
		t.Errorf("MeshEps admits a point 0.01px outside a 100px triangle; the slack is no longer negligible at the silhouette")
	}
}
