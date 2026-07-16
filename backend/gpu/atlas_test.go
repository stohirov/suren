package gpu

import (
	"testing"

	"github.com/stohirov/suren/geom"
	"github.com/stohirov/suren/internal/sample"
	"github.com/stohirov/suren/paint"
	"github.com/stohirov/suren/path"
	"github.com/stohirov/suren/render"
	"github.com/stohirov/suren/scene"
)

// Encoder unit tests: pure Go, no GPU. These pin the atlas's structure, which the
// parity gate can only check indirectly — a wrong origin usually shows up as wrong
// pixels, but a wrong origin that happens to land on identical texels does not.

func imgScene(imgs ...paint.Image) *scene.Scene {
	c := render.NewCanvas()
	for i, im := range imgs {
		c.Fill(path.Rect(geom.RectXYWH(float64(i*10), 0, 8, 8)), im, paint.NonZero)
	}
	return c.Scene()
}

func solidImage(w, h int, v uint8) paint.Image {
	pix := make([]uint8, w*h*4)
	for i := range pix {
		pix[i] = v
	}
	for k := 3; k < len(pix); k += 4 {
		pix[k] = 255
	}
	return paint.Image{W: w, H: h, Pix: pix}
}

// TestAtlasStacksImagesVertically pins the layout Node.G0 is an offset into. The
// widths differ on purpose: AtlasW is the widest, and the narrow image's row is
// padded — padding the shader must never read, since wrapIdx bounds the index by
// the image's own g1x before adding the origin.
func TestAtlasStacksImagesVertically(t *testing.T) {
	a, b := solidImage(4, 3, 10), solidImage(6, 5, 20)
	e := Encode(imgScene(a, b), 64, 32)

	if e.AtlasW != 6 || e.AtlasH != 8 {
		t.Fatalf("atlas = %dx%d, want 6x8 (widest, sum of heights)", e.AtlasW, e.AtlasH)
	}
	if len(e.Nodes) != 2 {
		t.Fatalf("got %d nodes, want 2", len(e.Nodes))
	}
	if got := e.Nodes[0].G0; got != [2]float32{0, 0} {
		t.Errorf("first image origin = %v, want (0,0)", got)
	}
	if got := e.Nodes[1].G0; got != [2]float32{0, 3} {
		t.Errorf("second image origin = %v, want (0,3) — it stacks below the first", got)
	}
	if got := e.Nodes[0].G1; got != [2]float32{4, 3} {
		t.Errorf("first image size = %v, want (4,3)", got)
	}
	// The second image's first row must be at atlas row 3, not row 0.
	if got := e.Atlas[(3*e.AtlasW)*4]; got != 20 {
		t.Errorf("atlas row 3 starts with %d, want 20 — the second image is misplaced", got)
	}
	// And the first image's padding is zeroed rather than holding the second's.
	if got := e.Atlas[(0*e.AtlasW+5)*4]; got != 0 {
		t.Errorf("padding beside the narrow image = %d, want 0", got)
	}
}

// TestAtlasSharesOneSlotForOneImage pins the dedup. Drawing one image in several
// places is the ordinary case, and without this the atlas would grow linearly in
// the number of nodes rather than in the number of distinct images.
func TestAtlasSharesOneSlotForOneImage(t *testing.T) {
	im := solidImage(4, 4, 10)
	nearest, bilinear := im, im
	bilinear.Filter = paint.Bilinear

	e := Encode(imgScene(im, im, im), 64, 32)
	if e.AtlasH != 4 {
		t.Errorf("three nodes sharing one image built an atlas %d rows tall, want 4", e.AtlasH)
	}
	for i, nd := range e.Nodes {
		if nd.G0 != [2]float32{0, 0} {
			t.Errorf("node %d origin = %v, want (0,0) — all three share a slot", i, nd.G0)
		}
	}

	// Filter and edge live in the flags word, not in the atlas, so the same texels
	// sampled two ways still share one slot. If the key ever grows to include them
	// this silently doubles every sprite sheet.
	e = Encode(imgScene(nearest, bilinear), 64, 32)
	if e.AtlasH != 4 {
		t.Errorf("one image at two filters built an atlas %d rows tall, want 4 — "+
			"the filter is not part of the texels", e.AtlasH)
	}
}

// TestFingerprintCoversTheTexels is the trap this phase had to close, and it is
// Phase 14's FallbackTiles lesson one field up.
//
// A node records WHERE its image sits and HOW to sample it. Nothing in
// Segments/Nodes/Stops records what is IN it. So a caller that redraws into its
// image's Pix in place — a video frame, a procedural texture, any animation that
// reuses its buffer — leaves the whole encoding byte-identical, and Phase 7c's skip
// would hand back last frame's pixels forever.
func TestFingerprintCoversTheTexels(t *testing.T) {
	im := solidImage(4, 4, 10)
	e := &Encoded{}
	EncodeInto(e, imgScene(im), 64, 32)
	before := e.Fingerprint

	EncodeInto(e, imgScene(im), 64, 32)
	if e.Fingerprint != before {
		t.Fatal("re-encoding an unchanged scene changed the fingerprint; the frame skip would never fire")
	}

	// Mutate one texel IN PLACE. The scene, the nodes and the segments are all
	// untouched — only the bytes behind the paint moved.
	im.Pix[0] = 200
	EncodeInto(e, imgScene(im), 64, 32)
	if e.Fingerprint == before {
		t.Fatal("mutating a texel in place left the fingerprint unchanged; " +
			"Render would skip the upload and present a stale frame")
	}
}

// TestAtlasFingerprintIgnoresGeometry pins the other half: moving an image node
// must NOT invalidate the texels, or every animated sprite would re-upload its
// atlas every frame.
func TestAtlasFingerprintIgnoresGeometry(t *testing.T) {
	im := solidImage(4, 4, 10)
	e := &Encoded{}

	EncodeInto(e, imgScene(im), 64, 32)
	texels := e.AtlasFP
	whole := e.Fingerprint

	EncodeInto(e, imgScene(im, im), 64, 32) // same image, a second node
	if e.AtlasFP != texels {
		t.Error("adding a node that reuses the image changed the texel hash; the atlas would re-upload for nothing")
	}
	if e.Fingerprint == whole {
		t.Error("adding a node did not change the frame fingerprint")
	}
}

// TestInvalidImageNodeIsDropped pins the agreement the encoder relies on. The GPU
// cannot upload texels that are not there, and dropping is sound only because
// backend/cpu's shader() drops the same node — see paint.ImageAt, which returns
// transparent for one.
func TestInvalidImageNodeIsDropped(t *testing.T) {
	bad := paint.Image{W: 4, H: 4, Pix: make([]uint8, 4)} // claims 16 texels, holds 1
	e := Encode(imgScene(bad), 64, 32)
	if len(e.Nodes) != 0 {
		t.Errorf("got %d nodes for an image whose Pix cannot hold its texels, want 0", len(e.Nodes))
	}
	if e.AtlasW != 0 || e.AtlasH != 0 {
		t.Errorf("dropped node still reserved atlas space %dx%d", e.AtlasW, e.AtlasH)
	}
}

// TestSampleFlagsDoNotCollideWithTheCompositingAxes is the packed-word gate at the
// encoder level. The corpus crosses filter with edge; this crosses BOTH with blend
// and composite, which no scene in the tree does.
func TestSampleFlagsDoNotCollideWithTheCompositingAxes(t *testing.T) {
	im := sample.Checker(4, 4)
	im.Filter, im.Edge = paint.Bilinear, paint.Mirror

	c := render.NewCanvas()
	c.SetBlend(paint.Overlay)
	c.SetComposite(paint.Xor)
	c.Fill(path.Rect(geom.RectXYWH(0, 0, 8, 8)), im, paint.NonZero)
	e := Encode(c.Scene(), 64, 32)

	f := e.Nodes[0].Flags
	if got := paint.BlendMode(f & 0xF); got != paint.Overlay {
		t.Errorf("blend unpacked as %v, want Overlay", got)
	}
	if got := paint.CompositeOp((f >> 4) & 0xF); got != paint.Xor {
		t.Errorf("composite unpacked as %v, want Xor", got)
	}
	if got := paint.Filter((f >> 8) & 0xF); got != paint.Bilinear {
		t.Errorf("filter unpacked as %v, want Bilinear", got)
	}
	if got := paint.EdgeMode((f >> 12) & 0xF); got != paint.Mirror {
		t.Errorf("edge unpacked as %v, want Mirror", got)
	}
}
