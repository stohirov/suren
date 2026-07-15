package corpus

import (
	"strings"
	"testing"

	"github.com/stohirov/sukho/paint"
)

func TestEntriesAreWellFormed(t *testing.T) {
	entries := All()
	if len(entries) == 0 {
		t.Fatal("corpus is empty")
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if e.Name == "" {
			t.Error("entry with an empty name")
			continue
		}
		if seen[e.Name] {
			t.Errorf("duplicate entry name %q; goldens would collide", e.Name)
		}
		seen[e.Name] = true

		if err := e.Tol.Validate(); err != nil {
			t.Errorf("%s: tolerance violates the contract: %v", e.Name, err)
		}
		if e.W <= 0 || e.H <= 0 {
			t.Errorf("%s: bad size %dx%d", e.Name, e.W, e.H)
		}
		sc := e.Build()
		if sc == nil || len(sc.Nodes) == 0 {
			t.Errorf("%s: Build produced no nodes", e.Name)
		}
	}
}

// Each blend entry must build its own mode. Capturing the loop variable wrongly
// would silently collapse all twelve into one scene, and every gate would still
// pass while testing a single mode twelve times.
func TestBlendEntriesBuildDistinctScenes(t *testing.T) {
	ops := map[paint.BlendMode]string{}
	for _, e := range All() {
		if !strings.HasPrefix(e.Name, "blend-") {
			continue
		}
		sc := e.Build()
		op := sc.Nodes[len(sc.Nodes)-1].Op
		if prev, ok := ops[op]; ok {
			t.Errorf("%s and %s build the same blend op %v", prev, e.Name, op)
		}
		ops[op] = e.Name
	}
	if len(ops) != len(blendModes) {
		t.Errorf("got %d distinct blend ops, want %d", len(ops), len(blendModes))
	}
}
