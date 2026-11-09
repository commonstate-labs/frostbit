package vm

import (
	"dex/keys"
	"dex/pb"
	"dex/rent"
	"encoding/json"
	"math/big"
	"testing"

	"google.golang.org/protobuf/proto"
)

const rentBlockedMaker = "fbc1qrentblockedmaker000000000000000000000000"

func seedMakerRentDebt(t *testing.T, sv StateView, owner string) {
	t.Helper()
	SetBalance(sv, owner, rent.FeeToken, &pb.TokenBalance{Balance: "1000"})
	state := rent.RentState{
		Version: rent.RentStateVersion, Owner: owner, LastAccruedHeight: 1,
		Mode: rent.RentModeCharging, ExpireHeight: 11, NominalFB: "1000",
		Work: &rent.RentWork{NextActionHeight: 11, Phase: rent.RentWorkSettle},
	}
	data, err := json.Marshal(&state)
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyRentState(owner), data)
	sv.Set(keys.KeyRentExpiry(11, owner), []byte(owner))
}

func assertRentSpotMakerOpen(t *testing.T, sv StateView, orderID string) {
	t.Helper()
	data, exists, err := sv.Get(keys.KeyOrderState(orderID))
	if err != nil || !exists {
		t.Fatalf("spot maker missing: exists=%v err=%v", exists, err)
	}
	var state pb.OrderState
	if err := proto.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if state.Status != pb.OrderStateStatus_ORDER_OPEN || state.IsFilled || state.FilledBase != "0" {
		t.Fatalf("spot maker changed: %+v", &state)
	}
}

func TestSpotMatchingSettlesAndSkipsMakerWithUnpaidRent(t *testing.T) {
	sv := newMemStateView()
	price := big.NewInt(100_000_000)
	qty := big.NewInt(10)
	placeSpotMaker(t, sv, "rent-blocked-spot", rentBlockedMaker, pb.OrderSide_SELL, price, qty)
	placeSpotMaker(t, sv, "healthy-spot", "healthy-maker", pb.OrderSide_SELL, big.NewInt(110_000_000), qty)
	seedMakerRentDebt(t, sv, rentBlockedMaker)

	trades, err := MatchSpotOrderAtHeight(sv, selfTradePair, pb.OrderSide_BUY, qty, big.NewInt(110_000_000), false, 0, "taker", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(trades) != 1 || trades[0].MakerOrderID != "healthy-spot" {
		t.Fatalf("unpaid maker was not skipped: %+v", trades)
	}
	if got := GetBalance(sv, rentBlockedMaker, rent.FeeToken).Balance; got != "0" && got != "" {
		t.Fatalf("maker rent was not paid from available FB: %s", got)
	}
	if got := GetBalance(sv, rent.RecycleFundAccount, rent.FeeToken).Balance; got != "1000" {
		t.Fatalf("rent fund=%s want=1000", got)
	}
	data, exists, err := sv.Get(keys.KeyRentState(rentBlockedMaker))
	if err != nil || !exists {
		t.Fatalf("rent state missing: exists=%v err=%v", exists, err)
	}
	var state rent.RentState
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if state.UnpaidFB != "900" || state.LastAccruedHeight != 20 {
		t.Fatalf("unexpected maker rent state: %+v", state)
	}
	assertRentSpotMakerOpen(t, sv, "rent-blocked-spot")
}

func TestMakerRentBarrierBlocksFundingAndSweepModesWithoutDebt(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state rent.RentState
	}{
		{
			name: "available FB is exactly consumed by accrued rent",
			state: rent.RentState{
				Version: rent.RentStateVersion, Owner: rentBlockedMaker, LastAccruedHeight: 10,
				Mode: rent.RentModeCharging, ExpireHeight: 20, NominalFB: "1000",
				Work: &rent.RentWork{NextActionHeight: 20, Phase: rent.RentWorkSettle},
			},
		},
		{
			name: "cleanup is already in progress",
			state: rent.RentState{
				Version: rent.RentStateVersion, Owner: rentBlockedMaker, LastAccruedHeight: 20,
				Mode: rent.RentModeSweeping, ExpireHeight: 20, NominalFB: "1000",
				Work: &rent.RentWork{NextActionHeight: 20, Phase: rent.RentWorkSweepClaims},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sv := newMemStateView()
			SetBalance(sv, rentBlockedMaker, rent.FeeToken, &pb.TokenBalance{Balance: "1000"})
			data, err := json.Marshal(&tc.state)
			if err != nil {
				t.Fatal(err)
			}
			sv.Set(keys.KeyRentState(rentBlockedMaker), data)
			sv.Set(keys.KeyRentExpiry(tc.state.ExpireHeight, rentBlockedMaker), []byte(rentBlockedMaker))

			allowed, err := makerRentCanTrade(sv, rentBlockedMaker, 20, 0)
			if err != nil {
				t.Fatal(err)
			}
			if allowed {
				t.Fatal("maker in funding/cleanup mode was allowed to trade")
			}
		})
	}
}

func TestFutureLiquidationDoesNotWaitForMakerRent(t *testing.T) {
	sv := newMemStateView()
	price := big.NewInt(100_000_000)
	qty := big.NewInt(10)
	placeFutureMakerAtHeight(t, sv, "rent-blocked-future", rentBlockedMaker, pb.OrderSide_SELL, price, qty, 1)
	seedMakerRentDebt(t, sv, rentBlockedMaker)

	ordinary, err := MatchFutureOrderAtHeight(sv, selfTradePair, pb.OrderSide_BUY, qty, price, false, 0, "ordinary-taker", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(ordinary) != 0 {
		t.Fatalf("ordinary match consumed an unpaid maker: %+v", ordinary)
	}
	assertFutureMakerOpen(t, sv, "rent-blocked-future", pb.OrderSide_SELL, price, "0")

	liquidation, err := MatchFutureOrderForLiquidationAtHeight(sv, selfTradePair, pb.OrderSide_BUY, qty, price, true, 0, "liquidated-owner", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(liquidation) != 1 || liquidation[0].MakerOrderID != "rent-blocked-future" {
		t.Fatalf("forced liquidation was blocked by maker rent: %+v", liquidation)
	}
}
