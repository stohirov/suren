package cpu

import (
	"image"
	"testing"

	"github.com/stohirov/sukho/internal/sample"
	"github.com/stohirov/sukho/raster"
	"github.com/stohirov/sukho/scene"
)

func oldRender(img *image.RGBA, s *scene.Scene) {
	view := viewRect(img.Bounds())
	for _, n := range s.Nodes {
		if culled(n, view) {
			continue
		}
		geo := n.Path
		rule := fillRule(n.FillRule)
		if n.Stroke != nil {
			geo = strokeOutline(n)
			rule = raster.NonZero
		}
		clip, hasClip := clipRect(n.Clip)
		if col, ok := solidColor(n.Paint); ok {
			if hasClip {
				raster.FillClip(img, geo, n.Transform, col, rule, clip)
			} else {
				raster.Fill(img, geo, n.Transform, col, rule)
			}
			continue
		}
		if sh, ok := shader(n.Paint, n.Transform); ok {
			if hasClip {
				raster.FillShaderClip(img, geo, n.Transform, sh, rule, clip)
			} else {
				raster.FillShader(img, geo, n.Transform, sh, rule)
			}
		}
	}
}

func benchOld(b *testing.B, s *scene.Scene, w, h int) {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		clear(img.Pix)
		oldRender(img, s)
	}
}

func BenchmarkSampleOld(b *testing.B)   { benchOld(b, sample.Scene(), sample.W, sample.H) }
func BenchmarkGradientOld(b *testing.B) { benchOld(b, sample.GradientScene(), sample.W, sample.H) }

func BenchmarkManyNodesOld(b *testing.B) {
	s, w, h := manyNodesScene()
	benchOld(b, s, w, h)
}

func benchScene(b *testing.B, s *scene.Scene, w, h int) {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	r := &Renderer{Img: img}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		clear(img.Pix)
		r.Render(s)
	}
}

func BenchmarkSample(b *testing.B)   { benchScene(b, sample.Scene(), sample.W, sample.H) }
func BenchmarkGradient(b *testing.B) { benchScene(b, sample.GradientScene(), sample.W, sample.H) }

func manyNodesScene() (*scene.Scene, int, int) {
	return sample.ManyNodes(1280, 720, 40, 24), 1280, 720
}

func BenchmarkManyNodes(b *testing.B) {
	s, w, h := manyNodesScene()
	benchScene(b, s, w, h)
}
