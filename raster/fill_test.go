package raster

import (
	"image"
	"image/color"
	"math"
	"testing"

	"github.com/stohirov/sukho/geom"
	"github.com/stohirov/sukho/path"
)

func TestFillPathCoverageArea(t *testing.T) {
	circle := path.Circle(geom.Pt(6, 6), 3)
	r := NewRasterizer(12, 12)
	r.FillPath(circle, path.DefaultTolerance, geom.Identity())
	sum := 0.0
	r.Sweep(NonZero, func(x, y int, a float64) { sum += a })
	if want := flatArea(circle, path.DefaultTolerance); math.Abs(sum-want) > 1e-6 {
		t.Fatalf("coverage = %v, want polygon area %v", sum, want)
	}

	fine := NewRasterizer(12, 12)
	fine.FillPath(circle, 0.001, geom.Identity())
	sum = 0
	fine.Sweep(NonZero, func(x, y int, a float64) { sum += a })
	if math.Abs(sum-9*math.Pi) > 0.02 {
		t.Fatalf("fine coverage = %v, want ~%v", sum, 9*math.Pi)
	}
}

func flatArea(p path.Path, tol float64) float64 {
	a := 0.0
	p.Flatten(tol, geom.Identity(), func(pts []geom.Point, closed bool) {
		for i := range pts {
			j := (i + 1) % len(pts)
			a += pts[i].X*pts[j].Y - pts[j].X*pts[i].Y
		}
	})
	return math.Abs(a) / 2
}

func TestFillOpaque(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 6, 6))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	Fill(img, path.Rect(geom.RectXYWH(1, 1, 4, 4)), geom.Identity(), color.RGBA{0, 0, 255, 255}, NonZero)

	if got := img.RGBAAt(3, 3); got != (color.RGBA{0, 0, 255, 255}) {
		t.Fatalf("interior = %v, want opaque blue", got)
	}
	if got := img.RGBAAt(0, 0); got != (color.RGBA{255, 255, 255, 255}) {
		t.Fatalf("corner = %v, want untouched white", got)
	}
}

func TestFillSourceOver(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	for i := range img.Pix {
		img.Pix[i] = 255
	}

	Fill(img, path.Rect(geom.RectXYWH(0, 0, 2, 2)), geom.Identity(), color.RGBA{128, 0, 0, 128}, NonZero)
	got := img.RGBAAt(0, 0)
	want := color.RGBA{255, 127, 127, 255}
	if diff(got.R, want.R) > 2 || diff(got.G, want.G) > 2 || diff(got.B, want.B) > 2 || diff(got.A, want.A) > 2 {
		t.Fatalf("blended = %v, want ~%v", got, want)
	}
}

func TestFillNonZeroOrigin(t *testing.T) {
	img := image.NewRGBA(image.Rect(10, 10, 14, 14))
	Fill(img, path.Rect(geom.RectXYWH(10, 10, 4, 4)), geom.Identity(), color.RGBA{0, 255, 0, 255}, NonZero)
	if got := img.RGBAAt(12, 12); got != (color.RGBA{0, 255, 0, 255}) {
		t.Fatalf("origin-shifted interior = %v, want green", got)
	}
}

func diff(a, b uint8) int {
	d := int(a) - int(b)
	if d < 0 {
		return -d
	}
	return d
}
