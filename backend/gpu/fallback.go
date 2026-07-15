package gpu

// Per-tile CPU fallback (Phase 14).
//
// Some features are not reachable at Δ≤1 on the GPU — hardware-filtered image
// sampling, mesh-gradient patch interpolation. The alternative to this file is
// widening the parity tolerance for every scene that contains one, which trades
// a whole frame's worth of guarantee for one node's inexactness. Instead the
// encoder flags the tiles a fallback-marked node touches (encode.go), the CPU
// reference rasterizes exactly those, and their pixels overwrite the GPU's
// before anything reads or presents the target. Every other tile stays on GPU.
//
// Why this is exact rather than merely close:
//
//   - A tile is a complete composite of the scene restricted to its area. The
//     fine rasterizer already composites each tile independently from a scalar
//     backdrop, and compositing is per-pixel, so a tile's final pixels depend on
//     no pixel outside it. Replacing one wholesale cannot leave a seam.
//   - The CPU pass renders the WHOLE scene into the flagged tiles, not just the
//     fallback node. A tile is not "the GPU's pixels with a CPU node blended
//     over" — that would composite the CPU node onto a backdrop the GPU had
//     already quantized differently, which is the exact mistake Phase 13 found
//     and fixed in the shader. It is the reference's answer for that tile.
//   - raster.TileMask gates the write and not the coverage sweep, so the CPU's
//     pixels here are bit-identical to the ones a full cpu.Render would produce
//     (raster/tile.go).
//
// The cost is honest and measured, not hidden: a flagged tile costs a CPU
// composite of every node overlapping it, and the CPU pass cannot skip the
// coverage sweep of a node that reaches a flagged tile even for the columns it
// will not write. BenchmarkFallback reports it against the flagged tile count.

import (
	"image"

	"github.com/stohirov/sukho/backend/cpu"
	"github.com/stohirov/sukho/raster"
	"github.com/stohirov/sukho/scene"
)

// Stats reports what the last frame did. FallbackTiles out of Tiles is "how
// much of the frame fell back"; Phase 22 surfaces it alongside per-stage timing.
type Stats struct {
	Tiles         int
	FallbackTiles int
	Dispatches    int
}

func (r *Renderer) Stats() Stats {
	return Stats{
		Tiles:         r.nx * r.ny,
		FallbackTiles: r.fbTiles,
		Dispatches:    r.dispatches,
	}
}

// markInto transfers the encoder's flagged tiles onto the mask the CPU
// rasterizer gates its writes with. The two grids share tileSize, so this is a
// re-indexing and not a resampling.
func (e *Encoded) markInto(m *raster.TileMask) {
	m.Reset()
	for i, on := range e.FallbackTiles {
		if on {
			m.MarkTile(i%e.NTilesX, i/e.NTilesX)
		}
	}
}

func tileMaskOf(e *Encoded) *raster.TileMask {
	m := raster.NewTileMask(e.Width, e.Height, tileSize)
	e.markInto(m)
	return m
}

// fallback rasterizes the flagged tiles on the CPU and writes them over the
// GPU's. It runs after the compute pass and before any readback or present, so
// both paths see the patched target.
func (r *Renderer) fallback(s *scene.Scene) error {
	r.fbTiles = r.enc.NFallback
	if r.enc.NFallback == 0 {
		return nil
	}

	if r.fbImg == nil || r.fbImg.Rect.Dx() != r.w || r.fbImg.Rect.Dy() != r.h {
		r.fbImg = image.NewRGBA(image.Rect(0, 0, r.w, r.h))
		r.fbCPU = &cpu.Renderer{Img: r.fbImg}
		r.fbMask = raster.NewTileMask(r.w, r.h, tileSize)
	} else {
		// The reference starts every frame from transparent black; reusing a dirty
		// buffer would composite this frame's first node over the last frame's.
		clear(r.fbImg.Pix)
		r.fbMask.Reset()
	}

	r.enc.markInto(r.fbMask)
	r.fbCPU.Tiles = r.fbMask
	if err := r.fbCPU.Render(s); err != nil {
		return err
	}

	return r.uploadFallbackTiles()
}

// uploadFallbackTiles writes each horizontal run of flagged tiles as one region,
// so a contiguous band costs one upload rather than one per tile.
func (r *Renderer) uploadFallbackTiles() error {
	for ty := 0; ty < r.ny; ty++ {
		for tx := 0; tx < r.nx; {
			if !r.enc.FallbackTiles[ty*r.nx+tx] {
				tx++
				continue
			}
			run := tx
			for run < r.nx && r.enc.FallbackTiles[ty*r.nx+run] {
				run++
			}
			x0, x1 := tx*tileSize, min(run*tileSize, r.w)
			y0, y1 := ty*tileSize, min((ty+1)*tileSize, r.h)
			var err error
			r.fbScratch, err = r.target.writeRegion(r.dev, r.fbImg, x0, y0, x1, y1, r.fbScratch)
			if err != nil {
				return err
			}
			tx = run
		}
	}
	return nil
}
