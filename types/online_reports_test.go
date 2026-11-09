package types

import "testing"

func TestOnlineReportsPreserveHistoricalCommitmentWhenEmpty(t *testing.T) {
	h := BlockHeader{
		Height: 10, ParentID: "parent", Timestamp: 123, StateRoot: "root",
		TxHash: "1234567890abcdef", Proposer: "miner", Miner: "v1_node_miner", Window: 1,
		VRFProof: []byte{1}, VRFOutput: []byte{2}, PublicKey: []byte{3},
		OfflineReports: []uint32{2}, SampledReports: []uint32{1, 2},
	}
	                                                                          
	const oldID = "block-10-miner-w1-123fe770ed522870f473f501bea9b88745ffc22b050b3d50f3ec05b7f5020e57"
	if got := h.ComputeIDForChain(7); got != oldID {
		t.Fatalf("nil online changed historical commitment: got %s want %s", got, oldID)
	}
	h.OnlineReports = []uint32{}
	if got := h.ComputeIDForChain(7); got != oldID {
		t.Fatalf("empty online changed historical commitment: got %s want %s", got, oldID)
	}
	if got := LivenessReportsTag(h.SampledReports, h.OfflineReports); got != "f4e6d4df" {
		t.Fatalf("legacy report tag changed: %s", got)
	}
	if got := LivenessReportsTag(h.SampledReports, h.OfflineReports, h.OnlineReports); got != "f4e6d4df" {
		t.Fatalf("empty online changed legacy report tag: %s", got)
	}
}

func TestOnlineReportsAreBoundToBlockCommitmentAndTag(t *testing.T) {
	h := BlockHeader{Height: 10, ParentID: "parent", Proposer: "miner", SampledReports: []uint32{1, 2, 3}}
	withoutOnline := h.ComputeIDForChain(7)
	oldTag := LivenessReportsTag(h.SampledReports, nil)
	h.OnlineReports = []uint32{1, 3}
	withOnline := h.ComputeIDForChain(7)
	tag := LivenessReportsTag(h.SampledReports, nil, h.OnlineReports)
	if withOnline == withoutOnline || tag == oldTag {
		t.Fatal("nonempty online reports are not committed")
	}
	for _, changed := range [][]uint32{{1}, {1, 2}, {3, 1}} {
		h.OnlineReports = changed
		if h.ComputeIDForChain(7) == withOnline || LivenessReportsTag(h.SampledReports, nil, changed) == tag {
			t.Fatalf("online content/order mutation did not change commitment: %v", changed)
		}
	}
}
