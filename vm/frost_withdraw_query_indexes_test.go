package vm

import (
	"testing"

	"dex/keys"
)

func TestWithdrawCancelQueryLifetimeStartsAtCancellationNotLaterFIFORelease(t *testing.T) {
	base := map[string][]byte{
		keys.KeyFrostWithdrawFIFOHead("eth", cancelTestAsset): []byte("1"),
		keys.KeyFrostWithdrawFIFOSeq("eth", cancelTestAsset):  []byte("2"),
	}
	for i, id := range []string{"withdraw-1", "withdraw-2"} {
		seedWithdrawCancelState(t, base, id, "alice", uint64(i+1), WithdrawStatusQueued, "")
		withdraw, _ := unmarshalWithdrawRequest(base[keys.KeyFrostWithdraw(id)])
		for _, key := range frostWithdrawQueryKeys(withdraw, false) {
			base[key] = []byte(id)
		}
	}
	sv := newMemoryStateView(base)
	ops, receipt, err := (&FrostWithdrawCancelTxHandler{}).DryRun(cancelTx("withdraw-2", "alice", "cancel-2", 500), sv)
	if err != nil || receipt.Status != "SUCCEED" {
		t.Fatalf("cancel: %v %+v", err, receipt)
	}
	withdraw, _ := unmarshalWithdrawRequest(base[keys.KeyFrostWithdraw("withdraw-2")])
	wanted := make(map[string]bool)
	for _, key := range frostWithdrawQueryKeys(withdraw, true) {
		wanted[key] = true
		if raw, exists, _ := sv.Get(key); !exists || string(raw) != "withdraw-2" {
			t.Fatalf("missing terminal %s", key)
		}
	}
	for _, key := range frostWithdrawQueryKeys(withdraw, false) {
		if _, exists, _ := sv.Get(key); exists {
			t.Fatalf("active survived cancellation %s", key)
		}
	}
	manifest := collectQueryPruneManifestKeys(ops)
	for _, key := range manifest {
		delete(wanted, key)
	}
	if len(wanted) != 0 {
		t.Fatalf("cancellation block omitted terminal manifest: %v", wanted)
	}
	                                                                          
	                                                                          
	ops, receipt, err = (&FrostWithdrawCancelTxHandler{}).DryRun(cancelTx("withdraw-1", "alice", "cancel-1", 600), sv)
	if err != nil || receipt.Status != "SUCCEED" {
		t.Fatalf("head cancel: %v %+v", err, receipt)
	}
	for _, op := range ops {
		for _, key := range frostWithdrawQueryKeys(withdraw, true) {
			if op.Key == key {
				t.Fatalf("FIFO cleanup refreshed history lifetime: %s", key)
			}
		}
	}
}

func TestWithdrawActiveQueryWritesAreExcludedFromManifest(t *testing.T) {
	withdraw := &FrostWithdrawRequest{WithdrawID: "active", Chain: "btc", VaultID: 2, Seq: 1, RequestHeight: 3}
	var ops []WriteOp
	for _, key := range frostWithdrawQueryKeys(withdraw, false) {
		ops = append(ops, WriteOp{Key: key, Value: []byte("active")})
	}
	if got := collectQueryPruneManifestKeys(ops); len(got) != 0 {
		t.Fatalf("active pointer registered for pruning: %v", got)
	}
	terminal := finalizeFrostWithdrawQueryIndexes(withdraw)
	if got := collectQueryPruneManifestKeys(terminal); len(got) != 3 {
		t.Fatalf("terminal manifest=%v", got)
	}
}
