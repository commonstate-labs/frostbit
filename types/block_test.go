package types

import "testing"

func TestBlockHeaderComputeIDBindsLivenessReports(t *testing.T) {
	base := BlockHeader{Height: 10, ParentID: "parent", TxHash: "1234567890abcdef", Proposer: "miner", Window: 1}
	noReports := base.ComputeID()

	withReports := base
	withReports.SampledReports = []uint32{1, 2}
	withReports.OfflineReports = []uint32{2}
	if got := withReports.ComputeID(); got == noReports {
		t.Fatalf("block ID should change when liveness reports change: %s", got)
	}

	reordered := base
	reordered.SampledReports = []uint32{2, 1}
	reordered.OfflineReports = []uint32{2}
	if got, wantNot := reordered.ComputeID(), withReports.ComputeID(); got == wantNot {
		t.Fatalf("block ID should bind exact report order: %s", got)
	}
}

func TestBlockHeaderComputeIDBindsFullHeaderAndChain(t *testing.T) {
	base := BlockHeader{
		Height: 10, ParentID: "parent", Timestamp: 123, StateRoot: string(make([]byte, 32)),
		TxHash: "1234567890abcdef", Proposer: "miner", Miner: "v1_node_miner", Window: 1,
		VRFProof: []byte{1}, VRFOutput: []byte{2}, PublicKey: []byte{3},
	}
	baseID := base.ComputeIDForChain(7)

	mutations := []struct {
		name string
		edit func(*BlockHeader)
	}{
		{name: "parent", edit: func(h *BlockHeader) { h.ParentID = "other-parent" }},
		{name: "state root", edit: func(h *BlockHeader) { h.StateRoot = string([]byte{1, 2, 3}) }},
		{name: "full tx hash suffix", edit: func(h *BlockHeader) { h.TxHash = "1234567890abcdee" }},
		{name: "vrf proof", edit: func(h *BlockHeader) { h.VRFProof = []byte{9} }},
		{name: "public key", edit: func(h *BlockHeader) { h.PublicKey = []byte{9} }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			changed := base
			mutation.edit(&changed)
			if got := changed.ComputeIDForChain(7); got == baseID {
				t.Fatalf("ID did not change after %s mutation", mutation.name)
			}
		})
	}
	if got := base.ComputeIDForChain(8); got == baseID {
		t.Fatal("ID should bind chain ID")
	}
}
