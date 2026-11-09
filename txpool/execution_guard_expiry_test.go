package txpool

import (
	"errors"
	"testing"
	"time"

	"dex/keys"
	"dex/pb"
	"google.golang.org/protobuf/proto"
	"hash/fnv"
)

func newExecutionGuardTestPool(t *testing.T) *TxPool {
	t.Helper()
	tp := newTestTxPool(t)
	tp.dbManager.InitWriteQueue(100, time.Millisecond)
	raw, err := proto.Marshal(&pb.TokenBalanceRecord{Balance: &pb.TokenBalance{Balance: "1000000000"}})
	if err != nil {
		t.Fatal(err)
	}
	tp.dbManager.EnqueueSet(keys.KeyBalance("account-a", "FB"), string(raw))
	if err := tp.dbManager.ForceFlushHot(); err != nil {
		t.Fatal(err)
	}
	return tp
}

func guardedTransferForPool(id string, target uint64) *pb.AnyTx {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return &pb.AnyTx{Content: &pb.AnyTx_Transaction{Transaction: &pb.Transaction{
		Base: &pb.BaseMessage{
			TxId:        id,
			FromAddress: "account-a",
			Fee:         "1000", Nonce: uint64(h.Sum32() % 2048),
			TargetHeight: target,
		},
	}}}
}

func TestRemoveExpiredExecutionGuardTxs(t *testing.T) {
	tp := newExecutionGuardTestPool(t)
	txs := []*pb.AnyTx{
		guardedTransferForPool("expired-before", 8),
		guardedTransferForPool("expired-at", 9),
		guardedTransferForPool("future", 10),
		guardedTransferForPool("legacy", 0),
		{Content: &pb.AnyTx_CertSignTx{CertSignTx: &pb.CertSignTx{Base: &pb.BaseMessage{
			TxId: "protocol", TargetHeight: 1, FromAddress: "account-a", Fee: "1000",
		}}}},
	}
	for _, tx := range txs {
		if err := tp.StoreAnyTx(tx); err != nil {
			t.Fatalf("StoreAnyTx(%s): %v", tx.GetTxId(), err)
		}
	}

	if got := tp.RemoveExpiredExecutionGuardTxs(9); got != 2 {
		t.Fatalf("removed=%d, want 2", got)
	}
	for _, id := range []string{"expired-before", "expired-at"} {
		if tp.HasTransaction(id) {
			t.Fatalf("expired transaction %s remains pending", id)
		}
	}
	for _, id := range []string{"future", "legacy", "protocol"} {
		if !tp.HasTransaction(id) {
			t.Fatalf("non-expired transaction %s was removed", id)
		}
	}
}

func TestLatePendingSaveDoesNotRestoreRemovedGuardedTx(t *testing.T) {
	tp := newExecutionGuardTestPool(t)
	tx := guardedTransferForPool("late-save", 7)
	if err := tp.StoreAnyTx(tx); err != nil {
		t.Fatalf("StoreAnyTx: %v", err)
	}
	if got := tp.RemoveExpiredExecutionGuardTxs(7); got != 1 {
		t.Fatalf("removed=%d, want 1", got)
	}

	                                                                       
	                                                                    
	if err := tp.savePendingAnyTxIfPresent(tx); err != nil {
		t.Fatalf("late save: %v", err)
	}
	if tp.HasTransaction(tx.GetTxId()) {
		t.Fatal("late save restored removed transaction in memory")
	}
	if err := tp.dbManager.ForceFlush(); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}
	persisted, err := tp.dbManager.LoadPendingAnyTx()
	if err != nil {
		t.Fatalf("LoadPendingAnyTx: %v", err)
	}
	for _, pending := range persisted {
		if pending != nil && pending.GetTxId() == tx.GetTxId() {
			t.Fatal("late save restored removed transaction on disk")
		}
	}
}

func TestExpiredExecutionGuardCannotReenterPool(t *testing.T) {
	tp := newExecutionGuardTestPool(t)
	tp.RemoveExpiredExecutionGuardTxs(12)
	tx := guardedTransferForPool("late-candidate", 12)
	if err := tp.StoreAnyTx(tx); !errors.Is(err, ErrExecutionGuardExpired) {
		t.Fatalf("StoreAnyTx error=%v, want ErrExecutionGuardExpired", err)
	}
	if tp.HasTransaction(tx.GetTxId()) {
		t.Fatal("expired guarded transaction reentered memory pool")
	}
	if err := tp.savePendingAnyTxIfPresent(tx); err != nil {
		t.Fatalf("late persistence attempt: %v", err)
	}
	if err := tp.dbManager.ForceFlush(); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}
	persisted, err := tp.dbManager.LoadPendingAnyTx()
	if err != nil {
		t.Fatalf("LoadPendingAnyTx: %v", err)
	}
	for _, pending := range persisted {
		if pending != nil && pending.GetTxId() == tx.GetTxId() {
			t.Fatal("expired guarded transaction reentered persistent pool")
		}
	}
}

func TestExecutionGuardExpiryCleansTransactionsEvictedFromPendingLRU(t *testing.T) {
	tp := newExecutionGuardTestPool(t)
	tp.pendingAnyTxCache.Resize(1)
	tp.shortPendingAnyTxCache.Resize(1)
	first := guardedTransferForPool("0x1111111111111111-first", 20)
	second := guardedTransferForPool("0x2222222222222222-second", 20)
	for _, tx := range []*pb.AnyTx{first, second} {
		if err := tp.StoreAnyTx(tx); err != nil {
			t.Fatalf("StoreAnyTx(%s): %v", tx.GetTxId(), err)
		}
	}
	if tp.HasTransaction(first.GetTxId()) {
		t.Fatal("test setup did not evict the first guarded transaction")
	}
	if err := tp.dbManager.ForceFlush(); err != nil {
		t.Fatalf("ForceFlush before expiry: %v", err)
	}

	if got := tp.RemoveExpiredExecutionGuardTxs(20); got != 2 {
		t.Fatalf("removed=%d, want both tracked guarded transactions", got)
	}
	if err := tp.dbManager.ForceFlush(); err != nil {
		t.Fatalf("ForceFlush after expiry: %v", err)
	}
	persisted, err := tp.dbManager.LoadPendingAnyTx()
	if err != nil {
		t.Fatalf("LoadPendingAnyTx: %v", err)
	}
	for _, pending := range persisted {
		if pending != nil && (pending.GetTxId() == first.GetTxId() || pending.GetTxId() == second.GetTxId()) {
			t.Fatalf("expired LRU-evicted transaction remains on disk: %s", pending.GetTxId())
		}
	}
}

func TestReconcileFinalizedHeightUsesRecoveredTipInsteadOfStaleMetadata(t *testing.T) {
	tp := newExecutionGuardTestPool(t)
	                                                                          
	                                                                             
	                                                                           
	tp.finalizedHeight.Store(100)
	tp.ReconcileFinalizedHeight(5)

	if err := tp.StoreAnyTx(guardedTransferForPool("future-after-reconcile", 10)); err != nil {
		t.Fatalf("future transaction was rejected by stale startup watermark: %v", err)
	}
	if err := tp.StoreAnyTx(guardedTransferForPool("expired-after-reconcile", 5)); !errors.Is(err, ErrExecutionGuardExpired) {
		t.Fatalf("recovered-height expired tx error=%v, want ErrExecutionGuardExpired", err)
	}
}

func TestPendingLimitCannotExceedPendingLRUCapacity(t *testing.T) {
	tp := newExecutionGuardTestPool(t)
	tp.cfg.TxPool.PendingTxCacheSize = 1
	tp.cfg.TxPool.MaxPendingTxs = 100
	tp.pendingAnyTxCache.Resize(1)
	tp.shortPendingAnyTxCache.Resize(1)

	first := guardedTransferForPool("0x1111111111111111-bounded", 100)
	second := guardedTransferForPool("0x2222222222222222-bounded", 100)
	if err := tp.StoreAnyTx(first); err != nil {
		t.Fatalf("StoreAnyTx(first): %v", err)
	}
	if err := tp.StoreAnyTx(second); err == nil {
		t.Fatal("second transaction bypassed effective pending/LRU capacity")
	}
	if !tp.HasTransaction(first.GetTxId()) {
		t.Fatal("capacity check allowed LRU eviction of the tracked first tx")
	}
	if _, tracked := tp.executionGuardByTxID[second.GetTxId()]; tracked {
		t.Fatal("rejected transaction leaked into guard expiry index")
	}
}
