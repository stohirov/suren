package fuzz

import (
	"github.com/stohirov/sukho/geom"
	"github.com/stohirov/sukho/paint"
)

// Fails reports whether a candidate still exhibits the divergence under
// investigation. Every call costs two renders, one per backend, so the moves
// below are ordered cheapest-and-most-reducing first.
type Fails func(Spec) bool

// Shrink minimizes a diverging spec by construction: drop nodes, then strip
// features from the nodes that remain, until neither makes progress.
//
// Every candidate is accepted only if it STILL diverges, which is what makes the
// result trustworthy — the shrinker cannot invent a failure, it can only fail to
// remove something. The caller holds the gate fixed at the original scene's
// tolerance for the whole run (see Diff): if shrinking strips the ColorDodge
// that earned a Δ≤2 budget and the residue still exceeds it, that is a stronger
// find, not a re-gated one.
//
// A candidate must also still be a VALID spec. Nothing the generator emits can
// currently shrink into an invalid one — bboxRect would have to collapse a
// polygon to an empty rect, which needs exactly collinear points — but the blast
// radius if it ever did is out of proportion to the guard: an invalid minimized
// spec would be written by -emit, rejected by Load, and panic corpus.All() for
// every test in the tree. Validate is also cheaper than the two renders it
// short-circuits.
func Shrink(s Spec, fails Fails) Spec {
	valid := func(c Spec) bool { return c.Validate() == nil && fails(c) }
	for {
		before := s.size()
		s = shrinkNodes(s, valid)
		s = shrinkFeatures(s, valid)
		if s.size() >= before {
			return s
		}
	}
}

// shrinkNodes is delta debugging over the node list: try dropping a contiguous
// chunk, and on success keep the smaller scene and retry; on failure halve the
// chunk size. This is the "drop-half, keep the failing half" bisect, generalized
// so a divergence that needs two distant nodes still minimizes.
func shrinkNodes(s Spec, fails Fails) Spec {
	chunks := 2
	for len(s.Nodes) >= 2 {
		size := max(len(s.Nodes)/chunks, 1)
		reduced := false
		for i := 0; i < len(s.Nodes); i += size {
			j := min(i+size, len(s.Nodes))
			cand := s
			cand.Nodes = make([]NodeSpec, 0, len(s.Nodes)-(j-i))
			cand.Nodes = append(cand.Nodes, s.Nodes[:i]...)
			cand.Nodes = append(cand.Nodes, s.Nodes[j:]...)
			if len(cand.Nodes) == 0 || !fails(cand) {
				continue
			}
			s = cand
			chunks = max(chunks-1, 2)
			reduced = true
			break
		}
		if reduced {
			continue
		}
		if chunks >= len(s.Nodes) {
			return s
		}
		chunks = min(chunks*2, len(s.Nodes))
	}
	return s
}

// shrinkFeatures strips one feature of one node at a time. The moves run from
// the most structural (clips, stroke) to the most cosmetic (fill rule), so the
// reported scene names the feature that actually matters rather than whatever
// the generator happened to roll.
func shrinkFeatures(s Spec, fails Fails) Spec {
	for i := range s.Nodes {
		// try is guarded by `when` so a move that is already a no-op (no stroke
		// to remove, transform already identity) never costs two renders to
		// discover it changed nothing.
		try := func(when bool, mod func(*NodeSpec)) {
			if !when {
				return
			}
			cand := s.clone()
			mod(&cand.Nodes[i])
			if fails(cand) {
				s = cand
			}
		}
		node := func() NodeSpec { return s.Nodes[i] }

		try(len(node().Clips) > 0, func(n *NodeSpec) { n.Clips = nil })
		// Downward, so dropping clip j never invalidates the indices still to
		// be tried.
		for j := len(node().Clips) - 1; j >= 0; j-- {
			try(true, func(n *NodeSpec) {
				next := make([]ClipSpec, 0, len(n.Clips)-1)
				next = append(next, n.Clips[:j]...)
				next = append(next, n.Clips[j+1:]...)
				n.Clips = next
			})
		}
		for j := range node().Clips {
			try(node().Clips[j].Shape.Kind != ShapeRect, func(n *NodeSpec) {
				next := append([]ClipSpec(nil), n.Clips...)
				next[j].Shape = next[j].Shape.bboxRect()
				n.Clips = next
			})
		}
		try(node().Stroke != nil, func(n *NodeSpec) { n.Stroke = nil })
		try(node().Stroke != nil && node().Stroke.Dashes != nil, func(n *NodeSpec) {
			st := *n.Stroke
			st.Dashes, st.DashOffset = nil, 0
			n.Stroke = &st
		})
		try(node().Paint.Kind != PaintSolid, func(n *NodeSpec) { n.Paint = n.Paint.solid() })
		try(node().Shape.Kind != ShapeRect, func(n *NodeSpec) { n.Shape = n.Shape.bboxRect() })
		try(node().Transform != geom.Identity(), func(n *NodeSpec) { n.Transform = geom.Identity() })
		try(node().Op != paint.SrcOver, func(n *NodeSpec) { n.Op = paint.SrcOver })
		try(node().Rule != paint.NonZero, func(n *NodeSpec) { n.Rule = paint.NonZero })
	}
	return s
}

// FirstDiverging returns the index of the earliest node whose prefix already
// diverges, or -1 if no prefix does — including the whole scene, which the
// caller has usually already established diverges.
//
// This is a LINEAR scan, deliberately, where the plan called for a binary
// search: prefix divergence is not monotone. A later opaque node can paint over
// the pixels where an earlier node diverged, so a clean prefix at k says nothing
// about k-1, and a binary search would report a node that merely happens to sit
// on a monotone boundary — attributing the bug to the wrong feature, which is
// the one thing this function exists to prevent. The scan runs on the SHRUNK
// spec, where the node count is small, so correctness costs a few renders.
//
// Prefixes are legitimate scenes to compare because rendering is a pure fold
// over nodes with no cross-node state — the property 12b's compositing law pins.
func FirstDiverging(s Spec, diverges Fails) int {
	for k := 1; k < len(s.Nodes); k++ {
		cand := s
		cand.Nodes = s.Nodes[:k]
		if diverges(cand) {
			return k - 1
		}
	}
	if len(s.Nodes) > 0 && diverges(s) {
		return len(s.Nodes) - 1
	}
	return -1
}
