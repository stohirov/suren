package raster

import (
	"image"
	"testing"
)

// premulShader returns a fixed premultiplied sample in the 0..255 convention
// solidShader uses.
type premulShader [4]float64

func (s premulShader) RGBA(int, int) (r, g, b, a float64) { return s[0], s[1], s[2], s[3] }

func blendPx(dst [4]uint8, src [4]float64, cov float64, mode BlendMode, comp CompositeOp) [4]uint8 {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	copy(img.Pix, dst[:])
	blend(img, 0, 0, premulShader(src), cov, mode, comp)
	return [4]uint8(img.Pix[0:4])
}

func near(a, b [4]uint8, tol int) bool {
	for i := range a {
		d := int(a[i]) - int(b[i])
		if d < 0 {
			d = -d
		}
		if d > tol {
			return false
		}
	}
	return true
}

// The three operators with constant coefficients have answers that need no
// arithmetic to predict, which makes them the one place the table can be checked
// against the definition rather than against itself.
func TestPorterDuffDegenerateOps(t *testing.T) {
	backdrop := [4]uint8{200, 100, 50, 255}
	src := [4]float64{180, 150, 30, 200} // premultiplied, αs≈0.78

	for _, tc := range []struct {
		name string
		comp CompositeOp
		want [4]uint8
	}{
		{"Clear erases", Clear, [4]uint8{0, 0, 0, 0}},
		{"Dst leaves the backdrop", Dst, backdrop},
		{"Src replaces with the source", Src, [4]uint8{180, 150, 30, 200}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := blendPx(backdrop, src, 1, Normal, tc.comp); !near(got, tc.want, 1) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestPorterDuffCoverageIsNotSourceAlpha is the phase's central correctness
// claim. Coverage is the fraction of the pixel the operator applies to, so a
// half-covered Clear must halve the pixel. Folding coverage into αs — the
// generalization SrcOver's fast path invites, since it is correct there — gives
// (Fa,Fb)=(0,0) at any coverage and erases the pixel outright.
func TestPorterDuffCoverageIsNotSourceAlpha(t *testing.T) {
	opaque := [4]uint8{255, 255, 255, 255}
	src := [4]float64{255, 255, 255, 255}

	got := blendPx(opaque, src, 0.5, Normal, Clear)
	if want := ([4]uint8{128, 128, 128, 128}); !near(got, want, 1) {
		t.Errorf("Clear at 50%% coverage = %v, want %v (coverage folded into αs would give all-zero)", got, want)
	}

	// DstOut with an opaque source is Clear's coefficients (Fb = 1-αs = 0), so it
	// is the same trap reached through an αs-dependent row of the table.
	got = blendPx(opaque, src, 0.25, Normal, DstOut)
	if want := ([4]uint8{191, 191, 191, 191}); !near(got, want, 1) {
		t.Errorf("DstOut at 25%% coverage = %v, want %v", got, want)
	}
}

// TestPorterDuffCoverageIsLinear pins the coverage lerp's shape for every
// operator: partial coverage must interpolate between the fully-applied operator
// and the untouched backdrop, and nothing else. The oracle is independent — it
// reads only the cov=1 result and the backdrop, never the coefficient table — so
// it holds each operator to its own full-strength answer.
//
// It replaces a test named TestPorterDuffFullCoverageIsUnlerped that could not
// fail: it compared one deterministic call to itself, hardcoded Src while
// ignoring its loop variable, and used a zero backdrop that annihilates the lerp
// term regardless. It survived deleting the lerp it was named after. Coverage of
// cov=1 is now the cov=1 row of this table, and the lerp itself is finally
// checked at the coverages where it exists.
func TestPorterDuffCoverageIsLinear(t *testing.T) {
	backdrops := [][4]uint8{{200, 100, 50, 255}, {60, 30, 15, 128}, {0, 0, 0, 0}}
	sources := [][4]float64{{180, 150, 30, 200}, {255, 255, 255, 255}, {0, 0, 0, 0}}

	for _, bd := range backdrops {
		for _, sc := range sources {
			for comp := SrcOver; comp <= Xor; comp++ {
				full := blendPx(bd, sc, 1, Normal, comp)
				for _, cov := range []float64{0.25, 0.5, 0.75} {
					got := blendPx(bd, sc, cov, Normal, comp)
					for c := range 4 {
						want := float64(full[c])*cov + float64(bd[c])*(1-cov)
						if d := float64(got[c]) - want; d > 1.5 || d < -1.5 {
							t.Errorf("op %d backdrop=%v src=%v cov=%v chan %d: got %d, want ~%.1f (lerp of full=%d and backdrop=%d)",
								comp, bd, sc, cov, c, got[c], want, full[c], bd[c])
						}
					}
				}
			}
		}
	}
}

// TestDstOverIsSrcOverSwapped checks the table against an algebraic identity
// rather than against a recomputation of itself: DstOver(s, b) is by definition
// SrcOver(b, s). It catches a transposed Fa/Fb row, which is the likeliest way
// to get this table wrong and the hardest to see by reading it.
func TestDstOverIsSrcOverSwapped(t *testing.T) {
	for _, c := range [][2][4]uint8{
		{{200, 100, 50, 255}, {90, 120, 30, 180}},
		{{10, 20, 30, 40}, {250, 250, 250, 255}},
		{{0, 0, 0, 0}, {120, 60, 200, 220}},
		{{100, 50, 25, 128}, {0, 0, 0, 0}},
	} {
		backdrop, source := c[0], c[1]
		srcF := [4]float64{float64(source[0]), float64(source[1]), float64(source[2]), float64(source[3])}
		bkF := [4]float64{float64(backdrop[0]), float64(backdrop[1]), float64(backdrop[2]), float64(backdrop[3])}

		got := blendPx(backdrop, srcF, 1, Normal, DstOver)
		want := blendPx(source, bkF, 1, Normal, SrcOver)
		if !near(got, want, 1) {
			t.Errorf("DstOver(src=%v, dst=%v) = %v, but SrcOver with the operands swapped = %v",
				source, backdrop, got, want)
		}
	}
}

// TestPorterDuffGeneralizesSrcOver backs the claim in blend()'s comment: the
// fast premultiplied path is the general form specialized, not a different
// operator. If they ever disagree by more than the LSB that separates two
// float orderings, one of them is wrong — and since SrcOver never routes through
// porterDuff in production, nothing else in the tree would notice.
func TestPorterDuffGeneralizesSrcOver(t *testing.T) {
	backdrops := [][4]uint8{{0, 0, 0, 0}, {200, 100, 50, 255}, {60, 30, 15, 128}}
	sources := [][4]float64{{180, 150, 30, 200}, {255, 255, 255, 255}, {0, 0, 0, 0}, {12, 9, 3, 15}}
	covs := []float64{1, 0.75, 0.5, 0.125}

	for _, bd := range backdrops {
		for _, sc := range sources {
			for _, cov := range covs {
				for _, mode := range []BlendMode{Normal, Multiply, Overlay, ColorDodge} {
					fast := blendPx(bd, sc, cov, mode, SrcOver)

					img := image.NewRGBA(image.Rect(0, 0, 1, 1))
					copy(img.Pix, bd[:])
					porterDuff(img.Pix[0:4], sc[0], sc[1], sc[2], sc[3], cov, mode, SrcOver)
					general := [4]uint8(img.Pix[0:4])

					if !near(fast, general, 1) {
						t.Errorf("SrcOver diverges: backdrop=%v src=%v cov=%v mode=%d: fast=%v general=%v",
							bd, sc, cov, mode, fast, general)
					}
				}
			}
		}
	}
}

// alphaOracle is the output alpha of each operator, written from Porter-Duff's
// coverage-geometry definition rather than from the (Fa,Fb) table: treat αs and
// αb as the areas of two independent subsets of the pixel, and each operator
// names which parts of that Venn diagram survive. SrcIn keeps the intersection,
// SrcOut the source-only part, Xor everything but the intersection, and so on.
//
// Deriving it independently is the point. Checking αo against αs·Fa + αb·Fb
// would restate Coefficients rather than test it: every row could be transposed
// and the check would still pass. Only Clear, Src, Dst, SrcOver and DstOver are
// otherwise pinned in this file — the remaining seven rows have no independent
// witness but this one.
func alphaOracle(comp CompositeOp, as, ab float64) float64 {
	switch comp {
	case Clear:
		return 0
	case Src:
		return as
	case Dst:
		return ab
	case SrcOver:
		return as + ab - as*ab
	case DstOver:
		return as + ab - as*ab
	case SrcIn:
		return as * ab
	case DstIn:
		return as * ab
	case SrcOut:
		return as * (1 - ab)
	case DstOut:
		return ab * (1 - as)
	case SrcAtop:
		return ab
	case DstAtop:
		return as
	case Xor:
		return as*(1-ab) + ab*(1-as)
	}
	panic("unknown op")
}

// TestPorterDuffAlphaOracle checks every operator's output alpha against the
// coverage geometry it is defined by, over three backdrops and three sources.
// Alpha is also independent of the blend mode by construction — blending moves
// color, never coverage — so a mode leaking into alpha is a category error, not
// a rounding one, and this runs each case under three modes to catch it.
//
// The loop starts at SrcOver, which is 0. It used to start at Clear (1) while
// claiming to cover everything, leaving SrcOver — the default every scene in the
// tree uses — untested here and alphaOracle's SrcOver branch dead.
func TestPorterDuffAlphaOracle(t *testing.T) {
	for _, bd := range [][4]uint8{{0, 0, 0, 0}, {200, 100, 50, 255}, {60, 30, 15, 128}} {
		for _, sc := range [][4]float64{{180, 150, 30, 200}, {0, 0, 0, 0}, {255, 255, 255, 255}} {
			for _, cov := range []float64{1, 0.5} {
				for comp := SrcOver; comp <= Xor; comp++ {
					as, ab := sc[3]/255, float64(bd[3])/255
					// Coverage picks between the composited alpha and the backdrop's.
					want := clamp8((alphaOracle(comp, as, ab)*cov + ab*(1-cov)) * 255)

					for _, mode := range []BlendMode{Normal, Multiply, Difference} {
						got := blendPx(bd, sc, cov, mode, comp)[3]
						if int(got)-int(want) > 1 || int(want)-int(got) > 1 {
							t.Errorf("op %d mode %d backdrop α=%d src α=%v cov=%v: αo = %d, want %d",
								comp, mode, bd[3], sc[3], cov, got, want)
						}
					}
				}
			}
		}
	}
}

// TestPorterDuffColorSource pins WHICH color each operator keeps, which alpha
// alone cannot see: SrcIn and DstIn have identical output alpha (αs·αb) and
// opposite colors, so a swap between them passes TestPorterDuffAlphaOracle
// unnoticed. Same for SrcOut/DstOut and SrcAtop/DstAtop.
func TestPorterDuffColorSource(t *testing.T) {
	// Opaque backdrop, opaque source, full coverage: every "in"-family operator
	// then reduces to picking one of the two colors outright.
	red := [4]uint8{255, 0, 0, 255}
	blueF := [4]float64{0, 0, 255, 255}

	for _, tc := range []struct {
		comp CompositeOp
		want [4]uint8
		why  string
	}{
		{SrcIn, [4]uint8{0, 0, 255, 255}, "source inside an opaque backdrop is the source"},
		{DstIn, [4]uint8{255, 0, 0, 255}, "backdrop inside an opaque source is the backdrop"},
		{SrcAtop, [4]uint8{0, 0, 255, 255}, "source atop an opaque backdrop is the source"},
		{DstAtop, [4]uint8{255, 0, 0, 255}, "backdrop atop an opaque source is the backdrop"},
		{SrcOut, [4]uint8{0, 0, 0, 0}, "source outside an opaque backdrop is nothing"},
		{DstOut, [4]uint8{0, 0, 0, 0}, "backdrop outside an opaque source is nothing"},
		{Xor, [4]uint8{0, 0, 0, 0}, "two opaque coincident shapes cancel"},
	} {
		if got := blendPx(red, blueF, 1, Normal, tc.comp); !near(got, tc.want, 1) {
			t.Errorf("op %d: got %v, want %v (%s)", tc.comp, got, tc.want, tc.why)
		}
	}
}
