package main

import (
	"log"
	"math"
	"os"

	"github.com/stohirov/suren/backend/png"
	"github.com/stohirov/suren/backend/svg"
	"github.com/stohirov/suren/geom"
	"github.com/stohirov/suren/paint"
	"github.com/stohirov/suren/path"
	"github.com/stohirov/suren/render"
)

const (
	w = 800
	h = 500
)

var (
	paper = paint.FromRGBA8(250, 249, 246, 255)
	ink   = paint.FromRGBA8(40, 44, 52, 255)
	rose  = paint.FromRGBA8(214, 69, 65, 255)
	blue  = paint.FromRGBA8(40, 120, 220, 255)
	gold  = paint.FromRGBA8(230, 160, 40, 255)
)

func build() *render.Canvas {
	c := render.NewCanvas()

	c.FillColor(path.Rect(geom.RectXYWH(0, 0, w, h)), paper)

	square := path.Rect(geom.RectXYWH(-40, -40, 80, 80))
	translucent := paint.RGBA(rose.R, rose.G, rose.B, 0.35)
	for i := 0; i < 12; i++ {
		c.Save()
		c.Translate(200, 250)
		c.Rotate(float64(i) / 12 * 2 * math.Pi)
		c.Translate(110, 0)
		c.FillColor(square, translucent)
		c.Restore()
	}

	c.Save()
	c.Translate(560, 170)
	c.FillColor(star(90, 40, 5), blue)
	c.Restore()

	c.StrokeColor(path.Circle(geom.Pt(560, 360), 90), gold, paint.Stroke{
		Width:  12,
		Join:   path.RoundJoin,
		Dashes: []float64{28, 16},
	})

	var bolt path.Path
	bolt.MoveTo(geom.Pt(60, 420))
	bolt.LineTo(geom.Pt(120, 320))
	bolt.LineTo(geom.Pt(100, 380))
	bolt.LineTo(geom.Pt(170, 300))
	c.StrokeColor(bolt, ink, paint.Stroke{Width: 10, Cap: path.RoundCap, Join: path.MiterJoin})

	return c
}

func star(outer, inner float64, points int) path.Path {
	var p path.Path
	for i := 0; i < points*2; i++ {
		r := outer
		if i%2 == 1 {
			r = inner
		}
		a := -math.Pi/2 + float64(i)/float64(points*2)*2*math.Pi
		sin, cos := math.Sincos(a)
		pt := geom.Pt(r*cos, r*sin)
		if i == 0 {
			p.MoveTo(pt)
		} else {
			p.LineTo(pt)
		}
	}
	p.Close()
	return p
}

func main() {
	c := build()

	pngFile, err := os.Create("05-scene.png")
	if err != nil {
		log.Fatal(err)
	}
	defer pngFile.Close()
	if err := png.Encode(pngFile, c.Scene(), w, h); err != nil {
		log.Fatal(err)
	}

	svgFile, err := os.Create("05-scene.svg")
	if err != nil {
		log.Fatal(err)
	}
	defer svgFile.Close()
	if err := svg.Encode(svgFile, c.Scene(), w, h); err != nil {
		log.Fatal(err)
	}

	log.Println("wrote 05-scene.png and 05-scene.svg")
}
