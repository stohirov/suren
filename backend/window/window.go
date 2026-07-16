package window

import (
	"image"
	"log"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/stohirov/suren/backend/cpu"
	"github.com/stohirov/suren/backend/gpu"
	"github.com/stohirov/suren/render"
	"github.com/stohirov/suren/scene"
)

type backend interface {
	frame(*scene.Scene) (*image.RGBA, error)
	resize(w, h int) error
	label() string
	release()
}

type cpuBackend struct {
	img *image.RGBA
	r   *cpu.Renderer
}

func (b *cpuBackend) frame(s *scene.Scene) (*image.RGBA, error) {
	clear(b.img.Pix)
	if err := b.r.Render(s); err != nil {
		return nil, err
	}
	return b.img, nil
}

func (b *cpuBackend) resize(w, h int) error {
	b.img = image.NewRGBA(image.Rect(0, 0, w, h))
	b.r.Img = b.img
	return nil
}

func (b *cpuBackend) label() string { return "cpu raster" }
func (b *cpuBackend) release()      {}

type gpuBackend struct {
	r *gpu.Renderer
}

func (b *gpuBackend) frame(s *scene.Scene) (*image.RGBA, error) {
	if err := b.r.Render(s); err != nil {
		return nil, err
	}
	b.r.Sync()
	return b.r.ReadRGBA()
}

func (b *gpuBackend) resize(w, h int) error { return b.r.Resize(w, h) }
func (b *gpuBackend) label() string         { return "gpu raster" }
func (b *gpuBackend) release()              { b.r.Release() }

type game struct {
	w, h   int
	frame  func(*render.Canvas)
	canvas *render.Canvas
	be     backend

	frames int
	acc    time.Duration
}

func Run(title string, w, h int, frame func(*render.Canvas)) error {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	return run(title, w, h, frame, &cpuBackend{img: img, r: &cpu.Renderer{Img: img}})
}

func RunGPU(title string, w, h int, frame func(*render.Canvas)) error {
	r, err := gpu.NewRenderer(w, h)
	if err != nil {
		return err
	}
	return run(title, w, h, frame, &gpuBackend{r: r})
}

func run(title string, w, h int, frame func(*render.Canvas), be backend) error {
	g := &game{w: w, h: h, frame: frame, canvas: render.NewCanvas(), be: be}
	ebiten.SetWindowSize(w, h)
	ebiten.SetWindowTitle(title)
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	err := ebiten.RunGame(g)
	be.release()
	return err
}

func (g *game) Update() error { return nil }

func (g *game) Draw(screen *ebiten.Image) {
	start := time.Now()

	g.canvas.Reset()
	g.frame(g.canvas)
	img, err := g.be.frame(g.canvas.Scene())
	if err != nil {
		log.Fatal(err)
	}
	screen.WritePixels(img.Pix)

	g.logTiming(time.Since(start))
}

func (g *game) Layout(outsideW, outsideH int) (int, int) {
	if outsideW != g.w || outsideH != g.h {
		if err := g.be.resize(outsideW, outsideH); err != nil {
			log.Fatal(err)
		}
		g.w, g.h = outsideW, outsideH
	}
	return g.w, g.h
}

func (g *game) logTiming(d time.Duration) {
	g.frames++
	g.acc += d
	if g.frames >= 60 {
		avg := g.acc / time.Duration(g.frames)
		log.Printf("%s %.2f ms/frame, %.1f fps", g.be.label(), float64(avg.Microseconds())/1000, ebiten.ActualFPS())
		g.frames, g.acc = 0, 0
	}
}
