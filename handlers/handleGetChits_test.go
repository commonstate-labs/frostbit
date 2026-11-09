package handlers

import (
	"testing"

	"dex/types"
)

func TestFinalizationChitEntryExposesEffectiveSampleWeight(t *testing.T) {
	weighted := finalizationChitEntry(types.FinalizationChit{
		NodeID:       "peer-1",
		PreferredID:  "block-1",
		Timestamp:    123,
		SampleWeight: 4,
	})
	if weighted.SampleWeight != 4 {
		t.Fatalf("weighted chit exposed sample weight %d, want 4", weighted.SampleWeight)
	}

	legacy := finalizationChitEntry(types.FinalizationChit{NodeID: "legacy-peer"})
	if legacy.SampleWeight != 1 {
		t.Fatalf("legacy chit exposed sample weight %d, want effective weight 1", legacy.SampleWeight)
	}
}
