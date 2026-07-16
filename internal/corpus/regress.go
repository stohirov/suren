package corpus

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/stohirov/suren/internal/parity/fuzz"
)

//go:embed regress
var regressFS embed.FS

// Regressions are the minimized scenes the differential fuzzer (Phase 12c) has
// caught. Each becomes an ordinary corpus entry, so a find is gated by the same
// machine as a hand-written scene — cross-backend parity at the tolerance the
// scene earns, plus a CPU golden — rather than living in a fuzz corpus that only
// runs when someone remembers to fuzz.
//
// A malformed file is a hard failure, not a skip. These exist to keep fixed bugs
// fixed; one that silently stops loading is worse than one that was never
// recorded, because the green run then asserts something untrue.
func Regressions() ([]Entry, error) {
	names, err := fs.Glob(regressFS, "regress/*.json")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)

	out := make([]Entry, 0, len(names))
	for _, name := range names {
		data, err := regressFS.ReadFile(name)
		if err != nil {
			return nil, err
		}
		spec, err := fuzz.Load(data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		out = append(out, Entry{
			Name:  strings.TrimSuffix(path.Base(name), ".json"),
			W:     spec.W,
			H:     spec.H,
			Build: spec.Scene,
			Tol:   spec.Tol(),
		})
	}
	return out, nil
}
