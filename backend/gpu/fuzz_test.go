package gpu

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stohirov/sukho/backend/cpu"
	"github.com/stohirov/sukho/internal/parity/fuzz"
)

var (
	seedFlag = flag.String("seed", "", "replay one differential seed (hex or decimal), e.g. -seed=0x2a")
	emitFlag = flag.Bool("emit", false, "on a failing -seed, record the minimized scene in internal/corpus/regress")
)

// Seeds swept on every run. The differential is a standing gate, not something
// that only runs when someone remembers to pass -fuzz: `go test ./...` must be
// able to catch a regression that the hand-written corpus does not cover.
const sweepSeeds = 64

func fuzzRenderers(t testing.TB) (got, want fuzz.RenderFunc) {
	return fuzz.RenderFunc(gpuRenderFunc(t)), fuzz.RenderFunc(cpu.Render)
}

func TestFuzzSweep(t *testing.T) {
	got, want := fuzzRenderers(t)
	for i := range sweepSeeds {
		seed := uint64(i) + 1
		t.Run(fmt.Sprintf("seed=0x%x", seed), func(t *testing.T) {
			fuzz.Check(t, seed, got, want)
		})
	}
}

// FuzzDifferential is the unbounded search: go test ./backend/gpu -fuzz
// FuzzDifferential. The oracle is CPU-vs-GPU exact delta at the tolerance the
// generated scene earns, and a find reports a minimized, replayable scene.
func FuzzDifferential(f *testing.F) {
	for i := range 8 {
		f.Add(uint64(i) + 1)
	}
	got, want := fuzzRenderers(f)
	f.Fuzz(func(t *testing.T, seed uint64) {
		fuzz.Check(t, seed, got, want)
	})
}

// TestFuzzReplay re-materializes one seed headlessly:
//
//	go test ./backend/gpu -run TestFuzzReplay -seed=0x2a
//
// Add -emit to record a failing seed's minimized scene as a permanent corpus
// entry, which is what turns a fuzz find into a regression that outlives the
// generator that produced it.
func TestFuzzReplay(t *testing.T) {
	if *seedFlag == "" {
		t.Skip("no -seed given; pass -seed=0x... to replay one differential seed")
	}
	seed, err := strconv.ParseUint(*seedFlag, 0, 64)
	if err != nil {
		t.Fatalf("bad -seed %q: %v", *seedFlag, err)
	}

	got, want := fuzzRenderers(t)
	spec := fuzz.Generate(seed)
	rep, diverged, err := fuzz.Diff(spec, got, want)
	if err != nil {
		t.Fatalf("differential [seed=0x%x]: %v", seed, err)
	}
	if !diverged {
		t.Logf("seed=0x%x agrees under %s: %s", seed, rep.Gate, rep.Result.Describe(rep.Gate))
		if rep.Trivial {
			t.Logf("seed=0x%x renders uniformly; the differential proves nothing here", seed)
		}
		return
	}
	if *emitFlag {
		emitRegression(t, rep)
	}
	t.Fatalf("%s", rep)
}

func emitRegression(t *testing.T, rep fuzz.Report) {
	t.Helper()
	data, err := rep.Spec.Encode()
	if err != nil {
		t.Fatalf("encode minimized spec: %v", err)
	}
	path := filepath.Join("..", "..", "internal", "corpus", "regress", fuzz.RegressionName(rep.Seed))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write regression: %v", err)
	}
	t.Logf("recorded %s; mint its golden with: go test ./backend/cpu -run TestGoldenCorpus -update", path)
}
