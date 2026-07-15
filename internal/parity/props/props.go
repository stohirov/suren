// Package props asserts algebraic laws the renderers must obey, on generated
// scenes rather than fixed goldens.
//
// A law is parameterised by a RenderFunc so the same suite runs against the CPU
// reference and the GPU renderer without this package importing either. That
// keeps backend/gpu's cgo dependency quarantined in its own module: the GPU test
// passes its own renderer in.
//
// Every failure prints the generating seed. The seed IS the reproduction — it
// re-materialises the exact scene for Phase 12c's replay and shrinking.
package props

import (
	"fmt"
	"image"
	"testing"

	"github.com/stohirov/sukho/geom"
	"github.com/stohirov/sukho/internal/parity"
	"github.com/stohirov/sukho/paint"
	"github.com/stohirov/sukho/path"
	"github.com/stohirov/sukho/render"
	"github.com/stohirov/sukho/scene"
)

type RenderFunc func(sc *scene.Scene, w, h int) *image.RGBA

// Cases is how many seeds each law runs.
const Cases = 16

// A Law carries its own within-backend gate, for the same reason a corpus entry
// does: the tolerance a law earns is a property of the law, not a global knob.
type Law struct {
	Name string
	Tol  parity.Config
	Run  func(t testing.TB, r RenderFunc, seed uint64, cfg parity.Config)
	// Scene builds one generated scene per seed for cross-backend agreement.
	Scene func(seed uint64) (sc *scene.Scene, w, h int)
}

func Laws() []Law {
	return []Law{
		{Name: "affine-composition", Tol: parity.Identical(), Run: AffineComposition, Scene: affineScene},
		{Name: "clip-idempotence", Tol: parity.Identical(), Run: ClipIdempotence, Scene: clipScene},
		{Name: "premultiply-round-trip", Tol: parity.Identical(), Run: PremultiplyRoundTrip, Scene: premulScene},
		{Name: "compositing-associativity", Tol: assocTol, Run: CompositingAssociativity, Scene: assocScene},
	}
}

// The split render rounds its intermediate to 8 bits where the whole render
// keeps full precision, so the two cannot agree exactly. This is the owner of
// the only budget in the suite.
var assocTol = parity.Budget(2, "split render quantizes its intermediate composite to 8 bits")

func check(t testing.TB, law string, seed uint64, got, want *image.RGBA, cfg parity.Config) {
	t.Helper()
	res, err := parity.Compare(got, want, cfg)
	if err != nil {
		t.Fatalf("%s [seed=0x%x]: %v", law, seed, err)
	}
	t.Logf("%s [seed=0x%x] %s: %s", law, seed, cfg, res.Describe(cfg))
	if !res.OK(cfg) {
		t.Fatalf("law %s violated [seed=0x%x]: gate %s exceeded: %s", law, seed, cfg, res.Describe(cfg))
	}
}

func requireNonTrivial(t testing.TB, law string, seed uint64, img *image.RGBA) {
	t.Helper()
	if !parity.NonTrivial(img) {
		t.Fatalf("law %s [seed=0x%x]: generated scene renders (almost) nothing; the law would pass vacuously", law, seed)
	}
}

// AffineComposition: applying A·B as one matrix equals baking B into the
// geometry and applying A. Both send a point through the same affine map, but by
// different float roundings, so this pins the renderer's transform composition
// rather than restating matrix associativity.
//
// Fills only: a stroke's width is scaled by its node transform, so baking B into
// the path would legitimately change the stroke width and the law would not hold.
func AffineComposition(t testing.TB, r RenderFunc, seed uint64, cfg parity.Config) {
	t.Helper()
	rng := newRNG(seed)
	p := randPath(rng)
	col := randColor(rng)
	a := randAboutCenter(rng)
	b := randAboutCenter(rng)

	left := &scene.Scene{}
	left.Add(scene.Node{Path: p, Transform: a.Mul(b), Paint: paint.Solid{Color: col}, FillRule: paint.NonZero})

	right := &scene.Scene{}
	right.Add(scene.Node{Path: p.Transform(b), Transform: a, Paint: paint.Solid{Color: col}, FillRule: paint.NonZero})

	gotLeft := r(left, W, H)
	requireNonTrivial(t, "affine-composition", seed, gotLeft)
	check(t, "affine-composition", seed, r(right, W, H), gotLeft, cfg)
}

func affineScene(seed uint64) (*scene.Scene, int, int) {
	rng := newRNG(seed)
	p := randPath(rng)
	col := randColor(rng)
	a := randAboutCenter(rng)
	b := randAboutCenter(rng)
	sc := &scene.Scene{}
	sc.Add(scene.Node{Path: p, Transform: a.Mul(b), Paint: paint.Solid{Color: col}, FillRule: paint.NonZero})
	return sc, W, H
}

// ClipIdempotence: clipping twice by the same path equals clipping once.
//
// Scoped deliberately to PIXEL-ALIGNED clips. Clip coverages compose by
// multiplication, so on an antialiased edge a coverage of 0.5 becomes 0.25 when
// the same path is applied twice — measured at Δ=52, which is a real property of
// multiplicative coverage composition, not a rounding artifact. The law therefore
// holds exactly where clip coverage is binary, and the antialiased case is a
// recorded deviation rather than a widened tolerance.
func ClipIdempotence(t testing.TB, r RenderFunc, seed uint64, cfg parity.Config) {
	t.Helper()
	once := buildClipScene(seed, 1)
	twice := buildClipScene(seed, 2)

	gotOnce := r(once, W, H)
	requireNonTrivial(t, "clip-idempotence", seed, gotOnce)
	check(t, "clip-idempotence", seed, r(twice, W, H), gotOnce, cfg)
}

func buildClipScene(seed uint64, times int) *scene.Scene {
	rng := newRNG(seed)
	bg := randColor(rng)
	fg := randColor(rng)
	clip := randAlignedRect(rng)

	c := render.NewCanvas()
	c.FillColor(path.Rect(geom.RectXYWH(0, 0, W, H)), bg)
	for range times {
		c.ClipPath(path.Rect(clip), paint.NonZero)
	}
	c.FillColor(path.Rect(geom.RectXYWH(0, 0, W, H)), fg)
	return c.Scene()
}

func clipScene(seed uint64) (*scene.Scene, int, int) { return buildClipScene(seed, 2), W, H }

// PremultiplyRoundTrip: over an EMPTY backdrop, every blend mode must produce
// exactly what SrcOver produces.
//
// SrcOver is the oracle because it is the one path that never unpremultiplies —
// it composites the premultiplied source directly. Every other mode divides the
// source by its alpha, blends, and re-multiplies. With a zero backdrop the W3C
// formula collapses to Co = αs·Cs, so that divide and re-multiply must cancel
// exactly, and any drift is a real defect in the un/re-premultiply the general
// blend path performs. Using SrcOver rather than an externally-computed
// premultiplied value keeps the renderer's own definition of the source color
// out of the oracle: restating it here would test Go's color model, not this.
func PremultiplyRoundTrip(t testing.TB, r RenderFunc, seed uint64, cfg parity.Config) {
	t.Helper()
	rng := newRNG(seed)
	col := randColor(rng)
	mode := randBlendGeneral(rng)
	rect := randAlignedRect(rng)

	want := r(fillScene(rect, col, paint.SrcOver), W, H)
	requireNonTrivial(t, "premultiply-round-trip", seed, want)
	check(t, "premultiply-round-trip", seed, r(fillScene(rect, col, mode), W, H), want, cfg)
}

func fillScene(rect geom.Rect, col paint.Color, mode paint.BlendMode) *scene.Scene {
	c := render.NewCanvas()
	c.SetBlend(mode)
	c.FillColor(path.Rect(rect), col)
	return c.Scene()
}

func premulScene(seed uint64) (*scene.Scene, int, int) {
	rng := newRNG(seed)
	col := randColor(rng)
	mode := randBlendGeneral(rng)
	rect := randAlignedRect(rng)
	return fillScene(rect, col, mode), W, H
}

// CheckAll runs every law against one backend. The law must hold within a
// backend on its own terms.
func CheckAll(t *testing.T, r RenderFunc) {
	t.Helper()
	for _, law := range Laws() {
		t.Run(law.Name, func(t *testing.T) {
			for i := range Cases {
				seed := uint64(i) + 1
				t.Run(seedName(seed), func(t *testing.T) {
					law.Run(t, r, seed, law.Tol)
				})
			}
		})
	}
}

// CheckAgreement renders each law's generated scenes on two backends and
// requires they agree. A law holding independently on each backend does not
// imply the backends agree, so this is a separate gate.
func CheckAgreement(t *testing.T, got, want RenderFunc, cfg parity.Config) {
	t.Helper()
	for _, law := range Laws() {
		t.Run(law.Name, func(t *testing.T) {
			for i := range Cases {
				seed := uint64(i) + 1
				t.Run(seedName(seed), func(t *testing.T) {
					sc, w, h := law.Scene(seed)
					wantImg := want(sc, w, h)
					requireNonTrivial(t, law.Name, seed, wantImg)
					check(t, law.Name+"/agreement", seed, got(sc, w, h), wantImg, cfg)
				})
			}
		})
	}
}

func seedName(seed uint64) string { return fmt.Sprintf("seed=0x%x", seed) }

// CompositingAssociativity: SrcOver is associative, so rendering a scene whole
// must equal rendering a prefix and a suffix separately and compositing the two.
//
// Scenes composite left-to-right onto the framebuffer, so
// render([A,B,C]) = C over (B over (A over T)). Splitting gives
// X = render([A]) = A and Y = render([B,C]) = C over B (T is the identity for
// over), and associativity says Y over X = (C over B) over A = the whole. The
// law therefore pins that rendering is a pure fold over nodes with no cross-node
// state — the grouped form (A over B) over C is not expressible until Phase 18
// adds isolated layers, since a scene admits only one grouping today.
//
// The split render quantizes its intermediate to 8 bits where the whole render
// keeps full precision, so this cannot be exact; that rounding is the gate's
// named owner.
func CompositingAssociativity(t testing.TB, r RenderFunc, seed uint64, cfg parity.Config) {
	t.Helper()
	a, b, c := assocNodes(seed)

	whole := &scene.Scene{}
	whole.Add(a)
	whole.Add(b)
	whole.Add(c)

	prefix := &scene.Scene{}
	prefix.Add(a)

	suffix := &scene.Scene{}
	suffix.Add(b)
	suffix.Add(c)

	batch := r(whole, W, H)
	requireNonTrivial(t, "compositing-associativity", seed, batch)
	split := over(r(suffix, W, H), r(prefix, W, H))
	check(t, "compositing-associativity", seed, split, batch, cfg)
}

func assocNodes(seed uint64) (a, b, c scene.Node) {
	rng := newRNG(seed)
	mk := func() scene.Node {
		return scene.Node{
			Path:      randPath(rng),
			Transform: randAboutCenter(rng),
			Paint:     paint.Solid{Color: randColor(rng)},
			Op:        paint.SrcOver,
			FillRule:  paint.NonZero,
		}
	}
	return mk(), mk(), mk()
}

func assocScene(seed uint64) (*scene.Scene, int, int) {
	a, b, c := assocNodes(seed)
	sc := &scene.Scene{}
	sc.Add(a)
	sc.Add(b)
	sc.Add(c)
	return sc, W, H
}

// over composites premultiplied src over premultiplied dst, mirroring the
// renderer's SrcOver arithmetic.
func over(src, dst *image.RGBA) *image.RGBA {
	out := image.NewRGBA(dst.Rect)
	for i := 0; i < len(out.Pix); i += 4 {
		inv := 1 - float64(src.Pix[i+3])/255
		for c := range 4 {
			v := float64(src.Pix[i+c]) + float64(dst.Pix[i+c])*inv
			switch {
			case v <= 0:
				out.Pix[i+c] = 0
			case v >= 255:
				out.Pix[i+c] = 255
			default:
				out.Pix[i+c] = uint8(v + 0.5)
			}
		}
	}
	return out
}
