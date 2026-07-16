package svg

import (
	"encoding/xml"
	"io"
	"sort"
	"strings"
	"testing"

	"github.com/stohirov/suren/internal/corpus"
)

// Phase 24's diagnosis was that the SVG backend drifted BECAUSE it is the one
// output path the parity machine does not watch. Fixing the five gaps without
// pointing something at it would leave that diagnosis true and simply reset the
// clock. This file is the something.
//
// It cannot be a parity test. SVG emits vectors, not pixels — there is nothing
// to diff at the channel level, and none of this belongs to the tolerance
// contract (see internal/parity). What it CAN do is run the same 43 scenes the
// differential runs, and assert the two things that are checkable without a
// second renderer: the document is well-formed, and the report is honest about
// what it dropped.

// TestCorpusScenesEncodeAsWellFormedXML is the coverage gate: every scene the
// parity machine holds to Δ≤1 also goes through this encoder, and the output has
// to parse. It is a weaker claim than parity by a wide margin — well-formed XML
// is not correct SVG — and it is stated here as the weaker claim it is. Its
// value is breadth: 43 scenes including every blend mode, every Porter-Duff
// operator, conic, mesh, clips and strokes, none of which this package tested at
// all before this phase.
func TestCorpusScenesEncodeAsWellFormedXML(t *testing.T) {
	for _, e := range corpus.All() {
		t.Run(e.Name, func(t *testing.T) {
			var b strings.Builder
			if _, err := Encode(&b, e.Build(), e.W, e.H); err != nil {
				t.Fatal(err)
			}
			dec := xml.NewDecoder(strings.NewReader(b.String()))
			for {
				_, err := dec.Token()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatalf("scene %q produced malformed XML: %v\n%s", e.Name, err, b.String())
				}
			}
		})
	}
}

// TestCorpusLossReportIsAudited prints, per corpus scene, what the SVG backend
// could not express — and FAILS if a scene the format fully covers reports a
// loss, or if the losses stop matching what this phase decided they are.
//
// The list is pinned rather than merely logged. A silently growing set of
// dropped features is exactly what Phase 24 found; a test that prints it without
// asserting it would let the same thing happen again while looking green. The
// register is borrowed from the reconciliation harness, which logs the backends
// it could NOT cover rather than passing quietly.
func TestCorpusLossReportIsAudited(t *testing.T) {
	// The scenes SVG cannot fully express, and the reason. Everything not named
	// here must encode losslessly.
	wantLossy := map[string]string{
		"gradient-conic":      "conic paint",
		"gradient-conic-seam": "conic paint",
	}
	// Composite scenes lose their operator; mesh and image scenes lose their
	// paint. All are matched by prefix because the corpus generates a family per
	// operator, and per filter x edge mode. See the comment above paintName for
	// why an image is a format limit rather than an omission — it is the one entry
	// here that looks emittable.
	lossyPrefix := []string{"composite-", "mesh", "gradient-mesh", "fallback-mesh", "image-"}

	var lossy []string
	for _, e := range corpus.All() {
		var b strings.Builder
		rep, err := Encode(&b, e.Build(), e.W, e.H)
		if err != nil {
			t.Fatalf("%s: %v", e.Name, err)
		}
		if !rep.Lossy() {
			if _, named := wantLossy[e.Name]; named {
				t.Errorf("scene %q was expected to lose %s but reported nothing — "+
					"either the encoder gained a feature (update this table) or the report went silent again",
					e.Name, wantLossy[e.Name])
			}
			continue
		}
		lossy = append(lossy, e.Name)

		_, named := wantLossy[e.Name]
		if !named {
			ok := false
			for _, p := range lossyPrefix {
				if strings.HasPrefix(e.Name, p) {
					ok = true
					break
				}
			}
			if !ok {
				t.Errorf("scene %q reports a loss this phase did not account for: %v\n"+
					"Every drop must be a decision. If SVG genuinely cannot express it, add it here with the reason.",
					e.Name, rep.Dropped)
			}
		}
	}

	sort.Strings(lossy)
	t.Logf("SVG cannot fully express %d/%d corpus scenes: %v", len(lossy), len(corpus.All()), lossy)
	t.Logf("the remaining %d encode losslessly", len(corpus.All())-len(lossy))
}

// TestCorpusBlendScenesEmitMixBlendMode closes the loop on the row that was
// wrong output rather than missing output. The corpus has one scene per W3C
// separable mode; each must now carry its mode onto the wire, and before this
// phase every one of them exported as Normal.
func TestCorpusBlendScenesEmitMixBlendMode(t *testing.T) {
	seen := 0
	for _, e := range corpus.All() {
		// blend-stack-* is a different family — 64 stacked layers, Phase 13's
		// per-node rounding gate — and its name is not a mode name. This test
		// derives the expected mode from the scene name, so it only covers the
		// one-scene-per-mode family. The stack scenes are still covered by the
		// well-formedness and loss-report gates above.
		if !strings.HasPrefix(e.Name, "blend-") || strings.HasPrefix(e.Name, "blend-stack-") {
			continue
		}
		mode := strings.TrimPrefix(e.Name, "blend-")
		if mode == "normal" {
			continue // the default; correctly absent from the output
		}
		var b strings.Builder
		if _, err := Encode(&b, e.Build(), e.W, e.H); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(b.String(), "mix-blend-mode:"+mode) {
			t.Errorf("corpus scene %q must emit mix-blend-mode:%s; it exported as Normal", e.Name, mode)
		}
		seen++
	}
	if seen == 0 {
		t.Fatal("no blend- scenes found in the corpus; this gate is testing nothing")
	}
	t.Logf("%d corpus blend scenes carry their mode into SVG", seen)
}
