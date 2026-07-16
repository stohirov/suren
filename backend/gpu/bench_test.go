package gpu

import (
	"image"
	"strconv"
	"testing"

	"github.com/cogentcore/webgpu/wgpu"
	"github.com/stohirov/suren/backend/cpu"
	"github.com/stohirov/suren/internal/sample"
	"github.com/stohirov/suren/scene"
)

const (
	benchW, benchH = 1280, 720
	benchSpikes    = 2000
)

func benchDispatch(b *testing.B, s *scene.Scene) {
	r, err := NewRenderer(benchW, benchH)
	if err != nil {
		b.Skipf("no gpu device: %v", err)
	}
	defer r.Release()
	if err := r.Render(s); err != nil {
		b.Fatal(err)
	}
	r.Sync()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := r.ras.run(r.dev, r.target, r.segBuf, r.nodeBuf, r.tileOff, r.tileNode, r.stopBuf, r.tileSegOf, r.tileSegIx, r.clipsBuf, r.nx, r.ny); err != nil {
			b.Fatal(err)
		}
		r.Sync()
	}
}

func BenchmarkGPUDispatchManySegments(b *testing.B) {
	benchDispatch(b, sample.ManySegments(benchW, benchH, benchSpikes))
}

func BenchmarkGPUDispatchManyNodes(b *testing.B) {
	benchDispatch(b, sample.ManyNodes(benchW, benchH, 40, 24))
}

func BenchmarkEncodeManySegments(b *testing.B) {
	s := sample.ManySegments(benchW, benchH, benchSpikes)
	e := &Encoded{}
	EncodeInto(e, s, benchW, benchH)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		EncodeInto(e, s, benchW, benchH)
	}
}

func TestPhase8Redundancy(t *testing.T) {
	report := func(name string, s *scene.Scene) {
		e := Encode(s, benchW, benchH)
		naive := 0
		for ni := range e.Nodes {
			tx0, tx1, ty0, ty1 := tileRange(e.Nodes[ni].BBox, e.NTilesX, e.NTilesY)
			naive += int(e.Nodes[ni].SegCount) * (tx1 - tx0) * (min(benchH, ty1*tileSize) - ty0*tileSize)
		}
		coarse := len(e.TileSegIdx)
		t.Logf("%-12s nodes=%d segs=%d  naive-scans=%d (%.0fx)  coarse-refs=%d (%.1fx)  reduction=%.0fx",
			name, len(e.Nodes), len(e.Segments),
			naive, float64(naive)/float64(len(e.Segments)),
			coarse, float64(coarse)/float64(len(e.Segments)),
			float64(naive)/float64(coarse))
	}
	report("many-nodes", sample.ManyNodes(benchW, benchH, 40, 24))
	report("many-segs", sample.ManySegments(benchW, benchH, benchSpikes))
}

func BenchmarkCPUManyNodes(b *testing.B) {
	s := sample.ManyNodes(benchW, benchH, 40, 24)
	img := image.NewRGBA(image.Rect(0, 0, benchW, benchH))
	r := &cpu.Renderer{Img: img}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		clear(img.Pix)
		r.Render(s)
	}
}

func BenchmarkEncodeManyNodes(b *testing.B) {
	s := sample.ManyNodes(benchW, benchH, 40, 24)
	e := &Encoded{}
	EncodeInto(e, s, benchW, benchH)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		EncodeInto(e, s, benchW, benchH)
	}
}

// BenchmarkFallback prices the per-tile CPU fallback against how much of the
// frame it covers. Each coverage runs twice over the same geometry — once marked,
// once not — so the pair isolates the fallback's cost from the scene's.
//
// It clears haveFrame every iteration: Phase 7's fingerprint skip would
// otherwise make every iteration after the first a no-op and report the cost of
// comparing a hash. That is also why these numbers include the encode.
func BenchmarkFallback(b *testing.B) {
	for _, frac := range []float64{0, 0.25, 0.5, 1} {
		for _, on := range []bool{false, true} {
			name := "coverage=" + strconv.Itoa(int(frac*100)) + "%"
			if on {
				name += "/fallback"
			} else {
				name += "/gpu-only"
			}
			b.Run(name, func(b *testing.B) {
				r, err := NewRenderer(benchW, benchH)
				if err != nil {
					b.Skipf("no gpu device: %v", err)
				}
				defer r.Release()
				s := sample.FallbackBand(benchW, benchH, frac, on)

				if err := r.Render(s); err != nil {
					b.Fatal(err)
				}
				r.Sync()

				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					r.haveFrame = false
					if err := r.Render(s); err != nil {
						b.Fatal(err)
					}
					r.Sync()
				}

				// After the loop: ResetTimer clears the extra-metric map.
				st := r.Stats()
				b.ReportMetric(float64(st.FallbackTiles), "cpu-tiles")
				b.ReportMetric(float64(st.FallbackTiles)/float64(st.Tiles)*100, "%frame")
			})
		}
	}
}

func BenchmarkGPUManyNodes(b *testing.B) {
	r, err := NewRenderer(benchW, benchH)
	if err != nil {
		b.Skipf("no gpu device: %v", err)
	}
	defer r.Release()
	s := sample.ManyNodes(benchW, benchH, 40, 24)

	if err := r.Render(s); err != nil {
		b.Fatal(err)
	}
	r.Sync()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := r.Render(s); err != nil {
			b.Fatal(err)
		}
		r.Sync()
	}
}

// The pair below prices what 6b removes, and prices it headlessly so the answer
// does not depend on which window had focus. Each runs the same dispatch on the
// same scene and size, then differs only in how the finished frame reaches a
// display:
//
//   - Readback is 6a's bridge: Sync, then pull the whole frame across the bus
//     into an image.RGBA for the window library to upload again.
//   - Blit is 6b: one render pass into a texture of the surface's own format.
//     A real swapchain image differs only in who allocated it, so this is the
//     present cost with the acquire/Present pacing left out.
//
// The window numbers from the demo cannot answer this: vsync pins them to the
// display, and macOS throttles an unfocused window, which is exactly the
// condition a backgrounded benchmark run would be measuring.
func benchPresentPath(b *testing.B, blit bool) {
	r, err := NewRenderer(benchW, benchH)
	if err != nil {
		b.Skipf("no gpu device: %v", err)
	}
	defer r.Release()

	if err := r.Render(sample.ManyNodes(benchW, benchH, 40, 24)); err != nil {
		b.Fatal(err)
	}
	r.Sync()

	dispatch := func() {
		if err := r.ras.run(r.dev, r.target, r.segBuf, r.nodeBuf, r.tileOff, r.tileNode, r.stopBuf, r.tileSegOf, r.tileSegIx, r.clipsBuf, r.nx, r.ny); err != nil {
			b.Fatal(err)
		}
	}

	if !blit {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			dispatch()
			// No Sync() first, though window.RunGPU's frame does exactly that:
			// ReadRGBA already polls until its map completes, which cannot
			// happen before the dispatch and the copy have. Syncing first only
			// adds a second full stall, and charging that to "readback" would
			// be beating 6a's approach by benchmarking 6a's redundancy.
			if _, err := r.ReadRGBA(); err != nil {
				b.Fatal(err)
			}
		}
		return
	}

	const format = wgpu.TextureFormatBGRA8Unorm
	dst, err := r.dev.device.CreateTexture(&wgpu.TextureDescriptor{
		Usage:         wgpu.TextureUsageRenderAttachment,
		Dimension:     wgpu.TextureDimension2D,
		Size:          wgpu.Extent3D{Width: benchW, Height: benchH, DepthOrArrayLayers: 1},
		Format:        format,
		MipLevelCount: 1,
		SampleCount:   1,
	})
	if err != nil {
		b.Fatal(err)
	}
	defer dst.Release()
	view, err := dst.CreateView(nil)
	if err != nil {
		b.Fatal(err)
	}
	defer view.Release()
	bl, err := newBlitter(r.dev, format)
	if err != nil {
		b.Fatal(err)
	}
	defer bl.release()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dispatch()
		if err := bl.draw(r.dev, r.target.view, view); err != nil {
			b.Fatal(err)
		}
		r.Sync()
	}
}

func BenchmarkPresentViaReadback(b *testing.B) { benchPresentPath(b, false) }

func BenchmarkPresentViaBlit(b *testing.B) { benchPresentPath(b, true) }
