package parity

import (
	"image"
	"math"
	"testing"
)

func TestValidateRequiresOwnerAboveFloor(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  Config
		ok   bool
	}{
		{"identical", Identical(), true},
		{"quantized", Quantized(), true},
		{"budget names its cause", Budget(3, "ColorBurn min(1,·) clamp"), true},
		{"budget without a cause", Config{Mode: ModeExact, Tol: 3}, false},
		{"floor needs no cause", Config{Mode: ModeExact, Tol: QuantizationFloor}, true},
		{"negative tolerance", Config{Mode: ModeExact, Tol: -1}, false},
		{"perceptual names its cause", Perceptual(2, 0.99, "bilinear f32 kernel"), true},
		{"perceptual without a cause", Config{Mode: ModePerceptual, MaxDeltaE: 2, MinSSIM: 0.99}, false},
		{"perceptual without deltaE", Config{Mode: ModePerceptual, MinSSIM: 0.99, Why: "x"}, false},
		{"perceptual with impossible ssim", Config{Mode: ModePerceptual, MaxDeltaE: 2, MinSSIM: 1.5, Why: "x"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.cfg.Validate(); (err == nil) != tc.ok {
				t.Fatalf("Validate() error = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

func TestCompareMeasuresMaxDeltaAndLocation(t *testing.T) {
	want := image.NewRGBA(image.Rect(0, 0, 4, 4))
	got := image.NewRGBA(image.Rect(0, 0, 4, 4))
	got.Pix[got.PixOffset(2, 1)+1] = 5

	r, err := Compare(got, want, Quantized())
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if r.MaxDelta != 5 {
		t.Errorf("MaxDelta = %d, want 5", r.MaxDelta)
	}
	if r.At != image.Pt(2, 1) {
		t.Errorf("At = %v, want (2,1)", r.At)
	}
	if r.Over != 1 {
		t.Errorf("Over = %d, want 1", r.Over)
	}
	if r.Total != 4*4*4 {
		t.Errorf("Total = %d, want %d", r.Total, 4*4*4)
	}
	if r.OK(Quantized()) {
		t.Error("OK reported pass for a delta of 5 against a Δ≤1 gate")
	}
	if !r.OK(Budget(5, "test")) {
		t.Error("OK reported fail for a delta of 5 against a Δ≤5 gate")
	}
}

func TestCompareRejectsMismatchedBounds(t *testing.T) {
	a := image.NewRGBA(image.Rect(0, 0, 2, 2))
	b := image.NewRGBA(image.Rect(0, 0, 3, 2))
	if _, err := Compare(a, b, Identical()); err == nil {
		t.Fatal("Compare accepted images with differing bounds")
	}
}

func fill(img *image.RGBA, r, g, b, a uint8) {
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = r, g, b, a
	}
}

func TestLabReferenceValues(t *testing.T) {
	for _, tc := range []struct {
		name     string
		r, g, b  uint8
		l, a, bb float64
	}{
		{"black", 0, 0, 0, 0, 0, 0},
		{"white", 255, 255, 255, 100, 0, 0},
		{"mid grey", 128, 128, 128, 53.5850, 0, 0},
		{"red", 255, 0, 0, 53.2408, 80.0925, 67.2032},
		{"green", 0, 255, 0, 87.7347, -86.1827, 83.1793},
		{"blue", 0, 0, 255, 32.2970, 79.1875, -107.8602},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l, a, bb := srgb8ToLab(tc.r, tc.g, tc.b)
			const eps = 0.01
			if math.Abs(l-tc.l) > eps || math.Abs(a-tc.a) > eps || math.Abs(bb-tc.bb) > eps {
				t.Errorf("Lab = (%.3f, %.3f, %.3f), want (%.3f, %.3f, %.3f)", l, a, bb, tc.l, tc.a, tc.bb)
			}
		})
	}
}

func TestDeltaEIsZeroForIdenticalColorAndGrowsWithDistance(t *testing.T) {
	if d := deltaE76(90, 140, 200, 90, 140, 200); d != 0 {
		t.Errorf("deltaE76 of a color with itself = %v, want 0", d)
	}
	near := deltaE76(90, 140, 200, 91, 140, 200)
	far := deltaE76(90, 140, 200, 200, 40, 30)
	if !(near > 0 && near < far) {
		t.Errorf("expected 0 < near (%v) < far (%v)", near, far)
	}
	// White vs black spans the full lightness range: ΔE76 is exactly ΔL = 100.
	if d := deltaE76(255, 255, 255, 0, 0, 0); math.Abs(d-100) > 0.01 {
		t.Errorf("deltaE76(white, black) = %v, want 100", d)
	}
}

func TestSSIMIsOneForIdenticalAndLessForDifferent(t *testing.T) {
	a := image.NewRGBA(image.Rect(0, 0, 32, 32))
	fill(a, 80, 120, 160, 255)
	b := image.NewRGBA(image.Rect(0, 0, 32, 32))
	fill(b, 80, 120, 160, 255)

	if s := ssim(a, b); math.Abs(s-1) > 1e-9 {
		t.Errorf("ssim of identical images = %v, want 1", s)
	}

	// Structural change: half the image goes dark.
	for y := range 16 {
		for x := range 32 {
			i := b.PixOffset(x, y)
			b.Pix[i], b.Pix[i+1], b.Pix[i+2] = 10, 10, 10
		}
	}
	if s := ssim(a, b); s >= 1 {
		t.Errorf("ssim of structurally different images = %v, want < 1", s)
	}
}

func TestPerceptualGateChecksDeltaESSIMAndAlpha(t *testing.T) {
	cfg := Perceptual(1.0, 0.99, "test")

	a := image.NewRGBA(image.Rect(0, 0, 32, 32))
	fill(a, 80, 120, 160, 255)
	b := image.NewRGBA(image.Rect(0, 0, 32, 32))
	fill(b, 80, 120, 160, 255)

	r, err := Compare(a, b, cfg)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if !r.OK(cfg) {
		t.Fatalf("identical images failed the perceptual gate: %s", r.Describe(cfg))
	}

	// A large color shift must fail on ΔE even though structure is untouched.
	far := image.NewRGBA(image.Rect(0, 0, 32, 32))
	fill(far, 200, 40, 30, 255)
	r, err = Compare(a, far, cfg)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if r.OK(cfg) {
		t.Errorf("perceptual gate passed a large color shift: %s", r.Describe(cfg))
	}

	// Transparent black vs opaque black composite identically over black, so ΔE
	// cannot see the difference: the alpha gate is what must catch it.
	clear1 := image.NewRGBA(image.Rect(0, 0, 32, 32))
	fill(clear1, 0, 0, 0, 0)
	opaque := image.NewRGBA(image.Rect(0, 0, 32, 32))
	fill(opaque, 0, 0, 0, 255)
	r, err = Compare(clear1, opaque, cfg)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if r.MaxDeltaE != 0 {
		t.Errorf("ΔE = %v for two blacks differing only in alpha, want 0", r.MaxDeltaE)
	}
	if r.OK(cfg) {
		t.Error("perceptual gate passed images differing only in alpha; the alpha gate did not fire")
	}
}
