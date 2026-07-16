package fuzz

import (
	"fmt"
	"image"
	"strings"
	"testing"

	"github.com/stohirov/suren/internal/parity"
	"github.com/stohirov/suren/scene"
)

// RenderFunc matches props.RenderFunc structurally. The two packages declare it
// separately on purpose: neither imports the other, and neither imports a
// backend, so backend/gpu's cgo dependency stays quarantined in its own module
// and the GPU test passes its renderer in.
type RenderFunc func(sc *scene.Scene, w, h int) *image.RGBA

type Report struct {
	Seed      uint64
	Gate      parity.Config
	Spec      Spec
	Result    parity.Result
	Box       image.Rectangle
	FirstNode int
	Renders   int
	Trivial   bool
}

// Diff renders a spec on both backends and, if they disagree beyond the gate the
// scene earned, minimizes the scene and attributes the divergence to one node.
//
// The gate is computed ONCE, from the original spec, and held fixed for the
// whole run. Recomputing it per candidate would let the shrinker re-gate its way
// to a "failure": strip the ColorDodge that earned a Δ≤2 budget and a Δ=2
// residue would suddenly violate the Δ≤1 floor, reporting a bug that the
// original scene never had. Fixed-gate shrinking can only ever under-report.
func Diff(spec Spec, got, want RenderFunc) (Report, bool, error) {
	gate := spec.Tol()
	if err := gate.Validate(); err != nil {
		return Report{}, false, err
	}
	rep := Report{Seed: spec.Seed, Gate: gate, Spec: spec, FirstNode: -1}

	render := func(s Spec) (*image.RGBA, *image.RGBA) {
		rep.Renders += 2
		sc := s.Scene()
		return got(sc, s.W, s.H), want(sc, s.W, s.H)
	}

	g, w := render(spec)
	res, err := parity.Compare(g, w, gate)
	if err != nil {
		return rep, false, err
	}
	rep.Result = res
	rep.Trivial = !parity.NonTrivial(w)
	if res.OK(gate) {
		return rep, false, nil
	}

	var cmpErr error
	diverges := func(s Spec) bool {
		if cmpErr != nil {
			return false
		}
		g, w := render(s)
		res, err := parity.Compare(g, w, gate)
		if err != nil {
			cmpErr = err
			return false
		}
		return !res.OK(gate)
	}

	rep.Spec = Shrink(spec, diverges)
	rep.FirstNode = FirstDiverging(rep.Spec, diverges)
	if cmpErr != nil {
		return rep, true, cmpErr
	}

	g, w = render(rep.Spec)
	if rep.Result, err = parity.Compare(g, w, gate); err != nil {
		return rep, true, err
	}
	rep.Box = divergeBox(g, w, gate.Tol)
	return rep, true, nil
}

// Check is the test entry point: generate from a seed, and fail with a minimized,
// replayable report.
func Check(t testing.TB, seed uint64, got, want RenderFunc) {
	t.Helper()
	spec := Generate(seed)
	if err := spec.Validate(); err != nil {
		t.Fatalf("generator produced an invalid spec [seed=0x%x]: %v", seed, err)
	}
	rep, diverged, err := Diff(spec, got, want)
	if err != nil {
		t.Fatalf("differential [seed=0x%x]: %v", seed, err)
	}
	if diverged {
		t.Fatalf("%s", rep)
	}
	// A differential oracle agrees perfectly on a uniform canvas and proves
	// nothing, so a seed whose nodes are all invisible is skipped rather than
	// counted as a pass. Skipped, not failed: an invisible node is usually
	// correct blending, not a bug — Darken with a source lighter than an opaque
	// backdrop yields the backdrop exactly, for any alpha. Measured at 0.8% of
	// seeds and bounded by TestGeneratedScenesAreRarelyTrivial, so this can
	// never quietly eat the suite.
	if rep.Trivial {
		t.Skipf("scene [seed=0x%x] renders uniformly; nothing to compare", seed)
	}
}

// divergeBox bounds the pixels that actually disagree. The max-delta point alone
// says where it is worst; the box says whether the divergence is one antialiased
// edge or the whole shape, which is usually what identifies the bug.
func divergeBox(got, want *image.RGBA, tol int) image.Rectangle {
	box := image.Rectangle{}
	first := true
	b := want.Rect
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			gi, wi := got.PixOffset(x, y), want.PixOffset(x, y)
			over := false
			for c := range 4 {
				d := int(got.Pix[gi+c]) - int(want.Pix[wi+c])
				if d < 0 {
					d = -d
				}
				if d > tol {
					over = true
					break
				}
			}
			if !over {
				continue
			}
			p := image.Rect(x, y, x+1, y+1)
			if first {
				box, first = p, false
			} else {
				box = box.Union(p)
			}
		}
	}
	return box
}

func (r Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "differential divergence [seed=0x%x]\n", r.Seed)
	fmt.Fprintf(&b, "  gate:      %s\n", r.Gate)
	fmt.Fprintf(&b, "  result:    %s\n", r.Result.Describe(r.Gate))
	fmt.Fprintf(&b, "  diverging: %d of %d channels, bounded by %v\n", r.Result.Over, r.Result.Total, r.Box)
	fmt.Fprintf(&b, "  minimized: %d nodes (from the generated scene), %d renders spent\n", len(r.Spec.Nodes), r.Renders)
	if r.FirstNode >= 0 && r.FirstNode < len(r.Spec.Nodes) {
		fmt.Fprintf(&b, "  first diverging node: #%d %s\n", r.FirstNode, describeNode(r.Spec.Nodes[r.FirstNode]))
	}
	fmt.Fprintf(&b, "  replay:    go test ./backend/gpu -run TestFuzzReplay -seed=0x%x\n", r.Seed)
	if data, err := r.Spec.Encode(); err == nil {
		fmt.Fprintf(&b, "  record it as a permanent regression with -emit, or by hand:\n%s", indent(string(data)))
	}
	return b.String()
}

// describeNode names the features in play, so the report reads as "this is a
// dashed round-join stroke under a rotation" rather than a wall of floats.
func describeNode(n NodeSpec) string {
	parts := []string{shapeNames[n.Shape.Kind], paintNames[n.Paint.Kind]}
	if n.Stroke != nil {
		s := "stroke"
		if n.Stroke.Dashes != nil {
			s = "dashed stroke"
		}
		parts = append(parts, s)
	} else {
		parts = append(parts, "fill")
	}
	if n.Rule == 1 {
		parts = append(parts, "even-odd")
	}
	if n.Op != 0 {
		parts = append(parts, fmt.Sprintf("blend=%d", n.Op))
	}
	if len(n.Clips) > 0 {
		parts = append(parts, fmt.Sprintf("%d clips", len(n.Clips)))
	}
	return strings.Join(parts, " ")
}

var shapeNames = map[ShapeKind]string{
	ShapeRect: "rect", ShapeRoundRect: "round-rect", ShapeCircle: "circle",
	ShapePolygon: "polygon", ShapeCurve: "curve",
}

var paintNames = map[PaintKind]string{
	PaintSolid: "solid", PaintLinear: "linear-gradient", PaintRadial: "radial-gradient",
}

func indent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "    " + l
	}
	return strings.Join(lines, "\n") + "\n"
}

// RegressionName is the corpus filename for a find. The seed is in the name so a
// recorded regression still points back at the run that produced it.
func RegressionName(seed uint64) string { return fmt.Sprintf("fuzz-%016x.json", seed) }
