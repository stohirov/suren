package main

import (
	"log"
	"os"

	"github.com/stohirov/sukho/backend/png"
	"github.com/stohirov/sukho/backend/svg"
	"github.com/stohirov/sukho/geom"
	"github.com/stohirov/sukho/paint"
	"github.com/stohirov/sukho/path"
	"github.com/stohirov/sukho/render"
)

const (
	w = 640
	h = 400
)

func build() *render.Canvas {
	c := render.NewCanvas()

	c.Fill(path.Rect(geom.RectXYWH(0, 0, w, h)), paint.LinearGradient{
		P0: geom.Pt(0, 0),
		P1: geom.Pt(0, h),
		Stops: []paint.Stop{
			{Offset: 0, Color: paint.FromRGBA8(24, 26, 32, 255)},
			{Offset: 1, Color: paint.FromRGBA8(40, 80, 140, 255)},
		},
	}, paint.NonZero)

	c.Fill(path.Circle(geom.Pt(190, 200), 130), paint.RadialGradient{
		Center: geom.Pt(150, 160),
		Radius: 170,
		Stops: []paint.Stop{
			{Offset: 0, Color: paint.RGBA(1, 1, 1, 1)},
			{Offset: 0.55, Color: paint.FromRGBA8(230, 160, 40, 255)},
			{Offset: 1, Color: paint.RGBA(214/255.0, 69/255.0, 65/255.0, 0)},
		},
	}, paint.NonZero)

	c.Fill(path.RoundedRect(geom.RectXYWH(370, 90, 210, 220), 28, 28), paint.LinearGradient{
		P0: geom.Pt(370, 90),
		P1: geom.Pt(580, 310),
		Stops: []paint.Stop{
			{Offset: 0, Color: paint.FromRGBA8(80, 220, 200, 255)},
			{Offset: 0.5, Color: paint.FromRGBA8(120, 90, 220, 255)},
			{Offset: 1, Color: paint.FromRGBA8(230, 70, 120, 255)},
		},
	}, paint.NonZero)

	c.Stroke(path.Circle(geom.Pt(475, 200), 70), paint.LinearGradient{
		P0: geom.Pt(405, 200),
		P1: geom.Pt(545, 200),
		Stops: []paint.Stop{
			{Offset: 0, Color: paint.RGB(1, 1, 1)},
			{Offset: 1, Color: paint.FromRGBA8(24, 26, 32, 255)},
		},
	}, paint.Stroke{Width: 14, Join: path.RoundJoin})

	return c
}

func main() {
	c := build()

	pngFile, err := os.Create("06-gradient.png")
	if err != nil {
		log.Fatal(err)
	}
	defer pngFile.Close()
	if err := png.Encode(pngFile, c.Scene(), w, h); err != nil {
		log.Fatal(err)
	}

	svgFile, err := os.Create("06-gradient.svg")
	if err != nil {
		log.Fatal(err)
	}
	defer svgFile.Close()
	if err := svg.Encode(svgFile, c.Scene(), w, h); err != nil {
		log.Fatal(err)
	}

	log.Println("wrote 06-gradient.png and 06-gradient.svg")
}
