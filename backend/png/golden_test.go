package png

import (
	"testing"

	"github.com/stohirov/sukho/internal/goldentest"
	"github.com/stohirov/sukho/internal/sample"
)

func TestGoldenSample(t *testing.T) {
	img := Render(sample.Scene(), sample.W, sample.H)
	goldentest.AssertPNG(t, "sample.png", img)
}

func TestGoldenGradient(t *testing.T) {
	img := Render(sample.GradientScene(), sample.W, sample.H)
	goldentest.AssertPNG(t, "gradient.png", img)
}
