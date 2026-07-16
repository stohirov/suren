package gpu

import (
	"bytes"
	"image"
	"testing"

	"github.com/stohirov/suren/backend/cpu"
	"github.com/stohirov/suren/geom"
	"github.com/stohirov/suren/internal/parity"
	"github.com/stohirov/suren/paint"
	"github.com/stohirov/suren/path"
	"github.com/stohirov/suren/render"
	"github.com/stohirov/suren/scene"
)

const (
	stripeW, stripeH = 32, 16
	stripeN          = 8
)

// stripes is a probe image, not a pretty one: opaque black and white columns, so
// a sample point that lands one texel over reads the OPPOSITE colour and the
// divergence is the maximum a channel can hold. Alpha is constant so nothing but
// the index can move a pixel.
func stripes(n int) paint.Image {
	pix := make([]uint8, n*n*4)
	for j := range n {
		for i := range n {
			var v uint8
			if i%2 == 1 {
				v = 255
			}
			k := (j*n + i) * 4
			pix[k], pix[k+1], pix[k+2], pix[k+3] = v, v, v, 255
		}
	}
	return paint.Image{W: n, H: n, Pix: pix, Edge: paint.Repeat}
}

// stripeScene tiles stripes over the whole canvas, translated by half a texel plus
// nudge.
//
// The half-texel offset is the mechanism, and the reason it is 0.5 rather than
// something exotic is the point of the test. It puts every pixel centre EXACTLY on
// a texel boundary: q.x = (px + 0.5) - 0.5 = px, an integer, for every pixel in the
// frame. That is the worst case for a step function and both backends handle it
// perfectly, because an exact tie is not a disagreement — f64 and f32 floor the
// same integer to the same texel. nudge=0 measures Δ=0 and is the control.
//
// nudge=1e-9 is what f32 cannot see. The inverse translate is -(0.5 + 1e-9), and
// f32's ulp at 0.5 is ~6e-8, so the matrix ROUNDS BACK to exactly -0.5 on the GPU
// and its sample point stays on the integer. The f64 reference keeps the hair and
// lands just below it. floor() then disagrees by one for every pixel in the frame,
// and every pixel reads the opposite stripe.
//
// Nothing here is exotic. Drawing an image at 1:1 offset by half a pixel is an
// ordinary thing to ask for, and "half a pixel plus a hair" is what an accumulated
// transform actually produces.
func stripeScene(nudge float64, f paint.Filter) *scene.Scene {
	c := render.NewCanvas()
	img := stripes(stripeN)
	img.Filter = f
	c.Save()
	c.Translate(0.5+nudge, 0)
	c.Fill(path.Rect(geom.RectXYWH(-1, -1, stripeW+2, stripeH+2)), img, paint.NonZero)
	c.Restore()
	return c.Scene()
}

// stripeDelta compares the two backends with the gate wide open: these tests
// MEASURE a divergence rather than gate one, so they must not fail inside Compare.
func stripeDelta(t *testing.T, nudge float64, f paint.Filter) parity.Result {
	t.Helper()
	r, err := NewRenderer(stripeW, stripeH)
	if err != nil {
		t.Skipf("no gpu device: %v", err)
	}
	defer r.Release()
	if err := r.Render(stripeScene(nudge, f)); err != nil {
		t.Fatalf("render: %v", err)
	}
	got, err := r.ReadRGBA()
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	res, err := parity.Compare(got, cpu.Render(stripeScene(nudge, f), stripeW, stripeH),
		parity.Budget(255, "measuring, not gating"))
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	return res
}

// TestNearestIsExactWhenF32CanResolveIt is the control, and without it the test
// below could equally mean "the image sampler is simply broken". Every pixel centre
// lands exactly on a texel boundary here — the hardest thing a step function can be
// asked — and the backends agree bit for bit.
func TestNearestIsExactWhenF32CanResolveIt(t *testing.T) {
	got := stripeDelta(t, 0, paint.Nearest)
	if got.MaxDelta != 0 {
		t.Fatalf("nearest with no nudge: Δ=%d at %v, want 0 — an exact tie must not diverge", got.MaxDelta, got.At)
	}
}

// TestNearestDivergesAtATexelBoundary measures the hazard Phase 17 predicted for
// the WRONG filter. Nearest introduces no arithmetic of its own, which is what made
// the plan call it exact; it is a step function, which is what actually matters.
//
// Both backends are individually CORRECT here. Neither has a bug to fix. The true
// sample point sits 1e-9 below an integer, f32 cannot represent that, and floor()
// has no headroom in front of it — a threshold does not care how small the
// perturbation is, only that it is non-zero. This is the conic seam
// (TestConicSeamDivergesWithoutBound) in a different key: the magnitude is set by
// the TEXELS, not by the arithmetic, so there is no derivative to bound and no
// budget that could be fitted.
func TestNearestDivergesAtATexelBoundary(t *testing.T) {
	got := stripeDelta(t, 1e-9, paint.Nearest)
	if got.MaxDelta < 128 {
		t.Fatalf("nearest with a sub-f32 nudge: Δ=%d at %v, want ≥128 — the probe has stopped probing", got.MaxDelta, got.At)
	}
	t.Logf("nearest at a texel boundary: Δ=%d at %v", got.MaxDelta, got.At)
}

// TestTheNudgeIsInvisibleToF32AndDecisiveForF64 proves the MECHANISM rather than
// observing its effect. The test above shows the two backends disagree; on its own
// that is equally consistent with the GPU sampler simply being broken near a
// boundary, which is a bug and not a hazard, and the two want opposite responses.
//
// So ask each backend the question directly. The nudge is 1e-9 against an f32 ulp
// of ~6e-8 at 0.5, so the GPU's inverse matrix rounds back to exactly -0.5 and its
// frame must be BYTE-IDENTICAL with and without the nudge — it never received the
// information. The f64 reference must see it and move. Both halves are asserted:
// if the GPU's frames differed, f32 would be resolving the nudge and the analysis
// in paint.ImageAt would be wrong; if the CPU's did not, the probe would be
// measuring nothing at all.
func TestTheNudgeIsInvisibleToF32AndDecisiveForF64(t *testing.T) {
	r, err := NewRenderer(stripeW, stripeH)
	if err != nil {
		t.Skipf("no gpu device: %v", err)
	}
	defer r.Release()

	gpuFrame := func(nudge float64) *image.RGBA {
		if err := r.Render(stripeScene(nudge, paint.Nearest)); err != nil {
			t.Fatalf("render: %v", err)
		}
		got, err := r.ReadRGBA()
		if err != nil {
			t.Fatalf("readback: %v", err)
		}
		return got
	}
	if !bytes.Equal(gpuFrame(0).Pix, gpuFrame(1e-9).Pix) {
		t.Fatal("the GPU resolved a 1e-9 nudge at 0.5, which f32 cannot represent — the hazard analysis in paint.ImageAt assumes it cannot")
	}
	if bytes.Equal(
		cpu.Render(stripeScene(0, paint.Nearest), stripeW, stripeH).Pix,
		cpu.Render(stripeScene(1e-9, paint.Nearest), stripeW, stripeH).Pix,
	) {
		t.Fatal("the f64 reference did not see the nudge either — the probe is measuring nothing")
	}
}

// TestBilinearAbsorbsTheBoundaryNudge is the measurement that overturns the plan,
// and it is deliberately the same geometry as the test above: identical scene,
// identical nudge, identical hazard, one field changed.
//
// Phase 17 budgeted perceptual mode and a Phase 14 fallback for bilinear on the
// grounds that averaging four texels in f32 cannot match averaging them in f64. The
// averaging was never the question. Bilinear is CONTINUOUS, so when the two
// backends pick different i0 they also pick fx near 1 and fx near 0 — and the
// weighted average lands in the same place. The filter the plan called safe is the
// one that breaks; the filter it hedged against needs neither hedge.
func TestBilinearAbsorbsTheBoundaryNudge(t *testing.T) {
	got := stripeDelta(t, 1e-9, paint.Bilinear)
	if !got.OK(parity.Quantized()) {
		t.Fatalf("bilinear with a sub-f32 nudge: Δ=%d at %v, want ≤1 — continuity is the claim", got.MaxDelta, got.At)
	}
	t.Logf("bilinear at the same boundary, same nudge: Δ=%d at %v", got.MaxDelta, got.At)
}
