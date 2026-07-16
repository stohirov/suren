package gpu

import (
	"image"
	"testing"

	"github.com/stohirov/suren/backend/cpu"
	"github.com/stohirov/suren/geom"
	"github.com/stohirov/suren/internal/parity"
	"github.com/stohirov/suren/internal/sample"
	"github.com/stohirov/suren/paint"
	"github.com/stohirov/suren/path"
	"github.com/stohirov/suren/render"
	"github.com/stohirov/suren/scene"
)

func fallbackRenderer(t *testing.T) *Renderer {
	t.Helper()
	r, err := NewRenderer(sample.FallbackW, sample.FallbackH)
	if err != nil {
		t.Skipf("no gpu device: %v", err)
	}
	t.Cleanup(r.Release)
	return r
}

func renderGPU(t *testing.T, r *Renderer, sc *scene.Scene) *image.RGBA {
	t.Helper()
	if err := r.Render(sc); err != nil {
		t.Fatalf("render: %v", err)
	}
	got, err := r.ReadRGBA()
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	return got
}

// TestFallbackFlagsExpectedTiles pins which tiles the encoder flags. The mark is
// a node property, so the flagged set must follow the node's bbox and nothing
// else: too few tiles leaves inexact pixels on the GPU, too many needlessly
// moves exact ones to the CPU.
func TestFallbackFlagsExpectedTiles(t *testing.T) {
	e := Encode(sample.FallbackScene(true), sample.FallbackW, sample.FallbackH)
	want := sample.FallbackTileRect

	nWant := (want[2] - want[0]) * (want[3] - want[1])
	if e.NFallback != nWant {
		t.Errorf("NFallback = %d, want %d", e.NFallback, nWant)
	}
	// A fallback covering every tile would pass every other assertion here while
	// proving nothing about the handshake.
	if e.NFallback >= e.NTilesX*e.NTilesY {
		t.Fatalf("fallback covers the whole frame (%d of %d tiles); the scene is meant to be partial",
			e.NFallback, e.NTilesX*e.NTilesY)
	}
	for ty := 0; ty < e.NTilesY; ty++ {
		for tx := 0; tx < e.NTilesX; tx++ {
			in := tx >= want[0] && tx < want[2] && ty >= want[1] && ty < want[3]
			if got := e.FallbackTiles[ty*e.NTilesX+tx]; got != in {
				t.Errorf("tile (%d,%d) flagged = %v, want %v", tx, ty, got, in)
			}
		}
	}
}

func TestNoFallbackFlagsNoTiles(t *testing.T) {
	e := Encode(sample.FallbackScene(false), sample.FallbackW, sample.FallbackH)
	if e.NFallback != 0 {
		t.Errorf("unmarked scene flagged %d tiles, want 0", e.NFallback)
	}
}

// TestFallbackBuysExactness is the Phase 14 gate. The corpus runs both scenes
// against their own tolerances; what it cannot state is that the fallback is
// what makes the difference. If the GPU ever renders this scene exactly on its
// own, the fallback-gradient entry would keep passing while measuring nothing —
// so this asserts the divergence the fallback removes actually exists.
func TestFallbackBuysExactness(t *testing.T) {
	r := fallbackRenderer(t)
	want := cpu.Render(sample.FallbackScene(false), sample.FallbackW, sample.FallbackH)

	off, err := parity.Compare(renderGPU(t, r, sample.FallbackScene(false)), want, parity.Quantized())
	if err != nil {
		t.Fatalf("compare without fallback: %v", err)
	}
	on, err := parity.Compare(renderGPU(t, r, sample.FallbackScene(true)), want, parity.Identical())
	if err != nil {
		t.Fatalf("compare with fallback: %v", err)
	}
	t.Logf("max channel delta: fallback off = %d, on = %d", off.MaxDelta, on.MaxDelta)

	if on.MaxDelta != 0 {
		t.Errorf("fallback did not reach exactness: max delta = %d, want 0", on.MaxDelta)
	}
	if off.MaxDelta == 0 {
		t.Error("scene renders exactly on GPU without the fallback, so the fallback-gradient gate proves nothing; " +
			"give sample.FallbackScene a node this backend actually gets wrong")
	}
}

// TestFallbackTouchesOnlyFlaggedTiles is the containment half of the handshake.
// Exactness over the whole frame would also be satisfied by CPU-rendering
// everything, which would pass the parity gate and quietly forfeit the GPU. This
// asserts the patch is confined to the flagged tiles: every unflagged pixel must
// still be the one the GPU compute pass produced, byte for byte.
func TestFallbackTouchesOnlyFlaggedTiles(t *testing.T) {
	r := fallbackRenderer(t)
	bare := renderGPU(t, r, sample.FallbackScene(false))
	patched := renderGPU(t, r, sample.FallbackScene(true))
	ref := cpu.Render(sample.FallbackScene(true), sample.FallbackW, sample.FallbackH)

	e := Encode(sample.FallbackScene(true), sample.FallbackW, sample.FallbackH)
	flagged := func(x, y int) bool { return e.FallbackTiles[(y/tileSize)*e.NTilesX+x/tileSize] }

	var outside, inside int
	for y := 0; y < sample.FallbackH; y++ {
		for x := 0; x < sample.FallbackW; x++ {
			i := patched.PixOffset(x, y)
			got := patched.Pix[i : i+4]
			if flagged(x, y) {
				// Inside: must be the CPU reference's own pixels, exactly.
				if [4]byte(got) != [4]byte(ref.Pix[i:i+4]) && inside < 4 {
					t.Errorf("flagged pixel (%d,%d) = %v, want CPU reference %v", x, y, got, ref.Pix[i:i+4])
					inside++
				}
				continue
			}
			// Outside: must be untouched by the CPU pass.
			if [4]byte(got) != [4]byte(bare.Pix[i:i+4]) && outside < 4 {
				t.Errorf("unflagged pixel (%d,%d) = %v, want GPU's own %v", x, y, got, bare.Pix[i:i+4])
				outside++
			}
		}
	}
}

// TestFallbackPixelsMatchFullCPURender is the claim raster.TileMask rests on: a
// masked fill writes the bits a full-frame fill would. The tile-gated CPU pass
// skips nodes and columns, and if any of that perturbed the coverage sweep the
// patched pixels would drift from the reference by an LSB — which is precisely
// the error a Δ≤1 gate elsewhere in the tree would absorb without noticing.
func TestFallbackPixelsMatchFullCPURender(t *testing.T) {
	sc := sample.FallbackScene(true)
	full := cpu.Render(sc, sample.FallbackW, sample.FallbackH)

	e := Encode(sc, sample.FallbackW, sample.FallbackH)
	partial := image.NewRGBA(image.Rect(0, 0, sample.FallbackW, sample.FallbackH))
	pr := &cpu.Renderer{Img: partial, Tiles: tileMaskOf(e)}
	if err := pr.Render(sc); err != nil {
		t.Fatalf("partial render: %v", err)
	}

	for y := 0; y < sample.FallbackH; y++ {
		for x := 0; x < sample.FallbackW; x++ {
			if !e.FallbackTiles[(y/tileSize)*e.NTilesX+x/tileSize] {
				continue
			}
			i := partial.PixOffset(x, y)
			if [4]byte(partial.Pix[i:i+4]) != [4]byte(full.Pix[i:i+4]) {
				t.Fatalf("masked CPU fill diverged from a full one at (%d,%d): %v vs %v",
					x, y, partial.Pix[i:i+4], full.Pix[i:i+4])
			}
		}
	}
}

// TestFallbackToggleRedispatches guards the interaction with Phase 7's frame
// skip. Toggling the mark leaves the segments, nodes and stops identical, so a
// fingerprint that ignored the tile mask would hash the two frames alike and
// skip the very patch the toggle asks for.
func TestFallbackToggleRedispatches(t *testing.T) {
	r := fallbackRenderer(t)

	off := renderGPU(t, r, sample.FallbackScene(false))
	if got := r.Stats().FallbackTiles; got != 0 {
		t.Fatalf("unmarked frame fell back on %d tiles, want 0", got)
	}
	on := renderGPU(t, r, sample.FallbackScene(true))
	st := r.Stats()
	if st.Dispatches != 2 {
		t.Errorf("toggling fallback dispatched %d times, want 2 (frame skipped as unchanged?)", st.Dispatches)
	}
	nWant := (sample.FallbackTileRect[2] - sample.FallbackTileRect[0]) *
		(sample.FallbackTileRect[3] - sample.FallbackTileRect[1])
	if st.FallbackTiles != nWant {
		t.Errorf("Stats().FallbackTiles = %d, want %d", st.FallbackTiles, nWant)
	}
	if st.Tiles != 48 {
		t.Errorf("Stats().Tiles = %d, want 48", st.Tiles)
	}

	if equalPix(off, on) {
		t.Error("toggling the fallback did not change any pixel")
	}
}

// TestFallbackRepeatFrameSkips is the other half: the mask is in the
// fingerprint, so an unchanged marked scene must still take the cheap path and
// must still present the patched pixels.
func TestFallbackRepeatFrameSkips(t *testing.T) {
	r := fallbackRenderer(t)

	a := renderGPU(t, r, sample.FallbackScene(true))
	b := renderGPU(t, r, sample.FallbackScene(true))
	if r.Stats().Dispatches != 1 {
		t.Errorf("unchanged fallback frame re-dispatched (%d), want 1", r.Stats().Dispatches)
	}
	if !equalPix(a, b) {
		t.Error("skipped fallback frame did not re-present the patched pixels")
	}
}

// TestFallbackOnPartialEdgeTiles renders at a size that is not a multiple of
// the tile size, with the marked node in the bottom-right corner, so the flagged
// tiles are the clipped ones. Every other fallback test uses 128x96 — exactly
// 8x6 whole tiles — which cannot see an upload that runs a partial tile off the
// edge of the target or reads past the end of a row.
func TestFallbackOnPartialEdgeTiles(t *testing.T) {
	// 130x100 is 8.125 x 6.25 tiles: both edges are partial, and by different
	// amounts, so a width/height mix-up cannot cancel out.
	const w, h = 130, 100
	build := func(fallback bool) *scene.Scene {
		c := render.NewCanvas()
		c.FillColor(path.Rect(geom.RectXYWH(0, 0, w, h)), paint.FromRGBA8(20, 22, 28, 255))
		c.SetFallback(fallback)
		c.Fill(path.Circle(geom.Pt(w, h), 30), paint.RadialGradient{
			Center: geom.Pt(w-8, h-8),
			Radius: 34,
			Stops: []paint.Stop{
				{Offset: 0, Color: paint.RGBA(1, 1, 1, 1)},
				{Offset: 1, Color: paint.FromRGBA8(40, 120, 220, 200)},
			},
		}, paint.NonZero)
		c.SetFallback(false)
		return c.Scene()
	}

	r, err := NewRenderer(w, h)
	if err != nil {
		t.Skipf("no gpu device: %v", err)
	}
	defer r.Release()

	want := cpu.Render(build(true), w, h)
	off, err := parity.Compare(renderGPU(t, r, build(false)), want, parity.Quantized())
	if err != nil {
		t.Fatalf("compare without fallback: %v", err)
	}
	on, err := parity.Compare(renderGPU(t, r, build(true)), want, parity.Identical())
	if err != nil {
		t.Fatalf("compare with fallback: %v", err)
	}
	t.Logf("%dx%d partial edge tiles, %d of %d flagged; max delta: off = %d, on = %d",
		w, h, r.Stats().FallbackTiles, r.Stats().Tiles, off.MaxDelta, on.MaxDelta)

	if on.MaxDelta != 0 {
		t.Errorf("fallback over partial edge tiles: max delta = %d, want 0", on.MaxDelta)
	}
	if off.MaxDelta == 0 {
		t.Skip("this backend renders the corner exactly without the fallback; the check above proves nothing here")
	}
}

func equalPix(a, b *image.RGBA) bool {
	for i := range a.Pix {
		if a.Pix[i] != b.Pix[i] {
			return false
		}
	}
	return true
}
