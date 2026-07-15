package gpu

import (
	"testing"

	"github.com/stohirov/sukho/backend/cpu"
	"github.com/stohirov/sukho/geom"
	"github.com/stohirov/sukho/internal/parity"
	"github.com/stohirov/sukho/paint"
	"github.com/stohirov/sukho/path"
	"github.com/stohirov/sukho/render"
	"github.com/stohirov/sukho/scene"
)

// TestFallbackKeepsMiterSpike pins the bug Phase 14 shipped: a mitered stroke
// whose apex reaches a flagged tile while the node's padded PATH bbox does not.
// cpu.nodeBounds padded by the stroke's half-width, but a miter reaches
// miterLimit*w/2 — 4x further by default — so tileCulled dropped the node from
// the CPU patch and uploadFallbackTiles then overwrote the GPU's correct pixels
// with a tile missing the spike. Measured Δ=220 over 76 channels before the fix:
// corruption, not a rounding delta.
//
// The geometry is load-bearing and fragile in the useful direction. The V is
// placed so its padded path bbox stops at x=63 (tile 3) while the outline
// reaches x~72 (tile 4), and the fallback node flags exactly tile 4. Widening
// the stroke or moving the vertex right would put the bbox into tile 4 and the
// node would survive culling for the wrong reason.
func TestFallbackKeepsMiterSpike(t *testing.T) {
	const w, h = 128, 96
	build := func() *scene.Scene {
		c := render.NewCanvas()
		c.FillColor(path.Rect(geom.RectXYWH(0, 0, w, h)), paint.FromRGBA8(20, 22, 28, 255))

		var v path.Path
		v.MoveTo(geom.Pt(18, 38))
		v.LineTo(geom.Pt(58, 50))
		v.LineTo(geom.Pt(18, 62))
		c.StrokeColor(v, paint.FromRGBA8(240, 200, 60, 255),
			paint.Stroke{Width: 8, Join: path.MiterJoin})

		c.SetFallback(true)
		c.Fill(path.Circle(geom.Pt(72, 52), 6), paint.RadialGradient{
			Center: geom.Pt(70, 50), Radius: 8,
			Stops: []paint.Stop{
				{Offset: 0, Color: paint.RGBA(1, 1, 1, 1)},
				{Offset: 1, Color: paint.FromRGBA8(40, 120, 220, 200)},
			},
		}, paint.NonZero)
		c.SetFallback(false)
		return c.Scene()
	}

	sc := build()
	if got := Encode(sc, w, h).NFallback; got == 0 {
		t.Fatal("no tiles flagged; the scene cannot exercise the patch")
	}

	r, err := NewRenderer(w, h)
	if err != nil {
		t.Skipf("no gpu device: %v", err)
	}
	defer r.Release()

	parity.Assert(t, renderGPU(t, r, build()), cpu.Render(build(), w, h), parity.Identical())
}

// TestFallbackWithPorterDuff covers the interaction Phases 14 and 15 left
// untested: a fallback-marked node whose operator READS the backdrop. It is
// exact because the CPU patch recomputes the whole node stack for a flagged tile
// from transparent black, exactly as the GPU does — the patch never reads the
// GPU's pixels, so there is no backdrop to disagree about. An implementation
// that instead composited the CPU's node over the GPU's tile would fail here.
func TestFallbackWithPorterDuff(t *testing.T) {
	const w, h = 128, 96
	build := func(op paint.CompositeOp) *scene.Scene {
		c := render.NewCanvas()
		c.FillColor(path.Rect(geom.RectXYWH(0, 0, w, h)), paint.FromRGBA8(20, 22, 28, 255))
		c.FillColor(path.Rect(geom.RectXYWH(0, 0, 64, 48)), paint.FromRGBA8(200, 90, 40, 255))
		c.SetFallback(true)
		c.SetComposite(op)
		c.Fill(path.Circle(geom.Pt(64, 48), 26), paint.RadialGradient{
			Center: geom.Pt(58, 42), Radius: 34,
			Stops: []paint.Stop{
				{Offset: 0, Color: paint.RGBA(1, 1, 1, 1)},
				{Offset: 1, Color: paint.FromRGBA8(40, 120, 220, 180)},
			},
		}, paint.NonZero)
		c.SetComposite(paint.SrcOver)
		c.SetFallback(false)
		return c.Scene()
	}

	for _, tc := range []struct {
		name string
		op   paint.CompositeOp
	}{
		{"clear", paint.Clear}, {"dst-out", paint.DstOut}, {"src-in", paint.SrcIn},
		{"dst-in", paint.DstIn}, {"xor", paint.Xor}, {"src-atop", paint.SrcAtop},
		{"dst-atop", paint.DstAtop},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := NewRenderer(w, h)
			if err != nil {
				t.Skipf("no gpu device: %v", err)
			}
			defer r.Release()
			parity.Assert(t, renderGPU(t, r, build(tc.op)), cpu.Render(build(tc.op), w, h), parity.Identical())
		})
	}
}
