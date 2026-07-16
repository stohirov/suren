package gpu

import (
	"testing"

	"github.com/stohirov/suren/internal/sample"
)

func TestEncodeSolidScene(t *testing.T) {
	e := Encode(sample.Scene(), sample.W, sample.H)
	if len(e.Nodes) != 4 {
		t.Fatalf("nodes = %d, want 4", len(e.Nodes))
	}
	if len(e.Segments) == 0 {
		t.Fatal("no segments emitted")
	}
	if len(e.Stops) != 0 {
		t.Fatalf("solid scene emitted %d stops, want 0", len(e.Stops))
	}
	for i, n := range e.Nodes {
		if n.Kind != uint32(PaintSolid) {
			t.Errorf("node %d kind = %d, want solid", i, n.Kind)
		}
		if n.SegCount == 0 || int(n.SegStart+n.SegCount) > len(e.Segments) {
			t.Errorf("node %d segment range [%d,+%d) out of bounds", i, n.SegStart, n.SegCount)
		}
	}
}

func TestEncodeGradientScene(t *testing.T) {
	e := Encode(sample.GradientScene(), sample.W, sample.H)
	if len(e.Nodes) != 3 {
		t.Fatalf("nodes = %d, want 3", len(e.Nodes))
	}
	kinds := []uint32{uint32(PaintLinear), uint32(PaintRadial), uint32(PaintLinear)}
	total := uint32(0)
	for i, n := range e.Nodes {
		if n.Kind != kinds[i] {
			t.Errorf("node %d kind = %d, want %d", i, n.Kind, kinds[i])
		}
		if n.StopCount == 0 || int(n.StopStart+n.StopCount) > len(e.Stops) {
			t.Errorf("node %d stop range [%d,+%d) out of bounds", i, n.StopStart, n.StopCount)
		}
		total += n.StopCount
	}
	if total != 7 || len(e.Stops) != 7 {
		t.Fatalf("stops = %d (sum %d), want 7", len(e.Stops), total)
	}
}

func TestEncodeTileSegLists(t *testing.T) {
	e := Encode(sample.ManySegments(320, 240, 200), 320, 240)
	entries := len(e.TileNodes)
	if len(e.TileSegOff) != entries+1 {
		t.Fatalf("TileSegOff len = %d, want entries+1 = %d", len(e.TileSegOff), entries+1)
	}
	if int(e.TileSegOff[entries]) != len(e.TileSegIdx) {
		t.Fatalf("TileSegOff tail = %d, want len(TileSegIdx) = %d", e.TileSegOff[entries], len(e.TileSegIdx))
	}
	for k := 0; k < entries; k++ {
		if e.TileSegOff[k] > e.TileSegOff[k+1] {
			t.Fatalf("TileSegOff not monotonic at %d", k)
		}
		nd := e.Nodes[e.TileNodes[k]]
		for j := e.TileSegOff[k]; j < e.TileSegOff[k+1]; j++ {
			si := e.TileSegIdx[j]
			if si < nd.SegStart || si >= nd.SegStart+nd.SegCount {
				t.Fatalf("entry %d lists segment %d outside node range [%d,+%d)", k, si, nd.SegStart, nd.SegCount)
			}
		}
	}
}

func TestEncodeClipFlag(t *testing.T) {
	e := Encode(sample.Scene(), sample.W, sample.H)
	for i, n := range e.Nodes {
		if n.HasClip == 0 {
			if n.Clip != [4]float32{0, 0, sample.W, sample.H} {
				t.Errorf("node %d unclipped but clip=%v", i, n.Clip)
			}
		}
	}
}
