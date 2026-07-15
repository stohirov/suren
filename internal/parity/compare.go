package parity

import (
	"fmt"
	"image"
	"testing"
)

type Result struct {
	MaxDelta      int
	At            image.Point
	Over          int
	Total         int
	MaxAlphaDelta int
	MaxDeltaE     float64
	DeltaEAt      image.Point
	SSIM          float64
}

func (r Result) OK(c Config) bool {
	if c.Mode == ModePerceptual {
		return r.MaxDeltaE <= c.MaxDeltaE && r.SSIM >= c.MinSSIM && r.MaxAlphaDelta <= c.Tol
	}
	return r.MaxDelta <= c.Tol
}

func (r Result) String() string {
	return fmt.Sprintf("max channel delta=%d at %v; over-tolerance channels: %d/%d", r.MaxDelta, r.At, r.Over, r.Total)
}

func (r Result) Describe(c Config) string {
	if c.Mode == ModePerceptual {
		return fmt.Sprintf("max ΔE=%.4f at %v; SSIM=%.6f; max alpha delta=%d; max channel delta=%d at %v",
			r.MaxDeltaE, r.DeltaEAt, r.SSIM, r.MaxAlphaDelta, r.MaxDelta, r.At)
	}
	return r.String()
}

func Compare(got, want *image.RGBA, cfg Config) (Result, error) {
	if err := cfg.Validate(); err != nil {
		return Result{}, err
	}
	if got.Rect != want.Rect {
		return Result{}, fmt.Errorf("bounds differ: got %v, want %v", got.Rect, want.Rect)
	}

	r := Result{}
	b := want.Rect
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			gi, wi := got.PixOffset(x, y), want.PixOffset(x, y)
			for c := range 4 {
				d := int(got.Pix[gi+c]) - int(want.Pix[wi+c])
				if d < 0 {
					d = -d
				}
				r.Total++
				if d > cfg.Tol {
					r.Over++
				}
				if d > r.MaxDelta {
					r.MaxDelta, r.At = d, image.Pt(x, y)
				}
				if c == 3 && d > r.MaxAlphaDelta {
					r.MaxAlphaDelta = d
				}
			}
			if cfg.Mode == ModePerceptual {
				de := deltaE76(
					got.Pix[gi], got.Pix[gi+1], got.Pix[gi+2],
					want.Pix[wi], want.Pix[wi+1], want.Pix[wi+2])
				if de > r.MaxDeltaE {
					r.MaxDeltaE, r.DeltaEAt = de, image.Pt(x, y)
				}
			}
		}
	}
	if cfg.Mode == ModePerceptual {
		r.SSIM = ssim(got, want)
	}
	return r, nil
}

func Assert(t testing.TB, got, want *image.RGBA, cfg Config) Result {
	t.Helper()
	r, err := Compare(got, want, cfg)
	if err != nil {
		t.Fatalf("parity: %v", err)
	}
	t.Logf("parity[%s]: %s", cfg, r.Describe(cfg))
	if !r.OK(cfg) {
		t.Fatalf("parity: gate %s exceeded: %s", cfg, r.Describe(cfg))
	}
	return r
}
