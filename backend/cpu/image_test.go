package cpu

import (
	"bytes"
	"testing"

	"github.com/stohirov/suren/internal/corpus"
	"github.com/stohirov/suren/internal/parity"
	"github.com/stohirov/suren/internal/sample"
	"github.com/stohirov/suren/paint"
)

// TestImageEntriesRenderDistinctly is the anti-vacuity gate for the six image
// corpus entries, and it is a stronger claim than the structural one
// TestBlendEntriesBuildDistinctScenes makes: that test reads the scene back and
// checks the field differs, which proves the corpus was BUILT right. This renders
// them and requires the PIXELS to differ, which additionally proves the renderer
// reads both fields.
//
// Both halves are needed. If ImageAt ignored Edge entirely, the three edge entries
// would build distinct scenes, render identical frames, and pass their parity gate
// at Δ=0 on both backends — six entries measuring two. The same is true of the
// packed flags: filter and edge share a word with the two compositing axes, so a
// wrong shift reads one field as another and this is what notices.
func TestImageEntriesRenderDistinctly(t *testing.T) {
	seen := map[string]string{}
	n := 0
	for _, e := range corpus.All() {
		if len(e.Name) < 6 || e.Name[:6] != "image-" {
			continue
		}
		n++
		img := Render(e.Build(), e.W, e.H)
		if !parity.NonTrivial(img) {
			t.Errorf("%s renders nothing worth comparing; every gate on it would pass vacuously", e.Name)
		}
		key := string(img.Pix)
		if prev, ok := seen[key]; ok {
			t.Errorf("%s and %s render IDENTICAL frames — one of Filter/Edge is not being read", prev, e.Name)
		}
		seen[key] = e.Name
	}
	if n != 6 {
		t.Fatalf("found %d image corpus entries, want 6 (2 filters x 3 edge modes)", n)
	}
}

// TestImageSceneLeavesTheImage is the guard behind the guard. The three edge modes
// agree exactly INSIDE the image — they differ only about out-of-range indices — so
// a scene whose geometry never leaves [0,W]x[0,H] would make the test above fail
// for a reason nobody could act on, or worse, would quietly stop distinguishing
// them if the scene were ever tidied up. This pins the property that makes the edge
// modes observable at all.
func TestImageSceneLeavesTheImage(t *testing.T) {
	sc := sample.ImageScene(paint.Nearest, paint.Repeat)
	out := false
	for _, nd := range sc.Nodes {
		im, ok := nd.Paint.(paint.Image)
		if !ok {
			continue
		}
		b := nd.Path.Bounds()
		if b.Min.X < 0 || b.Min.Y < 0 || b.Max.X > float64(im.W) || b.Max.Y > float64(im.H) {
			out = true
		}
	}
	if !out {
		t.Fatal("no image node reaches outside its own texels, so Clamp/Repeat/Mirror are the same function here")
	}
}

// TestImageSceneIsNotAxisAlignedOnly pins the other half of ImageScene's design.
// An exactly-representable transform cannot make Nearest diverge even where every
// pixel lands on a texel boundary (see TestNearestIsExactWhenF32CanResolveIt in
// backend/gpu), so a scene built only from clean integer scales would gate the
// hard filter vacuously.
func TestImageSceneIsNotAxisAlignedOnly(t *testing.T) {
	sc := sample.ImageScene(paint.Bilinear, paint.Clamp)
	for _, nd := range sc.Nodes {
		if _, ok := nd.Paint.(paint.Image); !ok {
			continue
		}
		// B and C are the off-diagonal terms: non-zero means rotation or shear, so
		// the sample point is not a tidy multiple of anything.
		if nd.Transform.B != 0 || nd.Transform.C != 0 {
			return
		}
	}
	t.Fatal("every image node is axis-aligned; the sampler is never asked an irrational question")
}

// TestCheckerIsPremultipliedAndAsymmetric pins the two properties the source image
// is chosen for. A texel with a channel above its own alpha is not a representable
// premultiplied colour, and it would make the bilinear average mean something else;
// a symmetric image would let a transposed index pass.
func TestCheckerIsPremultipliedAndAsymmetric(t *testing.T) {
	im := sample.Checker(8, 8)
	for k := 0; k < len(im.Pix); k += 4 {
		a := im.Pix[k+3]
		for c := range 3 {
			if im.Pix[k+c] > a {
				t.Fatalf("texel at %d has channel %d = %d above alpha %d — not premultiplied",
					k/4, c, im.Pix[k+c], a)
			}
		}
	}
	// Row 0 against column 0: if these matched, the image would be symmetric enough
	// to sample sideways undetected.
	row0 := im.Pix[:im.W*4]
	col0 := make([]byte, 0, im.H*4)
	for j := range im.H {
		col0 = append(col0, im.Pix[j*im.W*4:j*im.W*4+4]...)
	}
	if bytes.Equal(row0, col0) {
		t.Fatal("Checker's first row and first column are identical; a transposed index would be invisible")
	}
}
