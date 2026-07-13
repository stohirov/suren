package gpu

import (
	"image"
	"testing"

	"github.com/stohirov/sukho/backend/cpu"
	"github.com/stohirov/sukho/geom"
	"github.com/stohirov/sukho/internal/sample"
	"github.com/stohirov/sukho/paint"
	"github.com/stohirov/sukho/path"
	"github.com/stohirov/sukho/render"
	"github.com/stohirov/sukho/scene"
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

func parity(t *testing.T, want *image.RGBA, sc *scene.Scene) {
	t.Helper()
	r, err := NewRenderer(want.Rect.Dx(), want.Rect.Dy())
	if err != nil {
		t.Skipf("no gpu device: %v", err)
	}
	defer r.Release()

	if err := r.Render(sc); err != nil {
		t.Fatalf("render: %v", err)
	}
	got, err := r.ReadRGBA()
	if err != nil {
		t.Fatalf("readback: %v", err)
	}

	maxd, over := 0, 0
	for i := range want.Pix {
		d := int(got.Pix[i]) - int(want.Pix[i])
		if d < 0 {
			d = -d
		}
		if d > maxd {
			maxd = d
		}
		if d > 2 {
			over++
		}
	}
	t.Logf("max channel delta=%d, channels off-by->2: %d/%d", maxd, over, len(want.Pix))
	if maxd > 2 {
		t.Fatalf("gpu/cpu mismatch: max channel delta=%d (want <=2)", maxd)
	}
}

func TestParitySolid(t *testing.T) {
	parity(t, cpu.Render(sample.Scene(), sample.W, sample.H), sample.Scene())
}

func TestParityManyNodes(t *testing.T) {
	const w, h = 640, 360
	parity(t, cpu.Render(sample.ManyNodes(w, h, 40, 24), w, h), sample.ManyNodes(w, h, 40, 24))
}

func TestParityGradient(t *testing.T) {
	parity(t, cpu.Render(sample.GradientScene(), sample.W, sample.H), sample.GradientScene())
}

func clipScene() *scene.Scene {
	c := render.NewCanvas()
	c.FillColor(path.Rect(geom.RectXYWH(0, 0, 96, 96)), paint.FromRGBA8(30, 30, 40, 255))
	c.ClipRect(geom.RectXYWH(13, 13, 61, 47))
	c.FillColor(path.Circle(geom.Pt(48, 48), 40), paint.FromRGBA8(220, 80, 60, 255))
	return c.Scene()
}

func TestParityClip(t *testing.T) {
	parity(t, cpu.Render(clipScene(), 96, 96), clipScene())
}
