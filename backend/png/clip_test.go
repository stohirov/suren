package png

import (
	"image"
	"testing"

	"github.com/stohirov/sukho/geom"
	"github.com/stohirov/sukho/paint"
	"github.com/stohirov/sukho/path"
	"github.com/stohirov/sukho/render"
)

func TestClipRectRestrictsFill(t *testing.T) {
	c := render.NewCanvas()
	c.ClipRect(geom.RectXYWH(5, 5, 5, 5))
	c.FillColor(path.Rect(geom.RectXYWH(0, 0, 20, 20)), paint.RGB(1, 0, 0))

	img := Render(c.Scene(), 20, 20)
	want := image.Rect(5, 5, 10, 10)

	opaque := 0
	for y := 0; y < 20; y++ {
		for x := 0; x < 20; x++ {
			a := img.Pix[img.PixOffset(x, y)+3]
			inside := (image.Point{X: x, Y: y}).In(want)
			if a > 0 && !inside {
				t.Fatalf("pixel (%d,%d) painted outside clip", x, y)
			}
			if a == 255 {
				opaque++
			}
		}
	}
	if opaque != 25 {
		t.Fatalf("clipped fill covered %d opaque pixels, want 25", opaque)
	}
}

func TestClipRectSaveRestore(t *testing.T) {
	c := render.NewCanvas()
	c.Save()
	c.ClipRect(geom.RectXYWH(5, 5, 5, 5))
	c.Restore()
	c.FillColor(path.Rect(geom.RectXYWH(0, 0, 20, 20)), paint.RGB(0, 0, 1))

	img := Render(c.Scene(), 20, 20)
	opaque := 0
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] == 255 {
			opaque++
		}
	}
	if opaque != 400 {
		t.Fatalf("clip should not survive Restore: %d opaque pixels, want 400", opaque)
	}
}
