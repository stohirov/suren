package goldentest

import (
	"bytes"
	"flag"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

var update = flag.Bool("update", false, "regenerate golden files")

func goldenPath(name string) string { return filepath.Join("testdata", "golden", name) }

func AssertPNG(t *testing.T, name string, img image.Image) {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	path := goldenPath(name)

	if *update {
		writeGolden(t, path, buf.Bytes())
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with -update to create)", path, err)
	}
	if bytes.Equal(buf.Bytes(), want) {
		return
	}

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "got_"+name), buf.Bytes())
	if wantImg, err := png.Decode(bytes.NewReader(want)); err == nil {
		if d, ok := diffImage(wantImg, img); ok {
			var db bytes.Buffer
			if png.Encode(&db, d) == nil {
				writeFile(t, filepath.Join(dir, "diff_"+name), db.Bytes())
			}
		}
	}
	t.Errorf("golden mismatch for %s; wrote actual and diff to %s (run with -update to accept)", name, dir)
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

func writeGolden(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir golden dir: %v", err)
	}
	writeFile(t, path, data)
	t.Logf("wrote golden %s", path)
}

func writeFile(t *testing.T, path string, data []byte) {
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
