package vm

import (
	"dex/future"
	"dex/keys"
	"dex/pb"
	"encoding/json"
	"math/big"
	"testing"

	"google.golang.org/protobuf/proto"
)

                                                                                
                                                                          
                                                                           
                                                      
func TestProcessFutureBlockHooks_FundingExhaustionLiquidationTerminalMirror(t *testing.T) {
	const pair = "BTC_USDT"
	height := future.DefaultFundingInterval
	scaled := func(v int64) *big.Int {
		return new(big.Int).Mul(big.NewInt(v), future.PriceScale)
	}
	entryPrice := scaled(100)
	quantity := new(big.Int).Set(future.PriceScale)
	                                                                   
	                                                                
	initialMargin := new(big.Int).Mul(future.PriceScale, big.NewInt(6))
	initialMargin.Quo(initialMargin, big.NewInt(10))

	tests := []struct {
		name          string
		side          future.Side
		futureEMA     *big.Int
		wantRate      int64
		wantCloseSide pb.OrderSide
	}{
		{
			name:          "long pays positive funding",
			side:          future.Long,
			futureEMA:     scaled(101),
			wantRate:      future.DefaultMaxFundingRateBps,
			wantCloseSide: pb.OrderSide_SELL,
		},
		{
			name:          "short pays negative funding",
			side:          future.Short,
			futureEMA:     scaled(99),
			wantRate:      -future.DefaultMaxFundingRateBps,
			wantCloseSide: pb.OrderSide_BUY,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sv := newMemStateView()
			owner := "funding-terminal-owner"
			positionID := "funding-terminal-position"
			sv.Set(keys.KeyFutureMarketConfig(pair), []byte(pair))
			sv.Set(keys.KeyFutureSpotEMA(pair), entryPrice.Bytes())
			sv.Set(keys.KeyFutureEMA(pair), tc.futureEMA.Bytes())
			sv.Set(keys.KeyFutureMarkPrice(pair), entryPrice.Bytes())
			scheduleFutureFundingDue(sv, pair, height)

			pos := &future.Position{
				ID:                     positionID,
				Owner:                  owner,
				Pair:                   pair,
				Side:                   tc.side,
				EntryPrice:             new(big.Int).Set(entryPrice),
				EntryNotionalNumerator: new(big.Int).Mul(entryPrice, quantity),
				Quantity:               new(big.Int).Set(quantity),
				Margin:                 new(big.Int).Set(initialMargin),
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
				InitialMargin:          new(big.Int).Set(initialMargin),
			}
			if future.ShouldLiquidatePosition(pos, pos.Margin, entryPrice, future.DefaultMaintenanceMarginRateBps) {
				t.Fatal("test precondition invalid: position is already liquidatable before funding")
			}
			posStore := &svPositionStore{sv: sv}
			if err := posStore.SetPosition(pos); err != nil {
				t.Fatal(err)
			}

			liqPrice := future.CalcLiquidationPrice(tc.side, entryPrice, initialMargin, quantity, future.DefaultMaintenanceMarginRateBps)
			if err := (&svLiquidationIndex{sv: sv}).Upsert(positionID, pair, tc.side, liqPrice); err != nil {
				t.Fatal(err)
			}
			indexKeyBytes, exists, err := sv.Get(keys.KeyFutureLiqPosRef(positionID))
			if err != nil || !exists {
				t.Fatalf("seed liquidation index: exists=%v err=%v", exists, err)
			}
			indexKey := string(indexKeyBytes)

			stats, err := processFutureBlockHooksWithStats(sv, height, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if stats.FundingSettledPairs != 1 {
				t.Errorf("funding settled pairs=%d, want 1", stats.FundingSettledPairs)
			}

			for _, key := range []string{
				keys.KeyFuturePosition(positionID),
				keys.KeyFutureOwnerPosition(owner, positionID),
				keys.KeyFuturePairPosition(pair, positionID),
				keys.KeyFutureOwnerPairSidePosition(owner, pair, int32(tc.side)),
				keys.KeyFutureLiqPosRef(positionID),
				indexKey,
				keys.KeyAssetClaim(owner, "USDT", rentClaimFuturePositionMargin, positionID),
				keys.KeyAssetClaimTotal(owner, "USDT"),
			} {
				if _, exists, getErr := sv.Get(key); getErr != nil || exists {
					t.Errorf("funding-liquidated position retained live key %q: exists=%v err=%v", key, exists, getErr)
				}
			}

			historyData, exists, err := sv.Get(keys.KeyFuturePositionHistory(positionID))
			if err != nil || !exists {
				t.Fatalf("funding-liquidated history missing: exists=%v err=%v", exists, err)
			}
			var history pb.FuturePositionState
			if err := proto.Unmarshal(historyData, &history); err != nil {
				t.Fatal(err)
			}
			if history.Status != int32(future.StatusLiquidated) || history.Quantity != "0" || history.Margin != "0" {
				t.Errorf("terminal position is not fully closed: status=%d qty=%s margin=%s", history.Status, history.Quantity, history.Margin)
			}
			if history.CloseHeight != height {
				t.Errorf("close height=%d, want %d", history.CloseHeight, height)
			}
			wantFunding := new(big.Int).Neg(new(big.Int).Set(initialMargin)).String()
			if history.SettledFunding != wantFunding || history.LastSettledFunding != wantFunding {
				t.Errorf("settled funding total/last=%s/%s, want %s/%s", history.SettledFunding, history.LastSettledFunding, wantFunding, wantFunding)
			}
			if history.LastCumulativeFunding != big.NewInt(tc.wantRate).String() {
				t.Errorf("last cumulative funding=%s, want %d", history.LastCumulativeFunding, tc.wantRate)
			}
			if history.ClosedQty != quantity.String() {
				t.Errorf("closed quantity=%s, want %s", history.ClosedQty, quantity)
			}

			insuranceData, exists, err := sv.Get(keys.KeyFutureInsuranceFund(pair))
			if err != nil || !exists {
				t.Errorf("insurance fund missing after collected funding: exists=%v err=%v", exists, err)
			} else if got := new(big.Int).SetBytes(insuranceData); got.Cmp(initialMargin) != 0 {
				t.Errorf("insurance fund=%s, want collected margin %s", got, initialMargin)
			}

			tradeID := "liq_" + positionID
			tradeTsKey, _ := deterministicTradeTime(height, 0)
			tradeData, tradeExists, tradeErr := sv.Get(keys.KeyFutureTradeRecord(pair, tradeTsKey, tradeID))
			if tradeErr != nil || !tradeExists {
				t.Errorf("funding-triggered liquidation trade missing: exists=%v err=%v", tradeExists, tradeErr)
			} else {
				var trade pb.TradeRecord
				if err := proto.Unmarshal(tradeData, &trade); err != nil {
					t.Errorf("decode liquidation trade: %v", err)
				} else if trade.TakerSide != tc.wantCloseSide {
					t.Errorf("liquidation trade side=%s, want %s", trade.TakerSide, tc.wantCloseSide)
				}
			}

			orderKey := keys.KeyFutureAccountOrder(owner, pair, pb.FutureOrderStatus_FUTURE_ORDER_LIQUIDATED, height, tradeID)
			orderData, orderExists, orderErr := sv.Get(orderKey)
			if orderErr != nil || !orderExists {
				t.Errorf("funding-triggered liquidation order history missing: exists=%v err=%v", orderExists, orderErr)
			} else {
				var order pb.FutureOrderState
				if err := proto.Unmarshal(orderData, &order); err != nil {
					t.Errorf("decode liquidation order: %v", err)
				} else if order.Side != int32(tc.wantCloseSide) {
					t.Errorf("liquidation order side=%d, want %d", order.Side, tc.wantCloseSide)
				}
			}

			activities, scanErr := sv.Scan(keys.KeyUserActivityHeightPrefix(owner, height))
			if scanErr != nil {
				t.Errorf("scan user activities: %v", scanErr)
			} else {
				var fundingSeen, liquidationSeen bool
				for _, data := range activities {
					var activity UserActivityRecord
					if err := json.Unmarshal(data, &activity); err != nil {
						t.Errorf("decode user activity: %v", err)
						continue
					}
					fundingSeen = fundingSeen || activity.Kind == ActKindFundingSettle
					liquidationSeen = liquidationSeen || activity.Kind == ActKindLiquidation
				}
				if !fundingSeen {
					t.Errorf("funding settlement activity missing")
				}
				if !liquidationSeen {
					t.Errorf("funding-triggered liquidation activity missing")
				}
			}
		})
	}
}
