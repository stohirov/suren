package main

import (
	"flag"
	"log"
	"math"
	"time"

	"github.com/stohirov/sukho/backend/window"
	"github.com/stohirov/sukho/geom"
	"github.com/stohirov/sukho/paint"
	"github.com/stohirov/sukho/path"
	"github.com/stohirov/sukho/render"
)

const (
	w = 640
	h = 480
)

func main() {
	useGPU := flag.Bool("gpu", false, "render with the GPU backend")
	flag.Parse()

	start := time.Now()
	center := geom.Pt(w/2, h/2)

	run := window.Run
	title := "sukho — interactive (cpu)"
	if *useGPU {
		run = window.RunGPU
		title = "sukho — interactive (gpu)"
	}

	err := run(title, w, h, func(c *render.Canvas) {
		t := time.Since(start).Seconds()

		c.FillColor(path.Rect(geom.RectXYWH(0, 0, w, h)), paint.FromRGBA8(20, 22, 28, 255))

		c.Save()
		c.Translate(center.X, center.Y)
		c.Rotate(t * 0.6)
		for i := 0; i < 12; i++ {
			c.Save()
			c.Rotate(float64(i) / 12 * 2 * math.Pi)
			var spoke path.Path
			spoke.MoveTo(geom.Pt(48, 0))
			spoke.LineTo(geom.Pt(190, 0))
			c.StrokeColor(spoke, spokeColor(i), paint.Stroke{Width: 12, Cap: path.RoundCap})
			c.Restore()
		}
		c.Restore()

		r := 150 + 30*math.Sin(t*2)
		c.StrokeColor(path.Circle(center, r), paint.FromRGBA8(240, 240, 245, 255), paint.Stroke{
			Width:      6,
			Cap:        path.ButtCap,
			Join:       path.RoundJoin,
			Dashes:     []float64{22, 14},
			DashOffset: -t * 60,
		})
	})
	if err != nil {
		log.Fatal(err)
	}
}

func spokeColor(i int) paint.Color {
	a := float64(i) / 12 * 2 * math.Pi
	return paint.Color{
		R: 0.5 + 0.5*math.Sin(a),
		G: 0.5 + 0.5*math.Sin(a+2*math.Pi/3),
		B: 0.5 + 0.5*math.Sin(a+4*math.Pi/3),
		A: 1,
	}
}
