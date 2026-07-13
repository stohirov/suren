package svg

import (
	"strings"
	"testing"

	"github.com/stohirov/sukho/internal/goldentest"
	"github.com/stohirov/sukho/internal/sample"
)

func TestGoldenSample(t *testing.T) {
	var b strings.Builder
	if err := Encode(&b, sample.Scene(), sample.W, sample.H); err != nil {
		t.Fatal(err)
	}
	goldentest.AssertText(t, "sample.svg", b.String())
}
