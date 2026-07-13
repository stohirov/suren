package gpu

import (
	"image"
	"testing"

	"github.com/stohirov/sukho/backend/cpu"
	"github.com/stohirov/sukho/internal/sample"
)

const (
	benchW, benchH = 1280, 720
)

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
