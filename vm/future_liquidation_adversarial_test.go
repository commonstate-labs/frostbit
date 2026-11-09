package vm

import (
	"dex/db"
	"dex/future"
	"dex/keys"
	"dex/pb"
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
)

                                                                               
                                                                               
                                                                               
                                                        
type liquidationWriteFailureView struct {
	StateView
	failGetKey string
	setKeys    map[string]int
}

func (v *liquidationWriteFailureView) Get(key string) ([]byte, bool, error) {
	if key == v.failGetKey {
		return nil, false, fmt.Errorf("injected terminal position rent-claim read failure for %s", key)
	}
	return v.StateView.Get(key)
}

func (v *liquidationWriteFailureView) Set(key string, value []byte) {
	if v.setKeys == nil {
		v.setKeys = make(map[string]int)
	}
	v.setKeys[key]++
	v.StateView.Set(key, value)
}

func TestCheckFutureLiquidations_ShortFullMirrorState(t *testing.T) {
	const (
		pair       = "SHORT_MIRROR_USDT"
		owner      = "short-owner"
		positionID = "short-full-mirror"
		height     = uint64(77)
	)
	unit := new(big.Int).Set(future.PriceScale)
	entryPrice := new(big.Int).Mul(big.NewInt(100), unit)
	markPrice := new(big.Int).Add(new(big.Int).Mul(big.NewInt(100), unit), big.NewInt(60_000_000))         
	quantity := new(big.Int).Set(unit)
	margin := new(big.Int).Set(unit)
	initialInsurance := new(big.Int).Mul(big.NewInt(3), unit)

	sv := newMemStateView()
	sv.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
	sv.Set(keys.KeyFutureMarkPrice(pair), markPrice.Bytes())
	sv.Set(keys.KeyFutureLiqIndexBackfillDone(pair), []byte{1})
	sv.Set(keys.KeyFutureInsuranceFund(pair), initialInsurance.Bytes())
	SetBalance(sv, owner, "USDT", &pb.TokenBalance{Balance: "123"})

	pos := adversarialLiquidationPosition(positionID, owner, pair, future.Short, entryPrice, quantity, margin)
	store := &svPositionStore{sv: sv}
	if err := store.SetPosition(pos); err != nil {
		t.Fatalf("seed short position: %v", err)
	}
	eng := newFutureEngine(sv, height)
	cfg, err := eng.GetMarketConfig(pair)
	if err != nil {
		t.Fatal(err)
	}
	eng.RefreshLiquidationIndex(pos, cfg)
	refData, refExists, err := sv.Get(keys.KeyFutureLiqPosRef(positionID))
	if err != nil || !refExists || len(refData) == 0 {
		t.Fatalf("seed short liquidation index: exists=%v ref=%q err=%v", refExists, refData, err)
	}
	liqIndexKey := string(refData)

	if err := checkFutureLiquidations(sv, pair, height); err != nil {
		t.Fatalf("liquidate short: %v", err)
	}

	for _, key := range []string{
		keys.KeyFuturePosition(positionID),
		keys.KeyFutureOwnerPosition(owner, positionID),
		keys.KeyFuturePairPosition(pair, positionID),
		keys.KeyFutureOwnerPairSidePosition(owner, pair, int32(future.Short)),
		keys.KeyFutureLiqPosRef(positionID),
		liqIndexKey,
		keys.KeyAssetClaim(owner, "USDT", rentClaimFuturePositionMargin, positionID),
		keys.KeyAssetClaimTotal(owner, "USDT"),
	} {
		if _, exists, getErr := sv.Get(key); getErr != nil || exists {
			t.Fatalf("terminal short left live key %q: exists=%v err=%v", key, exists, getErr)
		}
	}

	historyData, exists, err := sv.Get(keys.KeyFuturePositionHistory(positionID))
	if err != nil || !exists {
		t.Fatalf("short liquidation history missing: exists=%v err=%v", exists, err)
	}
	var history pb.FuturePositionState
	if err := proto.Unmarshal(historyData, &history); err != nil {
		t.Fatal(err)
	}
	if history.Side != int32(future.Short) ||
		future.PositionStatus(history.Status) != future.StatusLiquidated ||
		history.Quantity != "0" || history.Margin != "0" ||
		history.ClosedQty != quantity.String() ||
		history.RealizedPnl != new(big.Int).Neg(margin).String() ||
		history.ExitNotional != future.CalcNotional(quantity, markPrice).String() ||
		history.CloseHeight != height {
		t.Fatalf("short terminal history is not the long-path mirror: %+v", &history)
	}

	insuranceData, exists, err := sv.Get(keys.KeyFutureInsuranceFund(pair))
	if err != nil || !exists {
		t.Fatalf("insurance balance missing: exists=%v err=%v", exists, err)
	}
	wantInsurance := new(big.Int).Add(initialInsurance, margin)
	if got := new(big.Int).SetBytes(insuranceData); got.Cmp(wantInsurance) != 0 {
		t.Fatalf("short liquidation insurance=%s, want %s", got, wantInsurance)
	}
	if _, exists, err := sv.Get(keys.KeyFutureInsuranceShortfall(pair)); err != nil || exists {
		t.Fatalf("non-bankrupt short created ADL shortfall: exists=%v err=%v", exists, err)
	}
	if got := GetBalance(sv, owner, "USDT").Balance; got != "123" {
		t.Fatalf("short liquidation changed available balance: got %q want 123", got)
	}

	tradeRows, err := sv.Scan(keys.KeyFutureTradeRecordPrefix(pair))
	if err != nil || len(tradeRows) != 1 {
		t.Fatalf("short liquidation trades=%d err=%v, want 1", len(tradeRows), err)
	}
	for _, data := range tradeRows {
		var trade pb.TradeRecord
		if err := proto.Unmarshal(data, &trade); err != nil {
			t.Fatal(err)
		}
		if trade.TradeId != "liq_"+positionID || trade.TakerSide != pb.OrderSide_BUY ||
			trade.Price != markPrice.String() || trade.Amount != quantity.String() || trade.Height != height {
			t.Fatalf("short liquidation trade is not a BUY close: %+v", &trade)
		}
	}

	orderID := "liq_" + positionID
	orderKey := keys.KeyFutureAccountOrder(owner, pair, pb.FutureOrderStatus_FUTURE_ORDER_LIQUIDATED, height, orderID)
	orderData, exists, err := sv.Get(orderKey)
	if err != nil || !exists {
		t.Fatalf("short liquidation order history missing: exists=%v err=%v", exists, err)
	}
	var order pb.FutureOrderState
	if err := proto.Unmarshal(orderData, &order); err != nil {
		t.Fatal(err)
	}
	if order.Side != int32(pb.OrderSide_BUY) || !order.IsClose ||
		order.ClosePositionId != positionID || order.Status != pb.FutureOrderStatus_FUTURE_ORDER_LIQUIDATED {
		t.Fatalf("short liquidation order is not a BUY close: %+v", &order)
	}
	if _, exists, err := sv.Get(keys.KeyFutureOrderState(orderID)); err != nil || exists {
		t.Fatalf("terminal liquidation order leaked into open StateDB: exists=%v err=%v", exists, err)
	}

	activities, err := sv.Scan(keys.KeyUserActivityPrefix(owner))
	if err != nil || len(activities) != 1 {
		t.Fatalf("short liquidation activities=%d err=%v, want 1", len(activities), err)
	}
	for _, data := range activities {
		var activity UserActivityRecord
		if err := json.Unmarshal(data, &activity); err != nil {
			t.Fatal(err)
		}
		if activity.Kind != ActKindLiquidation || activity.Height != height ||
			activity.Amount != quantity.String() || activity.Token != pair || activity.Ref != positionID {
			t.Fatalf("unexpected short liquidation activity: %+v", activity)
		}
	}
}

func TestCheckFutureLiquidations_MidRoundWriteFailureRollsBackBothSidesAndHistory(t *testing.T) {
	tests := []struct {
		name      string
		side      future.Side
		markPrice func(*big.Int) *big.Int
	}{
		{
			name: "long",
			side: future.Long,
			markPrice: func(unit *big.Int) *big.Int {
				return new(big.Int).Add(new(big.Int).Mul(big.NewInt(99), unit), big.NewInt(40_000_000))        
			},
		},
		{
			name: "short",
			side: future.Short,
			markPrice: func(unit *big.Int) *big.Int {
				return new(big.Int).Add(new(big.Int).Mul(big.NewInt(100), unit), big.NewInt(60_000_000))         
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			const height = uint64(88)
			pair := "ROLLBACK_" + strings.ToUpper(tc.name) + "_USDT"
			unit := new(big.Int).Set(future.PriceScale)
			entryPrice := new(big.Int).Mul(big.NewInt(100), unit)
			markPrice := tc.markPrice(unit)
			quantity := new(big.Int).Set(unit)
			margin := new(big.Int).Set(unit)

			base := newMemStateView()
			base.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
			base.Set(keys.KeyFutureMarkPrice(pair), markPrice.Bytes())
			base.Set(keys.KeyFutureLiqIndexBackfillDone(pair), []byte{1})
			base.Set(keys.KeyFutureInsuranceFund(pair), new(big.Int).Mul(big.NewInt(7), unit).Bytes())

			store := &svPositionStore{sv: base}
			eng := newFutureEngine(base, height)
			cfg, err := eng.GetMarketConfig(pair)
			if err != nil {
				t.Fatal(err)
			}
			ownersByID := make(map[string]string, 2)
			for _, id := range []string{"position-a", "position-z"} {
				owner := tc.name + "-owner-" + id
				ownersByID[id] = owner
				SetBalance(base, owner, "USDT", &pb.TokenBalance{Balance: "123"})
				pos := adversarialLiquidationPosition(id, owner, pair, tc.side, entryPrice, quantity, margin)
				if err := store.SetPosition(pos); err != nil {
					t.Fatalf("seed %s: %v", id, err)
				}
				eng.RefreshLiquidationIndex(pos, cfg)
			}

			ordered, err := eng.FindAtRiskPositions(pair, markPrice)
			if err != nil {
				t.Fatal(err)
			}
			ordered = onlyPositionIDs(ordered, ownersByID)
			if len(ordered) != 2 {
				t.Fatalf("at-risk order=%v, want both positions", ordered)
			}
			firstID, failingID := ordered[0], ordered[1]
			firstOwner, failingOwner := ownersByID[firstID], ownersByID[failingID]

			makerSide := pb.OrderSide_BUY
			if tc.side == future.Short {
				makerSide = pb.OrderSide_SELL
			}
			seedAdversarialLiquidationMaker(t, base, pair, "rollback-maker", "rollback-maker-order", makerSide, markPrice, quantity)
			probeSnap := base.Snapshot()
			takerSide := pb.OrderSide_SELL
			if tc.side == future.Short {
				takerSide = pb.OrderSide_BUY
			}
			probeTrades, probeErr := MatchFutureOrderForLiquidationAtHeight(
				base, pair, takerSide, quantity, markPrice, true,
				LiquidationOrderbookBufferBps, firstOwner, height,
			)
			if revertErr := base.Revert(probeSnap); revertErr != nil {
				t.Fatal(revertErr)
			}
			if probeErr != nil || len(probeTrades) != 1 {
				makerData, _, _ := base.Get(keys.KeyFutureOrderState("rollback-maker-order"))
				maker, parseErr := parseMakerOrderSnapshot(makerData, MarketFuture)
				covers := futureMakerReserveCoversTrade(base, pair, firstOwner, maker, quantity, false)
				t.Fatalf("maker precondition failed: trades=%d err=%v parse=%v covers=%v maker=%+v", len(probeTrades), probeErr, parseErr, covers, maker)
			}

			before := snapshotLiquidationState(t, base)
			tradeTsKey, _ := deterministicTradeTime(height, 0)
			firstHistoryKey := keys.KeyFuturePositionHistory(firstID)
			firstTradeKey := keys.KeyFutureTradeRecord(pair, tradeTsKey, "liq_ob_"+firstID+"_0")
			firstOrderKey := keys.KeyFutureAccountOrder(firstOwner, pair, pb.FutureOrderStatus_FUTURE_ORDER_LIQUIDATED, height, "liq_ob_"+firstID)
			firstActivityKey := keys.KeyUserActivity(firstOwner, height, 0)

			view := &liquidationWriteFailureView{
				StateView:  base,
				failGetKey: keys.KeyAssetClaim(failingOwner, "USDT", rentClaimFuturePositionMargin, failingID),
				setKeys:    make(map[string]int),
			}
			if err := checkFutureLiquidations(view, pair, height); err == nil {
				t.Fatal("expected second terminal position write to fail")
			}

			for _, key := range []string{firstHistoryKey, firstTradeKey, firstOrderKey, firstActivityKey} {
				if view.setKeys[key] == 0 {
					t.Fatalf("first liquidation never reached write %q before second failed; writes=%v", key, view.setKeys)
				}
			}
			after := snapshotLiquidationState(t, base)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("failed %s liquidation round retained state changes:\nbefore=%v\nafter=%v", tc.name, before, after)
			}

			                                                                    
			                                                                        
			for _, id := range ordered {
				owner := ownersByID[id]
				for _, key := range []string{
					keys.KeyFuturePosition(id),
					keys.KeyFutureOwnerPosition(owner, id),
					keys.KeyFuturePairPosition(pair, id),
					keys.KeyFutureOwnerPairSidePosition(owner, pair, int32(tc.side)),
					keys.KeyFutureLiqPosRef(id),
					keys.KeyAssetClaim(owner, "USDT", rentClaimFuturePositionMargin, id),
				} {
					if _, exists, getErr := base.Get(key); getErr != nil || !exists {
						t.Fatalf("rollback lost live %s key %q: exists=%v err=%v", id, key, exists, getErr)
					}
				}
				if _, exists, getErr := base.Get(keys.KeyFuturePositionHistory(id)); getErr != nil || exists {
					t.Fatalf("rollback retained terminal history for %s: exists=%v err=%v", id, exists, getErr)
				}
				if rows, scanErr := base.Scan(keys.KeyUserActivityPrefix(owner)); scanErr != nil || len(rows) != 0 {
					t.Fatalf("rollback retained activity for %s: rows=%d err=%v", id, len(rows), scanErr)
				}
			}
			if rows, scanErr := base.Scan(keys.KeyFutureTradeRecordPrefix(pair)); scanErr != nil || len(rows) != 0 {
				t.Fatalf("rollback retained liquidation trades: rows=%d err=%v", len(rows), scanErr)
			}
			for _, owner := range []string{firstOwner, failingOwner} {
				if rows, scanErr := base.Scan(keys.KeyFutureAccountOrderOwnerPrefix(owner)); scanErr != nil || len(rows) != 0 {
					t.Fatalf("rollback retained liquidation order history for %s: rows=%d err=%v", owner, len(rows), scanErr)
				}
			}
		})
	}
}

func adversarialLiquidationPosition(id, owner, pair string, side future.Side, entryPrice, quantity, margin *big.Int) *future.Position {
	return &future.Position{
		ID:                     id,
		Owner:                  owner,
		Pair:                   pair,
		Side:                   side,
		EntryPrice:             new(big.Int).Set(entryPrice),
		EntryNotionalNumerator: new(big.Int).Mul(entryPrice, quantity),
		Quantity:               new(big.Int).Set(quantity),
		Margin:                 new(big.Int).Set(margin),
		MarginBasisAmount:      new(big.Int).Set(margin),
		MarginBasisClosed:      new(big.Int),
		Status:                 future.StatusOpen,
		OpenHeight:             1,
		LastCumulativeFunding:  new(big.Int),
		FirstCumulativeFunding: new(big.Int),
		SettledFunding:         new(big.Int),
		LastSettledFunding:     new(big.Int),
		RealizedPnL:            new(big.Int),
		ADLWithheldPnL:         new(big.Int),
		ClosedQty:              new(big.Int),
		ExitNotional:           new(big.Int),
		ClosePnLRemainder:      new(big.Int),
		MaxQuantity:            new(big.Int).Set(quantity),
		InitialMargin:          new(big.Int).Set(margin),
	}
}

func seedAdversarialLiquidationMaker(t *testing.T, sv StateView, pair, owner, orderID string, side pb.OrderSide, price, quantity *big.Int) {
	t.Helper()
	const leverage = uint64(100)
	makerPositionSide := future.Long
	if side == pb.OrderSide_SELL {
		makerPositionSide = future.Short
	}
	state := &pb.FutureOrderState{
		OrderId: orderID, Owner: owner, OwnerAddress: owner, Pair: pair,
		Side: int32(makerPositionSide), Price: price.String(), Amount: quantity.String(),
		FilledBase: "0", FilledQuote: "0", Leverage: leverage,
		Status: pb.FutureOrderStatus_FUTURE_ORDER_OPEN, CreateHeight: 1,
	}
	if err := writeFutureOrderSnapshot(sv, state); err != nil {
		t.Fatal(err)
	}
	reserved := future.CalcRequiredMargin(quantity, price, leverage)
	SetBalance(sv, owner, "USDT", &pb.TokenBalance{Balance: "999999999999999", OrderFrozenBalance: reserved.String()})
	sv.Set(keys.KeyFutureOrderReservedMargin(orderID), reserved.Bytes())
	priceKey, err := db.PriceToKey128(price.String())
	if err != nil {
		t.Fatal(err)
	}
	data, err := proto.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyFutureOrderPriceIndex(pair, side, false, priceKey, orderID), data)
}

func onlyPositionIDs(ids []string, ownersByID map[string]string) []string {
	out := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if _, ok := ownersByID[id]; !ok {
			continue
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func snapshotLiquidationState(t *testing.T, sv StateView) map[string][]byte {
	t.Helper()
	rows, err := sv.Scan("")
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string][]byte, len(rows))
	for key, value := range rows {
		out[key] = append([]byte(nil), value...)
	}
	return out
}
