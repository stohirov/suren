package gpu

import (
	"math"
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
	seamW, seamH = 64, 32
	// The gradient's centre, on a pixel centre so a probe's offset is exact.
	seamCX, seamCY = 8.5, 8.5
	// The offset of the pixel the seam is aimed through, chosen so its angle is
	// not a tidy fraction of pi — an axis-aligned seam is exactly representable in
	// both precisions and diverges by nothing.
	seamDX, seamDY = 37.0, 11.0
)

// seamScene puts the seam ray through the centre of pixel (seamCX+seamDX,
// seamCY+seamDY), offset by nudge.
//
// nudge is the whole mechanism. At nudge=0 the two backends AGREE exactly, which
// is worth stating because it is the opposite of the intuition: f32 atan2(11,37)
// rounds to the same f32 as float32(f64 atan2(11,37)), so both compute t=0 on the
// nose and the probe measures nothing. A nudge of 1e-9 is far below f32's
// resolution near 0.29 rad (~3e-8) and far above f64's, so it is INVISIBLE to the
// GPU and decisive for the CPU: the reference sees the pixel a hair on the
// under-1 side of the wrap and reads the last stop, the GPU sees it exactly on
// the ray and reads the first.
func seamScene(nudge float64) *scene.Scene {
	c := render.NewCanvas()
	c.FillColor(path.Rect(geom.RectXYWH(0, 0, seamW, seamH)), paint.FromRGBA8(20, 22, 28, 255))
	c.Fill(path.Rect(geom.RectXYWH(0, 0, seamW, seamH)), paint.ConicGradient{
		Center: geom.Pt(seamCX, seamCY),
		Angle:  math.Atan2(seamDY, seamDX) + nudge,
		Stops: []paint.Stop{
			{Offset: 0, Color: paint.RGBA(0, 0, 0, 1)},
			{Offset: 1, Color: paint.RGBA(1, 1, 1, 1)},
		},
	}, paint.NonZero)
	return c.Scene()
}

func seamDelta(t *testing.T, nudge float64) parity.Result {
	t.Helper()
	r, err := NewRenderer(seamW, seamH)
	if err != nil {
		t.Skipf("no gpu device: %v", err)
	}
	defer r.Release()
	if err := r.Render(seamScene(nudge)); err != nil {
		t.Fatalf("render: %v", err)
	}
	got, err := r.ReadRGBA()
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	// Compare with the gate wide open: this test MEASURES a divergence rather than
	// gating one, so it must not fail inside Compare.
	res, err := parity.Compare(got, cpu.Render(seamScene(nudge), seamW, seamH), parity.Budget(255, "measuring, not gating"))
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	return res
}

// TestConicSeamDivergesWithoutBound is the evidence behind two decisions that are
// otherwise just assertions: why internal/parity/fuzz generates only CLOSED conic
// gradients, and why no budget is offered for open ones.
//
// A conic gradient with differing end stops is DISCONTINUOUS across the ray at
// Angle. Discontinuity is not a rounding artifact — it is an amplifier with no
// bound, the same species as ColorDodge's unbounded derivative, except that here
// the magnitude is set by the stop colours rather than by the arithmetic. Both
// backends are individually CORRECT at the diverging pixel: the true parameter
// there is within 1e-9 of the wrap, and f32 simply cannot resolve which side of it
// the pixel falls on.
//
// So the failure this test would report is not "the renderer broke". It is "the
// hazard analysis in paint.ConicGradient is wrong" — either the seam stopped being
// reachable, or something started resolving it. Both are worth being told about,
// and neither is fixed by widening a tolerance.
//
// The delta is asserted to be LARGE rather than merely non-zero. A seam that
// diverged by an LSB would be an ordinary quantization difference and would not
// justify the generator's restriction.
func TestConicSeamDivergesWithoutBound(t *testing.T) {
	res := seamDelta(t, 1e-9)
	if res.MaxDelta < 128 {
		t.Fatalf("seam probe diverged by only %d; paint.ConicGradient claims an open seam amplifies without bound, and the generator's closed-loop restriction rests on that claim being true: %s",
			res.MaxDelta, res)
	}
	wantX, wantY := int(math.Floor(seamCX+seamDX)), int(math.Floor(seamCY+seamDY))
	if res.At.X != wantX || res.At.Y != wantY {
		t.Errorf("divergence at %v, want the pixel the seam was aimed through (%d,%d); the probe is measuring something other than the seam",
			res.At, wantX, wantY)
	}
	t.Logf("open seam through a pixel centre: %s", res)
}

// The control, and the reason the test above is not circular. Everything about
// the scene is held fixed except the 1e-9 nudge that decides whether the seam
// lands ON the pixel centre or a hair off it. Without this, a Δ=255 could just as
// well mean the conic implementation is broken outright.
//
// It also records the near-miss: at nudge=0 the seam passes exactly through the
// pixel and the backends still agree, because both round the same true angle to
// the same f32. The hazard needs the true value to sit inside f32's blind spot,
// not merely on the ray, which is why the first draft of this probe measured Δ=0.
func TestConicSeamIsExactWhenF32CanResolveIt(t *testing.T) {
	res := seamDelta(t, 0)
	if !res.OK(parity.Quantized()) {
		t.Fatalf("seam exactly through a pixel centre diverged by %d, want the ordinary floor: %s", res.MaxDelta, res)
	}
	t.Logf("seam on the pixel centre, no sub-f32 nudge: %s", res)
}
