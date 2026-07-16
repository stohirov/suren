//go:build gpupresent

// Windowed present demo (Phase 6b). Needs a display, so it carries the same
// gpupresent tag as the code it drives:
//
//	go run -tags gpupresent ./example/present          # animated, fills the window
//	go run -tags gpupresent ./example/present -static  # the sample scene, 1:1
//
// -static presents internal/sample's Scene at its native 240x180 in the corner
// of the window — the same scene the offscreen PNG and the parity corpus use, so
// it can be compared against them by eye. The blit is already gated numerically
// by TestBlitToUnormSurfaceIsExact; what only a real window can show is that a
// live swapchain presents what the blit wrote.
package main

import (
	"flag"
	"log"
	"math"
	"time"

	"github.com/cogentcore/webgpu/wgpu"
	"github.com/stohirov/suren/backend/gpu"
	"github.com/stohirov/suren/geom"
	"github.com/stohirov/suren/internal/sample"
	"github.com/stohirov/suren/paint"
	"github.com/stohirov/suren/path"
	"github.com/stohirov/suren/render"
)

const (
	w = 640
	h = 480
)

func main() {
	static := flag.Bool("static", false, "present the sample scene instead of the animation")
	unsynced := flag.Bool("unsynced", false, "drop the vsync cap so the frame rate measures the renderer, not the display")
	frames := flag.Int("frames", 0, "exit after this many frames (0 = until the window closes)")
	flag.Parse()

	// Capturing the Presenter out of Ready is what lets -frames stop the run: it
	// is the only handle RunPresent hands back, and without it a scripted run
	// could only be killed, which never reaches Release.
	var p *gpu.Presenter
	o := gpu.Options{Unsynced: *unsynced, Ready: func(r *gpu.Presenter) {
		p = r
		logSetup(r)
	}}

	var n int
	limit := func() {
		if n++; *frames > 0 && n >= *frames && p != nil {
			p.Close()
		}
	}

	var err error
	if *static {
		err = runStatic(o, limit)
	} else {
		err = runAnimated(o, limit)
	}
	if err != nil {
		log.Fatal(err)
	}
}

// runStatic drives the Presenter directly: the sample scene is a prebuilt
// *scene.Scene, not canvas calls, so it does not go through RunPresent's canvas.
func runStatic(o gpu.Options, tick func()) error {
	p, err := gpu.NewPresenterWith("suren — sample scene (gpu present)", sample.W, sample.H, o)
	if err != nil {
		return err
	}
	defer p.Release()

	// Note this is the 7c skip path as much as the present path: the scene never
	// changes, so every frame after the first re-presents the retained target
	// without an upload or a dispatch.
	s := sample.Scene()
	for !p.ShouldClose() {
		if err := p.Frame(s); err != nil {
			return err
		}
		tick()
	}
	return nil
}

func runAnimated(o gpu.Options, limit func()) error {
	start := time.Now()
	center := geom.Pt(w/2, h/2)
	tick := newTicker()

	return gpu.RunPresentWith("suren — interactive (gpu present)", w, h, o, func(c *render.Canvas) {
		tick()
		limit()
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
}

// logSetup records what was actually negotiated rather than what was assumed:
// the surface format decides which blit shader runs, the framebuffer size is the
// resolution the renderer really drew at rather than the window's point size,
// and the present mode decides whether the frame rate below means anything.
func logSetup(p *gpu.Presenter) {
	fw, fh := p.Size()
	log.Printf("surface format=%v present=%v framebuffer=%dx%d scale=%.2gx adapter=%s",
		p.Format(), p.PresentMode(), fw, fh, p.ContentScale(), p.Renderer().Device().Describe())
	if p.PresentMode() == wgpu.PresentModeFifo {
		log.Print("note: vsync — the frame rate below is the display's refresh, not the renderer's ceiling; use -unsynced to measure")
	}
}

// newTicker returns a per-frame hook that logs the presented frame rate.
func newTicker() func() {
	var frames int
	last := time.Now()
	return func() {
		frames++
		if frames < 60 {
			return
		}
		d := time.Since(last)
		log.Printf("gpu present %.2f ms/frame, %.1f fps",
			float64(d.Microseconds())/1000/float64(frames), float64(frames)/d.Seconds())
		frames, last = 0, time.Now()
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
