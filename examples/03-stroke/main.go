package main

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"os"

	"github.com/stohirov/sukho/geom"
	"github.com/stohirov/sukho/path"
	"github.com/stohirov/sukho/raster"
)

var (
	ink  = color.RGBA{40, 44, 52, 255}
	rose = color.RGBA{214, 69, 65, 255}
	blue = color.RGBA{40, 120, 220, 255}
	gold = color.RGBA{230, 160, 40, 255}
)

func main() {
	const w, h = 820, 460
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{250, 249, 246, 255}), image.Point{}, draw.Src)

	caps := []path.Cap{path.ButtCap, path.RoundCap, path.SquareCap}
	for i, c := range caps {
		x := float64(60 + i*250)
		seg := segment(geom.Pt(x, 70), geom.Pt(x+150, 70))
		raster.Stroke(img, seg, geom.Identity(), path.Stroker{Width: 28, Cap: c}, blue)
		raster.Stroke(img, seg, geom.Identity(), path.Stroker{Width: 2, Cap: path.ButtCap}, color.NRGBA{255, 255, 255, 200})
	}

	joins := []path.Join{path.MiterJoin, path.RoundJoin, path.BevelJoin}
	for i, j := range joins {
		x := float64(60 + i*250)
		raster.Stroke(img, zigzag(x, 170), geom.Identity(),
			path.Stroker{Width: 22, Join: j, MiterLimit: 8}, ink)
	}

	raster.Stroke(img, heartPath(), geom.Translate(20, 250).Mul(geom.Scale(0.34, 0.34)),
		path.Stroker{Width: 34, Join: path.RoundJoin, Cap: path.RoundCap}, rose)

	rings := path.Circle(geom.Pt(430, 350), 70)
	raster.Stroke(img, rings, geom.Identity(), path.Stroker{Width: 14, Join: path.RoundJoin}, gold)
	raster.Stroke(img, path.Circle(geom.Pt(430, 350), 40), geom.Identity(),
		path.Stroker{Width: 6, Join: path.RoundJoin}, ink)

	rr := path.RoundedRect(geom.RectXYWH(0, 0, 190, 90), 30, 30)
	place := geom.Translate(600, 300).Mul(geom.Scale(1.1, 1.1))
	raster.Stroke(img, rr, place, path.Stroker{Width: 10, Join: path.MiterJoin}, blue)

	f, err := os.Create("03-stroke.png")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		log.Fatal(err)
	}
	log.Println("wrote 03-stroke.png")
}

func segment(a, b geom.Point) path.Path {
	var p path.Path
	p.MoveTo(a)
	p.LineTo(b)
	return p
}

func zigzag(x, y float64) path.Path {
	var p path.Path
	p.MoveTo(geom.Pt(x, y+60))
	p.LineTo(geom.Pt(x+50, y))
	p.LineTo(geom.Pt(x+100, y+60))
	p.LineTo(geom.Pt(x+150, y))
	return p
}

func heartPath() path.Path {
	var p path.Path
	p.MoveTo(geom.Pt(256, 464))
	p.CubicTo(geom.Pt(256, 464), geom.Pt(48, 320), geom.Pt(48, 192))
	p.CubicTo(geom.Pt(48, 112), geom.Pt(112, 64), geom.Pt(176, 64))
	p.CubicTo(geom.Pt(216, 64), geom.Pt(248, 88), geom.Pt(256, 120))
	p.CubicTo(geom.Pt(264, 88), geom.Pt(296, 64), geom.Pt(336, 64))
	p.CubicTo(geom.Pt(400, 64), geom.Pt(464, 112), geom.Pt(464, 192))
	p.CubicTo(geom.Pt(464, 320), geom.Pt(256, 464), geom.Pt(256, 464))
	p.Close()
	return p
}
