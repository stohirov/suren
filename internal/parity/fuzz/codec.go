package fuzz

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Spec round-trips through JSON exactly. encoding/json emits float64 in the
// shortest representation that parses back to the same bits, so a stored
// regression re-materializes the identical scene rather than one that merely
// renders close to it — which matters because these files are gated at Δ=0.
// TestSpecJSONRoundTripIsExact holds this to the render, not just the struct.

func (s Spec) MarshalJSON() ([]byte, error) {
	return json.Marshal(specJSON{Seed: s.Seed, W: s.W, H: s.H, Nodes: s.Nodes})
}

func (s *Spec) UnmarshalJSON(data []byte) error {
	var j specJSON
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&j); err != nil {
		return err
	}
	*s = Spec{Seed: j.Seed, W: j.W, H: j.H, Nodes: j.Nodes}
	return nil
}

// Load decodes a stored spec and validates it. A regression file that has drifted
// out of sync with the spec types must fail loudly here rather than silently
// decode into a scene that no longer reproduces the bug it was recorded for —
// which is the failure mode that makes regression corpora rot.
func Load(data []byte) (Spec, error) {
	var s Spec
	if err := json.Unmarshal(data, &s); err != nil {
		return Spec{}, fmt.Errorf("decode spec: %w", err)
	}
	if err := s.Validate(); err != nil {
		return Spec{}, fmt.Errorf("invalid spec: %w", err)
	}
	return s, nil
}

func (s Spec) Encode() ([]byte, error) {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
