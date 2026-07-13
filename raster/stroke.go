package raster

import (
	"image"
	"image/color"

	"github.com/stohirov/sukho/geom"
	"github.com/stohirov/sukho/path"
)

func Stroke(dst *image.RGBA, p path.Path, m geom.Matrix, s path.Stroker, c color.Color) {
	tol := path.DefaultTolerance
	if k := m.MaxScale(); k > 0 {
		tol /= k
	}
	Fill(dst, s.Stroke(p, tol), m, c, NonZero)
}
