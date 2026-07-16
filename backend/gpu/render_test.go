package gpu

import (
	"image"
	"testing"

	"github.com/stohirov/suren/backend/cpu"
	"github.com/stohirov/suren/internal/corpus"
	"github.com/stohirov/suren/internal/parity"
	"github.com/stohirov/suren/internal/sample"
	"github.com/stohirov/suren/scene"
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

func gpuParity(t *testing.T, want *image.RGBA, sc *scene.Scene, cfg parity.Config) {
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
	parity.Assert(t, got, want, cfg)
}

func TestParityCorpus(t *testing.T) {
	for _, e := range corpus.All() {
		t.Run(e.Name, func(t *testing.T) {
			gpuParity(t, cpu.Render(e.Build(), e.W, e.H), e.Build(), e.Tol)
		})
	}
}

func TestResizeParity(t *testing.T) {
	const w0, h0 = 96, 96
	const w1, h1 = 200, 140
	r, err := NewRenderer(w0, h0)
	if err != nil {
		t.Skipf("no gpu device: %v", err)
	}
	defer r.Release()

	if err := r.Render(sample.ClipRectScene()); err != nil {
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

	parity.Assert(t, got, cpu.Render(sample.ManyNodes(w1, h1, 12, 8), w1, h1), parity.Identical())
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

	a1 := read(sample.ClipRectScene())
	if r.dispatches != 1 {
		t.Fatalf("first render dispatches = %d, want 1", r.dispatches)
	}
	a2 := read(sample.ClipRectScene())
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

	a3 := read(sample.ClipRectScene())
	if r.dispatches != 3 {
		t.Fatalf("returning to prior scene skipped: dispatches = %d, want 3", r.dispatches)
	}
	if !equal(a1, a3) {
		t.Fatal("re-render of original scene diverged")
	}
}
