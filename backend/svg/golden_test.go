package svg

import (
	"strings"
	"testing"

	"github.com/stohirov/suren/internal/goldentest"
	"github.com/stohirov/suren/internal/sample"
)

func TestGoldenSample(t *testing.T) {
	var b strings.Builder
	if _, err := Encode(&b, sample.Scene(), sample.W, sample.H); err != nil {
		t.Fatal(err)
	}
	goldentest.AssertText(t, "sample.svg", b.String())
}

func TestGoldenGradient(t *testing.T) {
	var b strings.Builder
	if _, err := Encode(&b, sample.GradientScene(), sample.W, sample.H); err != nil {
		t.Fatal(err)
	}
	goldentest.AssertText(t, "gradient.svg", b.String())
}
