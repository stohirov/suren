package paint

import (
	"math"
	"testing"

	"github.com/stohirov/suren/geom"
)

// The FMA gate for MeshAt. See MeshAt's comment for the full finding; this file
// is what keeps it fixed.
//
// Phase 13 claimed FMA contraction was unobservable at 8 bits because f64 has
// ~13 orders of headroom. It is observable, because quantization is a threshold
// and a threshold has no headroom in front of it. Go fuses `x*y + z` on arm64 and
// not on amd64, so an unguarded MeshAt computes a different colour per
// architecture and CI went red on the mesh golden the first time it ran on amd64.
//
// WHY THIS TEST EXISTS RATHER THAN JUST CI: a gate that only fails on amd64 is a
// gate nobody developing on this machine can see. Deleting the float64()
// conversions in MeshAt is a one-keystroke "cleanup" that looks harmless and is
// not, and the person doing it is on arm64. This test fails on arm64 too, because
// it pins the VALUE rather than the architecture.

// meshFMAFixture is the exact triangle and query point at pixel (75,96) of
// internal/sample.MeshScene — the pixel where the divergence was found. The
// numbers are inlined rather than imported because internal/sample imports paint,
// so the dependency cannot run the other way.
//
// They must NOT be written as constant expressions in the arithmetic under test:
// Go evaluates untyped constants in arbitrary precision, which constant-folds the
// fusion away and makes the test pass vacuously on every architecture. Passing
// them through a package-level var defeats that, and TestMeshAtDoesNotFuse would
// otherwise be exactly the kind of test this project calls worse than none.
var meshFMAFixture = MeshTriangle{V: [3]MeshVertex{
	{P: geom.Pt(73.33333333333333, 79.33333333333333),
		Color: Color{R: 0.6833333333333333, G: 0.6166666666666667, B: 0.5833333333333334, A: 1}},
	{P: geom.Pt(106, 112),
		Color: Color{R: 0.9500000000000001, G: 0.8, B: 0.25, A: 1}},
	{P: geom.Pt(73.33333333333333, 112),
		Color: Color{R: 0.6833333333333333, G: 0.8, B: 0.45, A: 1}},
}}

var meshFMAQuery = geom.Pt(75.5, 96.5)

// TestMeshAtDoesNotFuse pins the blue channel's exact bit pattern.
//
// 0x3fe0000000000000 is 0.5. The fused answer is 0x3fdfffffffffffff (0.5 minus
// one ULP), so this fails on arm64 the moment a conversion is dropped.
//
// The exact value, by rational arithmetic, is 0.5 + 1.36e-17 — just above the
// tie. 0.5 quantizes to 32767.5, which round-half-up carries to 32768, the
// correct side. The fused value quantizes to 32767.49… and truncates to 32767,
// which is wrong by one. So this test pins the ACCURATE answer, not merely a
// portable one; the two happen to coincide, and that is not the direction anyone
// guesses first.
func TestMeshAtDoesNotFuse(t *testing.T) {
	c, in := MeshAt([]MeshTriangle{meshFMAFixture}, meshFMAQuery)
	if !in {
		t.Fatal("fixture point must be inside the fixture triangle; the fixture has drifted")
	}

	const wantBits = 0x3fe0000000000000 // 0.5
	got := math.Float64bits(c.B)
	if got != wantBits {
		t.Errorf("MeshAt blue = %.20g (bits 0x%016x), want 0.5 (bits 0x%016x).\n"+
			"If this is 0x3fdfffffffffffff, the compiler contracted a multiply-add in MeshAt:\n"+
			"a float64() conversion was removed. They are load-bearing — see MeshAt's comment\n"+
			"and Phase 13 in docs/roadmap.md. This value is BOTH the portable one and the\n"+
			"accurate one; the fused answer is wrong by one 8-bit level at this pixel.",
			c.B, got, uint64(wantBits))
	}
}

// TestMeshFMAFixtureStraddlesTheTie is the guard on the guard.
//
// TestMeshAtDoesNotFuse is only meaningful while its fixture still lands on a
// quantization tie. If someone edits the fixture to a comfortable interior value,
// that test keeps passing and stops testing anything — fusion would no longer
// change the result. This asserts the property that makes the fixture worth
// having: the channel sits exactly on 32767.5, the knife edge of round-half-up.
func TestMeshFMAFixtureStraddlesTheTie(t *testing.T) {
	c, in := MeshAt([]MeshTriangle{meshFMAFixture}, meshFMAQuery)
	if !in {
		t.Fatal("fixture point must be inside the fixture triangle")
	}
	x := c.B * clamp01(c.A) * 0xffff
	if x != 32767.5 {
		t.Errorf("fixture blue quantizes to %.20g, want exactly 32767.5.\n"+
			"The fixture no longer sits on the tie, so TestMeshAtDoesNotFuse can no longer\n"+
			"observe fusion and has become a test that cannot fail. Restore the fixture, or\n"+
			"find another tie pixel and record how it was found.", x)
	}
}
