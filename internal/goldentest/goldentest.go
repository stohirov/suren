package goldentest

import (
	"bytes"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/stohirov/sukho/internal/parity"
)

var update = flag.Bool("update", false, "regenerate golden files")

func goldenPath(name string) string { return filepath.Join("testdata", "golden", name) }

// Goldens hold PREMULTIPLIED bytes. A plain PNG round-trip of an *image.RGBA is
// lossy — the encoder unpremultiplies and the decoder re-premultiplies, which
// measures up to Δ=1 on ~13% of channels (worst observed: [1 1 1 2] -> [0 0 0 2]).
// That is the whole Quantized budget spent on the file format before the
// renderer is even compared. Carrying the premultiplied bytes through an NRGBA
// container instead makes the round-trip exact, because the encoder copies NRGBA
// through verbatim. Fully-opaque frames encode as RGB and decode as *image.RGBA,
// which is equally exact since premultiplied == straight at alpha 255.
//
// The cost is that a translucent golden displays as composited-over-black in an
// image viewer, which is what the renderer actually produced.
func encodeGolden(img *image.RGBA) ([]byte, error) {
	b := img.Rect
	held := image.NewNRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		di := held.PixOffset(b.Min.X, y)
		si := img.PixOffset(b.Min.X, y)
		copy(held.Pix[di:di+b.Dx()*4], img.Pix[si:si+b.Dx()*4])
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, held); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decodeGolden(data []byte) (*image.RGBA, error) {
	m, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	b := m.Bounds()
	out := image.NewRGBA(b)
	var pix []byte
	var stride int
	switch src := m.(type) {
	case *image.NRGBA:
		pix, stride = src.Pix, src.Stride
	case *image.RGBA:
		pix, stride = src.Pix, src.Stride
	default:
		return nil, fmt.Errorf("golden decoded as %T; want *image.NRGBA or *image.RGBA", m)
	}
	for y := range b.Dy() {
		di := out.PixOffset(b.Min.X, b.Min.Y+y)
		si := y * stride
		copy(out.Pix[di:di+b.Dx()*4], pix[si:si+b.Dx()*4])
	}
	return out, nil
}

func assertGolden(t testing.TB, name string, got *image.RGBA, cfg parity.Config) {
	t.Helper()
	path := goldenPath(name)

	data, err := encodeGolden(got)
	if err != nil {
		t.Fatalf("encode golden: %v", err)
	}
	if *update {
		writeGolden(t, path, data)
		return
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with -update to create)", path, err)
	}
	want, err := decodeGolden(raw)
	if err != nil {
		t.Fatalf("decode golden %s: %v", path, err)
	}
	if want.Rect != got.Rect {
		t.Fatalf("golden %s has bounds %v, got %v", name, want.Rect, got.Rect)
	}

	r, err := parity.Compare(got, want, cfg)
	if err != nil {
		t.Fatalf("compare golden %s: %v", name, err)
	}
	t.Logf("golden[%s]: %s", cfg, r.Describe(cfg))
	if r.OK(cfg) {
		return
	}

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "got_"+name), data)
	if d, ok := diffImage(want, got); ok {
		var db bytes.Buffer
		if png.Encode(&db, d) == nil {
			writeFile(t, filepath.Join(dir, "diff_"+name), db.Bytes())
		}
	}
	t.Fatalf("golden mismatch for %s: gate %s exceeded: %s; wrote actual and diff to %s (run with -update to accept)",
		name, cfg, r.Describe(cfg), dir)
}

// AssertExact gates a render against its stored golden on premultiplied RGBA.
func AssertExact(t testing.TB, name string, got *image.RGBA, cfg parity.Config) {
	t.Helper()
	if cfg.Mode != parity.ModeExact {
		t.Fatalf("AssertExact given a %v config; use AssertPerceptual", cfg.Mode)
	}
	assertGolden(t, name, got, cfg)
}

// AssertPerceptual gates a render against its stored golden on ΔE + SSIM.
func AssertPerceptual(t testing.TB, name string, got *image.RGBA, cfg parity.Config) {
	t.Helper()
	if cfg.Mode != parity.ModePerceptual {
		t.Fatalf("AssertPerceptual given a %v config; use AssertExact", cfg.Mode)
	}
	assertGolden(t, name, got, cfg)
}

func AssertText(t *testing.T, name, got string) {
	t.Helper()
	path := goldenPath(name)

	if *update {
		writeGolden(t, path, []byte(got))
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with -update to create)", path, err)
	}
	if got == string(want) {
		return
	}

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "got_"+name), []byte(got))
	t.Errorf("golden mismatch for %s; wrote actual to %s (run with -update to accept)", name, dir)
}

func writeGolden(t testing.TB, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir golden dir: %v", err)
	}
	writeFile(t, path, data)
	t.Logf("wrote golden %s", path)
}

func writeFile(t testing.TB, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func diffImage(a, b image.Image) (image.Image, bool) {
	ab, bb := a.Bounds(), b.Bounds()
	if ab != bb {
		return nil, false
	}
	out := image.NewRGBA(ab)
	for y := ab.Min.Y; y < ab.Max.Y; y++ {
		for x := ab.Min.X; x < ab.Max.X; x++ {
			ar, ag, ab2, aa := a.At(x, y).RGBA()
			br, bg, bb2, ba := b.At(x, y).RGBA()
			if ar == br && ag == bg && ab2 == bb2 && aa == ba {
				g := uint8(ar >> 10)
				out.Set(x, y, color.RGBA{g, g, g, 255})
			} else {
				out.Set(x, y, color.RGBA{255, 0, 0, 255})
			}
		}
	}
	return out, true
}
