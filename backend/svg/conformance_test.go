package svg

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stohirov/suren/geom"
	"github.com/stohirov/suren/internal/sample"
	"github.com/stohirov/suren/paint"
	"github.com/stohirov/suren/path"
	"github.com/stohirov/suren/render"
	"github.com/stohirov/suren/scene"
)

// Phase 24's conformance suite: one test per row of the audit table, plus the
// coupling between the blend and composite rows that the table's one-row-one-fix
// shape cannot express.
//
// Every test here failed against the code as it stood before Phase 24 — that is
// the point of them. The five gaps were not merely unfixed, they were UNOBSERVED:
// this package had six tests and two goldens and not one mentioned a clip, a
// blend mode, or a conic.

func unit(t *testing.T) *render.Canvas {
	t.Helper()
	return render.NewCanvas()
}

func box() path.Path { return path.Rect(geom.RectXYWH(1, 1, 6, 6)) }

// --- Row: blend mode (Node.Op) -------------------------------------------

func TestBlendModeEmitsMixBlendMode(t *testing.T) {
	c := unit(t)
	c.SetBlend(paint.Multiply)
	c.FillColor(box(), paint.RGB(1, 0, 0))
	got, rep := encode(t, c, 10, 10)

	if !strings.Contains(got, "mix-blend-mode:multiply") {
		t.Errorf("Node.Op=Multiply must export as mix-blend-mode; got:\n%s", got)
	}
	if rep.Lossy() {
		t.Errorf("blend under SrcOver is expressible and must not be reported: %v", rep.Dropped)
	}
}

func TestEverySeparableBlendModeHasACSSName(t *testing.T) {
	// The W3C separable set is exactly what mix-blend-mode names, so every mode
	// this renderer has must map. A mode with no name would silently export as
	// Normal, which is the wrong output this phase exists to remove.
	modes := []struct {
		mode paint.BlendMode
		css  string
	}{
		{paint.Multiply, "multiply"}, {paint.Screen, "screen"},
		{paint.Overlay, "overlay"}, {paint.Darken, "darken"},
		{paint.Lighten, "lighten"}, {paint.ColorDodge, "color-dodge"},
		{paint.ColorBurn, "color-burn"}, {paint.HardLight, "hard-light"},
		{paint.SoftLight, "soft-light"}, {paint.Difference, "difference"},
		{paint.Exclusion, "exclusion"},
	}
	for _, m := range modes {
		t.Run(m.css, func(t *testing.T) {
			c := unit(t)
			c.SetBlend(m.mode)
			c.FillColor(box(), paint.RGB(1, 0, 0))
			got, rep := encode(t, c, 10, 10)
			if !strings.Contains(got, "mix-blend-mode:"+m.css) {
				t.Errorf("blend %v must export as %q; got:\n%s", m.mode, m.css, got)
			}
			if rep.Lossy() {
				t.Errorf("unexpected report: %v", rep.Dropped)
			}
		})
	}
}

func TestNormalBlendEmitsNothing(t *testing.T) {
	c := unit(t)
	c.FillColor(box(), paint.RGB(1, 0, 0))
	got, _ := encode(t, c, 10, 10)
	if strings.Contains(got, "mix-blend-mode") {
		t.Errorf("Normal is the default and must stay off the wire:\n%s", got)
	}
}

// --- Row: composite op (Node.Composite) ----------------------------------

func TestCompositeOpIsReported(t *testing.T) {
	c := unit(t)
	c.SetComposite(paint.Xor)
	c.FillColor(box(), paint.RGB(1, 0, 0))
	_, rep := encode(t, c, 10, 10)

	if !rep.Lossy() {
		t.Fatal("a non-SrcOver composite is not expressible in SVG and must be reported, not dropped in silence")
	}
	if !strings.Contains(rep.Dropped[0].Feature, "xor") {
		t.Errorf("report must name the operator; got %q", rep.Dropped[0].Feature)
	}
}

func TestSrcOverCompositeIsNotReported(t *testing.T) {
	c := unit(t)
	c.SetComposite(paint.SrcOver)
	c.FillColor(box(), paint.RGB(1, 0, 0))
	_, rep := encode(t, c, 10, 10)
	if rep.Lossy() {
		t.Errorf("SrcOver is SVG's own default and loses nothing: %v", rep.Dropped)
	}
}

// --- The coupling the audit table cannot express -------------------------

// TestBlendUnderNonSrcOverCompositeIsNotEmitted is the finding that Phase 24's
// row-per-feature table structurally could not state.
//
// mix-blend-mode IMPLIES source-over compositing. So a node with Op=Multiply and
// Composite=Xor cannot be half-exported: emitting mix-blend-mode:multiply renders
// Multiply-OVER, which is not Multiply-XOR. It is a different wrong output, not a
// closer one. Fixing the blend row independently of the composite row — which is
// what a table of independent rows invites — would have shipped exactly that.
//
// So blend is emitted only where it is exactly right, and the report names both.
func TestBlendUnderNonSrcOverCompositeIsNotEmitted(t *testing.T) {
	c := unit(t)
	c.SetBlend(paint.Multiply)
	c.SetComposite(paint.Xor)
	c.FillColor(box(), paint.RGB(1, 0, 0))
	got, rep := encode(t, c, 10, 10)

	if strings.Contains(got, "mix-blend-mode") {
		t.Errorf("mix-blend-mode implies source-over; under Xor it renders Multiply-over, "+
			"which is wrong output rather than partial output:\n%s", got)
	}
	if !rep.Lossy() {
		t.Fatal("both axes were lost; the report must say so")
	}
	f := rep.Dropped[0].Feature
	if !strings.Contains(f, "xor") || !strings.Contains(f, "multiply") {
		t.Errorf("report must name the composite AND the blend it suppressed; got %q", f)
	}
}

// --- Row: path clips (Node.Clips) ----------------------------------------

func TestPathClipEmitsClipPath(t *testing.T) {
	c := unit(t)
	c.ClipPath(path.Circle(geom.Pt(5, 5), 3), paint.NonZero)
	c.FillColor(box(), paint.RGB(1, 0, 0))
	got, rep := encode(t, c, 10, 10)

	if !strings.Contains(got, "<clipPath") {
		t.Errorf("Node.Clips must export as <clipPath>; got:\n%s", got)
	}
	if !strings.Contains(got, `clip-path="url(#`) {
		t.Errorf("the clipped node must reference its clipPath; got:\n%s", got)
	}
	// A rect-only clipPath would prove nothing: the bbox clip already emits one.
	// The clip's own PATH DATA has to be on the wire.
	if !strings.Contains(got, "<clipPath") || !strings.Contains(clipPathBlock(got), "<path") {
		t.Errorf("clipPath must carry path data, not just the bbox rect; got:\n%s", got)
	}
	if rep.Lossy() {
		t.Errorf("path clips are expressible and must not be reported: %v", rep.Dropped)
	}
}

func TestPathClipEvenOddEmitsClipRule(t *testing.T) {
	c := unit(t)
	c.ClipPath(path.Circle(geom.Pt(5, 5), 3), paint.EvenOdd)
	c.FillColor(box(), paint.RGB(1, 0, 0))
	got, _ := encode(t, c, 10, 10)

	// clip-rule, not fill-rule: inside a <clipPath> the fill rule is spelled
	// differently, and getting it wrong silently changes which region survives.
	if !strings.Contains(got, `clip-rule="evenodd"`) {
		t.Errorf("an even-odd clip must emit clip-rule; got:\n%s", got)
	}
}

func TestNestedPathClipsIntersect(t *testing.T) {
	c := unit(t)
	c.ClipPath(path.Circle(geom.Pt(4, 5), 3), paint.NonZero)
	c.ClipPath(path.Circle(geom.Pt(6, 5), 3), paint.NonZero)
	c.FillColor(box(), paint.RGB(1, 0, 0))
	got, rep := encode(t, c, 10, 10)

	// Two clips must INTERSECT. Two children inside one <clipPath> would union
	// them, which is the opposite; nesting is what intersects in SVG.
	if n := strings.Count(got, "<clipPath"); n < 2 {
		t.Errorf("two path clips must produce two clipPaths (nested, so they intersect); got %d:\n%s", n, got)
	}
	if n := strings.Count(got, `<g clip-path="url(#`); n < 2 {
		t.Errorf("clips must nest as groups to intersect; got %d:\n%s", n, got)
	}
	if rep.Lossy() {
		t.Errorf("unexpected report: %v", rep.Dropped)
	}
}

// --- Rows: conic + mesh (genuine format limits) --------------------------

func TestConicPaintIsReported(t *testing.T) {
	c := unit(t)
	c.Fill(box(), paint.ConicGradient{
		Center: geom.Pt(5, 5),
		Stops:  []paint.Stop{{Offset: 0, Color: paint.RGB(1, 0, 0)}, {Offset: 1, Color: paint.RGB(1, 0, 0)}},
	}, paint.NonZero)
	got, rep := encode(t, c, 10, 10)

	if !rep.Lossy() {
		t.Fatal("SVG has no conic primitive; the drop is defensible but the silence is not")
	}
	if !strings.Contains(rep.Dropped[0].Feature, "conic") {
		t.Errorf("report must name the paint; got %q", rep.Dropped[0].Feature)
	}
	if strings.Contains(got, "<path") {
		t.Errorf("a dropped node must not leave a half-painted path behind:\n%s", got)
	}
}

// TestImagePaintIsReported pins the Phase 17 decision recorded above paintName.
// Unlike conic and mesh, this drop is not obviously right — SVG does have
// <pattern> and <image> — so the test states which half of the reasoning it
// guards: no CSS image-rendering value pins a filter kernel, so an emitted image
// would hand the sampling to the user agent, and <pattern> only ever repeats, so
// two of the three edge modes have nowhere to go.
//
// The subtest is the coupling. Repeat is the mode that IS tileable, and it must
// drop too: a per-row fix would emit it and ship UA-defined filtering under a node
// that looks correct, which trades a reported gap for an unreported wrong pixel.
func TestImagePaintIsReported(t *testing.T) {
	for _, e := range []struct {
		name string
		edge paint.EdgeMode
	}{{"clamp", paint.Clamp}, {"repeat-is-tileable-and-still-drops", paint.Repeat}, {"mirror", paint.Mirror}} {
		t.Run(e.name, func(t *testing.T) {
			img := sample.Checker(4, 4)
			img.Edge = e.edge
			c := unit(t)
			c.Fill(box(), img, paint.NonZero)
			got, rep := encode(t, c, 10, 10)

			if !rep.Lossy() {
				t.Fatal("no image-rendering value pins a filter kernel and <pattern> only repeats; the drop is defensible but the silence is not")
			}
			if !strings.Contains(rep.Dropped[0].Feature, "image") {
				t.Errorf("report must name the paint; got %q", rep.Dropped[0].Feature)
			}
			if strings.Contains(got, "<path") {
				t.Errorf("a dropped node must not leave a half-painted path behind:\n%s", got)
			}
		})
	}
}

func TestMeshPaintIsReported(t *testing.T) {
	c := unit(t)
	cols := []paint.Color{paint.RGB(1, 0, 0), paint.RGB(0, 1, 0), paint.RGB(0, 0, 1), paint.RGB(1, 1, 0)}
	c.Fill(box(), paint.MeshGrid(geom.RectXYWH(0, 0, 10, 10), 1, 1, cols), paint.NonZero)
	_, rep := encode(t, c, 10, 10)

	if !rep.Lossy() {
		t.Fatal("SVG has no mesh primitive; the drop must be reported")
	}
	if !strings.Contains(rep.Dropped[0].Feature, "mesh") {
		t.Errorf("report must name the paint; got %q", rep.Dropped[0].Feature)
	}
}

func TestReportIndexesTheSceneNode(t *testing.T) {
	// The index has to point at the caller's own node, or the report is a
	// description rather than a location.
	c := unit(t)
	c.FillColor(box(), paint.RGB(0, 0, 0))
	c.FillColor(box(), paint.RGB(0, 0, 0))
	c.Fill(box(), paint.ConicGradient{
		Center: geom.Pt(5, 5),
		Stops:  []paint.Stop{{Offset: 0, Color: paint.RGB(1, 0, 0)}, {Offset: 1, Color: paint.RGB(1, 0, 0)}},
	}, paint.NonZero)
	_, rep := encode(t, c, 10, 10)

	if !rep.Lossy() {
		t.Fatal("conic must be reported")
	}
	if got := rep.Dropped[0].Node; got != 2 {
		t.Errorf("report must index the scene's own Nodes slice: got %d, want 2", got)
	}
}

// --- Rows already honoured: regression guards ----------------------------

func TestRectClipStillHonoured(t *testing.T) {
	c := unit(t)
	c.ClipRect(geom.RectXYWH(2, 2, 4, 4))
	c.FillColor(box(), paint.RGB(1, 0, 0))
	got, rep := encode(t, c, 10, 10)

	if !strings.Contains(got, "<rect") {
		t.Errorf("rect clip must still emit a rect clipPath; got:\n%s", got)
	}
	if rep.Lossy() {
		t.Errorf("rect clips are expressible: %v", rep.Dropped)
	}
}

func TestFallbackIsNotAReportableLoss(t *testing.T) {
	// Fallback is a GPU-exactness hint (scene.Node's own doc says the CPU
	// reference ignores it). SVG is not a rasterizer, so there is nothing to
	// honour and nothing lost. Ignoring it is correct — but it has to be
	// DECIDED, not overlooked, which is what the field-contract test below is for.
	c := unit(t)
	c.SetFallback(true)
	c.FillColor(box(), paint.RGB(1, 0, 0))
	_, rep := encode(t, c, 10, 10)
	if rep.Lossy() {
		t.Errorf("Fallback is not a rendering property and must not be reported: %v", rep.Dropped)
	}
}

func TestFaithfulSceneReportsNothing(t *testing.T) {
	// The negative claim has to be testable too, or Lossy() is just always true
	// and the report means nothing.
	c := unit(t)
	c.Translate(1, 1)
	c.SetBlend(paint.Screen)
	c.Fill(box(), paint.LinearGradient{
		P0: geom.Pt(0, 0), P1: geom.Pt(8, 8),
		Stops: []paint.Stop{{Offset: 0, Color: paint.RGB(1, 0, 0)}, {Offset: 1, Color: paint.RGB(0, 0, 1)}},
	}, paint.NonZero)
	c.StrokeColor(box(), paint.RGB(0, 0, 0), paint.Stroke{Width: 2, Dashes: []float64{3, 1}})
	_, rep := encode(t, c, 10, 10)

	if rep.Lossy() {
		t.Errorf("every feature in this scene is expressible in SVG: %v", rep.Dropped)
	}
}

// --- The thing that watches the watcher ----------------------------------

// svgFieldContract records what the SVG backend does with each scene.Node field.
//
// This is the test Phase 24 asked for by name: the conic drop was found because
// someone added conic, and the blend drop was found by reading the file two
// phases later. Both were found by luck. A field added to scene.Node tomorrow
// with no SVG answer is the same bug again, and this map is what makes that a
// build failure instead of a discovery.
//
// The parity machine watches the two rasterizers against each other; it has
// never watched this backend, because SVG emits vectors and there is nothing to
// diff at the channel level. This is the closest thing to a gate that the SVG
// path can have: not "is the output right" but "has every field been DECIDED".
var svgFieldContract = map[string]string{
	"Path":      "emitted as <path d=…>",
	"Transform": "emitted as transform=matrix(…)",
	"Paint":     "emitted (solid/linear/radial); reported (conic/mesh — no SVG primitive)",
	"Op":        "emitted as mix-blend-mode when Composite==SrcOver; reported otherwise (mix-blend-mode implies source-over)",
	"Composite": "reported (SVG merges an element with its backdrop by source-over only; the Porter-Duff set is canvas-2D-only. feComposite composites filter inputs, not the backdrop)",
	"FillRule":  "emitted as fill-rule",
	"Stroke":    "emitted as stroke-* attributes",
	"Clip":      "emitted as <clipPath><rect>",
	"Clips":     "emitted as nested <clipPath> with path data",
	"Fallback":  "ignored — a GPU exactness hint, not a rendering property",
}

func TestEverySceneNodeFieldHasAnSVGContract(t *testing.T) {
	ty := reflect.TypeFor[scene.Node]()
	seen := map[string]bool{}
	for i := range ty.NumField() {
		name := ty.Field(i).Name
		seen[name] = true
		if _, ok := svgFieldContract[name]; !ok {
			t.Errorf("scene.Node.%s has no recorded SVG contract.\n"+
				"Every field must be either honoured by this backend or reported to the caller.\n"+
				"Add it to svgFieldContract and to the table in docs/roadmap.md Phase 24 — "+
				"deciding to ignore it is a valid answer, overlooking it is what shipped five silent gaps.", name)
		}
	}
	for name := range svgFieldContract {
		if !seen[name] {
			t.Errorf("svgFieldContract names %q, which scene.Node no longer has; the contract has drifted from the model", name)
		}
	}
}

// clipPathBlock returns the text inside the <defs> block, where clipPaths live.
func clipPathBlock(doc string) string {
	i := strings.Index(doc, "<defs>")
	j := strings.Index(doc, "</defs>")
	if i < 0 || j < 0 || j < i {
		return ""
	}
	return doc[i:j]
}
