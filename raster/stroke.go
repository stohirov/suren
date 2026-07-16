package raster

import (
	"image"
	"image/color"

	"github.com/stohirov/suren/geom"
	"github.com/stohirov/suren/path"
)

func Stroke(dst *image.RGBA, p path.Path, m geom.Matrix, s path.Stroker, c color.Color) {
	tol := path.DefaultTolerance
	if k := m.MaxScale(); k > 0 {
		tol /= k
	}
	Fill(dst, s.Stroke(p, tol), m, c, NonZero)
}

func StrokeDashed(dst *image.RGBA, p path.Path, m geom.Matrix, s path.Stroker, d path.Dash, c color.Color) {
	tol := path.DefaultTolerance
	if k := m.MaxScale(); k > 0 {
		tol /= k
	}
	Fill(dst, s.Stroke(d.Apply(p, tol), tol), m, c, NonZero)
}
