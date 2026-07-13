package main

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"log"
	"math"
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

	patterns := []path.Dash{
		{Pattern: []float64{24, 12}},
		{Pattern: []float64{4, 8}},
		{Pattern: []float64{20, 8, 4, 8}},
	}
	colors := []color.RGBA{ink, blue, rose}
	for i, d := range patterns {
		y := float64(50 + i*44)
		seg := segment(geom.Pt(60, y), geom.Pt(760, y))
		raster.StrokeDashed(img, seg, geom.Identity(),
			path.Stroker{Width: 10, Cap: path.ButtCap}, d, colors[i])
	}

	phase := path.Dash{Pattern: []float64{18, 14}}
	for i := 0; i < 4; i++ {
		y := float64(210 + i*10)
		phase.Phase = float64(i) * 8
		raster.StrokeDashed(img, segment(geom.Pt(60, y), geom.Pt(360, y)),
			geom.Identity(), path.Stroker{Width: 6, Cap: path.ButtCap}, phase, gold)
	}

	wave := sineWave(60, 330, 300, 40)
	raster.StrokeDashed(img, wave, geom.Identity(),
		path.Stroker{Width: 8, Cap: path.RoundCap, Join: path.RoundJoin},
		path.Dash{Pattern: []float64{2, 14}}, rose)

	ring := path.Circle(geom.Pt(560, 320), 90)
	raster.StrokeDashed(img, ring, geom.Identity(),
		path.Stroker{Width: 12, Cap: path.ButtCap, Join: path.RoundJoin},
		path.Dash{Pattern: []float64{30, 18}}, blue)

	box := path.RoundedRect(geom.RectXYWH(430, 60, 320, 120), 24, 24)
	raster.StrokeDashed(img, box, geom.Identity(),
		path.Stroker{Width: 4, Cap: path.ButtCap, Join: path.MiterJoin},
		path.Dash{Pattern: []float64{10, 6}}, ink)

	f, err := os.Create("04-dash.png")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		log.Fatal(err)
	}
	log.Println("wrote 04-dash.png")
}

func segment(a, b geom.Point) path.Path {
	var p path.Path
	p.MoveTo(a)
	p.LineTo(b)
	return p
}

func sineWave(x, y, width, amp float64) path.Path {
	var p path.Path
	const steps = 64
	for i := 0; i <= steps; i++ {
		t := float64(i) / steps
		px := x + width*t
		py := y + amp*math.Sin(t*4*math.Pi)
		if i == 0 {
			p.MoveTo(geom.Pt(px, py))
		} else {
			p.LineTo(geom.Pt(px, py))
		}
	}
	return p
}
