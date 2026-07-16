package raster

import (
	"image"
	"image/color"
	"math"
	"testing"

	"github.com/stohirov/suren/geom"
	"github.com/stohirov/suren/path"
)

func TestStrokeDashedArea(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 110, 40))
	var p path.Path
	p.MoveTo(geom.Pt(5, 20))
	p.LineTo(geom.Pt(105, 20))

	s := path.Stroker{Width: 10, Cap: path.ButtCap}
	d := path.Dash{Pattern: []float64{20, 20}}
	StrokeDashed(img, p, geom.Identity(), s, d, color.RGBA{0, 0, 0, 255})

	sum := 0.0
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			sum += float64(img.RGBAAt(x, y).A) / 255
		}
	}
	if math.Abs(sum-600) > 1 {
		t.Fatalf("dashed stroke area = %v, want ~600 (three 20x10 dashes)", sum)
	}
}

func TestStrokeDashedGaps(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 110, 40))
	var p path.Path
	p.MoveTo(geom.Pt(5, 20))
	p.LineTo(geom.Pt(105, 20))
	s := path.Stroker{Width: 10, Cap: path.ButtCap}
	d := path.Dash{Pattern: []float64{20, 20}}
	StrokeDashed(img, p, geom.Identity(), s, d, color.RGBA{0, 0, 0, 255})

	if a := img.RGBAAt(15, 20).A; a == 0 {
		t.Fatalf("expected ink inside first dash at x=15, got alpha 0")
	}
	if a := img.RGBAAt(35, 20).A; a != 0 {
		t.Fatalf("expected a gap at x=35, got alpha %d", a)
	}
}
