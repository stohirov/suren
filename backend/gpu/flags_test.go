package gpu

import (
	"testing"

	"github.com/stohirov/suren/paint"
)

// TestPackFlagsRoundTrip exercises packFlags over its ENTIRE domain — all 16x16
// values each field can hold, not just the 12x12 the enums declare today —
// checking that every pair round-trips through the same shifts raster.wgsl's
// composite() applies, and that no two pairs collide. The shader is the real
// consumer and cannot be unit-tested from Go, so this pins the Go half of the
// contract; if the WGSL moves a shift, the crossed corpus entries
// (composite-x-blend-*) are what catch it.
//
// The full domain matters because the failure mode is arithmetic: a mask one bit
// too narrow, or a shift one too short, only misbehaves for the values that set
// the bit it drops. Testing the declared members alone would leave the 4 unused
// slots in each field — the ones W3C's non-separable blend modes would fill —
// unchecked until the day something occupies them.
//
// This replaces a test that asserted `paint.Exclusion > 0xF`: a comparison of two
// compile-time constants (11 > 15), so its body was unreachable and it could not
// fail. It also read the wrong symbol — a mode appended after Exclusion leaves
// Exclusion at 11, so the guard would pass while the enum overflowed. Four bits
// hold 16 values and both enums are complete at 12 (Porter-Duff has exactly 12;
// blending would reach 16 with W3C's non-separable modes), so the fields have
// room for the only growth either axis has coming, and a 17th member is the point
// at which the packing must widen.
func TestPackFlagsRoundTrip(t *testing.T) {
	seen := map[uint32][2]uint32{}
	for m := uint32(0); m < 16; m++ {
		for o := uint32(0); o < 16; o++ {
			f := packFlags(paint.BlendMode(m), paint.CompositeOp(o))
			if prev, dup := seen[f]; dup {
				t.Fatalf("packFlags is not injective: (mode=%d,op=%d) and (mode=%d,op=%d) both pack to %#x",
					m, o, prev[0], prev[1], f)
			}
			seen[f] = [2]uint32{m, o}
			if gm, gop := f&0xF, (f>>4)&0xF; gm != m || gop != o {
				t.Errorf("packFlags(%d, %d) = %#x -> (mode=%d, op=%d)", m, o, f, gm, gop)
			}
		}
	}
}
