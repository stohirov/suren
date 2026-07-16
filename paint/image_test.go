package paint

import (
	"math"
	"testing"

	"github.com/stohirov/suren/geom"
)

// The independent-oracle leg for image sampling (Phase 17), and the reason it
// exists is the one Phase 16 wrote down for conic and mesh: every other gate in
// this tree compares the two backends against EACH OTHER, or compares the CPU
// against a golden the CPU itself generated. Both are blind to a semantic error the
// two renderers share. An image sampled with i and j transposed, or shifted half a
// texel the wrong way, or with Mirror reflecting about the wrong edge, would render
// identically on both backends, match its golden byte for byte, and pass every
// corpus entry and every fuzz seed.
//
// So these tests ask neither renderer. They hand-compute what sampling MEANS.

// wrapCases is the edge-mode table written out by hand rather than derived from
// the implementation. n=4, so the image is texels 0..3 and the interesting part is
// what happens on either side.
//
// Mirror is the row worth checking by hand: it reflects WITHOUT repeating the edge
// texel, so index -1 is texel 0 and index 4 is texel 3 — the period is 8, not 6.
// The off-by-one alternative (reflecting about the texel centre rather than the
// edge, period 6) is a perfectly plausible implementation that would make both
// backends agree with each other and disagree with every other renderer.
func TestEdgeModeWrapMatchesAHandWrittenTable(t *testing.T) {
	const n = 4
	cases := []struct {
		mode EdgeMode
		name string
		want map[int]int
	}{
		{Clamp, "clamp", map[int]int{
			-9: 0, -2: 0, -1: 0, 0: 0, 1: 1, 2: 2, 3: 3, 4: 3, 5: 3, 12: 3,
		}},
		{Repeat, "repeat", map[int]int{
			-9: 3, -5: 3, -4: 0, -3: 1, -2: 2, -1: 3, 0: 0, 1: 1, 2: 2, 3: 3, 4: 0, 5: 1, 8: 0, 9: 1,
		}},
		{Mirror, "mirror", map[int]int{
			-1: 0, -2: 1, -3: 2, -4: 3, -5: 3, -6: 2, -8: 0, -9: 0,
			0: 0, 1: 1, 2: 2, 3: 3, 4: 3, 5: 2, 6: 1, 7: 0, 8: 0, 9: 1,
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for i, want := range c.want {
				if got := c.mode.Wrap(i, n); got != want {
					t.Errorf("%s.Wrap(%d, %d) = %d, want %d", c.name, i, n, got, want)
				}
			}
		})
	}
}

// TestWrapIsTotal is the guarantee ImageAt's float→int conversion leans on: no
// matter what integer arrives, the index names a real texel. Without it the clamp
// in ImageAt would be the only thing between a degenerate transform and a read out
// of bounds.
func TestWrapIsTotal(t *testing.T) {
	for _, n := range []int{1, 2, 3, 7} {
		for _, m := range []EdgeMode{Clamp, Repeat, Mirror} {
			for i := -40; i <= 40; i++ {
				if got := m.Wrap(i, n); got < 0 || got >= n {
					t.Fatalf("Wrap(%d, %d) mode %v = %d, outside [0,%d)", i, n, m, got, n)
				}
			}
		}
	}
	// n<=0 is reachable only from an invalid Image, which callers drop — but Wrap
	// is exported and must not divide by zero for anyone.
	if got := Repeat.Wrap(3, 0); got != 0 {
		t.Errorf("Wrap on an empty image = %d, want 0", got)
	}
}

// ramp is a NON-SQUARE image whose texels are all distinct, which is what makes a
// transposed index visible. A square image with symmetric content would let
// Texel(i,j) and Texel(j,i) pass for each other.
func ramp(w, h int) Image {
	pix := make([]uint8, w*h*4)
	for j := range h {
		for i := range w {
			k := (j*w + i) * 4
			pix[k], pix[k+1], pix[k+2], pix[k+3] = uint8(10*i), uint8(100*j), 7, 255
		}
	}
	return Image{W: w, H: h, Pix: pix}
}

func TestTexelIsNotTransposed(t *testing.T) {
	im := ramp(2, 3)
	for j := range 3 {
		for i := range 2 {
			c := im.Texel(i, j)
			if got, want := c.R*255, float64(10*i); got != want {
				t.Errorf("Texel(%d,%d).R = %v, want %v — x must index the ROW, not the column", i, j, got, want)
			}
			if got, want := c.G*255, float64(100*j); got != want {
				t.Errorf("Texel(%d,%d).G = %v, want %v", i, j, got, want)
			}
		}
	}
}

// TestNearestSamplesTheTexelContainingThePoint pins the coordinate convention:
// texel (i,j) covers [i,i+1)x[j,j+1), so the image occupies [0,W]x[0,H] and a texel
// CENTRE is at (i+0.5, j+0.5). The half-texel offset elsewhere in this file is only
// correct relative to this.
func TestNearestSamplesTheTexelContainingThePoint(t *testing.T) {
	im := ramp(4, 4)
	im.Filter = Nearest
	cases := []struct {
		q    geom.Point
		want int // the expected i, read back out of the red channel
	}{
		{geom.Pt(0.5, 0.5), 0},   // centre of texel 0
		{geom.Pt(0.01, 0.5), 0},  // just inside its left edge
		{geom.Pt(0.99, 0.5), 0},  // just inside its right edge
		{geom.Pt(1.0, 0.5), 1},   // ON the boundary: half-open, so the NEXT texel
		{geom.Pt(1.5, 0.5), 1},   // centre of texel 1
		{geom.Pt(3.999, 0.5), 3}, // just inside the last texel
	}
	for _, c := range cases {
		if got := int(ImageAt(im, c.q).R*255 + 0.5); got != 10*c.want {
			t.Errorf("ImageAt(%v).R = %d, want texel %d (red %d)", c.q, got, c.want, 10*c.want)
		}
	}
}

// TestBilinearAtATexelCentreIsThatTexel is the strongest hand-computable statement
// about the kernel: at (i+0.5, j+0.5) the weights are exactly (1,0,0,0), so the
// filter must return the texel UNCHANGED. It catches a half-texel shift in either
// direction — the single most likely way to get bilinear subtly wrong — because a
// sampler missing the -0.5 would return a blend of four texels here instead.
func TestBilinearAtATexelCentreIsThatTexel(t *testing.T) {
	im := ramp(4, 4)
	im.Filter = Bilinear
	for j := range 4 {
		for i := range 4 {
			q := geom.Pt(float64(i)+0.5, float64(j)+0.5)
			got, want := ImageAt(im, q), im.Texel(i, j)
			if got != want {
				t.Errorf("ImageAt(%v) = %+v, want the texel itself %+v — the half-texel shift is off", q, got, want)
			}
		}
	}
}

// TestBilinearMidwayBetweenTwoTexelsIsTheirMean hand-computes the one other point
// where the weights are exact: halfway between two texel centres, both get 1/2.
func TestBilinearMidwayBetweenTwoTexelsIsTheirMean(t *testing.T) {
	im := ramp(4, 4)
	im.Filter = Bilinear
	// Midway between texel (0,0) and (1,0) horizontally; vertically on row 0's
	// centre so the other axis contributes nothing.
	got := ImageAt(im, geom.Pt(1.0, 0.5))
	a, b := im.Texel(0, 0), im.Texel(1, 0)
	want := Color{R: (a.R + b.R) / 2, G: (a.G + b.G) / 2, B: (a.B + b.B) / 2, A: (a.A + b.A) / 2}
	if got != want {
		t.Errorf("ImageAt at the midpoint = %+v, want the mean of the two texels %+v", got, want)
	}
}

// TestBilinearAveragesPremultiplied states what the premultiplied storage buys, and
// the interesting part is that the test can only be written one way round.
//
// Texel 1 is transparent. In STRAIGHT alpha it would still carry a colour — some
// arbitrary green nobody can see — and averaging it with an opaque red would drag
// the midpoint toward green: colour bleeding out of an invisible texel. Stored
// premultiplied, an invisible colour is not REPRESENTABLE (any colour times alpha
// 0 is 0), so there is nothing to bleed. The midpoint is half-alpha red with the
// hue untouched, and that is the whole argument for the storage convention.
func TestBilinearAveragesPremultiplied(t *testing.T) {
	im := Image{W: 2, H: 1, Filter: Bilinear, Edge: Clamp, Pix: []uint8{
		255, 0, 0, 255, // opaque red
		0, 0, 0, 0, // transparent
	}}
	got := ImageAt(im, geom.Pt(1.0, 0.5))
	want := Color{R: 0.5, G: 0, B: 0, A: 0.5}
	if got != want {
		t.Fatalf("midpoint of opaque red and transparent = %+v, want %+v", got, want)
	}
	// The hue is unmoved: un-premultiplying the midpoint gives back pure red. A
	// straight-alpha average against a stored green would have failed here.
	if got.R/got.A != 1 {
		t.Errorf("un-premultiplied midpoint red = %v, want 1 — colour bled from a transparent texel", got.R/got.A)
	}
}

// TestNearestIsATexelCopy pins the arithmetic claim the CPU shader's *255 rests on
// (backend/cpu's imgShader): nearest must introduce NO arithmetic of its own, so
// the round trip through the 0..1 canonical form has to be exact for every byte. It
// is — but "very likely exact for such small integers" is not a proof, and this
// phase's whole finding is that nearest's only divergence is its index. If that were
// false for even one byte the claim would be false too.
func TestNearestIsATexelCopy(t *testing.T) {
	for b := range 256 {
		im := Image{W: 1, H: 1, Filter: Nearest, Pix: []uint8{uint8(b), uint8(b), uint8(b), 255}}
		if got := ImageAt(im, geom.Pt(0.5, 0.5)).R * 255; got != float64(b) {
			t.Fatalf("byte %d round-tripped to %v; nearest is supposed to be a copy", b, got)
		}
	}
}

// TestInvalidImageIsTransparent pins the contract backend/gpu's encoder relies on
// when it drops such a node: dropping is only sound because the reference paints
// nothing either.
func TestInvalidImageIsTransparent(t *testing.T) {
	for _, im := range []Image{
		{W: 0, H: 0},
		{W: 2, H: 2, Pix: make([]uint8, 4)}, // claims 4 texels, holds 1
		{W: -1, H: 3, Pix: make([]uint8, 64)},
	} {
		if got := ImageAt(im, geom.Pt(0.5, 0.5)); got != (Color{}) {
			t.Errorf("ImageAt on %+v = %+v, want transparent", im, got)
		}
	}
}

// TestSamplingADegenerateCoordinateIsDefined is the reason imageCoordLimit exists.
// A caller's NaN or absurd Transform reaches this function as a NaN or huge q, and
// converting that to an int is undefined in Go — a garbage colour is a wrong pixel,
// a garbage index is a different question entirely. The colours below are
// meaningless on purpose; that they exist at all is the assertion.
func TestSamplingADegenerateCoordinateIsDefined(t *testing.T) {
	im := ramp(4, 4)
	nan := math.NaN()
	for _, f := range []Filter{Nearest, Bilinear} {
		im.Filter = f
		for _, m := range []EdgeMode{Clamp, Repeat, Mirror} {
			im.Edge = m
			for _, q := range []geom.Point{
				{X: nan, Y: 0.5}, {X: 0.5, Y: nan},
				{X: 1e300, Y: 0.5}, {X: -1e300, Y: 0.5},
				{X: math.Inf(1), Y: math.Inf(-1)},
			} {
				c := ImageAt(im, q)
				if c.A < 0 || c.A > 1 {
					t.Errorf("ImageAt(%v) filter %v edge %v = %+v, alpha outside [0,1]", q, f, m, c)
				}
			}
		}
	}
}
