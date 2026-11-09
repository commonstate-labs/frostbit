package keys

import "testing"

func TestRechargeRewardOwnerIsPrunableQueryHistory(t *testing.T) {
	first := KeyRechargeRewardOwner("fbc1owner", "request-1")
	second := KeyRechargeRewardOwner("fbc1owner", "request-2")
	if first == second || first == KeyRechargeRewardOwner("fbc1other", "request-1") {
		t.Fatal("owner and request must both distinguish reward attributions")
	}
	if prefix := KeyRechargeRewardOwnerPrefix("fbc1owner"); first[:len(prefix)] != prefix || second[:len(prefix)] != prefix {
		t.Fatal("owner prefix must enumerate its reward attributions")
	}
	if IsStatefulKey(first) || StorageClassForKey(first) != StorageQuery || !IsPrunableQueryHistoryKey(first) {
		t.Fatalf("reward attribution must be prunable query history outside consensus state: %s", first)
	}
}
