package window

import (
	"image"
	"log"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/stohirov/sukho/backend/cpu"
	"github.com/stohirov/sukho/render"
)

type game struct {
	w, h     int
	frame    func(*render.Canvas)
	canvas   *render.Canvas
	img      *image.RGBA
	renderer *cpu.Renderer

	frames int
	acc    time.Duration
}

func Run(title string, w, h int, frame func(*render.Canvas)) error {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	g := &game{
		w:        w,
		h:        h,
		frame:    frame,
		canvas:   render.NewCanvas(),
		img:      img,
		renderer: &cpu.Renderer{Img: img},
	}
	ebiten.SetWindowSize(w, h)
	ebiten.SetWindowTitle(title)
	return ebiten.RunGame(g)
}

func (g *game) Update() error { return nil }

func (g *game) Draw(screen *ebiten.Image) {
	start := time.Now()

	g.canvas.Reset()
	g.frame(g.canvas)
	clear(g.img.Pix)
	g.renderer.Render(g.canvas.Scene())
	screen.WritePixels(g.img.Pix)

	g.logTiming(time.Since(start))
}

func (g *game) logTiming(d time.Duration) {
	g.frames++
	g.acc += d
	if g.frames >= 60 {
		avg := g.acc / time.Duration(g.frames)
		log.Printf("cpu raster %.2f ms/frame, %.1f fps", float64(avg.Microseconds())/1000, ebiten.ActualFPS())
		g.frames, g.acc = 0, 0
	}
}

func (g *game) Layout(int, int) (int, int) { return g.w, g.h }
