package gpu

import (
	"testing"

	"github.com/cogentcore/webgpu/wgpu"
	"github.com/stohirov/sukho/internal/sample"
)

func TestDeviceInit(t *testing.T) {
	d, err := NewDevice()
	if err != nil {
		t.Skipf("no gpu device: %v", err)
	}
	defer d.Release()
	info := d.Info()
	t.Logf("adapter backend=%v type=%v", info.BackendType, info.AdapterType)
}

func TestRenderClearReadback(t *testing.T) {
	r, err := NewRenderer(sample.W, sample.H)
	if err != nil {
		t.Skipf("no gpu device: %v", err)
	}
	defer r.Release()

	r.Background = wgpu.Color{R: 0.2, G: 0.4, B: 0.6, A: 1}
	if err := r.Render(sample.Scene()); err != nil {
		t.Fatalf("render: %v", err)
	}
	img, err := r.ReadRGBA()
	if err != nil {
		t.Fatalf("readback: %v", err)
	}

	want := [4]uint8{51, 102, 153, 255}
	for _, p := range [][2]int{{0, 0}, {sample.W / 2, sample.H / 2}, {sample.W - 1, sample.H - 1}} {
		i := img.PixOffset(p[0], p[1])
		got := [4]uint8{img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3]}
		for c := 0; c < 4; c++ {
			if d := int(got[c]) - int(want[c]); d < -1 || d > 1 {
				t.Fatalf("pixel %v = %v, want ~%v", p, got, want)
			}
		}
	}
}
