package svg

import (
	"strings"
	"testing"

	"github.com/stohirov/sukho/geom"
	"github.com/stohirov/sukho/paint"
	"github.com/stohirov/sukho/path"
	"github.com/stohirov/sukho/render"
)

func encode(t *testing.T, c *render.Canvas, w, h int) string {
	t.Helper()
	var b strings.Builder
	if err := Encode(&b, c.Scene(), w, h); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestFillPath(t *testing.T) {
	c := render.NewCanvas()
	c.Fill(path.Rect(geom.RectXYWH(1, 2, 3, 4)), paint.Solid{Color: paint.RGB(1, 0, 0)}, paint.EvenOdd)
	got := encode(t, c, 10, 10)

	want := `<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10" viewBox="0 0 10 10">
<path d="M1 2 L4 2 L4 6 L1 6 Z" fill="#ff0000" fill-rule="evenodd"/>
</svg>
`
	if got != want {
		t.Errorf("SVG mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestFillOpacityAndTransform(t *testing.T) {
	c := render.NewCanvas()
	c.Translate(5, 6)
	c.FillColor(path.Rect(geom.RectXYWH(0, 0, 2, 2)), paint.RGBA(0, 0.5, 1, 0.5))
	got := encode(t, c, 10, 10)

	if !strings.Contains(got, `transform="matrix(1 0 0 1 5 6)"`) {
		t.Errorf("missing/incorrect transform in:\n%s", got)
	}
	if !strings.Contains(got, `fill-opacity="0.5"`) {
		t.Errorf("missing fill-opacity in:\n%s", got)
	}
	if strings.Contains(got, "fill-rule") {
		t.Errorf("non-zero fill should not emit fill-rule:\n%s", got)
	}
}

func TestStrokeAttributes(t *testing.T) {
	c := render.NewCanvas()
	var line path.Path
	line.MoveTo(geom.Pt(0, 0))
	line.LineTo(geom.Pt(10, 0))
	c.StrokeColor(line, paint.RGB(0, 0, 0), paint.Stroke{
		Width:  3,
		Cap:    path.RoundCap,
		Join:   path.BevelJoin,
		Dashes: []float64{4, 2},
	})
	got := encode(t, c, 10, 10)

	for _, sub := range []string{
		`fill="none"`,
		`stroke="#000000"`,
		`stroke-width="3"`,
		`stroke-linecap="round"`,
		`stroke-linejoin="bevel"`,
		`stroke-dasharray="4,2"`,
	} {
		if !strings.Contains(got, sub) {
			t.Errorf("stroke SVG missing %q in:\n%s", sub, got)
		}
	}
}

func TestQuadAndCubic(t *testing.T) {
	c := render.NewCanvas()
	var p path.Path
	p.MoveTo(geom.Pt(0, 0))
	p.QuadTo(geom.Pt(1, 2), geom.Pt(3, 0))
	p.CubicTo(geom.Pt(4, 1), geom.Pt(5, 1), geom.Pt(6, 0))
	c.FillColor(p, paint.RGB(0, 0, 0))
	got := encode(t, c, 10, 10)

	if !strings.Contains(got, "Q1 2 3 0") {
		t.Errorf("missing quad segment in:\n%s", got)
	}
	if !strings.Contains(got, "C4 1 5 1 6 0") {
		t.Errorf("missing cubic segment in:\n%s", got)
	}
}
