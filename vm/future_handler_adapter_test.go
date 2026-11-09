package vm

import (
	"dex/future"
	"dex/keys"
	"dex/pb"
	"encoding/binary"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
)

type failFuturePositionHistoryGetStateView struct{ StateView }

func (s failFuturePositionHistoryGetStateView) Get(key string) ([]byte, bool, error) {
	if strings.HasPrefix(key, keys.KeyFuturePositionHistoryPrefix()) {
		return nil, false, fmt.Errorf("cold history read is forbidden during consensus execution")
	}
	return s.StateView.Get(key)
}

type failFutureStateGetKeyView struct {
	StateView
	key string
}

func (s failFutureStateGetKeyView) Get(key string) ([]byte, bool, error) {
	if key == s.key {
		return nil, false, fmt.Errorf("injected read failure for %s", key)
	}
	return s.StateView.Get(key)
}

type failNthFutureActivitySeqView struct {
	StateView
	failAt int
	calls  int
}

func (s *failNthFutureActivitySeqView) NextUserActivitySeq(prefix string) (uint32, error) {
	s.calls++
	if s.calls == s.failAt {
		return 0, fmt.Errorf("injected activity sequence failure at call %d", s.calls)
	}
	if allocator, ok := s.StateView.(userActivitySeqAllocator); ok {
		return allocator.NextUserActivitySeq(prefix)
	}
	existing, err := s.StateView.Scan(prefix)
	if err != nil {
		return 0, err
	}
	return uint32(len(existing)), nil
}

func TestEnsureFutureMarketPropagatesConfigReadErrorWithoutScheduling(t *testing.T) {
	base := newMemStateView()
	const pair = "BTC_USDT"
	oldHeight := make([]byte, 8)
	binary.BigEndian.PutUint64(oldHeight, 7)
	base.Set(keys.KeyFutureFundingHeight(pair), oldHeight)
	wrapped := failFutureStateGetKeyView{StateView: base, key: keys.KeyFutureMarketConfig(pair)}

	if err := ensureFutureMarket(wrapped, pair, 99); err == nil {
		t.Fatal("market config read failure was treated as an unregistered market")
	}
	if _, exists, err := base.Get(keys.KeyFutureMarketConfig(pair)); err != nil || exists {
		t.Fatalf("failed market lookup unexpectedly registered market: exists=%v err=%v", exists, err)
	}
	gotHeight, exists, err := base.Get(keys.KeyFutureFundingHeight(pair))
	if err != nil || !exists || binary.BigEndian.Uint64(gotHeight) != 7 {
		t.Fatalf("failed market lookup overwrote funding height: exists=%v height=%x err=%v", exists, gotHeight, err)
	}
	if _, exists, err := base.Get(keys.KeyFutureFundingDue(99+future.DefaultFundingInterval, pair)); err != nil || exists {
		t.Fatalf("failed market lookup scheduled funding: exists=%v err=%v", exists, err)
	}
}

func TestFutureOrderPropagatesUnusedMarginRefundError(t *testing.T) {
	base := newMemStateView()
	const (
		pair  = "BTC_USDT"
		owner = "refund-owner"
	)
	price := big.NewInt(50_000 * 1e8)
	base.Set(keys.KeySpotLastPrice(pair), price.Bytes())
	base.Set(keys.KeyFutureMarkPrice(pair), price.Bytes())
	giveBalance(t, base, owner, "USDT", big.NewInt(1_000_000_000_000_000))
	wrapped := &failNthFutureActivitySeqView{StateView: base, failAt: 2}
	tx := &pb.AnyTx{Content: &pb.AnyTx_FutureOrderTx{FutureOrderTx: &pb.FutureOrderTx{
		Base: &pb.BaseMessage{TxId: "refund-error", FromAddress: owner, ExecutedHeight: 1},
		Pair: pair, Side: int32(future.Long), Quantity: big.NewInt(1e8).String(),
		Leverage: 10, IsMarket: true,
	}}}

	_, receipt, err := (&FutureOrderTxHandler{}).DryRun(tx, wrapped)
	if err == nil || !strings.Contains(err.Error(), "refund unused future order margin") {
		t.Fatalf("unused-margin refund failure was ignored: receipt=%+v err=%v", receipt, err)
	}
	if receipt == nil || receipt.Status != "FAILED" {
		t.Fatalf("refund failure must return a failed receipt: %+v", receipt)
	}
	if wrapped.calls != 2 {
		t.Fatalf("fault did not target refund ledger entry: activity calls=%d", wrapped.calls)
	}
}

                                                                              
                                                                     
  
            
                                                                      
                                                        
                                                                               
                                                                       
                                                               
                                                     
                                                         

func TestSvPositionStore_SetGet_RoundTrip(t *testing.T) {
	sv := newMemStateView()
	store := &svPositionStore{sv: sv}

	pos := &future.Position{
		ID: "pos1", Owner: "alice", Pair: "BTC_USDT",
		Side:                  future.Long,
		EntryPrice:            big.NewInt(50000),
		Quantity:              big.NewInt(100),
		Margin:                big.NewInt(500),
		Status:                future.StatusOpen,
		OpenHeight:            10,
		LastCumulativeFunding: big.NewInt(3),
	}
	if err := store.SetPosition(pos); err != nil {
		t.Fatalf("set: %v", err)
	}

	got, err := store.GetPosition("pos1")
	if err != nil || got == nil {
		t.Fatalf("get: err=%v got=%v", err, got)
	}
	if got.Owner != "alice" || got.Pair != "BTC_USDT" ||
		got.EntryPrice.Cmp(big.NewInt(50000)) != 0 ||
		got.Margin.Cmp(big.NewInt(500)) != 0 {
		t.Fatalf("round trip mismatch: %+v", got)
	}

	                                                
	if _, ok, _ := sv.Get(keys.KeyFuturePosition("pos1")); !ok {
		t.Fatalf("KeyFuturePosition missing")
	}
	if _, ok, _ := sv.Get(keys.KeyFutureOwnerPosition("alice", "pos1")); !ok {
		t.Fatalf("KeyFutureOwnerPosition missing")
	}
	if _, ok, _ := sv.Get(keys.KeyFuturePairPosition("BTC_USDT", "pos1")); !ok {
		t.Fatalf("KeyFuturePairPosition missing")
	}
}

func TestSvPositionStore_GetPositionPropagatesStateReadError(t *testing.T) {
	base := newMemStateView()
	const positionID = "read-failure"
	base.Set(keys.KeyFuturePosition(positionID), []byte("must-not-be-treated-as-missing"))
	wrapped := failFutureStateGetKeyView{StateView: base, key: keys.KeyFuturePosition(positionID)}

	pos, err := (&svPositionStore{sv: wrapped}).GetPosition(positionID)
	if err == nil {
		t.Fatal("position state read failure was treated as a missing position")
	}
	if pos != nil {
		t.Fatalf("read failure returned a position: %+v", pos)
	}
}

func TestFutureLiquidationCandidatePropagatesPositionReadError(t *testing.T) {
	base := newMemStateView()
	const (
		pair       = "BTC_USDT"
		positionID = "liq-read-failure"
	)
	pos := &future.Position{
		ID: positionID, Owner: "alice", Pair: pair, Side: future.Long,
		EntryPrice: big.NewInt(100), Quantity: big.NewInt(10), Margin: big.NewInt(1), Status: future.StatusOpen,
	}
	store := &svPositionStore{sv: base}
	if err := store.SetPosition(pos); err != nil {
		t.Fatal(err)
	}
	if err := (&svLiquidationIndex{sv: base}).Upsert(positionID, pair, future.Long, big.NewInt(99)); err != nil {
		t.Fatal(err)
	}
	base.Set(keys.KeyFutureLiqIndexBackfillDone(pair), []byte{1})

	wrapped := failFutureStateGetKeyView{StateView: base, key: keys.KeyFuturePosition(positionID)}
	posStore := &svPositionStore{sv: wrapped}
	eng := newFutureEngine(wrapped, 1)
	_, err := collectFutureLiquidationCandidates(posStore, eng, &future.MarketConfig{
		Pair: pair, QuoteToken: "USDT", MaxLeverage: 100, MaintenanceMarginRateBps: 50,
	}, pair, big.NewInt(98))
	if err == nil {
		t.Fatal("liquidation candidate position read failure was silently skipped")
	}
	if _, exists, getErr := base.Get(keys.KeyFutureLiqPosRef(positionID)); getErr != nil || !exists {
		t.Fatalf("failed read damaged liquidation index reference: exists=%v err=%v", exists, getErr)
	}
}

func TestSvPositionStore_GetAllOpenPositionsRejectsPairPrefixCollisionAndTerminalRows(t *testing.T) {
	sv := newMemStateView()
	store := &svPositionStore{sv: sv}

	putPairRow := func(pos *pb.FuturePositionState) {
		t.Helper()
		data, err := proto.Marshal(pos)
		if err != nil {
			t.Fatal(err)
		}
		sv.Set(keys.KeyFuturePairPosition(pos.Pair, pos.PositionId), data)
	}
	putPairRow(&pb.FuturePositionState{
		PositionId: "wanted-open", Pair: "A_B", Side: int32(future.Long),
		Quantity: "1", Margin: "1", Status: int32(future.StatusOpen),
	})
	putPairRow(&pb.FuturePositionState{
		PositionId: "foreign-open", Pair: "A_B_C", Side: int32(future.Long),
		Quantity: "1", Margin: "1", Status: int32(future.StatusOpen),
	})
	putPairRow(&pb.FuturePositionState{
		PositionId: "wanted-closed", Pair: "A_B", Side: int32(future.Long),
		Status: int32(future.StatusClosed),
	})

	positions, err := store.GetAllOpenPositions("A_B")
	if err != nil {
		t.Fatal(err)
	}
	if len(positions) != 1 || positions[0].ID != "wanted-open" || positions[0].Pair != "A_B" {
		t.Fatalf("strict pair/open scan returned %+v", positions)
	}
}

func TestSvPositionStore_PositionIndexDecodeErrorsFailClosed(t *testing.T) {
	tests := []struct {
		name string
		key  string
		scan func(*svPositionStore) error
	}{
		{
			name: "pair liquidation and funding scan",
			key:  keys.KeyFuturePairPosition("BTC_USDT", "malformed"),
			scan: func(store *svPositionStore) error {
				_, err := store.GetAllOpenPositions("BTC_USDT")
				return err
			},
		},
		{
			name: "owner position view",
			key:  keys.KeyFutureOwnerPosition("alice", "malformed"),
			scan: func(store *svPositionStore) error {
				_, err := store.GetPositionsByOwner("alice")
				return err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sv := newMemStateView()
			sv.Set(tc.key, []byte{0xff})
			if err := tc.scan(&svPositionStore{sv: sv}); err == nil {
				t.Fatal("malformed consensus position index was silently skipped")
			}
		})
	}
}

func TestSvPositionStore_AllocatePositionIDPersistsPerHeightOrdinal(t *testing.T) {
	sv := newMemStateView()
	firstStore := &svPositionStore{sv: sv}

	first, err := firstStore.AllocatePositionID("alice", "BTC_USDT", future.Long, 77)
	if err != nil {
		t.Fatal(err)
	}
	second, err := firstStore.AllocatePositionID("alice", "BTC_USDT", future.Long, 77)
	if err != nil {
		t.Fatal(err)
	}
	                                                                                   
	third, err := (&svPositionStore{sv: sv}).AllocatePositionID("alice", "BTC_USDT", future.Long, 77)
	if err != nil {
		t.Fatal(err)
	}
	if first != "alice_BTC_USDT_1_77" || second != first+"_2" || third != first+"_3" {
		t.Fatalf("unexpected deterministic IDs: first=%q second=%q third=%q", first, second, third)
	}

	closed := &future.Position{
		ID: first, Owner: "alice", Pair: "BTC_USDT", Side: future.Long,
		EntryPrice: big.NewInt(100), Quantity: big.NewInt(1), Margin: big.NewInt(1), Status: future.StatusOpen,
	}
	if err := firstStore.SetPosition(closed); err != nil {
		t.Fatal(err)
	}
	closed.Status = future.StatusClosed
	closed.Quantity = new(big.Int)
	closed.Margin = new(big.Int)
	if err := firstStore.SetPosition(closed); err != nil {
		t.Fatal(err)
	}
	if err := firstStore.SetPosition(&future.Position{
		ID: second, Owner: "alice", Pair: "BTC_USDT", Side: future.Long,
		EntryPrice: big.NewInt(100), Quantity: big.NewInt(1), Margin: big.NewInt(1), Status: future.StatusOpen,
	}); err != nil {
		t.Fatal(err)
	}
	if _, exists, err := sv.Get(keys.KeyFuturePositionHistory(first)); err != nil || !exists {
		t.Fatalf("same-height reopen overwrote first history: exists=%v err=%v", exists, err)
	}

	nextHeight, err := firstStore.AllocatePositionID("alice", "BTC_USDT", future.Long, 78)
	if err != nil {
		t.Fatal(err)
	}
	if nextHeight != "alice_BTC_USDT_1_78" {
		t.Fatalf("new-height ID = %q, want legacy-compatible base ID", nextHeight)
	}
}

func TestSvPositionStore_AllocatePositionIDRejectsMalformedSequence(t *testing.T) {
	encode := func(height, ordinal uint64) []byte {
		out := make([]byte, 16)
		binary.BigEndian.PutUint64(out[:8], height)
		binary.BigEndian.PutUint64(out[8:], ordinal)
		return out
	}
	tests := []struct {
		name  string
		value []byte
	}{
		{name: "wrong length", value: []byte{1, 2, 3}},
		{name: "zero ordinal", value: encode(77, 0)},
		{name: "height regression", value: encode(78, 1)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sv := newMemStateView()
			sv.Set(keys.KeyFuturePositionIDSequence("alice", "BTC_USDT", int32(future.Long)), tc.value)
			if _, err := (&svPositionStore{sv: sv}).AllocatePositionID("alice", "BTC_USDT", future.Long, 77); err == nil {
				t.Fatal("malformed sequence must fail closed")
			}
		})
	}
}

func TestSvPositionStore_AllocatePositionIDRejectsOccupiedLiveCandidate(t *testing.T) {
	const candidate = "alice_BTC_USDT_1_77"
	sv := newMemStateView()
	sv.Set(keys.KeyFuturePosition(candidate), []byte{1})
	if _, err := (&svPositionStore{sv: sv}).AllocatePositionID("alice", "BTC_USDT", future.Long, 77); err == nil {
		t.Fatal("occupied live position ID must fail closed")
	}
	if _, exists, err := sv.Get(keys.KeyFuturePositionIDSequence("alice", "BTC_USDT", int32(future.Long))); err != nil || exists {
		t.Fatalf("failed allocation advanced sequence: exists=%v err=%v", exists, err)
	}
}

func TestSvPositionStore_AllocatePositionIDIgnoresKVOnlyColdHistory(t *testing.T) {
	const candidate = "alice_BTC_USDT_1_77"
	withHistory := newMemStateView()
	withoutHistory := newMemStateView()
	withHistory.Set(keys.KeyFuturePositionHistory(candidate), []byte("query-only"))

	gotWith, err := (&svPositionStore{sv: failFuturePositionHistoryGetStateView{StateView: withHistory}}).AllocatePositionID("alice", "BTC_USDT", future.Long, 77)
	if err != nil {
		t.Fatalf("allocation consulted cold history: %v", err)
	}
	gotWithout, err := (&svPositionStore{sv: withoutHistory}).AllocatePositionID("alice", "BTC_USDT", future.Long, 77)
	if err != nil {
		t.Fatal(err)
	}
	if gotWith != candidate || gotWithout != candidate {
		t.Fatalf("KV history changed deterministic allocation: with=%q without=%q", gotWith, gotWithout)
	}
}

func TestSvPositionStore_AllocatePositionIDRollsBackWithStateView(t *testing.T) {
	sv := newMemStateView()
	snap := sv.Snapshot()
	first, err := (&svPositionStore{sv: sv}).AllocatePositionID("alice", "BTC_USDT", future.Long, 77)
	if err != nil {
		t.Fatal(err)
	}
	if err := sv.Revert(snap); err != nil {
		t.Fatal(err)
	}
	replayed, err := (&svPositionStore{sv: sv}).AllocatePositionID("alice", "BTC_USDT", future.Long, 77)
	if err != nil {
		t.Fatal(err)
	}
	if replayed != first {
		t.Fatalf("replayed allocation=%q want=%q", replayed, first)
	}
}

func TestSvPositionStore_OpenPositionDoesNotConsultOrDeleteKVOnlyColdHistory(t *testing.T) {
	sv := newMemStateView()
	const id = "occupied-history"
	history := []byte("immutable-history")
	sv.Set(keys.KeyFuturePositionHistory(id), history)
	store := &svPositionStore{sv: failFuturePositionHistoryGetStateView{StateView: sv}}
	err := store.SetPosition(&future.Position{
		ID: id, Owner: "alice", Pair: "BTC_USDT", Side: future.Long,
		EntryPrice: big.NewInt(100), Quantity: big.NewInt(1), Margin: big.NewInt(1), Status: future.StatusOpen,
	})
	if err != nil {
		t.Fatalf("open position consulted KV-only history: %v", err)
	}
	if _, exists, getErr := sv.Get(keys.KeyFuturePosition(id)); getErr != nil || !exists {
		t.Fatalf("live position missing: exists=%v err=%v", exists, getErr)
	}
	got, exists, getErr := sv.Get(keys.KeyFuturePositionHistory(id))
	if getErr != nil || !exists || string(got) != string(history) {
		t.Fatalf("cold history changed: exists=%v got=%q err=%v", exists, got, getErr)
	}
}

func TestSvPositionStore_NonHistoryWriteDoesNotDeleteAppendOnlyColdHistory(t *testing.T) {
	sv := newMemStateView()
	const id = "partial-terminal-history"
	history := []byte("immutable-history")
	sv.Set(keys.KeyFuturePositionHistory(id), history)
	store := &svPositionStore{sv: failFuturePositionHistoryGetStateView{StateView: sv}}
	err := store.SetPosition(&future.Position{
		ID: id, Owner: "alice", Pair: "BTC_USDT", Side: future.Long,
		EntryPrice: big.NewInt(100), Quantity: big.NewInt(1), Margin: big.NewInt(1), Status: future.StatusClosed,
	})
	if err != nil {
		t.Fatalf("non-history write consulted cold history: %v", err)
	}
	got, exists, getErr := sv.Get(keys.KeyFuturePositionHistory(id))
	if getErr != nil || !exists || string(got) != string(history) {
		t.Fatalf("append-only cold history changed: exists=%v got=%q err=%v", exists, got, getErr)
	}
}

func TestSvPositionStore_LiveUpdateDoesNotReadColdHistory(t *testing.T) {
	sv := newMemStateView()
	pos := &future.Position{
		ID: "hot-update", Owner: "alice", Pair: "BTC_USDT", Side: future.Long,
		EntryPrice: big.NewInt(100), Quantity: big.NewInt(1), Margin: big.NewInt(1), Status: future.StatusOpen,
	}
	if err := (&svPositionStore{sv: sv}).SetPosition(pos); err != nil {
		t.Fatal(err)
	}
	                                                                           
	                                                                          
	wrapped := failFuturePositionHistoryGetStateView{StateView: sv}
	pos.Margin = big.NewInt(2)
	if err := (&svPositionStore{sv: wrapped}).SetPosition(pos); err != nil {
		t.Fatalf("live update unexpectedly read cold history: %v", err)
	}
}

func TestRealizedPnLBatchCommitDoesNotReadColdPositionHistory(t *testing.T) {
	base := newMemStateView()
	pair := "BTC_USDT"
	price := new(big.Int).Mul(big.NewInt(100), future.PriceScale)
	qty := new(big.Int).Set(future.PriceScale)
	margin := new(big.Int).Mul(big.NewInt(10), future.PriceScale)
	base.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
	base.Set(keys.KeyFutureMarkPrice(pair), price.Bytes())
	writeFuturePosition(t, base, &pb.FuturePositionState{
		PositionId: "cold-history-close", Owner: "alice", Pair: pair,
		Side: int32(future.Long), EntryPrice: price.String(),
		Quantity: qty.String(), Margin: margin.String(),
		Status: int32(future.StatusOpen), OpenHeight: 1,
	})

	wrapped := failFuturePositionHistoryGetStateView{StateView: base}
	eng := newFutureEngine(wrapped, 10)
	if err := eng.BeginRealizedPnLBatch(); err != nil {
		t.Fatal(err)
	}
	if err := eng.ProcessTradeForPosition("alice", pair, future.Short, qty, price, new(big.Int), 10); err != nil {
		t.Fatalf("close position with forbidden cold-history reads: %v", err)
	}
	if _, err := eng.CommitRealizedPnLBatch(); err != nil {
		t.Fatalf("batch commit consulted cold history: %v", err)
	}

	if _, exists, err := base.Get(keys.KeyFuturePosition("cold-history-close")); err != nil || exists {
		t.Fatalf("terminal position remained hot: exists=%v err=%v", exists, err)
	}
	history := readFuturePositionHistory(t, base, "cold-history-close")
	if future.PositionStatus(history.Status) != future.StatusClosed || history.Quantity != "0" || history.Margin != "0" {
		t.Fatalf("unexpected cold history after batch commit: %+v", history)
	}
}

func TestSvPositionStore_CacheDoesNotSurviveRollbackSnapshotABA(t *testing.T) {
	sv := newMemStateView()
	store := &svPositionStore{sv: sv}
	snap := sv.Snapshot()
	pos := &future.Position{
		ID: "rolled-back", Owner: "alice", Pair: "BTC_USDT", Side: future.Long,
		EntryPrice: big.NewInt(100), Quantity: big.NewInt(1), Margin: big.NewInt(1), Status: future.StatusOpen,
	}
	if err := store.SetPosition(pos); err != nil {
		t.Fatal(err)
	}
	if cached, err := store.GetPosition(pos.ID); err != nil || cached == nil {
		t.Fatalf("prime cache: pos=%v err=%v", cached, err)
	}
	cachedSnap := store.cacheSnap
	if cachedSnap <= snap {
		t.Fatalf("test setup did not create writes: snap=%d cached=%d", snap, cachedSnap)
	}
	if err := sv.Revert(snap); err != nil {
		t.Fatal(err)
	}
	                                                                         
	                                                                             
	for sv.Snapshot() < cachedSnap {
		i := sv.Snapshot()
		sv.Set(fmt.Sprintf("v1_unrelated_cache_aba_%d", i), []byte{1})
	}
	if sv.Snapshot() != cachedSnap {
		t.Fatalf("test setup snapshot=%d want=%d", sv.Snapshot(), cachedSnap)
	}
	got, err := store.GetPosition(pos.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("rollback-resurrected cached position: %+v", got)
	}
}

func TestSvPositionStore_FirstSetAfterRollbackDropsRevertedCacheEntries(t *testing.T) {
	sv := newMemStateView()
	store := &svPositionStore{sv: sv}
	snap := sv.Snapshot()
	rolledBack := &future.Position{
		ID: "rolled-back-before-set", Owner: "alice", Pair: "BTC_USDT", Side: future.Long,
		EntryPrice: big.NewInt(100), Quantity: big.NewInt(1), Margin: big.NewInt(1), Status: future.StatusOpen,
	}
	if err := store.SetPosition(rolledBack); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetPosition(rolledBack.ID); err != nil {
		t.Fatal(err)
	}
	if err := sv.Revert(snap); err != nil {
		t.Fatal(err)
	}

	committed := &future.Position{
		ID: "written-after-rollback", Owner: "bob", Pair: "ETH_USDT", Side: future.Short,
		EntryPrice: big.NewInt(200), Quantity: big.NewInt(1), Margin: big.NewInt(1), Status: future.StatusOpen,
	}
	if err := store.SetPosition(committed); err != nil {
		t.Fatal(err)
	}
	if got, err := store.GetPosition(rolledBack.ID); err != nil || got != nil {
		t.Fatalf("first post-rollback Set retained reverted cache entry: pos=%+v err=%v", got, err)
	}
	if got, err := store.GetPosition(committed.ID); err != nil || got == nil {
		t.Fatalf("post-rollback position missing: pos=%+v err=%v", got, err)
	}
}

func TestSvPositionStore_BulkSeedMutationInvalidatesCache(t *testing.T) {
	sv := newMemStateView()
	store := &svPositionStore{sv: sv}
	pos := &future.Position{
		ID: "bulk-invalidated", Owner: "alice", Pair: "BTC_USDT", Side: future.Long,
		EntryPrice: big.NewInt(100), Quantity: big.NewInt(1), Margin: big.NewInt(1), Status: future.StatusOpen,
	}
	if err := store.SetPosition(pos); err != nil {
		t.Fatal(err)
	}
	if got, err := store.GetPosition(pos.ID); err != nil || got == nil {
		t.Fatalf("prime cache: pos=%+v err=%v", got, err)
	}
	applyWriteOpsToStateView(sv, []WriteOp{{Key: keys.KeyFuturePosition(pos.ID), Del: true}})
	if got, err := store.GetPosition(pos.ID); err != nil || got != nil {
		t.Fatalf("ApplyWriteOps left stale position cache: pos=%+v err=%v", got, err)
	}
}

func TestFuturePosition_ClosePnLRemainderRoundTripAndClone(t *testing.T) {
	sv := newMemStateView()
	writer := &svPositionStore{sv: sv}
	pos := &future.Position{
		ID: "pnl_remainder", Owner: "alice", Pair: "BTC_USDT",
		Side:                   future.Long,
		EntryPrice:             big.NewInt(50_000),
		Quantity:               big.NewInt(100),
		Margin:                 big.NewInt(500),
		MarginBasisAmount:      big.NewInt(700),
		MarginBasisClosed:      big.NewInt(40),
		Status:                 future.StatusOpen,
		ADLWithheldPnL:         big.NewInt(19),
		ClosedQty:              big.NewInt(7),
		ExitNotional:           big.NewInt(11),
		ClosePnLRemainder:      big.NewInt(-12345),
		EntryNotionalNumerator: big.NewInt(5_000_123),
		LastCumulativeFunding:  big.NewInt(13),
		FirstCumulativeFunding: big.NewInt(17),
	}
	if err := writer.SetPosition(pos); err != nil {
		t.Fatal(err)
	}

	                                                                            
	reader := &svPositionStore{sv: sv}
	got, err := reader.GetPosition(pos.ID)
	if err != nil || got == nil {
		t.Fatalf("read persisted position: pos=%v err=%v", got, err)
	}
	if got.ClosePnLRemainder.Cmp(pos.ClosePnLRemainder) != 0 {
		t.Fatalf("remainder round trip = %s, want %s", got.ClosePnLRemainder, pos.ClosePnLRemainder)
	}
	if got.EntryNotionalNumerator.Cmp(pos.EntryNotionalNumerator) != 0 {
		t.Fatalf("entry numerator round trip = %s, want %s", got.EntryNotionalNumerator, pos.EntryNotionalNumerator)
	}
	if got.MarginBasisAmount.Cmp(pos.MarginBasisAmount) != 0 || got.MarginBasisClosed.Cmp(pos.MarginBasisClosed) != 0 {
		t.Fatalf("margin basis round trip = %s/%s, want %s/%s", got.MarginBasisAmount, got.MarginBasisClosed, pos.MarginBasisAmount, pos.MarginBasisClosed)
	}
	if got.ADLWithheldPnL.Cmp(pos.ADLWithheldPnL) != 0 {
		t.Fatalf("ADL withheld PnL round trip = %s, want %s", got.ADLWithheldPnL, pos.ADLWithheldPnL)
	}

	                                                                        
	got.ClosePnLRemainder.SetInt64(99)
	got.EntryNotionalNumerator.SetInt64(99)
	got.MarginBasisAmount.SetInt64(99)
	got.MarginBasisClosed.SetInt64(99)
	got.ADLWithheldPnL.SetInt64(99)
	got.ClosedQty.SetInt64(99)
	got.ExitNotional.SetInt64(99)
	got.LastCumulativeFunding.SetInt64(99)
	got.FirstCumulativeFunding.SetInt64(99)
	again, err := reader.GetPosition(pos.ID)
	if err != nil || again == nil {
		t.Fatalf("read cached position: pos=%v err=%v", again, err)
	}
	if again.ClosePnLRemainder.Cmp(big.NewInt(-12345)) != 0 ||
		again.EntryNotionalNumerator.Cmp(big.NewInt(5_000_123)) != 0 ||
		again.MarginBasisAmount.Cmp(big.NewInt(700)) != 0 ||
		again.MarginBasisClosed.Cmp(big.NewInt(40)) != 0 ||
		again.ADLWithheldPnL.Cmp(big.NewInt(19)) != 0 ||
		again.ClosedQty.Cmp(big.NewInt(7)) != 0 ||
		again.ExitNotional.Cmp(big.NewInt(11)) != 0 ||
		again.LastCumulativeFunding.Cmp(big.NewInt(13)) != 0 ||
		again.FirstCumulativeFunding.Cmp(big.NewInt(17)) != 0 {
		t.Fatalf("cached position was mutated through clone: %+v", again)
	}

	legacy := protoToPosition(&pb.FuturePositionState{})
	if legacy.ClosePnLRemainder == nil || legacy.ClosePnLRemainder.Sign() != 0 {
		t.Fatalf("missing legacy remainder = %v, want zero", legacy.ClosePnLRemainder)
	}
	if legacy.EntryNotionalNumerator == nil || legacy.EntryNotionalNumerator.Sign() != 0 {
		t.Fatalf("missing legacy entry numerator = %v, want zero", legacy.EntryNotionalNumerator)
	}
	if legacy.MarginBasisAmount == nil || legacy.MarginBasisAmount.Sign() != 0 ||
		legacy.MarginBasisClosed == nil || legacy.MarginBasisClosed.Sign() != 0 {
		t.Fatalf("missing legacy margin basis = %v/%v, want zero", legacy.MarginBasisAmount, legacy.MarginBasisClosed)
	}
	if encoded := positionToProto(&future.Position{ClosePnLRemainder: new(big.Int)}); encoded.ClosePnlRemainder != "" {
		t.Fatalf("zero remainder encoded as %q, want omitted empty string", encoded.ClosePnlRemainder)
	}
	if encoded := positionToProto(&future.Position{EntryNotionalNumerator: new(big.Int)}); encoded.EntryNotionalNumerator != "" {
		t.Fatalf("zero entry numerator encoded as %q, want omitted empty string", encoded.EntryNotionalNumerator)
	}
	if encoded := positionToProto(&future.Position{MarginBasisAmount: new(big.Int), MarginBasisClosed: new(big.Int)}); encoded.MarginBasisAmount != "" || encoded.MarginBasisClosed != "" {
		t.Fatalf("zero margin basis encoded as %q/%q, want omitted empty strings", encoded.MarginBasisAmount, encoded.MarginBasisClosed)
	}
}

                                                                                                
func TestSvPositionStore_SetPosition_ClosedWritesOnlyCanonicalHistory(t *testing.T) {
	sv := newMemStateView()
	store := &svPositionStore{sv: sv}

	pos := &future.Position{
		ID: "pos1", Owner: "alice", Pair: "BTC_USDT",
		Side:       future.Long,
		EntryPrice: big.NewInt(50000), Quantity: big.NewInt(100),
		Margin: big.NewInt(500), Status: future.StatusOpen,
	}
	_ = store.SetPosition(pos)

	                   
	pos.Status = future.StatusClosed
	pos.Quantity = new(big.Int)
	pos.Margin = new(big.Int)
	_ = store.SetPosition(pos)

	if _, ok, _ := sv.Get(keys.KeyFuturePairPosition("BTC_USDT", "pos1")); ok {
		t.Fatalf("pair index should be cleared on Closed")
	}
	                                               
	if _, ok, _ := sv.Get(keys.KeyFuturePosition("pos1")); ok {
		t.Fatalf("terminal position should be removed from StateDB")
	}
	if _, ok, _ := sv.Get(keys.KeyFutureOwnerPosition("alice", "pos1")); ok {
		t.Fatalf("terminal owner position index should be removed from StateDB")
	}
	if _, ok, _ := sv.Get(keys.KeyFuturePositionHistory("pos1")); !ok {
		t.Fatalf("terminal position history should remain in KV index")
	}
	for _, indexKey := range []string{
		keys.KeyFuturePositionHistoryOwnerIndex("alice", int32(future.StatusClosed), 0, "pos1"),
		keys.KeyFuturePositionHistoryPairIndex("BTC_USDT", int32(future.StatusClosed), 0, "pos1"),
		keys.KeyFuturePositionHistoryRecentIndex(int32(future.StatusClosed), 0, "pos1"),
	} {
		if ref, ok, _ := sv.Get(indexKey); ok || len(ref) != 0 {
			t.Fatalf("Explorer-only terminal index leaked into StateView: key=%s ok=%v ref=%q", indexKey, ok, string(ref))
		}
	}
}

func TestSvPositionStore_GetPositionByOwnerAndPairSide(t *testing.T) {
	sv := newMemStateView()
	store := &svPositionStore{sv: sv}

	                                                        
	store.SetPosition(&future.Position{ID: "a_long", Owner: "alice", Pair: "BTC_USDT", Side: future.Long, EntryPrice: big.NewInt(50), Quantity: big.NewInt(1), Margin: big.NewInt(5), Status: future.StatusOpen})
	store.SetPosition(&future.Position{ID: "a_short", Owner: "alice", Pair: "BTC_USDT", Side: future.Short, EntryPrice: big.NewInt(50), Quantity: big.NewInt(1), Margin: big.NewInt(5), Status: future.StatusOpen})
	store.SetPosition(&future.Position{ID: "b_long", Owner: "bob", Pair: "BTC_USDT", Side: future.Long, EntryPrice: big.NewInt(50), Quantity: big.NewInt(1), Margin: big.NewInt(5), Status: future.StatusOpen})

	p, _ := store.GetPositionByOwnerAndPairSide("alice", "BTC_USDT", future.Long)
	if p == nil || p.ID != "a_long" {
		t.Fatalf("want alice-Long, got %+v", p)
	}
	p, _ = store.GetPositionByOwnerAndPairSide("alice", "BTC_USDT", future.Short)
	if p == nil || p.ID != "a_short" {
		t.Fatalf("want alice-Short, got %+v", p)
	}
	p, _ = store.GetPositionByOwnerAndPairSide("alice", "ETH_USDT", future.Long)
	if p != nil {
		t.Fatalf("unknown pair should return nil, got %+v", p)
	}
}

func TestSvInsuranceFund_RoundTrip(t *testing.T) {
	sv := newMemStateView()
	fund := &svInsuranceFund{sv: sv}

	bal, err := fund.GetBalance("BTC_USDT")
	if err != nil || bal == nil || bal.Sign() != 0 {
		t.Fatalf("empty fund: got %v err=%v", bal, err)
	}

	want := big.NewInt(12345)
	if err := fund.SetBalance("BTC_USDT", want); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, _ := fund.GetBalance("BTC_USDT")
	if got.Cmp(want) != 0 {
		t.Fatalf("round trip: want %s got %s", want, got)
	}
}

func TestSvInsuranceFund_ReadErrorsDoNotOverwriteExistingState(t *testing.T) {
	tests := []struct {
		name     string
		stateKey func(string) string
		seed     *big.Int
		mutate   func(*future.Engine, *svInsuranceFund) error
	}{
		{
			name:     "balance deposit",
			stateKey: keys.KeyFutureInsuranceFund,
			seed:     big.NewInt(12345),
			mutate: func(eng *future.Engine, _ *svInsuranceFund) error {
				return eng.DepositInsuranceFund("BTC_USDT", big.NewInt(7))
			},
		},
		{
			name:     "shortfall accumulation",
			stateKey: keys.KeyFutureInsuranceShortfall,
			seed:     big.NewInt(6789),
			mutate: func(_ *future.Engine, fund *svInsuranceFund) error {
				return fund.AddShortfall("BTC_USDT", big.NewInt(7))
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base := newMemStateView()
			key := tc.stateKey("BTC_USDT")
			base.Set(key, tc.seed.Bytes())
			wrapped := failFutureStateGetKeyView{StateView: base, key: key}
			fund := &svInsuranceFund{sv: wrapped}
			eng := newFutureEngine(wrapped, 1)

			if err := tc.mutate(eng, fund); err == nil {
				t.Fatal("insurance state read failure did not fail closed")
			}
			got, exists, err := base.Get(key)
			if err != nil || !exists {
				t.Fatalf("read seeded insurance state: exists=%v err=%v", exists, err)
			}
			if value := new(big.Int).SetBytes(got); value.Cmp(tc.seed) != 0 {
				t.Fatalf("insurance state overwritten after read error: got=%s want=%s", value, tc.seed)
			}
		})
	}
}

func TestSvMarkPriceProvider_SettleReadErrorDoesNotFallbackToLiveMark(t *testing.T) {
	base := newMemStateView()
	const pair = "BTC_USDT"
	base.Set(keys.KeyFutureMarkPrice(pair), big.NewInt(100).Bytes())
	base.Set(keys.KeyFutureSettleMarkPrice(pair), big.NewInt(90).Bytes())
	wrapped := failFutureStateGetKeyView{StateView: base, key: keys.KeyFutureSettleMarkPrice(pair)}

	price, err := (&svMarkPriceProvider{sv: wrapped}).GetSettleMarkPrice(pair)
	if err == nil {
		t.Fatal("settle-mark storage error incorrectly fell back to live mark")
	}
	if price != nil {
		t.Fatalf("settle-mark read error returned fabricated price %s", price)
	}
}

func TestSvBalanceAccessor_DeductInsufficient(t *testing.T) {
	sv := newMemStateView()
	                         
	rec := &pb.TokenBalanceRecord{
		Address: "alice", Token: "USDT",
		Balance: &pb.TokenBalance{Balance: "100"},
	}
	data, _ := proto.Marshal(rec)
	sv.Set(keys.KeyBalance("alice", "USDT"), data)

	ba := &svBalanceAccessor{sv: sv, height: 0}
	                    
	if err := ba.DeductBalance("alice", "USDT", big.NewInt(200)); err == nil {
		t.Fatalf("insufficient deduct should fail")
	}
	               
	got, _ := ba.GetAvailableBalance("alice", "USDT")
	if got.Cmp(big.NewInt(100)) != 0 {
		t.Fatalf("balance disturbed after failed deduct: %s", got)
	}
	                
	if err := ba.DeductBalance("alice", "USDT", big.NewInt(50)); err != nil {
		t.Fatalf("valid deduct: %v", err)
	}
	got, _ = ba.GetAvailableBalance("alice", "USDT")
	if got.Cmp(big.NewInt(50)) != 0 {
		t.Fatalf("after deduct: want 50 got %s", got)
	}
}

func TestApplyPendingFundingBalanceReadErrorDoesNotAdvanceAnchor(t *testing.T) {
	base := newMemStateView()
	const (
		pair       = "BTC_USDT"
		owner      = "funding-payer"
		positionID = "funding-payer-position"
	)
	mark := big.NewInt(50_000 * 1e8)
	pos := &future.Position{
		ID: positionID, Owner: owner, Pair: pair, Side: future.Long,
		EntryPrice: mark, Quantity: big.NewInt(1e8), Margin: big.NewInt(1),
		Status: future.StatusOpen, LastCumulativeFunding: new(big.Int),
	}
	if err := (&svPositionStore{sv: base}).SetPosition(pos); err != nil {
		t.Fatal(err)
	}
	if err := (&svCumulativeFundingStore{sv: base}).SetCumulativeFunding(pair, big.NewInt(10), new(big.Int)); err != nil {
		t.Fatal(err)
	}
	base.Set(keys.KeyFutureSettleMarkPrice(pair), mark.Bytes())
	SetBalance(base, owner, "USDT", &pb.TokenBalance{Balance: "100"})
	wrapper := failFutureStateGetKeyView{StateView: base, key: keys.KeyBalance(owner, "USDT")}
	eng := newFutureEngine(wrapper, 2)
	cfg, err := eng.GetMarketConfig(pair)
	if err != nil {
		t.Fatal(err)
	}

	if err := eng.ApplyPendingFunding(pos, cfg); err == nil || !strings.Contains(err.Error(), "funding payer balance") {
		t.Fatalf("funding payer balance read failure was ignored: %v", err)
	}
	back, err := (&svPositionStore{sv: base}).GetPosition(positionID)
	if err != nil || back == nil {
		t.Fatalf("read position after failed funding: pos=%v err=%v", back, err)
	}
	if back.Margin.Cmp(big.NewInt(1)) != 0 || back.LastCumulativeFunding.Sign() != 0 {
		t.Fatalf("failed funding read changed position: margin=%s lastCum=%s", back.Margin, back.LastCumulativeFunding)
	}
	if got := GetBalance(base, owner, "USDT").Balance; got != "100" {
		t.Fatalf("failed funding read changed available balance: %s", got)
	}
}

func TestSvCumulativeFundingStore_EmptyReturnsZero(t *testing.T) {
	sv := newMemStateView()
	store := &svCumulativeFundingStore{sv: sv}

	                                             
	long, short, err := store.GetCumulativeFunding("BTC_USDT")
	if err != nil {
		t.Fatalf("empty: %v", err)
	}
	if long.Sign() != 0 || short.Sign() != 0 {
		t.Fatalf("zero init expected, got %s/%s", long, short)
	}

	                  
	_ = store.SetCumulativeFunding("BTC_USDT", big.NewInt(10), big.NewInt(-3))
	long, short, _ = store.GetCumulativeFunding("BTC_USDT")
	if long.Cmp(big.NewInt(10)) != 0 || short.Cmp(big.NewInt(-3)) != 0 {
		t.Fatalf("round trip: got %s/%s", long, short)
	}
}

                                                        
                                                                                        
                                                            
  
                                                                                                   
                                                                                                          
func TestSvLiquidationIndex_UpsertRemove_ReverseRefTracked(t *testing.T) {
	sv := newMemStateView()
	idx := &svLiquidationIndex{sv: sv}

	if err := idx.Upsert("pos1", "BTC_USDT", future.Long, big.NewInt(51000)); err != nil {
		t.Fatalf("upsert1: %v", err)
	}
	if err := idx.Upsert("pos1", "BTC_USDT", future.Long, big.NewInt(50500)); err != nil {
		t.Fatalf("upsert2: %v", err)
	}

	                                                                                    
	ids, err := idx.FindLongsAtRisk("BTC_USDT", big.NewInt(50000))
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	count := 0
	for _, id := range ids {
		if id == "pos1" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expect pos1 to appear exactly once, got %d (all=%v)", count, ids)
	}

	                         
	if err := idx.Remove("pos1", "BTC_USDT", future.Long, nil); err != nil {
		t.Fatalf("remove: %v", err)
	}
	ids, _ = idx.FindLongsAtRisk("BTC_USDT", big.NewInt(50000))
	for _, id := range ids {
		if id == "pos1" {
			t.Fatalf("pos1 should be removed")
		}
	}
}

                                                                                            
  
                                                         
                                                                                        
                                     
  
                                                                                          
                                                                                                         
func TestSvLiquidationIndex_FindLongsAtRisk_WithBuffer(t *testing.T) {
	sv := newMemStateView()
	idx := &svLiquidationIndex{sv: sv}

	positions := []struct {
		id       string
		liqPrice int64
		atRisk   bool
	}{
		{"pos_at_risk_1", 51000, true},           
		{"pos_at_risk_2", 50500, true},           
		{"pos_buffer_1", 49500, false},          
		{"pos_buffer_2", 48000, false},          
		{"pos_buffer_3", 47000, false},          
	}
	for _, p := range positions {
		if err := idx.Upsert(p.id, "BTC_USDT", future.Long, big.NewInt(p.liqPrice)); err != nil {
			t.Fatalf("upsert %s: %v", p.id, err)
		}
	}

	ids, err := idx.FindLongsAtRisk("BTC_USDT", big.NewInt(50000))
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(ids) != 5 {
		t.Fatalf("expect 5 ids (2 at-risk + 3 buffer), got %d: %v", len(ids), ids)
	}
	                                          
	if ids[0] != "pos_at_risk_1" || ids[1] != "pos_at_risk_2" {
		t.Fatalf("at-risk ordering wrong: %v", ids[:2])
	}
	                                                                 
	if ids[2] != "pos_buffer_1" || ids[3] != "pos_buffer_2" || ids[4] != "pos_buffer_3" {
		t.Fatalf("buffer ordering wrong: %v", ids[2:])
	}
}

                                                                     
                                                                                  
                                                                   
func TestSvLiquidationIndex_FindShortsAtRisk_WithBuffer(t *testing.T) {
	sv := newMemStateView()
	idx := &svLiquidationIndex{sv: sv}

	positions := []struct {
		id       string
		liqPrice int64
	}{
		{"pos_at_risk_1", 49000},                             
		{"pos_at_risk_2", 49500},           
		{"pos_buffer_1", 50500},           
		{"pos_buffer_2", 51000},           
		{"pos_buffer_3", 52000},           
	}
	for _, p := range positions {
		if err := idx.Upsert(p.id, "BTC_USDT", future.Short, big.NewInt(p.liqPrice)); err != nil {
			t.Fatalf("upsert %s: %v", p.id, err)
		}
	}

	ids, err := idx.FindShortsAtRisk("BTC_USDT", big.NewInt(50000))
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(ids) != 5 {
		t.Fatalf("expect 5 ids (2 at-risk + 3 buffer), got %d: %v", len(ids), ids)
	}
	if ids[0] != "pos_at_risk_1" || ids[1] != "pos_at_risk_2" {
		t.Fatalf("at-risk ordering wrong: %v", ids[:2])
	}
	if ids[2] != "pos_buffer_1" || ids[3] != "pos_buffer_2" || ids[4] != "pos_buffer_3" {
		t.Fatalf("buffer ordering wrong: %v", ids[2:])
	}
}
