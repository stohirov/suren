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
)

func main() {
	const w, h = 800, 420
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)

	ink := color.RGBA{30, 30, 40, 255}

	heart := heartPath()
	drawOutline(img, heart, geom.Translate(-20, 10).Mul(geom.Scale(0.78, 0.78)), ink)

	drawOutline(img, path.Circle(geom.Pt(490, 110), 70), geom.Identity(), ink)
	ellipse := path.Ellipse(geom.Pt(660, 110), 100, 45)
	spin := geom.Translate(660, 110).Mul(geom.Rotate(math.Pi / 6)).Mul(geom.Translate(-660, -110))
	drawOutline(img, ellipse, spin, ink)

	drawOutline(img, path.RoundedRect(geom.RectXYWH(440, 230, 320, 150), 36, 36), geom.Identity(), ink)

	f, err := os.Create("01-outline.png")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		log.Fatal(err)
	}
	log.Println("wrote 01-outline.png")
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

func drawOutline(img *image.RGBA, p path.Path, m geom.Matrix, c color.RGBA) {
	p.Flatten(path.DefaultTolerance, m, func(pts []geom.Point, closed bool) {
		for i := 0; i+1 < len(pts); i++ {
			line(img, pts[i], pts[i+1], c)
		}
		if closed {
			line(img, pts[len(pts)-1], pts[0], c)
		}
	})
}

func line(img *image.RGBA, a, b geom.Point, c color.RGBA) {
	dx, dy := b.X-a.X, b.Y-a.Y
	steps := int(math.Max(math.Abs(dx), math.Abs(dy))) + 1
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		img.SetRGBA(int(a.X+dx*t+0.5), int(a.Y+dy*t+0.5), c)
	}
}
