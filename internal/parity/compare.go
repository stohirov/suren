package parity

import (
	"fmt"
	"image"
	"testing"
)

type Result struct {
	MaxDelta int
	At       image.Point
	Over     int
	Total    int
}

func (r Result) OK(c Config) bool { return r.MaxDelta <= c.Tol }

func (r Result) String() string {
	return fmt.Sprintf("max channel delta=%d at %v; over-tolerance channels: %d/%d", r.MaxDelta, r.At, r.Over, r.Total)
}

func Compare(got, want *image.RGBA, cfg Config) (Result, error) {
	if err := cfg.Validate(); err != nil {
		return Result{}, err
	}
	if cfg.Mode == Perceptual {
		return Result{}, fmt.Errorf("Perceptual mode is declared by the contract but not implemented until Phase 12a")
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
			}
		}
	}
	return r, nil
}

func Assert(t testing.TB, got, want *image.RGBA, cfg Config) Result {
	t.Helper()
	r, err := Compare(got, want, cfg)
	if err != nil {
		t.Fatalf("parity: %v", err)
	}
	t.Logf("parity[%s]: %s", cfg, r)
	if !r.OK(cfg) {
		t.Fatalf("parity: gate %s exceeded: %s", cfg, r)
	}
	return r
}
