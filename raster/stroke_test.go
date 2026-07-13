package raster

import (
	"image"
	"image/color"
	"testing"

	"github.com/stohirov/sukho/geom"
	"github.com/stohirov/sukho/path"
)

func TestStrokeComposites(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 40, 40))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	var p path.Path
	p.MoveTo(geom.Pt(5, 20))
	p.LineTo(geom.Pt(35, 20))
	Stroke(img, p, geom.Identity(), path.Stroker{Width: 8, Cap: path.ButtCap}, color.RGBA{200, 0, 0, 255})

	if got := img.RGBAAt(20, 20); got != (color.RGBA{200, 0, 0, 255}) {
		t.Fatalf("on-stroke pixel = %v, want red", got)
	}
	if got := img.RGBAAt(20, 5); got != (color.RGBA{255, 255, 255, 255}) {
		t.Fatalf("off-stroke pixel = %v, want white", got)
	}
}

func TestStrokeToleranceScales(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 200, 80))
	var p path.Path
	p.MoveTo(geom.Pt(4, 5))
	p.LineTo(geom.Pt(20, 5))
	m := geom.Scale(8, 8)
	Stroke(img, p, m, path.Stroker{Width: 4, Cap: path.RoundCap}, color.RGBA{0, 0, 0, 255})

	partial := false
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if a := img.RGBAAt(x, y).A; a > 0 && a < 255 {
				partial = true
			}
		}
	}
	if !partial {
		t.Fatal("no anti-aliased edge pixels: round cap tessellated too coarsely")
	}
}
