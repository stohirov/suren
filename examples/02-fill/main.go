package main

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"math"
	"os"

	"github.com/stohirov/suren/geom"
	"github.com/stohirov/suren/path"
	"github.com/stohirov/suren/raster"
)

func main() {
	const w, h = 800, 420
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{250, 249, 246, 255}), image.Point{}, draw.Src)

	heart := heartPath()
	raster.Fill(img, heart, geom.Translate(-20, 10).Mul(geom.Scale(0.78, 0.78)),
		color.RGBA{214, 69, 65, 255}, raster.NonZero)

	raster.Fill(img, path.Circle(geom.Pt(300, 300), 80), geom.Identity(),
		premul(color.RGBA{40, 120, 220, 255}, 150), raster.NonZero)
	raster.Fill(img, path.Circle(geom.Pt(370, 300), 80), geom.Identity(),
		premul(color.RGBA{40, 200, 140, 255}, 150), raster.NonZero)

	ellipse := path.Ellipse(geom.Pt(620, 120), 110, 46)
	spin := geom.Translate(620, 120).Mul(geom.Rotate(math.Pi / 6)).Mul(geom.Translate(-620, -120))
	raster.Fill(img, ellipse, spin, color.RGBA{240, 170, 40, 255}, raster.NonZero)

	donut := path.Circle(geom.Pt(620, 300), 80)
	appendPath(&donut, path.Circle(geom.Pt(620, 300), 40))
	raster.Fill(img, donut, geom.Identity(), color.RGBA{120, 80, 200, 255}, raster.EvenOdd)

	raster.Fill(img, path.RoundedRect(geom.RectXYWH(40, 300, 180, 90), 28, 28), geom.Identity(),
		color.RGBA{60, 60, 72, 255}, raster.NonZero)

	f, err := os.Create("02-fill.png")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		log.Fatal(err)
	}
	log.Println("wrote 02-fill.png")
}

func premul(c color.RGBA, a uint8) color.RGBA {
	s := func(v uint8) uint8 { return uint8(uint32(v) * uint32(a) / 255) }
	return color.RGBA{s(c.R), s(c.G), s(c.B), a}
}

func appendPath(dst *path.Path, src path.Path) {
	it := src.Iter()
	for {
		v, pts, ok := it.Next()
		if !ok {
			return
		}
		switch v {
		case path.MoveTo:
			dst.MoveTo(pts[0])
		case path.LineTo:
			dst.LineTo(pts[0])
		case path.QuadTo:
			dst.QuadTo(pts[0], pts[1])
		case path.CubicTo:
			dst.CubicTo(pts[0], pts[1], pts[2])
		case path.Close:
			dst.Close()
		}
	}
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
