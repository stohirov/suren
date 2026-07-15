// Package parity defines the correctness contract between sukho's renderers and
// the harness that enforces it.
//
// Correctness in this project is not "looks right" — it is parity: the GPU
// renderer must reproduce what the CPU reference renderer produces, and the
// measurement of "must reproduce" has a budget with an owner. Every parity gate
// in the tree cites this package; no test spells a parity tolerance as a bare
// literal.
//
// # Tolerance, defined once
//
// Comparison is over raw premultiplied RGBA channels. Three tiers exist, and
// only three:
//
//   - Identical (Δ=0) is REQUIRED wherever both backends run the same
//     integer/analytic path, so there is no float pipeline to diverge. Today
//     that is the many-nodes and rect-clip scenes, and any render after a
//     resize. A regression here is a logic bug, never a rounding artifact, so
//     the gate is zero and stays zero.
//
//   - Quantized (Δ≤1) is the FLOOR for anything crossing independent float
//     pipelines, and it is quantization rather than error. The CPU composites in
//     f64 and quantizes with clamp8(v+0.5) (raster/fill.go); the GPU composites
//     in f32 and quantizes when the shader stores to an rgba8unorm texture,
//     where the f32→u8 conversion belongs to the driver. Two pipelines that
//     compute the same real value can land on opposite sides of a .5 boundary
//     and differ by one least-significant bit. Driving that to 0 would mean
//     making one backend reproduce the other's rounding rather than the true
//     value, which is worse. Phase 13 pins the rounding rules; until then Δ≤1 is
//     the honest floor.
//
//   - Budget (Δ>1) is a bug budget with an owner, not a free parameter. It is
//     admitted only where a specific operation is known to diverge, and Why must
//     name that operation — Validate rejects a budget that does not. New
//     features may not silently widen a gate: raising a tolerance means writing
//     down which arithmetic forced it. The only budgets today are the two
//     division-based blend modes, where f32-vs-f64 divergence is amplified
//     through the min(1,·) clamp (ColorDodge Δ≤2, ColorBurn Δ≤3).
//
// # Two comparison modes, named
//
// Exact is the primary gate: max per-channel delta over premultiplied RGBA. It
// is colorspace-free — it never decodes, so it cannot hide a divergence behind a
// transfer function.
//
// Perceptual (ΔE + SSIM over sRGB→Lab) exists only for features where exactness
// is provably unreachable — image resampling with f32 kernels, mesh-gradient
// patch interpolation. It never REPLACES Exact; it is a second gate for a named
// subset, and wherever it is used the Exact failure that forced it is recorded,
// which is why Perceptual requires a Why just as Budget does.
//
// The perceptual gate has three parts, because ΔE alone would not be sound here:
//
//   - ΔE is measured over the frame composited against opaque black. The
//     premultiplied RGB IS that composite, so this is a total function — no
//     unpremultiply division, no undefined color at alpha 0, and no divergence
//     amplified by a small alpha.
//   - Compositing over black makes two pixels that differ ONLY in alpha
//     (transparent black vs opaque black) indistinguishable to ΔE, so alpha is
//     gated separately by Tol.
//   - SSIM (luma, 11×11 Gaussian σ=1.5, Wang et al.) catches structural drift
//     that a per-pixel color metric averages away.
//
// ΔE is CIE76, not CIEDE2000. CIE76 is known to overestimate distance for
// saturated colors, which is a defect when ranking arbitrary color pairs but not
// when gating two renders that should already be near-identical: in that regime
// it can only be stricter than CIEDE2000, and a stricter gate cannot admit a
// divergence CIEDE2000 would have caught. Upgrade to CIEDE2000 if a real feature
// ever needs finer discrimination than "these should be nearly the same".
//
// # The AA contract
//
// Both backends use analytic signed-area coverage — one coverage value per
// pixel, computed by raster.coverage() and ported verbatim to raster.wgsl. There
// is no sampling. Analytic AA IS the contract.
//
// MSAA and supersampling are alternative AA models, not implementations of this
// one: they answer a different question about what a pixel means, and their
// output cannot be bit-compared against analytic coverage. They are therefore
// out of scope for this parity gate. If either is ever added it gets its own
// golden set at perceptual tolerance and is never diffed against analytic
// output. Hinted/subpixel glyph AA falls under the same clause.
package parity

import "fmt"

type Mode int

const (
	ModeExact Mode = iota
	ModePerceptual
)

func (m Mode) String() string {
	switch m {
	case ModeExact:
		return "exact"
	case ModePerceptual:
		return "perceptual"
	}
	return fmt.Sprintf("Mode(%d)", int(m))
}

const QuantizationFloor = 1

type Config struct {
	Mode      Mode
	Tol       int
	MaxDeltaE float64
	MinSSIM   float64
	Why       string
}

func Identical() Config { return Config{Mode: ModeExact, Tol: 0} }

func Quantized() Config { return Config{Mode: ModeExact, Tol: QuantizationFloor} }

func Budget(tol int, why string) Config {
	return Config{Mode: ModeExact, Tol: tol, Why: why}
}

func Perceptual(maxDeltaE, minSSIM float64, why string) Config {
	return Config{Mode: ModePerceptual, Tol: QuantizationFloor, MaxDeltaE: maxDeltaE, MinSSIM: minSSIM, Why: why}
}

func (c Config) Validate() error {
	if c.Tol < 0 {
		return fmt.Errorf("negative tolerance %d", c.Tol)
	}
	switch c.Mode {
	case ModeExact:
		if c.Tol > QuantizationFloor && c.Why == "" {
			return fmt.Errorf("tolerance %d exceeds the quantization floor (%d) without naming the operation responsible; use Budget(tol, why)", c.Tol, QuantizationFloor)
		}
	case ModePerceptual:
		if c.Why == "" {
			return fmt.Errorf("perceptual mode without recording the exact-mode failure that forced it; use Perceptual(maxDeltaE, minSSIM, why)")
		}
		if c.MaxDeltaE <= 0 {
			return fmt.Errorf("perceptual mode needs a positive MaxDeltaE, got %v", c.MaxDeltaE)
		}
		if c.MinSSIM <= 0 || c.MinSSIM > 1 {
			return fmt.Errorf("perceptual mode needs MinSSIM in (0,1], got %v", c.MinSSIM)
		}
	default:
		return fmt.Errorf("unknown mode %v", c.Mode)
	}
	return nil
}

func (c Config) String() string {
	var s string
	switch c.Mode {
	case ModePerceptual:
		s = fmt.Sprintf("perceptual ΔE≤%g SSIM≥%g α≤%d", c.MaxDeltaE, c.MinSSIM, c.Tol)
	default:
		s = fmt.Sprintf("%s Δ≤%d", c.Mode, c.Tol)
	}
	if c.Why != "" {
		s += ": " + c.Why
	}
	return s
}
