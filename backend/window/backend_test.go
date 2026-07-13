package window

import (
	"image"
	"testing"

	"github.com/stohirov/sukho/backend/cpu"
	"github.com/stohirov/sukho/backend/gpu"
	"github.com/stohirov/sukho/internal/sample"
)

func maxDelta(a, b *image.RGBA) int {
	maxd := 0
	for i := range a.Pix {
		d := int(a.Pix[i]) - int(b.Pix[i])
		if d < 0 {
			d = -d
		}
		if d > maxd {
			maxd = d
		}
	}
	return maxd
}

func TestBackendParityAndResize(t *testing.T) {
	gr, err := gpu.NewRenderer(sample.W, sample.H)
	if err != nil {
		t.Skipf("no gpu device: %v", err)
	}
	gb := &gpuBackend{r: gr}
	defer gb.release()

	img := image.NewRGBA(image.Rect(0, 0, sample.W, sample.H))
	cb := &cpuBackend{img: img, r: &cpu.Renderer{Img: img}}

	cimg, err := cb.frame(sample.GradientScene())
	if err != nil {
		t.Fatalf("cpu frame: %v", err)
	}
	gimg, err := gb.frame(sample.GradientScene())
	if err != nil {
		t.Fatalf("gpu frame: %v", err)
	}
	if d := maxDelta(cimg, gimg); d > 2 {
		t.Fatalf("cpu/gpu backend mismatch: max channel delta=%d", d)
	}

	const w2, h2 = 320, 200
	if err := cb.resize(w2, h2); err != nil {
		t.Fatalf("cpu resize: %v", err)
	}
	if err := gb.resize(w2, h2); err != nil {
		t.Fatalf("gpu resize: %v", err)
	}

	sc := sample.ManyNodes(w2, h2, 16, 10)
	cimg2, err := cb.frame(sc)
	if err != nil {
		t.Fatalf("cpu frame after resize: %v", err)
	}
	gimg2, err := gb.frame(sample.ManyNodes(w2, h2, 16, 10))
	if err != nil {
		t.Fatalf("gpu frame after resize: %v", err)
	}
	if cimg2.Rect.Dx() != w2 || cimg2.Rect.Dy() != h2 {
		t.Fatalf("cpu frame size after resize = %v", cimg2.Rect)
	}
	if gimg2.Rect.Dx() != w2 || gimg2.Rect.Dy() != h2 {
		t.Fatalf("gpu frame size after resize = %v", gimg2.Rect)
	}
	if d := maxDelta(cimg2, gimg2); d > 2 {
		t.Fatalf("cpu/gpu backend mismatch after resize: max channel delta=%d", d)
	}
}
