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
	parityTol(t, want, sc, 2)
}

func parityTol(t *testing.T, want *image.RGBA, sc *scene.Scene, tol int) {
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
		if d > tol {
			over++
		}
	}
	t.Logf("max channel delta=%d, channels off-by->%d: %d/%d", maxd, tol, over, len(want.Pix))
	if maxd > tol {
		t.Fatalf("gpu/cpu mismatch: max channel delta=%d (want <=%d)", maxd, tol)
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

func TestParityBlendModes(t *testing.T) {
	modes := []struct {
		name string
		op   paint.BlendMode
		tol  int
	}{
		{"SrcOver", paint.SrcOver, 2},
		{"Multiply", paint.Multiply, 2},
		{"Screen", paint.Screen, 2},
		{"Overlay", paint.Overlay, 2},
		{"Darken", paint.Darken, 2},
		{"Lighten", paint.Lighten, 2},
		{"ColorDodge", paint.ColorDodge, 3},
		{"ColorBurn", paint.ColorBurn, 3},
		{"HardLight", paint.HardLight, 2},
		{"SoftLight", paint.SoftLight, 2},
		{"Difference", paint.Difference, 2},
		{"Exclusion", paint.Exclusion, 2},
	}
	for _, m := range modes {
		t.Run(m.name, func(t *testing.T) {
			parityTol(t, cpu.Render(sample.BlendScene(m.op), sample.W, sample.H), sample.BlendScene(m.op), m.tol)
		})
	}
}

func TestParityClipPath(t *testing.T) {
	for _, tc := range []struct {
		name   string
		nested bool
	}{{"single", false}, {"nested", true}} {
		t.Run(tc.name, func(t *testing.T) {
			parity(t, cpu.Render(sample.ClipPathScene(tc.nested), sample.W, sample.H), sample.ClipPathScene(tc.nested))
		})
	}
}

func TestParityManySegments(t *testing.T) {
	const w, h = 400, 300
	parity(t, cpu.Render(sample.ManySegments(w, h, 300), w, h), sample.ManySegments(w, h, 300))
}

func TestResizeParity(t *testing.T) {
	const w0, h0 = 96, 96
	const w1, h1 = 200, 140
	r, err := NewRenderer(w0, h0)
	if err != nil {
		t.Skipf("no gpu device: %v", err)
	}
	defer r.Release()

	if err := r.Render(clipScene()); err != nil {
		t.Fatalf("render at initial size: %v", err)
	}

	if err := r.Resize(w1, h1); err != nil {
		t.Fatalf("resize: %v", err)
	}
	if gw, gh := r.Size(); gw != w1 || gh != h1 {
		t.Fatalf("size after resize = %dx%d, want %dx%d", gw, gh, w1, h1)
	}
	if err := r.Render(sample.ManyNodes(w1, h1, 12, 8)); err != nil {
		t.Fatalf("render after resize: %v", err)
	}
	got, err := r.ReadRGBA()
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if got.Rect.Dx() != w1 || got.Rect.Dy() != h1 {
		t.Fatalf("readback size = %v, want %dx%d", got.Rect, w1, h1)
	}

	want := cpu.Render(sample.ManyNodes(w1, h1, 12, 8), w1, h1)
	maxd := 0
	for i := range want.Pix {
		d := int(got.Pix[i]) - int(want.Pix[i])
		if d < 0 {
			d = -d
		}
		if d > maxd {
			maxd = d
		}
	}
	t.Logf("post-resize max channel delta=%d", maxd)
	if maxd > 2 {
		t.Fatalf("gpu/cpu mismatch after resize: max channel delta=%d", maxd)
	}
}

func TestUnchangedSceneSkips(t *testing.T) {
	const w, h = 96, 96
	r, err := NewRenderer(w, h)
	if err != nil {
		t.Skipf("no gpu device: %v", err)
	}
	defer r.Release()

	read := func(sc *scene.Scene) *image.RGBA {
		if err := r.Render(sc); err != nil {
			t.Fatalf("render: %v", err)
		}
		got, err := r.ReadRGBA()
		if err != nil {
			t.Fatalf("readback: %v", err)
		}
		return got
	}
	equal := func(a, b *image.RGBA) bool {
		for i := range a.Pix {
			if a.Pix[i] != b.Pix[i] {
				return false
			}
		}
		return true
	}

	a1 := read(clipScene())
	if r.dispatches != 1 {
		t.Fatalf("first render dispatches = %d, want 1", r.dispatches)
	}
	a2 := read(clipScene())
	if r.dispatches != 1 {
		t.Fatalf("unchanged re-render dispatched (%d), want skip", r.dispatches)
	}
	if !equal(a1, a2) {
		t.Fatal("skipped frame did not re-present identical pixels")
	}

	b := read(sample.ManyNodes(w, h, 8, 6))
	if r.dispatches != 2 {
		t.Fatalf("changed scene skipped: dispatches = %d, want 2", r.dispatches)
	}
	if equal(a1, b) {
		t.Fatal("different scenes produced identical pixels")
	}

	a3 := read(clipScene())
	if r.dispatches != 3 {
		t.Fatalf("returning to prior scene skipped: dispatches = %d, want 3", r.dispatches)
	}
	if !equal(a1, a3) {
		t.Fatal("re-render of original scene diverged")
	}
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
