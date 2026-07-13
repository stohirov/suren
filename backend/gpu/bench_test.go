package gpu

import (
	"image"
	"testing"

	"github.com/stohirov/sukho/backend/cpu"
	"github.com/stohirov/sukho/internal/sample"
	"github.com/stohirov/sukho/scene"
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
		if err := r.ras.run(r.dev, r.target, r.segBuf, r.nodeBuf, r.tileOff, r.tileNode, r.stopBuf, r.tileSegOf, r.tileSegIx, r.nx, r.ny); err != nil {
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
