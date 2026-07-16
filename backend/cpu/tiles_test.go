package cpu

import (
	"image"
	"testing"

	"github.com/stohirov/suren/internal/sample"
	"github.com/stohirov/suren/raster"
)

// TestTileMaskConfinesWrites pins Renderer.Tiles' promise: it leaves every
// unflagged pixel of Img untouched.
//
// The GPU's fallback does not depend on this — it uploads only the flagged tile
// rects, so a mask that leaked would still produce a correct frame, just a
// slower one (measured at ~33% of the fallback's cost at 25% coverage). That is
// exactly why this test exists: with containment unobservable in the rendered
// output, breaking the mask would be silent, and the field's documented contract
// would rot into a comment nothing enforces.
func TestTileMaskConfinesWrites(t *testing.T) {
	const w, h = sample.FallbackW, sample.FallbackH
	sc := sample.FallbackScene(true)

	m := raster.NewTileMask(w, h, 16)
	r := sample.FallbackTileRect
	for ty := r[1]; ty < r[3]; ty++ {
		for tx := r[0]; tx < r[2]; tx++ {
			m.MarkTile(tx, ty)
		}
	}

	// A sentinel no node in the scene paints, so any write shows up.
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 0x5A
	}
	if err := (&Renderer{Img: img, Tiles: m}).Render(sc); err != nil {
		t.Fatalf("render: %v", err)
	}

	full := Render(sc, w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := img.PixOffset(x, y)
			got := [4]byte(img.Pix[i : i+4])
			if m.At(x, y) {
				if want := [4]byte(full.Pix[i : i+4]); got != want {
					t.Fatalf("flagged pixel (%d,%d) = %v, want the full render's %v", x, y, got, want)
				}
				continue
			}
			if got != [4]byte{0x5A, 0x5A, 0x5A, 0x5A} {
				t.Fatalf("unflagged pixel (%d,%d) = %v, want the sentinel: the mask leaked a write", x, y, got)
			}
		}
	}
}

// TestTileMaskCullsDistantNodes covers the other half of the mask: a node that
// reaches no flagged tile is skipped outright, which is the only saving that
// does not risk the arithmetic. Culling by bbox is what makes the CPU pass
// cheaper than a full render; culling one pixel too eagerly would clip an
// antialiased edge that a flagged tile can still see.
func TestTileMaskCullsDistantNodes(t *testing.T) {
	const w, h = sample.FallbackW, sample.FallbackH
	sc := sample.FallbackScene(true)

	// One tile in the far corner, which only the full-frame background reaches.
	m := raster.NewTileMask(w, h, 16)
	m.MarkTile(m.NX-1, m.NY-1)

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	if err := (&Renderer{Img: img, Tiles: m}).Render(sc); err != nil {
		t.Fatalf("render: %v", err)
	}

	full := Render(sc, w, h)
	x, y := w-1, h-1
	i := img.PixOffset(x, y)
	if got, want := [4]byte(img.Pix[i:i+4]), [4]byte(full.Pix[i:i+4]); got != want {
		t.Errorf("pixel (%d,%d) = %v, want %v: culling dropped a node the tile needed", x, y, got, want)
	}
}
