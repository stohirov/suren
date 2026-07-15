package gpu

import (
	"testing"

	"github.com/stohirov/sukho/paint"
)

// TestPackFlagsRoundTrip checks every blend×composite pair unpacks to what it
// packed, using the same shifts raster.wgsl's composite() applies. The shader is
// the real consumer and cannot be unit-tested from Go, so this pins the contract
// the two sides share; if the WGSL ever moves a shift, the crossed corpus
// entries (composite-x-blend-*) are what catch it.
//
// All 144 pairs, because the failure mode is arithmetic: a mask one bit too
// narrow only shows up for operators whose index sets the bit it drops.
func TestPackFlagsRoundTrip(t *testing.T) {
	modes := []paint.BlendMode{
		paint.Normal, paint.Multiply, paint.Screen, paint.Overlay, paint.Darken, paint.Lighten,
		paint.ColorDodge, paint.ColorBurn, paint.HardLight, paint.SoftLight, paint.Difference, paint.Exclusion,
	}
	ops := []paint.CompositeOp{
		paint.SrcOver, paint.Clear, paint.Src, paint.Dst, paint.DstOver, paint.SrcIn,
		paint.DstIn, paint.SrcOut, paint.DstOut, paint.SrcAtop, paint.DstAtop, paint.Xor,
	}

	for _, m := range modes {
		for _, o := range ops {
			f := packFlags(m, o)
			gotMode := paint.BlendMode(f & 0xF)
			gotOp := paint.CompositeOp((f >> 4) & 0xF)
			if gotMode != m || gotOp != o {
				t.Errorf("packFlags(%d, %d) = %#x -> (mode=%d, op=%d)", m, o, f, gotMode, gotOp)
			}
		}
	}
}

// The enums must stay inside the four bits each is given. Adding a thirteenth
// blend mode would silently set the composite axis's low bit instead of failing
// to compile, so this is the guard for a change made in paint/ that never looks
// at this file.
func TestFlagsFitTheirFields(t *testing.T) {
	if paint.Exclusion > 0xF {
		t.Errorf("blend modes overflow bits 0-3: highest is %d", paint.Exclusion)
	}
	if paint.Xor > 0xF {
		t.Errorf("composite ops overflow bits 4-7: highest is %d", paint.Xor)
	}
}
