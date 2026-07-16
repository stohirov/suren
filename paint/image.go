package paint

import (
	"math"

	"github.com/stohirov/suren/geom"
)

// Filter selects the reconstruction kernel ImageAt uses to turn a grid of texels
// back into a continuous colour field. Both kernels are stated once, here, and
// ported verbatim to raster.wgsl.
//
// # Why this is written out rather than handed to a sampler
//
// The GPU has hardware bilinear filtering and it is the obvious thing to reach
// for. It is also driver-defined: the WebGPU spec does not pin the weight
// arithmetic, so Metal, Vulkan and DX12 may each round it differently, and no CPU
// reference can match a function whose definition is "whatever this driver does".
// That would put an implementation-defined rule inside the parity contract —
// exactly what Phase 13 removed when it stopped letting the driver round the
// rgba8unorm store and pinned quant8 instead. So raster.wgsl reads texels with
// textureLoad, which is an exact fetch and takes no sampler, and computes the
// weights itself from this file.
type Filter uint8

const (
	// Nearest is the texel containing the sample point. It is a copy, not an
	// average, so it introduces no arithmetic of its own — and it is nonetheless
	// the ill-conditioned one of the two. See ImageAt.
	Nearest Filter = iota
	// Bilinear is the weighted average of the four texels around the sample point.
	Bilinear
)

// EdgeMode decides which texel an out-of-range index names. All three are total:
// every integer maps to a texel, so an Image is defined over the whole plane and
// has no silhouette. That is not a detail — a mesh gradient's outer boundary is a
// discontinuity precisely because it drops to transparent (see MeshGradient), and
// an image paint has no such edge to be ill-conditioned at.
type EdgeMode uint8

const (
	// Clamp extends the border texels outward.
	Clamp EdgeMode = iota
	// Repeat tiles the image. This is the "pattern" of Phase 17's pattern fills:
	// an image, a transform, and a repeat rule are together a tiled pattern.
	Repeat
	// Mirror tiles the image, flipping alternate copies, so the result is
	// continuous across every tile boundary.
	Mirror
)

// Wrap maps any index to [0,n), per the edge mode. It is integer arithmetic on
// both backends — the same additions and remainders in the same order, with no
// float anywhere — so the edge mode itself cannot be a source of divergence. Every
// divergence an Image can have is in the INDEX reaching this function, never in
// what this function does with it.
//
// It is also what makes the float→int conversion in ImageAt safe rather than
// merely unlikely to be a problem: whatever integer arrives, the texel fetched is
// in range by construction.
//
// WGSL's % on i32 is truncated, like Go's, so the two negative-index corrections
// below port across unchanged.
func (m EdgeMode) Wrap(i, n int) int {
	if n <= 0 {
		return 0
	}
	switch m {
	case Repeat:
		i %= n
		if i < 0 {
			i += n
		}
		return i
	case Mirror:
		p := 2 * n
		i %= p
		if i < 0 {
			i += p
		}
		if i >= n {
			return p - 1 - i
		}
		return i
	default:
		if i < 0 {
			return 0
		}
		if i >= n {
			return n - 1
		}
		return i
	}
}

// Image is a rectangle of PREMULTIPLIED RGBA8 texels sampled as a paint. Pix is
// tight and row-major, W*H*4 bytes, texel (i,j) at (j*W+i)*4; the image occupies
// [0,W]x[0,H] in paint space, so texel (i,j) covers [i,i+1]x[j,j+1] and its centre
// is at (i+0.5, j+0.5). The node's Transform maps paint space to device space, the
// same convention gradients use.
//
// # Premultiplied, and it is not a storage convention
//
// Straight-alpha texels would be the obvious choice and they are wrong for
// Bilinear: averaging straight colours across an alpha edge weights a transparent
// texel's colour as if it were visible, which bleeds whatever happens to be in the
// invisible channels into the edge. The average has to be taken on premultiplied
// values. Storing them premultiplied means the sampler never converts, the CPU
// reference and the shader read the same numbers, and it matches both image.RGBA's
// documented semantics and the space this renderer composites in.
//
// The cost is that a caller with straight-alpha pixels must premultiply once, on
// the way in, rather than have every sample do it.
type Image struct {
	W, H   int
	Pix    []uint8
	Filter Filter
	Edge   EdgeMode
}

func (Image) isPaint() {}

// Valid reports whether Pix holds the texels W and H claim. An invalid Image is
// transparent rather than a panic, and both backends agree on that: the encoder
// treats it as an unsupported paint and drops the node, which is only sound
// because ImageAt returns transparent here.
func (im Image) Valid() bool {
	return im.W > 0 && im.H > 0 && len(im.Pix) >= im.W*im.H*4
}

// imageCoordLimit bounds the paint-space coordinate before it is converted to an
// integer, and the bound is chosen rather than guessed: 2^24 is the first float32
// magnitude at which adjacent integers are no longer distinguishable, so past it
// floor() has stopped meaning anything on the GPU regardless of what this file
// does. Below it the conversion is exact in f32 and in f64, and comfortably inside
// int32.
//
// It exists for definedness, not for taste. Converting an out-of-range float to an
// int is undefined in Go and unspecified in WGSL, and this is the first paint
// whose sample point becomes an INDEX — a garbage colour is a wrong pixel, a
// garbage index is a wrong answer to a different question. The same reasoning that
// pins atan2(0,0) to t=0 on both sides in ConicGradient rather than leaving it to
// two languages' corner cases.
const imageCoordLimit = 1 << 24

// clampCoord is written as !(v >= -lim) rather than v < -lim so that a NaN takes
// the first branch instead of falling through to the conversion. A NaN is
// reachable only from a caller's NaN Transform, and the colour it produces is
// meaningless either way; what matters is that it is a meaningless colour and not
// an undefined conversion.
func clampCoord(v float64) float64 {
	if !(v >= -imageCoordLimit) {
		return -imageCoordLimit
	}
	if v > imageCoordLimit {
		return imageCoordLimit
	}
	return v
}

// Texel returns the premultiplied colour at integer texel coordinates, with the
// edge mode applied. Out-of-range is not an error: Wrap makes every index name a
// texel.
func (im Image) Texel(i, j int) Color {
	if !im.Valid() {
		return Color{}
	}
	k := (im.Edge.Wrap(j, im.H)*im.W + im.Edge.Wrap(i, im.W)) * 4
	return Color{
		R: float64(im.Pix[k]) / 255,
		G: float64(im.Pix[k+1]) / 255,
		B: float64(im.Pix[k+2]) / 255,
		A: float64(im.Pix[k+3]) / 255,
	}
}

// ImageAt is the canonical image sampler: raster.wgsl's imageColor is a verbatim
// port, term for term and in the same order, and the two must not drift. Stated
// once here for the reason MeshAt and raster.Coefficients are stated once.
//
// q is in paint space. The returned colour is PREMULTIPLIED, like the texels it
// averages — see Image.
//
// # The plan predicted the two filters' conditioning backwards
//
// Phase 17 planned "nearest is exact; bilinear is the first real candidate for
// perceptual mode + Phase 14 fallback", reasoning that an average of four texels
// in f32 cannot match an average in f64. The averaging is not the question. The
// question is whether the paint is CONTINUOUS in the pixel position, because that
// is what decides whether the f32-vs-f64 difference in q stays sub-LSB or gets
// amplified — the lesson ConicGradient's seam and MeshGradient's silhouette each
// taught once already.
//
//   - Bilinear is continuous, including across every tile boundary and every edge
//     mode, since all three are total. Near a texel boundary the two backends may
//     pick different i0 — but then fx is near 1 on one side and near 0 on the
//     other, and the weighted average lands in the same place. A sub-LSB difference
//     in q produces a sub-LSB difference in colour, which the quantization floor
//     absorbs. It needs no budget, no perceptual mode and no fallback.
//
//   - Nearest is a STEP FUNCTION. It introduces no arithmetic, which is what made
//     it look exact, and it is discontinuous at every texel boundary, which is what
//     matters. floor(q) is a threshold, and a threshold has no headroom in front of
//     it: the two backends do not need to disagree by much, only to disagree at
//     all, and then the colour jumps a whole texel. The magnitude is set by the
//     TEXELS, not by the arithmetic, so — exactly like a conic's seam — there is no
//     derivative to bound and no budget to fit. It is bounded in extent rather than
//     magnitude: only a pixel whose q lands within f32's reach of a texel boundary
//     can diverge, so it is rare rather than mild.
//
// Measured, not argued: see TestNearestDivergesAtATexelBoundary, which puts a
// texel boundary inside f32's blind spot and gets Δ=255 with both backends
// individually correct, and TestNearestIsExactWhenF32CanResolveIt, the control at
// the same geometry without the nudge. An exactly-representable transform is safe
// even when every pixel lands exactly ON a boundary — a 2x downscale puts q on an
// integer for every pixel and both backends floor it identically — because an
// exact tie is not a disagreement. It is the near-ties that diverge.
//
// So the hedges the plan budgeted for bilinear are unused, and the filter it
// called exact is the one that carries the hazard. Both sit at Δ=1 on the corpus;
// what differs is whether that is a property or a fact about one scene.
func ImageAt(im Image, q geom.Point) Color {
	if !im.Valid() {
		return Color{}
	}
	if im.Filter == Bilinear {
		// The half-texel shift takes q from "distance across the image" to "distance
		// between texel CENTRES", which is what the weights interpolate between.
		u := clampCoord(q.X - 0.5)
		v := clampCoord(q.Y - 0.5)
		i0 := int(math.Floor(u))
		j0 := int(math.Floor(v))
		fx := u - math.Floor(u)
		fy := v - math.Floor(v)
		top := lerp(im.Texel(i0, j0), im.Texel(i0+1, j0), fx)
		bot := lerp(im.Texel(i0, j0+1), im.Texel(i0+1, j0+1), fx)
		return lerp(top, bot, fy)
	}
	return im.Texel(int(math.Floor(clampCoord(q.X))), int(math.Floor(clampCoord(q.Y))))
}
