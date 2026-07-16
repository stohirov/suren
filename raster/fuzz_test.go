package raster

import (
	"image"
	"image/color"
	"math/rand"
	"testing"

	"github.com/stohirov/suren/geom"
	"github.com/stohirov/suren/path"
)

func FuzzFillExtreme(f *testing.F) {
	f.Add(int64(1), uint(6))
	f.Add(int64(42), uint(20))
	f.Add(int64(-7), uint(2))

	f.Fuzz(func(t *testing.T, seed int64, n uint) {
		rng := rand.New(rand.NewSource(seed))
		verts := int(n%40) + 1

		coord := func() float64 {
			switch rng.Intn(3) {
			case 0:
				return rng.Float64() * 64
			case 1:
				return (rng.Float64()*2 - 1) * 2e6
			default:
				return (rng.Float64()*2 - 1) * 1e12
			}
		}

		var p path.Path
		p.MoveTo(geom.Pt(coord(), coord()))
		for i := 0; i < verts; i++ {
			switch rng.Intn(4) {
			case 0:
				p.LineTo(geom.Pt(coord(), coord()))
			case 1:
				p.QuadTo(geom.Pt(coord(), coord()), geom.Pt(coord(), coord()))
			case 2:
				p.CubicTo(geom.Pt(coord(), coord()), geom.Pt(coord(), coord()), geom.Pt(coord(), coord()))
			default:
				p.Close()
			}
		}
		p.Close()

		img := image.NewRGBA(image.Rect(0, 0, 48, 32))
		m := geom.Scale(rng.Float64()*4, rng.Float64()*4)
		Fill(img, p, m, color.RGBA{255, 0, 0, 255}, NonZero)
		Fill(img, p, m, color.RGBA{0, 255, 0, 128}, EvenOdd)
	})
}
