package txpool

import (
	"dex/keys"
	"testing"
	"time"
)

func TestIsTxApplied_BloomMissSkipsDB(t *testing.T) {
	tp := newTestTxPool(t)
	tp.appliedBloom.finishRebuild(tp.appliedBloom.newFilter())

	txID := "applied-but-not-in-bloom"
	if err := tp.dbManager.Db.Set([]byte(keys.KeyVMAppliedTx(txID)), []byte("SUCCEED"), nil); err != nil {
		t.Fatalf("Set applied tx: %v", err)
	}

	if tp.isTxApplied(txID) {
		t.Fatal("Bloom miss must skip DB lookup on the txpool hot path")
	}
}

func TestIsTxApplied_BloomHitRequiresDBConfirmation(t *testing.T) {
	tp := newTestTxPool(t)
	filter := tp.appliedBloom.newFilter()
	txID := "bloom-hit-without-db"
	filter.AddString(txID)
	tp.appliedBloom.finishRebuild(filter)

	if tp.isTxApplied(txID) {
		t.Fatal("Bloom hit alone must not mark tx as applied")
	}

	appliedID := "bloom-hit-with-db"
	tp.appliedBloom.add(appliedID)
	if err := tp.dbManager.Db.Set([]byte(keys.KeyVMAppliedTx(appliedID)), []byte("SUCCEED"), nil); err != nil {
		t.Fatalf("Set applied tx: %v", err)
	}
	if !tp.isTxApplied(appliedID) {
		t.Fatal("Bloom hit with DB status must mark tx as applied")
	}
}

func TestRemoveAppliedAnyTxsMarksAppliedInMemory(t *testing.T) {
	tp := newTestTxPool(t)
	tp.appliedBloom.finishRebuild(tp.appliedBloom.newFilter())
	txID := "finalized-tx"

	tp.mu.Lock()
	tp.cacheAnyTxUnlocked(txID, txWithID(txID), true)
	tp.mu.Unlock()

	tp.dbManager = nil
	tp.RemoveAppliedAnyTxs([]string{txID})
	if tp.HasTransaction(txID) {
		t.Fatal("finalized tx must be removed from pending cache")
	}
	if !tp.isTxApplied(txID) {
		t.Fatal("finalized tx must be marked applied in memory")
	}
}

func TestAppliedBloomRebuildLoadsLocalAppliedKV(t *testing.T) {
	tp := newTestTxPool(t)
	txID := "local-applied"
	if err := tp.dbManager.Db.Set([]byte(keys.KeyVMAppliedTx(txID)), []byte("SUCCEED"), nil); err != nil {
		t.Fatalf("Set applied tx: %v", err)
	}

	tp.startAppliedBloomRebuild()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ready, might := tp.appliedBloom.mightContain(txID)
		if ready {
			if !might {
				t.Fatal("rebuilt Bloom must contain local applied tx")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Bloom rebuild did not finish")
}
