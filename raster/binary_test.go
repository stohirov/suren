package raster

import (
	"image"
	"image/color"
	"testing"

	"github.com/stohirov/suren/geom"
	"github.com/stohirov/suren/path"
)

func rotatedSquare() path.Path {
	var p path.Path
	p.MoveTo(geom.Pt(20, 4))
	p.LineTo(geom.Pt(36, 20))
	p.LineTo(geom.Pt(20, 36))
	p.LineTo(geom.Pt(4, 20))
	p.Close()
	return p
}

func alphaSet(img *image.RGBA) (intermediate, opaque int) {
	for i := 3; i < len(img.Pix); i += 4 {
		switch a := img.Pix[i]; {
		case a == 255:
			opaque++
		case a > 0:
			intermediate++
		}
	}
	return
}

func TestBinaryFillNoAA(t *testing.T) {
	red := color.RGBA{255, 0, 0, 255}

	aa := image.NewRGBA(image.Rect(0, 0, 40, 40))
	Fill(aa, rotatedSquare(), geom.Identity(), red, NonZero)
	aaInter, aaOpaque := alphaSet(aa)

	bin := image.NewRGBA(image.Rect(0, 0, 40, 40))
	FillBinary(bin, rotatedSquare(), geom.Identity(), red, NonZero)
	binInter, binOpaque := alphaSet(bin)

	if binInter != 0 {
		t.Errorf("binary fill produced %d anti-aliased pixels, want 0", binInter)
	}
	if aaInter == 0 {
		t.Errorf("AA fill produced no intermediate-coverage pixels (edges should be soft)")
	}
	if binOpaque == 0 || aaOpaque == 0 {
		t.Fatalf("expected filled pixels: aa=%d bin=%d", aaOpaque, binOpaque)
	}
}
