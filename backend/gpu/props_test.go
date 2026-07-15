package gpu

import (
	"image"
	"testing"

	"github.com/stohirov/sukho/backend/cpu"
	"github.com/stohirov/sukho/internal/parity"
	"github.com/stohirov/sukho/internal/parity/props"
	"github.com/stohirov/sukho/scene"
)

func gpuRenderFunc(t *testing.T) props.RenderFunc {
	t.Helper()
	r, err := NewRenderer(props.W, props.H)
	if err != nil {
		t.Skipf("no gpu device: %v", err)
	}
	t.Cleanup(r.Release)
	return func(sc *scene.Scene, w, h int) *image.RGBA {
		if gw, gh := r.Size(); gw != w || gh != h {
			if err := r.Resize(w, h); err != nil {
				t.Fatalf("resize: %v", err)
			}
		}
		if err := r.Render(sc); err != nil {
			t.Fatalf("render: %v", err)
		}
		img, err := r.ReadRGBA()
		if err != nil {
			t.Fatalf("readback: %v", err)
		}
		return img
	}
}

func TestPropsGPU(t *testing.T) {
	props.CheckAll(t, gpuRenderFunc(t))
}

func TestPropsAgreement(t *testing.T) {
	props.CheckAgreement(t, gpuRenderFunc(t), cpu.Render, parity.Quantized())
}
