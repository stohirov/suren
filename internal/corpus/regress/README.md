# Fuzz regressions

Each `fuzz-*.json` here is a **minimized** scene that once made the CPU and GPU
renderers disagree, recorded by `internal/parity/fuzz` (Phase 12c). Files are
loaded by `corpus.All()`, so a find becomes a permanent parity entry and a CPU
golden — it is checked on every `go test ./...` forever, not only while the
fuzzer happens to roll that seed again.

They are stored as explicit scene data rather than as a bare seed on purpose: a
seed only reproduces while the generator is untouched, so the first change to
`gen.go` would silently retire every past find. The spec is the repro.

To record one from a failing run:

    go test ./backend/gpu -run TestFuzzReplay -seed=0x<seed> -emit
    go test ./backend/cpu -run TestGoldenCorpus -update

The first writes the minimized spec here; the second mints its golden. Commit
both, and fix the bug.

An empty directory is the intended steady state: it means the differential has
found nothing outstanding.
