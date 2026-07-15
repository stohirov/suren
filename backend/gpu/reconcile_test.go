package gpu

import (
	"testing"

	"github.com/stohirov/sukho/backend/cpu"
	"github.com/stohirov/sukho/internal/corpus"
	"github.com/stohirov/sukho/internal/parity"
)

// Cross-backend reconciliation (Phase 12d).
//
// Everywhere else in this package the GPU is whatever adapter wgpu picked, which
// on this developer's machine is always Metal — so "GPU parity" has so far meant
// "parity on my laptop". This runs the whole corpus on each native backend the
// host exposes, by name, and says which ones those were.
//
// # Why there is no stored GPU golden
//
// The plan called for per-corpus GPU golden RGBA that every backend diffs
// against. That is the wrong instrument, for three reasons that only became
// clear once Phase 13 landed:
//
//   - It invents a second oracle with no authority. If Metal's recording says
//     128 and Vulkan says 129 while the true value is 128.5, both are correct;
//     failing Vulkan against Metal's recording asserts "reproduce Metal's
//     rounding rather than the true value", which is precisely the trade
//     internal/parity's contract rejects.
//   - The CPU reference is already a machine-independent oracle. Phase 13
//     measured that: Go fuses FMA on arm64, yet every golden reproduces
//     bit-for-bit pinned or not, because f64 sits ~13 orders of magnitude away
//     from the 8-bit rounding decision. So gating each backend against the CPU on
//     its own host is a claim that composes across hosts without a shared file.
//   - It would be WEAKER than what is already gated. A GPU golden could only be
//     held at the quantization floor, but many-nodes, clip-rect and both
//     blend-stack entries are gated at Δ=0 against the CPU reference today.
//
// A direct backend-vs-backend gate is no better: given both pass their CPU gate
// at tolerance T they are within 2T of each other automatically, so asserting 2T
// is a tautology, and asserting T claims two independent f32 drivers must agree
// more tightly than either agrees with the reference, which nothing supports.
//
// So the CPU reference stays the only oracle, and this test's job is to make sure
// every backend faces it — and to report the ones that never did.
func TestReconcileBackends(t *testing.T) {
	var ran, skipped []Backend

	for _, b := range Selectable() {
		probe, err := NewRendererOn(b, 8, 8)
		if err != nil {
			skipped = append(skipped, b)
			t.Run(b.String(), func(t *testing.T) {
				t.Skipf("host does not expose %v: %v", b, err)
			})
			continue
		}
		desc := probe.Device().Describe()
		probe.Release()
		ran = append(ran, b)

		t.Run(b.String(), func(t *testing.T) {
			t.Logf("reconciling against adapter: %s", desc)
			for _, e := range corpus.All() {
				t.Run(e.Name, func(t *testing.T) {
					r, err := NewRendererOn(b, e.W, e.H)
					if err != nil {
						t.Fatalf("%v vanished between probe and use: %v", b, err)
					}
					defer r.Release()
					if err := r.Render(e.Build()); err != nil {
						t.Fatalf("render: %v", err)
					}
					got, err := r.ReadRGBA()
					if err != nil {
						t.Fatalf("readback: %v", err)
					}
					parity.Assert(t, got, cpu.Render(e.Build(), e.W, e.H), e.Tol)
				})
			}
		})
	}

	// The scope of a green run, stated. Without this the test passes on a
	// Metal-only host and reads as "backends reconciled" when it reconciled one.
	t.Logf("reconciled %d backend(s): %v", len(ran), ran)
	if len(skipped) > 0 {
		t.Logf("NOT reconciled here (host does not expose them): %v — the portability claim covers %v only",
			skipped, ran)
	}
	if len(ran) == 0 {
		t.Skip("host exposes no GPU backend at all")
	}
}

// A backend must never be reported as reconciled when a different one ran.
//
// This is cheap insurance on a Metal-only host, where it can only assert that
// metal is metal; its value is on a CI matrix, and its motivation is concrete.
// wgpu-native accepts RequestAdapterOptions.BackendType, warns that it is
// unsupported, and returns whatever adapter it has — so the obvious way to pick a
// backend silently yields Metal on this host, and a harness built on it would
// have printed "vulkan: PASS" having tested Metal. The instance mask that
// NewDeviceOn uses instead does filter, so this holds the *result* to the same
// standard the selection is.
func TestBackendSelectionIsHonest(t *testing.T) {
	any := 0
	for _, b := range Selectable() {
		d, err := NewDeviceOn(b)
		if err != nil {
			t.Logf("%v: unavailable (%v)", b, err)
			continue
		}
		any++
		got := d.Info().BackendType
		want, _ := b.wants()
		if got != want {
			t.Errorf("NewDeviceOn(%v) returned a %v adapter; a portability result would name the wrong backend", b, got)
		}
		t.Logf("%v: %s", b, d.Describe())
		d.Release()
	}
	if any == 0 {
		t.Skip("host exposes no selectable backend")
	}
}
