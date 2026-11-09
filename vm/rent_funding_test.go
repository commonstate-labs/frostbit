package vm

import (
	bridgeasset "dex/frost/asset"
	"dex/keys"
	"dex/pb"
	"dex/protocol"
	"dex/rent"
	"dex/utils"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"strings"
	"testing"
)

const fundingOwner = "fbc1qfundingowner000000000000000000000000000"

func fundingAsset(t *testing.T) string {
	t.Helper()
	asset, err := bridgeasset.Resolve("btc", "")
	if err != nil {
		t.Fatal(err)
	}
	return asset.ID
}

func rentFundingContainsOwner(owners []string, target string) bool {
	for _, owner := range owners {
		if owner == target {
			return true
		}
	}
	return false
}

                                                                            
                                                                               
func persistFundingFixture(sv StateView) (StateView, ReadThroughFn) {
	base := make(map[string][]byte)
	for _, op := range sv.Diff() {
		if !op.Del {
			base[op.Key] = append([]byte(nil), op.Value...)
		}
	}
	read := func(key string) ([]byte, error) { return base[key], nil }
	scan := func(prefix string) (map[string][]byte, error) {
		out := make(map[string][]byte)
		for key, value := range base {
			if strings.HasPrefix(key, prefix) {
				out[key] = value
			}
		}
		return out, nil
	}
	return NewStateView(read, scan), read
}

func fundingOrder(t *testing.T, sv StateView, owner, id, base, quote string, side pb.OrderSide, amount, price int64) {
	t.Helper()
	giveSpotAccount(t, sv, owner)
	ord := &pb.OrderTx{Base: &pb.BaseMessage{TxId: id, FromAddress: owner, ExecutedHeight: 1}, Op: pb.OrderOp_ADD,
		BaseToken: base, QuoteToken: quote, Side: side, Amount: fmt.Sprint(amount), Price: fmt.Sprint(price)}
	_, rc, err := (&OrderTxHandler{}).handleAddOrder(ord, sv)
	if err != nil || rc == nil || rc.Status != "SUCCEED" {
		t.Fatalf("seed order: rc=%+v err=%v", rc, err)
	}
}

func fundingFixture(t *testing.T, reversed bool, liquidity int64) (StateView, string, string) {
	t.Helper()
	sv := newMemStateView()
	asset := fundingAsset(t)
	for _, token := range []string{asset, rent.FeeToken} {
		sv.Set(keys.KeyToken(token), []byte{1})
	}
	giveSpotAccount(t, sv, fundingOwner)
	giveBalance(t, sv, fundingOwner, asset, big.NewInt(5000))
	giveBalance(t, sv, "maker", rent.FeeToken, big.NewInt(2_000_000_000))
	base, quote, side := asset, rent.FeeToken, pb.OrderSide_BUY
	if reversed {
		base, quote, side = rent.FeeToken, asset, pb.OrderSide_SELL
	}
	fundingOrder(t, sv, "maker", "rent-test-maker", base, quote, side, liquidity, 100_000_000)
	pair := utils.GeneratePairKey(base, quote)
	sv.Set(keys.KeySpotLastPrice(pair), big.NewInt(100_000_000).Bytes())
	return sv, asset, pair
}

func TestRentFundingPartialSellRenewsExpiredLeaseAndConserves(t *testing.T) {
	sv, asset, _ := fundingFixture(t, false, 3000)
	giveBalance(t, sv, fundingOwner, rent.FeeToken, big.NewInt(200))
	old, _ := json.Marshal(rent.RentState{Owner: fundingOwner, NominalFB: "200", ExpireHeight: 10, LastSettledHeight: 8})
	sv.Set(keys.KeyRentState(fundingOwner), old)
	sv.Set(keys.KeyRentExpiry(10, fundingOwner), []byte(fundingOwner))
	sv, read := persistFundingFixture(sv)
	rt := rent.NewRuntime(rentStateViewStore{sv: sv}, rent.WithCleaner(vmRentCleaner{sv: sv}), rent.WithFundingAttempt(newRentFundingAttempt(sv, nil)))
	if err := rt.ProcessBlock(10, []string{fundingOwner}); err != nil {
		t.Fatal(err)
	}
	                                                                         
	                                          
	if got := GetBalance(sv, fundingOwner, rent.FeeToken).Balance; got != "3000" {
		t.Fatalf("FB=%s", got)
	}
	if got := GetBalance(sv, fundingOwner, asset).Balance; got != "2000" {
		t.Fatalf("unfilled asset=%s", got)
	}
	status, err := rent.NewService(rentStateViewStore{sv: sv}).RentStatus(fundingOwner, 10)
	if err != nil || status.State != "renting" || status.ExpireHeight != 40 {
		t.Fatalf("lease must credit only new 3000 FB raw: %+v err=%v", status, err)
	}
	if _, exists, _ := sv.Get(keys.KeyRentExpiry(10, fundingOwner)); exists {
		t.Fatal("old expiry was retained")
	}
	AuditGlobalConservationDiff(sv.Diff(), 10, read)
}

func TestRentFundingBuyUsesQuoteBudgetAndConserves(t *testing.T) {
	sv, asset, pair := fundingFixture(t, true, 10000)
	sv, read := persistFundingFixture(sv)
	result, err := newRentFundingAttempt(sv, nil)(fundingOwner, 10, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.ReceivedFB == nil || result.ReceivedFB.String() != "4761" {
		t.Fatalf("received=%v, want quote-budget-limited fill", result.ReceivedFB)
	}
	if got := GetBalance(sv, fundingOwner, asset).Balance; got != "239" {
		t.Fatalf("remaining quote=%s", got)
	}
	price, _, _ := sv.Get(keys.KeySpotLastPrice(pair))
	if new(big.Int).SetBytes(price).Int64() != 100_000_000 {
		t.Fatal("protocol funding changed ordinary price feed")
	}
	AuditGlobalConservationDiff(sv.Diff(), 10, read)
}

func TestRentFundingCapsConversionAtRequestedFB(t *testing.T) {
	for _, reversed := range []bool{false, true} {
		t.Run(fmt.Sprintf("reversed=%v", reversed), func(t *testing.T) {
			sv, asset, _ := fundingFixture(t, reversed, 5000)
			sv, read := persistFundingFixture(sv)
			result, err := newRentFundingAttempt(sv, nil)(fundingOwner, 10, big.NewInt(1200), nil)
			if err != nil || result.ReceivedFB == nil || result.ReceivedFB.String() != "1200" || result.Deferred {
				t.Fatalf("targeted result=%+v err=%v", result, err)
			}
			if got := GetBalance(sv, fundingOwner, rent.FeeToken).Balance; got != "1200" {
				t.Fatalf("FB=%s want=1200", got)
			}
			if got := GetBalance(sv, fundingOwner, asset).Balance; got != "3800" {
				t.Fatalf("remaining asset=%s want=3800", got)
			}
			AuditGlobalConservationDiff(sv.Diff(), 10, read)
		})
	}
}

func TestRentFundingSizesSellAtBetterMakerPrice(t *testing.T) {
	sv, asset, _ := fundingFixture(t, false, 5000)
	                                                                           
	                                                                            
	                                            
	fundingOrder(t, sv, "maker", "rent-better-maker", asset, rent.FeeToken, pb.OrderSide_BUY, 5000, 200_000_000)
	sv, read := persistFundingFixture(sv)
	result, err := newRentFundingAttempt(sv, nil)(fundingOwner, 10, big.NewInt(1200), nil)
	if err != nil || result.ReceivedFB == nil || result.ReceivedFB.String() != "1200" || result.Deferred {
		t.Fatalf("targeted result=%+v err=%v", result, err)
	}
	if got := GetBalance(sv, fundingOwner, asset).Balance; got != "4400" {
		t.Fatalf("remaining asset=%s want=4400", got)
	}
	AuditGlobalConservationDiff(sv.Diff(), 10, read)
}

func TestRentFundingCannotUseSelfTradeAsRentCredit(t *testing.T) {
	sv := newMemStateView()
	asset := fundingAsset(t)
	for _, token := range []string{asset, rent.FeeToken} {
		sv.Set(keys.KeyToken(token), []byte{1})
	}
	giveSpotAccount(t, sv, fundingOwner)
	giveBalance(t, sv, fundingOwner, asset, big.NewInt(5000))
	giveBalance(t, sv, fundingOwner, rent.FeeToken, big.NewInt(5000))
	fundingOrder(t, sv, fundingOwner, "rent-self-maker", asset, rent.FeeToken, pb.OrderSide_BUY, 5000, 100_000_000)
	                                                                            
	                                                             
	sv.Del(keys.KeyAccountOrderItem(fundingOwner, "rent-self-maker"))
	pair := utils.GeneratePairKey(asset, rent.FeeToken)
	sv.Set(keys.KeySpotLastPrice(pair), big.NewInt(100_000_000).Bytes())
	sv, read := persistFundingFixture(sv)
	result, err := newRentFundingAttempt(sv, nil)(fundingOwner, 10, big.NewInt(1000), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.ReceivedFB != nil && result.ReceivedFB.Sign() != 0 {
		t.Fatalf("self trade produced rent credit: %+v", result)
	}
	if got := GetBalance(sv, fundingOwner, asset).Balance; got != "5000" {
		t.Fatalf("self funding spent asset: %s", got)
	}
	if got := GetBalance(sv, fundingOwner, rent.FeeToken); got.Balance != "5000" || got.OrderFrozenBalance != "0" && got.OrderFrozenBalance != "" {
		t.Fatalf("self maker was not safely unfrozen: %+v", got)
	}
	if _, exists, _ := sv.Get(keys.KeyOrderState("rent-self-maker")); exists {
		t.Fatal("self maker remained at the front of the funding book")
	}
	AuditGlobalConservationDiff(sv.Diff(), 10, read)
}

func TestRentFundingCancelsFrozenSpotBeforeConversion(t *testing.T) {
	sv, asset, _ := fundingFixture(t, false, 5000)
	fundingOrder(t, sv, fundingOwner, "rent-test-owner", asset, rent.FeeToken, pb.OrderSide_SELL, 5000, 200_000_000)
	rent.MarkOwnerInitialized(sv, fundingOwner)
	for _, kind := range []string{rent.OwnerMarkerNonFBBalance, rent.OwnerMarkerSpotOrder} {
		if _, exists, err := sv.Get(keys.KeyRentOwnerMarker(fundingOwner, kind)); err != nil || !exists {
			t.Fatalf("missing complete funding marker %s: exists=%v err=%v", kind, exists, err)
		}
	}
	sv, read := persistFundingFixture(sv)
	result, err := newRentFundingAttempt(sv, nil)(fundingOwner, 10, nil, nil)
	if err != nil || result.ReceivedFB == nil || result.ReceivedFB.String() != "5000" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, exists, _ := sv.Get(keys.KeyOrderState("rent-test-owner")); exists {
		t.Fatal("funding must cancel the owner's unfilled order")
	}
	if frozen := GetBalance(sv, fundingOwner, asset).OrderFrozenBalance; frozen != "0" && frozen != "" {
		t.Fatalf("frozen=%s", frozen)
	}
	AuditGlobalConservationDiff(sv.Diff(), 10, read)
}

type rentFundingFutureScanGuard struct {
	StateView
	futureScans int
}

type rentFundingFutureScanCounter struct {
	StateView
	futureScans int
}

type rentFundingAssetReadCounter struct {
	StateView
	owner      string
	assetReads int
}

func (c *rentFundingAssetReadCounter) Get(key string) ([]byte, bool, error) {
	for _, asset := range rent.DefaultPolicy().ExemptAssets {
		if key == keys.KeyBalance(c.owner, asset.Token) {
			c.assetReads++
		}
	}
	return c.StateView.Get(key)
}

func (c *rentFundingFutureScanCounter) ScanWithLimitOrderedAfter(prefix, startAfter string, limit int, reverse bool) ([]KVPair, error) {
	if prefix == keys.KeyFutureOrderStatePrefix() {
		c.futureScans++
	}
	if scanner, ok := c.StateView.(orderedStateScannerAfter); ok {
		return scanner.ScanWithLimitOrderedAfter(prefix, startAfter, limit, reverse)
	}
	return scanStateViewOrderedAfter(c.StateView, prefix, startAfter, limit, reverse)
}

func (g *rentFundingFutureScanGuard) rejectFutureScan(prefix string) error {
	if prefix != keys.KeyFutureOrderStatePrefix() {
		return nil
	}
	g.futureScans++
	return errors.New("unexpected future order scan")
}

func (g *rentFundingFutureScanGuard) Scan(prefix string) (map[string][]byte, error) {
	if err := g.rejectFutureScan(prefix); err != nil {
		return nil, err
	}
	return g.StateView.Scan(prefix)
}

func (g *rentFundingFutureScanGuard) ScanWithLimit(prefix string, limit int) (map[string][]byte, error) {
	if err := g.rejectFutureScan(prefix); err != nil {
		return nil, err
	}
	return g.StateView.ScanWithLimit(prefix, limit)
}

func (g *rentFundingFutureScanGuard) ScanWithLimitReverse(prefix string, limit int) (map[string][]byte, error) {
	if err := g.rejectFutureScan(prefix); err != nil {
		return nil, err
	}
	return g.StateView.ScanWithLimitReverse(prefix, limit)
}

func TestRentFundingCompleteMarkersSkipAbsentExternalBalanceReads(t *testing.T) {
	initialized := "fbc1qrentfundingmarkers0000000000000000000"
	sv := newMemStateView()
	rent.MarkOwnerInitialized(sv, initialized)
	complete := &rentFundingAssetReadCounter{StateView: sv, owner: initialized}
	if result, err := newRentFundingAttempt(complete, nil)(initialized, 10, big.NewInt(1), nil); err != nil {
		t.Fatal(err)
	} else if result.ReceivedFB != nil || result.Deferred {
		t.Fatalf("empty initialized owner produced funding result: %+v", result)
	}
	if complete.assetReads != 0 {
		t.Fatalf("complete empty owner read %d external balances", complete.assetReads)
	}

	legacy := "fbc1qrentfundinglegacy00000000000000000000"
	legacyReads := &rentFundingAssetReadCounter{StateView: newMemStateView(), owner: legacy}
	if _, err := newRentFundingAttempt(legacyReads, nil)(legacy, 10, big.NewInt(1), nil); err != nil {
		t.Fatal(err)
	}
	if legacyReads.assetReads == 0 {
		t.Fatal("legacy owner skipped conservative external balance reads")
	}
}

func TestRentFundingUsesAvailableUSDTBeforeScanningFutureOrders(t *testing.T) {
	sv := newMemStateView()
	usdt, err := bridgeasset.Resolve("eth", "0xdAC17F958D2ee523a2206206994597C13D831ec7")
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{usdt.ID, rent.FeeToken} {
		sv.Set(keys.KeyToken(token), []byte{1})
	}
	giveSpotAccount(t, sv, fundingOwner)
	giveBalance(t, sv, fundingOwner, usdt.ID, big.NewInt(1_000_000))          
	giveBalance(t, sv, "maker", rent.FeeToken, big.NewInt(2_000_000))
	fundingOrder(t, sv, "maker", "rent-usdt-ask", rent.FeeToken, usdt.ID, pb.OrderSide_SELL, 2_000_000, 1_000_000)
	pair := utils.GeneratePairKey(rent.FeeToken, usdt.ID)
	sv.Set(keys.KeySpotLastPrice(pair), big.NewInt(1_000_000).Bytes())

	ownerFutureID := "future-load-299"
	for i := 0; i < rentFundingFutureScan+44; i++ {
		owner := "other"
		id := fmt.Sprintf("future-load-%03d", i)
		if id == ownerFutureID {
			owner = fundingOwner
		}
		order := &pb.FutureOrderState{OrderId: id, OwnerAddress: owner, Pair: pair, Price: "1000000", Amount: "1",
			Side: int32(pb.OrderSide_BUY), Leverage: 1, Status: pb.FutureOrderStatus_FUTURE_ORDER_OPEN, CreateHeight: 1}
		if err := writeFutureOrderSnapshot(sv, order); err != nil {
			t.Fatal(err)
		}
	}
	sv, read := persistFundingFixture(sv)
	guard := &rentFundingFutureScanGuard{StateView: sv}
	work := &rent.RentWork{Phase: rentFundingFuturePhase, Cursor: keys.KeyFutureOrderState("future-load-255")}
	result, err := newRentFundingAttempt(guard, nil)(fundingOwner, 10, big.NewInt(1_003_300), work)
	if err != nil {
		t.Fatal(err)
	}
	if result.ReceivedFB == nil || result.ReceivedFB.String() != "1003300" || result.Deferred {
		t.Fatalf("available USDT was not used directly: %+v", result)
	}
	if result.Phase == rentFundingFuturePhase || result.Cursor != "" {
		t.Fatalf("successful available conversion entered future cancellation: %+v", result)
	}
	if guard.futureScans != 0 {
		t.Fatalf("future order state was scanned %d times", guard.futureScans)
	}
	if _, exists, _ := guard.Get(keys.KeyFutureOrderState(ownerFutureID)); !exists {
		t.Fatal("successful available conversion cancelled the owner's future order")
	}
	AuditGlobalConservationDiff(guard.Diff(), 10, read)
}

func TestRentFundingFutureOrdersAreIndexedOnceAndCompleteMarkersSkipTheScan(t *testing.T) {
	sv := newMemStateView()
	initialized := "fbc1qrentinitialized000000000000000000000"
	legacyA := "fbc1qrentlegacya00000000000000000000000"
	legacyB := "fbc1qrentlegacyb00000000000000000000000"
	for _, owner := range []string{initialized, legacyA, legacyB} {
		giveSpotAccount(t, sv, owner)
	}
	rent.MarkOwnerInitialized(sv, initialized)
	for i := 0; i < rentFundingFutureScan+44; i++ {
		order := &pb.FutureOrderState{
			OrderId:      fmt.Sprintf("future-index-once-%03d", i),
			OwnerAddress: "unrelated",
			Pair:         "FB_USDT",
			Price:        "1",
			Amount:       "1",
			Side:         int32(pb.OrderSide_BUY),
			Leverage:     1,
			Status:       pb.FutureOrderStatus_FUTURE_ORDER_OPEN,
			CreateHeight: 1,
		}
		if err := writeFutureOrderSnapshot(sv, order); err != nil {
			t.Fatal(err)
		}
	}
	counter := &rentFundingFutureScanCounter{StateView: sv}
	attempt := newRentFundingAttempt(counter, nil)
	if _, err := attempt(initialized, 10, big.NewInt(1), nil); err != nil {
		t.Fatal(err)
	}
	if counter.futureScans != 0 {
		t.Fatalf("complete marker without future activity scanned global future state %d times", counter.futureScans)
	}
	if _, err := attempt(legacyA, 10, big.NewInt(1), nil); err != nil {
		t.Fatal(err)
	}
	firstBuildScans := counter.futureScans
	if firstBuildScans == 0 {
		t.Fatal("legacy fallback did not build the consensus future-order index")
	}
	if _, err := attempt(legacyB, 10, big.NewInt(1), nil); err != nil {
		t.Fatal(err)
	}
	if counter.futureScans != firstBuildScans {
		t.Fatalf("second legacy owner rescanned global future state: before=%d after=%d", firstBuildScans, counter.futureScans)
	}
}

func TestRentFundingFailureFallsThroughToRecycle(t *testing.T) {
	for _, reason := range []string{"no market", "no reference", "slippage"} {
		t.Run(reason, func(t *testing.T) {
			sv, asset, pair := fundingFixture(t, false, 5000)
			switch reason {
			case "no market":
				sv.Del(keys.KeySpotMarketConfig(pair))
			case "no reference":
				sv.Del(keys.KeySpotLastPrice(pair))
			case "slippage":
				sv.Set(keys.KeySpotLastPrice(pair), big.NewInt(200_000_000).Bytes())
			}
			sv, read := persistFundingFixture(sv)
			rt := rent.NewRuntime(rentStateViewStore{sv: sv}, rent.WithCleaner(vmRentCleaner{sv: sv}), rent.WithFundingAttempt(newRentFundingAttempt(sv, nil)))
			if err := rt.ProcessBlock(10, []string{fundingOwner}); err != nil {
				t.Fatal(err)
			}
			if _, exists, _ := sv.Get(keys.KeyAccount(fundingOwner)); exists {
				t.Fatal("failed attempt unexpectedly protected the account")
			}
			if got := GetBalance(sv, rent.RecycleFundAccount, asset).Balance; got != "5000" {
				t.Fatalf("recycled=%s", got)
			}
			AuditGlobalConservationDiff(sv.Diff(), 10, read)
		})
	}
}

func TestRentFundingFrozenAvailableAssetWithFullMakerPageFallsThroughToRecycle(t *testing.T) {
	sv, asset, _ := fundingFixture(t, false, 1)
	for i := 1; i < 40; i++ {
		fundingOrder(t, sv, "maker", fmt.Sprintf("rent-frozen-maker-%03d", i), asset, rent.FeeToken, pb.OrderSide_BUY, 1, 100_000_000)
	}
	sv.Set(keys.KeyFreeze(fundingOwner, asset), []byte("true"))
	sv, read := persistFundingFixture(sv)
	rt := rent.NewRuntime(rentStateViewStore{sv: sv}, rent.WithCleaner(vmRentCleaner{sv: sv}), rent.WithFundingAttempt(newRentFundingAttempt(sv, nil)))
	if err := rt.ProcessBlock(10, []string{fundingOwner}); err != nil {
		t.Fatalf("deterministically unavailable funding source blocked finalization: %v", err)
	}
	if _, exists, _ := sv.Get(keys.KeyAccount(fundingOwner)); exists {
		t.Fatal("frozen funding source unexpectedly kept the expired account alive")
	}
	if got := GetBalance(sv, rent.RecycleFundAccount, asset).Balance; got != "5000" {
		t.Fatalf("recycled frozen source=%s want=5000", got)
	}
	AuditGlobalConservationDiff(sv.Diff(), 10, read)
}

func TestRentFundingUsesParentReferenceAndBoundedMakers(t *testing.T) {
	sv, _, pair := fundingFixture(t, false, 1)
	for i := 1; i < 40; i++ {
		fundingOrder(t, sv, "maker", fmt.Sprintf("rent-maker-%03d", i), fundingAsset(t), rent.FeeToken, pb.OrderSide_BUY, 1, 100_000_000)
	}
	sv, read := persistFundingFixture(sv)
	attempt := newRentFundingAttempt(sv, nil)
	result, err := attempt(fundingOwner, 10, big.NewInt(40), nil)
	if err != nil || result.ReceivedFB == nil || result.ReceivedFB.Int64() != rentFundingMakerLimit || result.Deferred {
		t.Fatalf("bounded result=%+v err=%v", result, err)
	}
	if !result.Progress {
		t.Fatalf("first same-block page did not report progress: %+v", result)
	}
	result, err = attempt(fundingOwner, 10, big.NewInt(8), &rent.RentWork{Phase: result.Phase, Cursor: result.Cursor})
	if err != nil || result.ReceivedFB == nil || result.ReceivedFB.Int64() != 8 || result.Deferred {
		t.Fatalf("second same-block page=%+v err=%v", result, err)
	}
	trades, err := sv.Scan(keys.KeyTradeRecordPrefix(pair))
	if err != nil || len(trades) != 40 {
		t.Fatalf("same owner/pair trade records collided: trades=%d err=%v", len(trades), err)
	}
	AuditGlobalConservationDiff(sv.Diff(), 10, read)

	sv, _, pair = fundingFixture(t, false, 5000)
	sv, _ = persistFundingFixture(sv)
	                                                                
	result, err = newRentFundingAttempt(sv, []WriteOp{{Key: keys.KeySpotLastPrice(pair), HasOldValue: true}})(fundingOwner, 10, nil, nil)
	if err != nil || result.ReceivedFB != nil {
		t.Fatalf("same-block-only reference was used: %+v err=%v", result, err)
	}
}

func TestRentFundingCancelsBoundedRentBlockedPageAndContinuesSameBlock(t *testing.T) {
	sv, asset, _ := fundingFixture(t, false, 5000)
	blockedOwners := make([]string, 0, rentFundingMakerLimit)
	for i := 0; i < rentFundingMakerLimit; i++ {
		owner := fmt.Sprintf("fbc1qrentblocked%02d000000000000000000000000", i)
		blockedOwners = append(blockedOwners, owner)
		seedMakerRentDebt(t, sv, owner)
		fundingOrder(t, sv, owner, fmt.Sprintf("rent-blocked-%02d", i), asset, rent.FeeToken, pb.OrderSide_BUY, 1, 200_000_000+int64(i))
	}
	sv, read := persistFundingFixture(sv)
	attempt := newRentFundingAttempt(sv, nil)
	result, err := attempt(fundingOwner, 20, big.NewInt(1200), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Deferred || result.ReceivedFB == nil || result.ReceivedFB.String() != "1200" {
		t.Fatalf("later eligible maker was not reached in one complete attempt: %+v", result)
	}
	if !result.Progress {
		t.Fatalf("blocked maker page did not make same-block progress: %+v", result)
	}
	if got := GetBalance(sv, blockedOwners[0], rent.FeeToken); got.Balance != "2" || got.OrderFrozenBalance != "0" && got.OrderFrozenBalance != "" {
		t.Fatalf("blocked maker rent/cancel did not persist outside taker rollback: %+v", got)
	}
	var state rent.RentState
	data, exists, err := sv.Get(keys.KeyRentState(blockedOwners[0]))
	if err != nil || !exists || json.Unmarshal(data, &state) != nil || state.UnpaidFB == "" || state.UnpaidFB == "0" {
		t.Fatalf("skipped maker debt was not persisted: exists=%v state=%+v err=%v", exists, state, err)
	}
	if _, exists, _ := sv.Get(keys.KeyOrderState("rent-blocked-00")); exists {
		t.Fatal("rent-blocked maker remained at the head of the next page")
	}
	AuditGlobalConservationDiff(sv.Diff(), 20, read)
}

func TestRentFundingCleansSelfAndStalePageBeforeLaterLiquidity(t *testing.T) {
	sv, asset, pair := fundingFixture(t, true, 5000)
	giveBalance(t, sv, fundingOwner, rent.FeeToken, big.NewInt(1))
	fundingOrder(t, sv, fundingOwner, "aaa-self-000", rent.FeeToken, asset, pb.OrderSide_SELL, 1, 100_000_000)
	for i := 0; i < rentFundingMakerLimit; i++ {
		state := &pb.OrderState{OrderId: fmt.Sprintf("aaa-stale-%03d", i), BaseToken: rent.FeeToken, QuoteToken: asset,
			Side: pb.OrderSide_SELL, Amount: "1", Price: "100000000", Status: pb.OrderStateStatus_ORDER_OPEN}
		indexKey, ok := spotOrderPriceIndexKey(state)
		if !ok {
			t.Fatal("failed to build stale price index")
		}
		sv.Set(indexKey, []byte{1})
	}
	sv, read := persistFundingFixture(sv)
	attempt := newRentFundingAttempt(sv, nil)
	first, err := attempt(fundingOwner, 10, big.NewInt(1200), nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.Deferred || !first.Progress || first.ReceivedFB == nil || first.ReceivedFB.String() != "1200" {
		t.Fatalf("blocking page did not complete in the same attempt: %+v", first)
	}
	if _, exists, _ := sv.Get(keys.KeyOrderState("aaa-self-000")); exists {
		t.Fatal("self maker was not safely cancelled")
	}
	for i := 0; i < rentFundingMakerLimit; i++ {
		state := &pb.OrderState{OrderId: fmt.Sprintf("aaa-stale-%03d", i), BaseToken: rent.FeeToken, QuoteToken: asset,
			Side: pb.OrderSide_SELL, Amount: "1", Price: "100000000", Status: pb.OrderStateStatus_ORDER_OPEN}
		indexKey, _ := spotOrderPriceIndexKey(state)
		if _, exists, _ := sv.Get(indexKey); exists {
			t.Fatalf("stale index %d survived bounded cleanup", i)
		}
	}
	trades, err := sv.Scan(keys.KeyTradeRecordPrefix(pair))
	if err != nil || len(trades) != 1 {
		t.Fatalf("unexpected conversion history: trades=%d err=%v", len(trades), err)
	}
	AuditGlobalConservationDiff(sv.Diff(), 10, read)
}

func TestRentFundingFullStalePageTerminatesWithoutLiquidity(t *testing.T) {
	sv, asset, _ := fundingFixture(t, true, 5000)
	validData, exists, err := sv.Get(keys.KeyOrderState("rent-test-maker"))
	if err != nil || !exists {
		t.Fatalf("missing fixture maker: exists=%v err=%v", exists, err)
	}
	var valid pb.OrderState
	if err := unmarshalProtoCompat(validData, &valid); err != nil {
		t.Fatal(err)
	}
	if key, ok := spotOrderPriceIndexKey(&valid); ok {
		sv.Del(key)
	}
	sv.Del(keys.KeyOrderState(valid.OrderId))
	for i := 0; i < rentFundingMakerLimit+1; i++ {
		state := &pb.OrderState{OrderId: fmt.Sprintf("stale-only-%03d", i), BaseToken: rent.FeeToken, QuoteToken: asset,
			Side: pb.OrderSide_SELL, Amount: "1", Price: "100000000", Status: pb.OrderStateStatus_ORDER_OPEN}
		indexKey, ok := spotOrderPriceIndexKey(state)
		if !ok {
			t.Fatal("failed to build stale price index")
		}
		sv.Set(indexKey, []byte{1})
	}
	sv, _ = persistFundingFixture(sv)
	attempt := newRentFundingAttempt(sv, nil)
	var result rent.FundingResult
	for page := 0; page < 3; page++ {
		result, err = attempt(fundingOwner, 10, big.NewInt(1200), &rent.RentWork{Phase: result.Phase, Cursor: result.Cursor})
		if err != nil {
			t.Fatal(err)
		}
		if !result.Deferred {
			break
		}
		if !result.Progress || result.NextActionHeight != 10 {
			t.Fatalf("stale page deferred without same-block progress: %+v", result)
		}
	}
	if result.Deferred || result.ReceivedFB != nil && result.ReceivedFB.Sign() != 0 {
		t.Fatalf("exhausted stale-only market did not terminate: %+v", result)
	}
}

func TestRentFundingCancelsNominallyZeroDustMakerPage(t *testing.T) {
	sv, asset, _ := fundingFixture(t, true, 5000)
	for i := 0; i < rentFundingMakerLimit; i++ {
		owner := fmt.Sprintf("dust-maker-%02d", i)
		id := fmt.Sprintf("rent-dust-%02d", i)
		giveSpotAccount(t, sv, owner)
		SetBalance(sv, owner, rent.FeeToken, &pb.TokenBalance{OrderFrozenBalance: "1"})
		order := &pb.OrderState{OrderId: id, Owner: owner, BaseToken: rent.FeeToken, QuoteToken: asset,
			Side: pb.OrderSide_SELL, Amount: "1", FilledBase: "0", Price: "1", Status: pb.OrderStateStatus_ORDER_OPEN}
		if err := writeSpotOrderSnapshot(sv, order); err != nil {
			t.Fatal(err)
		}
	}
	sv, read := persistFundingFixture(sv)
	attempt := newRentFundingAttempt(sv, nil)
	first, err := attempt(fundingOwner, 10, big.NewInt(1200), nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.Deferred || !first.Progress || first.ReceivedFB == nil || first.ReceivedFB.String() != "1200" {
		t.Fatalf("dust page did not complete: %+v", first)
	}
	if _, exists, _ := sv.Get(keys.KeyOrderState("rent-dust-00")); exists {
		t.Fatal("zero-notional dust maker remained open")
	}
	if got := GetBalance(sv, "dust-maker-00", rent.FeeToken); got.Balance != "1" || got.OrderFrozenBalance != "0" && got.OrderFrozenBalance != "" {
		t.Fatalf("dust maker funds were not returned exactly: %+v", got)
	}
	AuditGlobalConservationDiff(sv.Diff(), 10, read)
}

func TestRentFundingPostFillProbeCancelsRentBlockedTail(t *testing.T) {
	sv, asset, pair := fundingFixture(t, false, 1)
	for i := 1; i < rentFundingMakerLimit; i++ {
		fundingOrder(t, sv, "maker", fmt.Sprintf("rent-valid-%03d", i), asset, rent.FeeToken, pb.OrderSide_BUY, 1, 100_000_000)
	}
	blocked := "fbc1qpostfillblocked00000000000000000000000"
	seedMakerRentDebt(t, sv, blocked)
	fundingOrder(t, sv, blocked, "rent-blocked-tail", asset, rent.FeeToken, pb.OrderSide_BUY, 100, 95_000_000)
	sv, read := persistFundingFixture(sv)
	result, err := newRentFundingAttempt(sv, nil)(fundingOwner, 20, big.NewInt(40), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.ReceivedFB == nil || result.ReceivedFB.String() != "32" || result.Deferred {
		t.Fatalf("rent-blocked tail caused false liquidity deferral: %+v", result)
	}
	if !result.Progress || !rentFundingContainsOwner(result.TouchedOwners, blocked) {
		t.Fatalf("post-fill cleanup was not reported: %+v", result)
	}
	if _, exists, _ := sv.Get(keys.KeyOrderState("rent-blocked-tail")); exists {
		t.Fatal("rent-blocked tail remained in the orderbook")
	}
	trades, err := sv.Scan(keys.KeyTradeRecordPrefix(pair))
	if err != nil || len(trades) != rentFundingMakerLimit {
		t.Fatalf("unexpected filled trade count=%d err=%v", len(trades), err)
	}
	AuditGlobalConservationDiff(sv.Diff(), 20, read)
}

func TestRentFundingTradeRecordsDoNotCollideAcrossOwners(t *testing.T) {
	sv, asset, pair := fundingFixture(t, false, 10000)
	second := fundingOwner + "2"
	giveSpotAccount(t, sv, second)
	giveBalance(t, sv, second, asset, big.NewInt(5000))
	sv, read := persistFundingFixture(sv)
	attempt := newRentFundingAttempt(sv, nil)
	for _, owner := range []string{fundingOwner, second} {
		if result, err := attempt(owner, 10, nil, nil); err != nil || result.ReceivedFB == nil {
			t.Fatalf("owner=%s result=%+v err=%v", owner, result, err)
		}
	}
	trades, err := sv.Scan(keys.KeyTradeRecordPrefix(pair))
	if err != nil || len(trades) != 2 {
		t.Fatalf("trades=%d err=%v", len(trades), err)
	}
	AuditGlobalConservationDiff(sv.Diff(), 10, read)
}

type rentFundingReadErrorView struct {
	StateView
	key string
}

func (s rentFundingReadErrorView) Get(key string) ([]byte, bool, error) {
	if key == s.key {
		return nil, false, errors.New("storage unavailable")
	}
	return s.StateView.Get(key)
}

func TestRentFundingDoesNotTreatReadFailureAsNoLiquidity(t *testing.T) {
	for _, key := range []string{keys.KeyAccount(fundingOwner), keys.KeyFreeze(fundingOwner, fundingAsset(t))} {
		sv, _, _ := fundingFixture(t, false, 5000)
		sv, _ = persistFundingFixture(sv)
		_, err := newRentFundingAttempt(rentFundingReadErrorView{StateView: sv, key: key}, nil)(fundingOwner, 10, nil, nil)
		if err == nil || !strings.Contains(err.Error(), "storage unavailable") {
			t.Fatalf("read error was swallowed: %v", err)
		}
		if got := GetBalance(sv, fundingOwner, fundingAsset(t)).Balance; got != "5000" {
			t.Fatalf("asset mutated on failed swap: %s", got)
		}
	}
}

func TestRentFundingRepeatedCallsDoNotExhaustClosureBudget(t *testing.T) {
	sv, _, _ := fundingFixture(t, false, 5000)
	sv, _ = persistFundingFixture(sv)
	attempt := newRentFundingAttempt(sv, nil)
	for i := 0; i < 100; i++ {
		if _, err := attempt(fmt.Sprintf("fbc1qbudget%02d", i), 10, nil, nil); err != nil {
			t.Fatalf("bounded call %d: %v", i, err)
		}
	}
	result, err := attempt(fundingOwner, 10, big.NewInt(1200), nil)
	if err != nil || result.ReceivedFB == nil || result.ReceivedFB.String() != "1200" {
		t.Fatalf("later same-block owner was starved: result=%+v err=%v", result, err)
	}
}

func TestRentActivationIncludesIncomingAndFrozenEligibleAsset(t *testing.T) {
	sv := newMemStateView()
	asset := fundingAsset(t)
	SetBalance(sv, fundingOwner, asset, &pb.TokenBalance{Balance: "0", OrderFrozenBalance: "8000"})
	transfer := &pb.Transaction{Base: &pb.BaseMessage{FromAddress: "sender"}, To: fundingOwner, TokenAddress: asset, Amount: "5000"}
	activated, err := maybeApplyRentActivationFee(sv, transfer)
	if err != nil || activated {
		t.Fatalf("incoming threshold must waive activation without sender FB: activated=%v err=%v", activated, err)
	}
	transfer.Amount = "4999"
	if _, err := maybeApplyRentActivationFee(sv, transfer); err == nil {
		t.Fatal("below threshold must retain existing activation rule")
	}
}

func TestProcessRentHooksIncludesFinalizedBalanceWrites(t *testing.T) {
	sv, asset, _ := fundingFixture(t, false, 5000)
	sv, read := persistFundingFixture(sv)
	                                                                         
	                                                        
	SetBalance(sv, fundingOwner, asset, GetBalance(sv, fundingOwner, asset))
	_, _, err := (&Executor{}).processRentHooks(&pb.Block{Header: &pb.BlockHeader{Height: 10}}, sv, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := GetBalance(sv, fundingOwner, rent.FeeToken).Balance; got != "5000" {
		t.Fatalf("finalized hook deposit was not funded: %s", got)
	}
	AuditGlobalConservationDiff(sv.Diff(), 10, read)
}

func TestRentFundingFallbackSweepsFutureOrdersWithoutColdHistory(t *testing.T) {
	initial := newMemStateView()
	asset := fundingAsset(t)
	giveSpotAccount(t, initial, fundingOwner)
	pair := utils.GeneratePairKey(rent.FeeToken, asset)
	config, err := protocol.EncodeMarketIdentity(rent.FeeToken, asset)
	if err != nil {
		t.Fatal(err)
	}
	initial.Set(keys.KeyFutureMarketConfig(pair), config)
	for _, token := range []string{asset, rent.FeeToken} {
		initial.Set(keys.KeyToken(token), []byte{1})
	}
	                                                                    
	for i := 0; i <= rentFundingFutureScan; i++ {
		owner, id := "other", fmt.Sprintf("%04d", i)
		if i == rentFundingFutureScan {
			owner = fundingOwner
		}
		order := &pb.FutureOrderState{OrderId: id, OwnerAddress: owner, Pair: pair, Price: "100000000", Amount: "5000",
			Side: int32(pb.OrderSide_BUY), Leverage: 1, Status: pb.FutureOrderStatus_FUTURE_ORDER_OPEN, CreateHeight: 1}
		if err := writeFutureOrderSnapshot(initial, order); err != nil {
			t.Fatal(err)
		}
		if owner == fundingOwner {
			reserveFutureOrderMarginForTest(t, initial, owner, asset, id, big.NewInt(5000))
		}
	}
	var finalStates []map[string][]byte
	for _, withHistory := range []bool{true, false} {
		seed := newMemStateView()
		for _, op := range initial.Diff() {
			if !op.Del && (withHistory || keys.IsStatefulKey(op.Key)) {
				seed.Set(op.Key, op.Value)
			}
		}
		sv, read := persistFundingFixture(seed)
		rt := rent.NewRuntime(rentStateViewStore{sv: sv}, rent.WithCleaner(vmRentCleaner{sv: sv}), rent.WithFundingAttempt(newRentFundingAttempt(sv, nil)))
		if err := rt.ProcessBlock(10, []string{fundingOwner}); err != nil {
			t.Fatalf("history=%v: %v", withHistory, err)
		}
		if _, exists, _ := sv.Get(keys.KeyFutureOrderState(fmt.Sprintf("%04d", rentFundingFutureScan))); exists {
			t.Fatal("same-block cursor continuation left an open order behind")
		}
		if got := GetBalance(sv, rent.RecycleFundAccount, asset).Balance; got != "5000" {
			t.Fatalf("recycled=%s", got)
		}
		AuditGlobalConservationDiff(sv.Diff(), 10, read)
		all, err := sv.Scan("")
		if err != nil {
			t.Fatal(err)
		}
		state := make(map[string][]byte)
		for key, value := range all {
			if keys.IsStatefulKey(key) {
				state[key] = value
			}
		}
		finalStates = append(finalStates, state)
	}
	if !reflect.DeepEqual(finalStates[0], finalStates[1]) {
		t.Fatal("cleanup depends on cold history missing after snapshot sync")
	}
}
