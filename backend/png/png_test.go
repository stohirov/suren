package png

import (
	"testing"

	"github.com/stohirov/sukho/geom"
	"github.com/stohirov/sukho/paint"
	"github.com/stohirov/sukho/path"
	"github.com/stohirov/sukho/render"
)

func TestFillRectExactCoverage(t *testing.T) {
	c := render.NewCanvas()
	c.FillColor(path.Rect(geom.RectXYWH(5, 5, 10, 10)), paint.RGB(1, 0, 0))

	img := Render(c.Scene(), 20, 20)
	full := 0
	for i := 0; i < len(img.Pix); i += 4 {
		if img.Pix[i+3] == 255 {
			full++
		}
	}
	if full != 100 {
		t.Fatalf("fully-opaque pixels = %d, want 100", full)
	}
}

func TestStrokeRendersInk(t *testing.T) {
	c := render.NewCanvas()
	var line path.Path
	line.MoveTo(geom.Pt(2, 10))
	line.LineTo(geom.Pt(18, 10))
	c.StrokeColor(line, paint.RGB(0, 0, 0), paint.Stroke{Width: 4})

	img := Render(c.Scene(), 20, 20)
	inked := 0
	for i := 0; i < len(img.Pix); i += 4 {
		if img.Pix[i+3] > 0 {
			inked++
		}
	}
	if inked == 0 {
		t.Fatal("stroke produced no pixels")
	}
}
