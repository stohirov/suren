package parity

import (
	"image"
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
		{"budget without a cause", Config{Mode: Exact, Tol: 3}, false},
		{"floor needs no cause", Config{Mode: Exact, Tol: QuantizationFloor}, true},
		{"negative tolerance", Config{Mode: Exact, Tol: -1}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.cfg.Validate(); (err == nil) != tc.ok {
				t.Fatalf("Validate() error = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

func TestPerceptualRejectedUntilImplemented(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	if _, err := Compare(img, img, Config{Mode: Perceptual}); err == nil {
		t.Fatal("Compare accepted Perceptual mode; it must reject until Phase 12a implements it")
	}
}

func TestCompareMeasuresMaxDeltaAndLocation(t *testing.T) {
	want := image.NewRGBA(image.Rect(0, 0, 4, 4))
	got := image.NewRGBA(image.Rect(0, 0, 4, 4))
	got.Pix[got.PixOffset(2, 1)+1] = 5 // green channel at (2,1)

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
