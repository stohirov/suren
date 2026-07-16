package cpu

import (
	"math"
	"testing"

	"github.com/stohirov/suren/geom"
	"github.com/stohirov/suren/paint"
	"github.com/stohirov/suren/path"
	"github.com/stohirov/suren/render"
	"github.com/stohirov/suren/scene"
)

// TestConicParameterMapping is an INDEPENDENT witness of what a conic gradient
// means, and the parity machine cannot supply one.
//
// Every other gate on this feature compares the two backends, or compares the CPU
// against a golden the CPU itself generated. Both are blind to a semantic error
// the two renderers share: a conic sweeping the wrong way, or starting a quarter
// turn off, would render identically on Metal and on the reference, match its
// golden byte for byte, and pass every corpus entry. So the mapping is asserted
// against hand-computed angles here instead.
//
// The centre sits at a pixel CENTRE (16.5), so each probe's offset from it is a
// whole number of pixels along an axis and its angle is exact — no antialiasing
// and no near-miss on the arithmetic. Stops ramp black to white across one full
// turn, which makes the expected channel value simply t*255 and lets the four
// probes distinguish every rotation and both sweep directions from one another.
func TestConicParameterMapping(t *testing.T) {
	const size = 32
	const cx, cy = 16.5, 16.5

	build := func(angle float64) *scene.Scene {
		c := render.NewCanvas()
		c.Fill(path.Rect(geom.RectXYWH(0, 0, size, size)), paint.ConicGradient{
			Center: geom.Pt(cx, cy),
			Angle:  angle,
			Stops: []paint.Stop{
				{Offset: 0, Color: paint.RGBA(0, 0, 0, 1)},
				{Offset: 1, Color: paint.RGBA(1, 1, 1, 1)},
			},
		}, paint.NonZero)
		return c.Scene()
	}

	// Device y grows DOWNWARD, so increasing atan2 sweeps clockwise on screen.
	// Naming each probe by where it is on screen keeps that visible: a renderer
	// that swept counter-clockwise would put 0.25 up rather than down.
	for _, tc := range []struct {
		name  string
		angle float64
		// px, py is a pixel whose centre is exactly (dx, dy) from the gradient's.
		px, py int
		wantT  float64
	}{
		{"right is the start", 0, 24, 16, 0},
		{"down is a quarter turn", 0, 16, 24, 0.25},
		{"left is a half turn", 0, 8, 16, 0.5},
		{"up is three quarters", 0, 16, 8, 0.75},
		// Angle rotates the START of the sweep, so the ray at Angle reads t=0 and
		// everything else shifts with it. A quarter turn of Angle moves the start
		// from right to down.
		{"angle moves the start to down", math.Pi / 2, 16, 24, 0},
		{"angle shifts right to three quarters", math.Pi / 2, 24, 16, 0.75},
	} {
		t.Run(tc.name, func(t *testing.T) {
			img := Render(build(tc.angle), size, size)
			got := img.RGBAAt(tc.px, tc.py).R
			want := uint8(math.Round(tc.wantT * 255))
			// Within an LSB: this pins the SEMANTICS (is the start at 0.25 or
			// 0.75?), which no rounding rule can move, not the quantization the
			// corpus already gates.
			if d := int(got) - int(want); d > 1 || d < -1 {
				t.Errorf("pixel (%d,%d) at Angle=%.4f = %d, want %d (t=%.2f)",
					tc.px, tc.py, tc.angle, got, want, tc.wantT)
			}
		})
	}
}

// The centre pixel is the one place the parameter has no answer: atan2(0,0) is 0
// in Go and UNDEFINED in WGSL. Both backends pin it to t=0 rather than inherit two
// languages' corner cases, so the choice is asserted rather than left implicit —
// a driver returning something else for atan2(0,0) would otherwise show up as a
// one-pixel parity failure with no stated expectation to compare against.
func TestConicCentreIsPinnedToTheFirstStop(t *testing.T) {
	const size = 16
	c := render.NewCanvas()
	c.Fill(path.Rect(geom.RectXYWH(0, 0, size, size)), paint.ConicGradient{
		// A pixel centre, so the offset really is exactly (0,0) for pixel (8,8).
		Center: geom.Pt(8.5, 8.5),
		// Non-zero on purpose: with Angle=0 the guard and the natural atan2(0,0)=0
		// agree by accident, and the test would pass without the guard existing.
		Angle: 1.0,
		Stops: []paint.Stop{
			{Offset: 0, Color: paint.RGBA(1, 0, 0, 1)},
			{Offset: 1, Color: paint.RGBA(0, 0, 1, 1)},
		},
	}, paint.NonZero)

	got := Render(c.Scene(), size, size).RGBAAt(8, 8)
	if got.R != 255 || got.B != 0 {
		t.Errorf("centre pixel = %v, want the first stop (255,0,0,255); t is not pinned to 0 there", got)
	}
}
