package raster

// TileMask gates which pixels a fill is allowed to WRITE, in square blocks of
// Size pixels. Coordinates are image-local: (0,0) is the destination image's
// Bounds().Min.
//
// It gates the write and nothing else. FillPaint's coverage sweep still runs
// from the path's own left edge and accumulates across masked-out columns
// before reaching a live one (raster/fill.go), so the winding total arriving at
// any written pixel is the same total a full-frame fill would have computed.
// That is what makes a masked fill bit-identical to the corresponding pixels of
// an unmasked one, rather than merely close — and it is the property the GPU's
// per-tile CPU fallback rests on (backend/gpu, Phase 14). Narrowing the sweep
// itself would break it.
type TileMask struct {
	Size   int
	NX, NY int
	Flags  []bool
}

func NewTileMask(w, h, size int) *TileMask {
	nx, ny := (w+size-1)/size, (h+size-1)/size
	return &TileMask{Size: size, NX: nx, NY: ny, Flags: make([]bool, nx*ny)}
}

func (m *TileMask) Reset() { clear(m.Flags) }

// Mark flags every tile overlapping the half-open pixel rect [x0,x1)×[y0,y1).
func (m *TileMask) Mark(x0, y0, x1, y1 int) {
	tx0, ty0 := clampInt(x0/m.Size, 0, m.NX), clampInt(y0/m.Size, 0, m.NY)
	tx1 := clampInt((x1+m.Size-1)/m.Size, 0, m.NX)
	ty1 := clampInt((y1+m.Size-1)/m.Size, 0, m.NY)
	for ty := ty0; ty < ty1; ty++ {
		for tx := tx0; tx < tx1; tx++ {
			m.Flags[ty*m.NX+tx] = true
		}
	}
}

func (m *TileMask) MarkTile(tx, ty int) {
	if tx >= 0 && tx < m.NX && ty >= 0 && ty < m.NY {
		m.Flags[ty*m.NX+tx] = true
	}
}

func (m *TileMask) At(px, py int) bool {
	tx, ty := px/m.Size, py/m.Size
	if tx < 0 || tx >= m.NX || ty < 0 || ty >= m.NY {
		return false
	}
	return m.Flags[ty*m.NX+tx]
}

func (m *TileMask) Count() int {
	n := 0
	for _, f := range m.Flags {
		if f {
			n++
		}
	}
	return n
}

// Overlaps reports whether any flagged tile meets the half-open pixel rect. A
// node that fails this contributes to no written pixel and can be skipped
// outright — the only sound way to make a masked fill cheaper than a full one,
// since the alternative (clipping the sweep) would change the arithmetic.
func (m *TileMask) Overlaps(x0, y0, x1, y1 int) bool {
	tx0, ty0 := clampInt(x0/m.Size, 0, m.NX), clampInt(y0/m.Size, 0, m.NY)
	tx1 := clampInt((x1+m.Size-1)/m.Size, 0, m.NX)
	ty1 := clampInt((y1+m.Size-1)/m.Size, 0, m.NY)
	for ty := ty0; ty < ty1; ty++ {
		for tx := tx0; tx < tx1; tx++ {
			if m.Flags[ty*m.NX+tx] {
				return true
			}
		}
	}
	return false
}

// Bounds returns the pixel bounding box of the flagged tiles, and false if none
// are flagged.
func (m *TileMask) Bounds() (x0, y0, x1, y1 int, ok bool) {
	tx0, ty0, tx1, ty1 := m.NX, m.NY, 0, 0
	for ty := 0; ty < m.NY; ty++ {
		for tx := 0; tx < m.NX; tx++ {
			if !m.Flags[ty*m.NX+tx] {
				continue
			}
			tx0, ty0 = min(tx0, tx), min(ty0, ty)
			tx1, ty1 = max(tx1, tx+1), max(ty1, ty+1)
		}
	}
	if tx0 >= tx1 || ty0 >= ty1 {
		return 0, 0, 0, 0, false
	}
	return tx0 * m.Size, ty0 * m.Size, tx1 * m.Size, ty1 * m.Size, true
}
