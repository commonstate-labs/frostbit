package db

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"dex/config"
	"dex/keys"
	"dex/logs"
	"dex/pb"

	"github.com/cockroachdb/pebble"
)

func TestEnqueueQueryPruneManifestPreservesInputOrder(t *testing.T) {
	mgr := newQueryPruneTestManager(t)
	oldTarget := queryPruneManifestTargetBytes
	queryPruneManifestTargetBytes = 96
	t.Cleanup(func() { queryPruneManifestTargetBytes = oldTarget })

	queryKeys := []string{
		keys.KeyUserActivity("z", 7, 0),
		keys.KeyUserActivity("a", 7, 0),
		keys.KeyUserActivity("a", 7, 0),
	}
	receipts := []string{"tx-z", "tx-a"}
	mgr.EnqueueQueryPruneManifest(7, queryKeys, receipts, []string{receipts[1]})
	if err := mgr.ForceFlush(); err != nil {
		t.Fatalf("force flush: %v", err)
	}

	chunkCount, found, err := queryPruneManifestChunkCount(mgr.queryDB, 7)
	if err != nil || !found || chunkCount < 2 {
		t.Fatalf("manifest chunks=%d found=%t err=%v", chunkCount, found, err)
	}
	got := make([]string, 0, len(queryKeys)+len(receipts))
	for chunk := uint32(0); chunk < chunkCount; chunk++ {
		raw, found, err := queryPruneManifestChunk(mgr.queryDB, 7, chunk)
		if err != nil || !found {
			t.Fatalf("read chunk %d found=%t err=%v", chunk, found, err)
		}
		if err := visitQueryPruneManifest(raw, func(kind byte, value string) error {
			got = append(got, fmt.Sprintf("%d:%s", kind, value))
			return nil
		}); err != nil {
			t.Fatalf("decode chunk %d: %v", chunk, err)
		}
	}
	want := []string{
		fmt.Sprintf("%d:%s", queryPruneManifestRecordKey, queryKeys[0]),
		fmt.Sprintf("%d:%s", queryPruneManifestRecordKey, queryKeys[1]),
		fmt.Sprintf("%d:%s", queryPruneManifestRecordKey, queryKeys[2]),
		fmt.Sprintf("%d:%s", queryPruneManifestRecordReceipt, receipts[0]),
		fmt.Sprintf("%d:%s", queryPruneManifestRecordNonceReceipt, receipts[1]),
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("manifest records\ngot:  %v\nwant: %v", got, want)
	}
}

func TestQueryPruneKeyFamilyCoversPrunableHistoryContract(t *testing.T) {
	tests := []struct {
		name   string
		key    string
		family string
	}{
		{name: "user activity", key: keys.KeyUserActivity("alice", 1, 0), family: "user_act"},
		{name: "spot account order", key: keys.KeySpotAccountOrder("alice", "BTC_USDT", pb.OrderStateStatus_ORDER_FILLED, 1, "spot-1"), family: "spot_acc_order"},
		{name: "future account order", key: keys.KeyFutureAccountOrder("alice", "BTC_USDT", pb.FutureOrderStatus_FUTURE_ORDER_FILLED, 1, "future-1"), family: "future_acc_order"},
		{name: "terminal recharge", key: keys.KeyRechargeQueryRecentGlobal(true, 1, "recharge-1"), family: "recharge_query_recent"},
		{name: "frost withdraw global", key: keys.KeyFrostWithdrawQueryGlobal(1, 1, "withdraw-1"), family: "frost_withdraw_query"},
		{name: "frost withdraw chain", key: keys.KeyFrostWithdrawQueryChain("btc", 1, 1, "withdraw-1"), family: "frost_withdraw_query"},
		{name: "frost withdraw chain vault", key: keys.KeyFrostWithdrawQueryChainVault("btc", 7, 1, 1, "withdraw-1"), family: "frost_withdraw_query"},
		{name: "spot trade", key: keys.KeyTradeRecord("BTC_USDT", tradePruneTestTS(1, 0), "trade-1"), family: "trade"},
		{name: "future trade", key: keys.KeyFutureTradeRecord("BTC_USDT", tradePruneTestTS(1, 0), "future-trade-1"), family: "future_trade"},
		{name: "future position", key: keys.KeyFuturePositionHistory("position-1"), family: "future_pos_history"},
		{name: "trading fee", key: keys.KeyTradingFeeRecord("alice", 1, "tx-1", 0), family: "trading_fee"},
		{name: "referral rebate", key: keys.KeyReferralRebateRecord("referrer", 1, "tx-1", 0), family: "referral_rebate_record"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !keys.IsPrunableQueryHistoryKey(tt.key) {
				t.Fatalf("key %q must participate in query retention", tt.key)
			}
			family, prefix, ok := queryPruneKeyFamily(tt.key)
			if !ok || prefix == "" || family != tt.family {
				t.Fatalf("query prune family=(%q, %q, %t), want family %q", family, prefix, ok, tt.family)
			}
		})
	}
}

func TestQueryPruneDeletesEveryKeyInArchiveAlignedHeights(t *testing.T) {
	mgr := newQueryPruneTestManager(t)
	oldTarget := queryPruneManifestTargetBytes
	queryPruneManifestTargetBytes = 256
	t.Cleanup(func() { queryPruneManifestTargetBytes = oldTarget })

	height1Keys := make([]string, 0, 250)
	for i := 0; i < 250; i++ {
		key := keys.KeyUserActivity("alice", 1, uint32(i))
		height1Keys = append(height1Keys, key)
		mgr.EnqueueSet(key, "activity")
	}
	receiptID := "receipt-height-1"
	protocolReceiptID := "protocol-receipt-height-1"
	for _, key := range []string{
		keys.KeyVMTxHeight(receiptID),
		keys.KeyVMTxFee(receiptID),
		keys.KeyVMTxFBBalance(receiptID),
		keys.KeyVMTxError(receiptID),
		keys.KeyTransferHistory(receiptID),
	} {
		mgr.EnqueueSet(key, "value")
	}
	mgr.EnqueueSet(keys.KeyVMAppliedTx(receiptID), "SUCCEED")
	mgr.EnqueueSet(keys.KeyVMAppliedTx(protocolReceiptID), "SUCCEED")
	mgr.EnqueueQueryPruneManifest(1, height1Keys, []string{receiptID, protocolReceiptID}, []string{receiptID})
	mgr.EnqueueSet(keys.KeyBlockReward(1), "reward-1")

	height2Keys := []string{
		keys.KeyFutureAccountOrder("alice", "BTC_USDT", pb.FutureOrderStatus_FUTURE_ORDER_FILLED, 1, "order-1"),
		keys.KeyFuturePositionHistory("position-2"),
		keys.KeyFutureTradeRecord("BTC_USDT", tradePruneTestTS(2, 0), "trade-2"),
		keys.KeyReferralRebateRecord("referrer", 2, "trade-2", 0),
		keys.KeyRechargeQueryRecentGlobal(true, 2, "recharge-2"),
		keys.KeyFrostWithdrawQueryGlobal(2, 1, "withdraw-2"),
		keys.KeyFrostWithdrawQueryChain("btc", 2, 1, "withdraw-2"),
		keys.KeyFrostWithdrawQueryChainVault("btc", 7, 2, 1, "withdraw-2"),
		keys.KeyUserActivityKindIndex("alice", "FUNDING_SETTLE", 2, "tx-2", 0),
	}
	for _, key := range height2Keys {
		mgr.EnqueueSet(key, "history")
	}
	mgr.EnqueueQueryPruneManifest(2, height2Keys, nil, nil)
	mgr.EnqueueSet(keys.KeyBlockReward(2), "reward-2")

	recent := keys.KeyUserActivity("alice", 3, 0)
	mgr.EnqueueSet(recent, "recent")
	mgr.EnqueueQueryPruneManifest(3, []string{recent}, nil, nil)
	mgr.EnqueueSet(keys.KeyBlockReward(3), "reward-3")
	if err := mgr.setBlockPruneCursor(3); err != nil {
		t.Fatalf("set archive cursor: %v", err)
	}
	if err := mgr.ForceFlush(); err != nil {
		t.Fatalf("force flush: %v", err)
	}

	res, err := mgr.PruneQueryDataUnderDiskPressure(20)
	if err != nil {
		t.Fatalf("query prune: %v", err)
	}
	if res.StartCursor != 1 || res.NextCursor != 3 || res.ScannedHeights != 2 || res.PrunedHeights != 2 || !res.ArchiveAligned {
		t.Fatalf("unexpected query prune result: %+v", res)
	}
	if res.DeletedKeys != uint64(len(height1Keys)+len(height2Keys)+13) {
		t.Fatalf("deleted keys=%d, want %d", res.DeletedKeys, len(height1Keys)+len(height2Keys)+13)
	}
	if got := res.DeletedByFamily["block_reward"]; got != 2 {
		t.Fatalf("deleted block rewards=%d, want 2", got)
	}
	if got := res.DeletedByFamily["frost_withdraw_query"]; got != 3 {
		t.Fatalf("deleted frost withdraw query keys=%d, want 3", got)
	}
	for _, key := range append(append([]string{}, height1Keys...), height2Keys...) {
		mustNotExistPebbleKey(t, mgr.queryDB, key)
	}
	mustNotExistPebbleKey(t, mgr.runtimeDB, keys.KeyVMAppliedTx(receiptID))
	if _, err := mgr.GetTxReceipt(receiptID); err == nil {
		t.Fatal("pruned nonce receipt should no longer be queryable")
	}
	if got := mustGetPebbleValue(t, mgr.runtimeDB, keys.KeyVMAppliedTx(protocolReceiptID)); got != "SUCCEED" {
		t.Fatalf("protocol applied marker=%q, want SUCCEED", got)
	}
	for _, key := range []string{
		keys.KeyVMTxHeight(receiptID),
		keys.KeyVMTxFee(receiptID),
		keys.KeyVMTxFBBalance(receiptID),
		keys.KeyVMTxError(receiptID),
		keys.KeyTransferHistory(receiptID),
	} {
		mustNotExistPebbleKey(t, mgr.queryDB, key)
	}
	if got := mustGetPebbleValue(t, mgr.queryDB, recent); got != "recent" {
		t.Fatalf("recent history=%q", got)
	}
	for _, height := range []uint64{1, 2} {
		mustNotExistPebbleKey(t, mgr.queryDB, keys.KeyBlockReward(height))
	}
	if got := mustGetPebbleValue(t, mgr.queryDB, keys.KeyBlockReward(3)); got != "reward-3" {
		t.Fatalf("recent block reward=%q, want reward-3", got)
	}
	mustNotExistPebbleKey(t, mgr.queryDB, keys.KeyQueryPruneManifestDone(1))
	mustNotExistPebbleKey(t, mgr.queryDB, keys.KeyQueryPruneManifestDone(2))
	if got := mustGetPebbleValue(t, mgr.queryDB, keys.KeyQueryPruneManifestDone(3)); got == "" {
		t.Fatal("height 3 manifest should remain")
	}
	if got := mustGetPebbleValue(t, mgr.runtimeDB, keys.KeyQueryPruneHeightCursor()); got != "3" {
		t.Fatalf("query height cursor=%q, want 3", got)
	}
}

func TestQueryPruneRetriesHeightAfterCompletedChunk(t *testing.T) {
	mgr := newQueryPruneTestManager(t)
	oldTarget := queryPruneManifestTargetBytes
	queryPruneManifestTargetBytes = 64
	t.Cleanup(func() { queryPruneManifestTargetBytes = oldTarget })

	queryKeys := []string{
		keys.KeyUserActivity("alice-long-address", 1, 0),
		keys.KeyUserActivity("bob-long-address", 1, 0),
	}
	for _, key := range queryKeys {
		mgr.EnqueueSet(key, "activity")
	}
	mgr.EnqueueQueryPruneManifest(1, queryKeys, nil, nil)
	if err := mgr.setBlockPruneCursor(2); err != nil {
		t.Fatalf("set archive cursor: %v", err)
	}
	if err := mgr.ForceFlush(); err != nil {
		t.Fatalf("force flush: %v", err)
	}
	chunkCount, found, err := queryPruneManifestChunkCount(mgr.queryDB, 1)
	if err != nil || !found || chunkCount < 2 {
		t.Fatalf("manifest chunks=%d found=%t err=%v", chunkCount, found, err)
	}

	batch := mgr.queryDB.NewBatch()
	if err := batch.Delete([]byte(queryKeys[0]), pebble.NoSync); err != nil {
		t.Fatalf("delete first data key: %v", err)
	}
	if err := batch.Delete([]byte(keys.KeyQueryPruneManifestChunk(1, 0)), pebble.NoSync); err != nil {
		t.Fatalf("delete completed chunk: %v", err)
	}
	if err := batch.Commit(pebble.NoSync); err != nil {
		t.Fatalf("commit simulated partial prune: %v", err)
	}
	if err := batch.Close(); err != nil {
		t.Fatalf("close simulated partial prune batch: %v", err)
	}

	res, err := mgr.PruneQueryDataUnderDiskPressure(11)
	if err != nil {
		t.Fatalf("retry query prune: %v", err)
	}
	if !res.ArchiveAligned || res.NextCursor != 2 || res.PrunedHeights != 1 {
		t.Fatalf("retry result: %+v", res)
	}
	for _, key := range queryKeys {
		mustNotExistPebbleKey(t, mgr.queryDB, key)
	}
	mustNotExistPebbleKey(t, mgr.queryDB, keys.KeyQueryPruneManifestDone(1))
}

func TestFreshStateSyncInitializesQueryPruneCursor(t *testing.T) {
	mgr := newQueryPruneTestManager(t)
	if err := mgr.recordQueryPruneStateSync(0, 3400); err != nil {
		t.Fatalf("record fresh state sync: %v", err)
	}
	cursor, found, err := mgr.queryPruneHeightCursor()
	if err != nil || !found || cursor != 3401 {
		t.Fatalf("query prune cursor=%d found=%t err=%v, want 3401", cursor, found, err)
	}
	mustNotExistPebbleKey(t, mgr.runtimeDB, keys.KeyQueryPruneSyncGap(1))
}

func TestQueryPruneSkipsRecordedStateSyncGap(t *testing.T) {
	mgr := newQueryPruneTestManager(t)
	height1Key := keys.KeyUserActivity("alice", 1, 0)
	height5Key := keys.KeyUserActivity("alice", 5, 0)
	for _, key := range []string{height1Key, height5Key} {
		mgr.EnqueueSet(key, "activity")
	}
	mgr.EnqueueQueryPruneManifest(1, []string{height1Key}, nil, nil)
	mgr.EnqueueQueryPruneManifest(5, []string{height5Key}, nil, nil)
	if err := mgr.recordQueryPruneStateSync(1, 4); err != nil {
		t.Fatalf("record state sync gap: %v", err)
	}
	if err := mgr.setBlockPruneCursor(6); err != nil {
		t.Fatalf("set archive cursor: %v", err)
	}
	if err := mgr.ForceFlush(); err != nil {
		t.Fatalf("force flush: %v", err)
	}

	res, err := mgr.PruneQueryDataUnderDiskPressure(20)
	if err != nil {
		t.Fatalf("query prune: %v", err)
	}
	if res.StartCursor != 1 || res.NextCursor != 6 || res.PrunedHeights != 2 || res.SkippedHeights != 3 || !res.ArchiveAligned {
		t.Fatalf("unexpected query prune result: %+v", res)
	}
	mustNotExistPebbleKey(t, mgr.queryDB, height1Key)
	mustNotExistPebbleKey(t, mgr.queryDB, height5Key)
	mustNotExistPebbleKey(t, mgr.runtimeDB, keys.KeyQueryPruneSyncGap(2))
}

func TestQueryPruneQuarantinesMissingManifestAndContinues(t *testing.T) {
	mgr := newQueryPruneTestManager(t)
	height1Key := keys.KeyUserActivity("alice", 1, 0)
	height2Key := keys.KeyUserActivity("alice", 2, 0)
	height3Key := keys.KeyUserActivity("alice", 3, 0)
	mgr.EnqueueSet(height1Key, "height-1")
	mgr.EnqueueSet(height2Key, "height-2")
	mgr.EnqueueSet(height3Key, "height-3")
	mgr.EnqueueQueryPruneManifest(1, []string{height1Key}, nil, nil)
	                                                                           
	                                                             
	mgr.EnqueueQueryPruneManifest(3, []string{height3Key}, nil, nil)
	if err := mgr.setBlockPruneCursor(4); err != nil {
		t.Fatalf("set archive cursor: %v", err)
	}
	if err := mgr.ForceFlush(); err != nil {
		t.Fatalf("force flush: %v", err)
	}

	res, err := mgr.PruneQueryDataUnderDiskPressure(20)
	if err != nil {
		t.Fatalf("query prune: %v", err)
	}
	if res.NextCursor != 4 || res.PrunedHeights != 2 || res.SkippedHeights != 1 || !res.ArchiveAligned {
		t.Fatalf("missing manifest result: %+v", res)
	}
	mustNotExistPebbleKey(t, mgr.queryDB, height1Key)
	mustNotExistPebbleKey(t, mgr.queryDB, height3Key)
	if got := mustGetPebbleValue(t, mgr.queryDB, height2Key); got != "height-2" {
		t.Fatalf("height 2 history=%q", got)
	}
	if got := mustGetPebbleValue(t, mgr.runtimeDB, keys.KeyQueryPruneHeightCursor()); got != "4" {
		t.Fatalf("query height cursor=%q, want 4", got)
	}
	gapKey := keys.KeyQueryPruneSyncGap(2)
	wantGap := queryPruneMissingManifestGapPrefix + "3"
	if got := mustGetPebbleValue(t, mgr.runtimeDB, gapKey); got != wantGap {
		t.Fatalf("query recovery gap=%q, want %q", got, wantGap)
	}
	gap, found, err := mgr.queryPruneSyncGap(2)
	if err != nil || !found || gap.resumeHeight != 3 || !gap.persistent {
		t.Fatalf("query recovery gap=%+v found=%t err=%v", gap, found, err)
	}

	                                                              
	res, err = mgr.PruneQueryDataUnderDiskPressure(21)
	if err != nil {
		t.Fatalf("repeat query prune: %v", err)
	}
	if !res.ArchiveAligned || res.StartCursor != 4 || res.NextCursor != 4 {
		t.Fatalf("repeat query prune result: %+v", res)
	}
	if got := mustGetPebbleValue(t, mgr.runtimeDB, gapKey); got != wantGap {
		t.Fatalf("query recovery gap after repeat=%q, want %q", got, wantGap)
	}
}

func TestQueryPruneLimitsNewRecoveryGapsPerRun(t *testing.T) {
	mgr := newQueryPruneTestManager(t)
	height1Key := keys.KeyUserActivity("alice", 1, 0)
	height2Key := keys.KeyUserActivity("alice", 2, 0)
	height3Key := keys.KeyUserActivity("alice", 3, 0)
	for height, key := range map[uint64]string{1: height1Key, 2: height2Key, 3: height3Key} {
		mgr.EnqueueSet(key, fmt.Sprintf("height-%d", height))
	}
	                                                                        
	                                                                       
	mgr.EnqueueQueryPruneManifest(2, []string{height2Key}, nil, nil)
	if err := mgr.setBlockPruneCursor(4); err != nil {
		t.Fatalf("set archive cursor: %v", err)
	}
	if err := mgr.ForceFlush(); err != nil {
		t.Fatalf("force flush: %v", err)
	}

	                                                                        
	                                                                
	res, err := mgr.pruneQueryDataThroughHeight(20, 3, true)
	if err != nil {
		t.Fatalf("query prune: %v", err)
	}
	if res.NextCursor != 3 || res.PrunedHeights != 1 || res.SkippedHeights != 1 || res.ArchiveAligned {
		t.Fatalf("query prune result: %+v", res)
	}
	compactedUserActivity := false
	for _, family := range res.CompactedFamilies {
		if family == "user_act" {
			compactedUserActivity = true
			break
		}
	}
	if !compactedUserActivity {
		t.Fatalf("compacted families=%v, want user_act", res.CompactedFamilies)
	}
	if got := mustGetPebbleValue(t, mgr.queryDB, height1Key); got != "height-1" {
		t.Fatalf("quarantined height 1 history=%q", got)
	}
	mustNotExistPebbleKey(t, mgr.queryDB, height2Key)
	if got := mustGetPebbleValue(t, mgr.queryDB, height3Key); got != "height-3" {
		t.Fatalf("stopped height 3 history=%q", got)
	}
	if got := mustGetPebbleValue(t, mgr.runtimeDB, keys.KeyQueryPruneHeightCursor()); got != "3" {
		t.Fatalf("query height cursor=%q, want 3", got)
	}
	if got := mustGetPebbleValue(t, mgr.runtimeDB, keys.KeyQueryPruneSyncGap(1)); got != queryPruneMissingManifestGapPrefix+"2" {
		t.Fatalf("height 1 recovery gap=%q", got)
	}
	mustNotExistPebbleKey(t, mgr.runtimeDB, keys.KeyQueryPruneSyncGap(3))
}

func newQueryPruneTestManager(t *testing.T) *Manager {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Database.BlockPruneKeepRecentHeights = 10
	cfg.Database.BlockPruneStartFreePercent = 100
	cfg.Database.BlockPruneAggressiveFreePercent = 1
	mgr, err := NewManagerWithConfig(t.TempDir(), logs.NewNodeLogger("test", 0), cfg)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	mgr.InitWriteQueue(10_000, time.Hour)
	t.Cleanup(func() { mgr.Close() })
	return mgr
}

func tradePruneTestTS(height uint64, seq int64) int64 {
	return int64(height)*1_000_000 + seq
}
