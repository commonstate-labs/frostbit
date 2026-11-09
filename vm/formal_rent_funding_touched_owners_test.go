package vm

import (
	"dex/keys"
	"dex/pb"
	"dex/rent"
	"encoding/json"
	"fmt"
	"math/big"
	"testing"
)

type formalRentSweepCursorRecorder struct {
	StateView
	cursors map[string][]string
}

func (r *formalRentSweepCursorRecorder) ScanWithLimitOrderedAfter(prefix, startAfter string, limit int, reverse bool) ([]KVPair, error) {
	if r.cursors == nil {
		r.cursors = make(map[string][]string)
	}
	r.cursors[prefix] = append(r.cursors[prefix], startAfter)
	if scanner, ok := r.StateView.(orderedStateScannerAfter); ok {
		return scanner.ScanWithLimitOrderedAfter(prefix, startAfter, limit, reverse)
	}
	return scanStateViewOrderedAfter(r.StateView, prefix, startAfter, limit, reverse)
}

                                                                   
                                                                              
                                                                               
                                                                          
                                               
func TestFormalRentFundingReportsMakersBeyondPreparedPage(t *testing.T) {
	sv, asset, pair := fundingFixture(t, false, 5000)

	const (
		smallMaker   = "fbc1qformalsmallmaker00000000000000000000000"
		blockedMaker = "fbc1qformalblockedmaker000000000000000000000"
	)
	giveBalance(t, sv, smallMaker, rent.FeeToken, big.NewInt(1000))
	fundingOrder(t, sv, smallMaker, "zzzz-formal-small", asset, rent.FeeToken, pb.OrderSide_BUY, 1, 105_000_000)

	                                                                           
	                                                                            
	                                                         
	for i := 0; i < rentFundingMakerLimit-1; i++ {
		state := &pb.OrderState{
			OrderId: fmt.Sprintf("zyy-formal-stale-%02d", i), BaseToken: asset,
			QuoteToken: rent.FeeToken, Side: pb.OrderSide_BUY, Amount: "1",
			Price: fmt.Sprint(104_999_999 - i), Status: pb.OrderStateStatus_ORDER_OPEN,
		}
		indexKey, ok := spotOrderPriceIndexKey(state)
		if !ok {
			t.Fatal("failed to build formal stale index")
		}
		sv.Set(indexKey, []byte{1})
	}

	seedMakerRentDebt(t, sv, blockedMaker)
	fundingOrder(t, sv, blockedMaker, "zxx-formal-blocked-beyond-page", asset, rent.FeeToken, pb.OrderSide_BUY, 1, 104_000_000)
	sv, read := persistFundingFixture(sv)
	prepared, err := scanStateViewOrderedAfter(
		sv, keys.KeyOrderPriceIndexPrefix(pair, pb.OrderSide_BUY, false), "", rentFundingMakerLimit, true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared) > rentFundingMakerLimit {
		prepared = prepared[:rentFundingMakerLimit]
	}
	preparedIDs := make([]string, 0, len(prepared))
	for _, item := range prepared {
		preparedIDs = append(preparedIDs, extractOrderIDFromKey(item.Key))
		if extractOrderIDFromKey(item.Key) == "zxx-formal-blocked-beyond-page" {
			t.Fatalf("fixture error: blocked maker is inside the prepared page: %v", preparedIDs)
		}
	}

	result, err := newRentFundingAttempt(sv, nil)(fundingOwner, 20, big.NewInt(10), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Deferred || result.ReceivedFB == nil || result.ReceivedFB.Sign() <= 0 {
		t.Fatalf("formal funding did not complete: %+v", result)
	}
	if !rentFundingContainsOwner(result.TouchedOwners, blockedMaker) {
		t.Fatalf("matcher committed rent state for %s without reporting it in TouchedOwners: %v", blockedMaker, result.TouchedOwners)
	}
	AuditGlobalConservationDiff(sv.Diff(), 20, read)
}

                                                                         
                                                                           
                                                                              
                                                                         
func TestFormalRentSweepUsesStrictMonotonicCursor(t *testing.T) {
	const (
		owner       = "fbc1qformalsweepcursor000000000000000000000"
		height      = uint64(25)
		balanceRows = 17
		pageSize    = 4
	)
	base := newMemStateView()
	base.Set(keys.KeyAccount(owner), []byte("account"))
	state := rent.RentState{
		Version: rent.RentStateVersion, Owner: owner,
		LastSettledHeight: height, LastAccruedHeight: height,
		UnpaidFB: "1", Mode: rent.RentModeNeedsFunding,
		ExpireHeight: height, NominalFB: "0",
		Work: &rent.RentWork{NextActionHeight: height, Phase: rent.RentWorkFunding},
	}
	encoded, err := json.Marshal(&state)
	if err != nil {
		t.Fatal(err)
	}
	base.Set(keys.KeyRentState(owner), encoded)
	base.Set(keys.KeyRentExpiry(height, owner), []byte(owner))
	rent.MarkOwnerInitialized(base, owner)
	rent.MarkOwnerActivity(base, owner, rent.OwnerMarkerNonFBBalance)
	for i := 0; i < balanceRows; i++ {
		SetBalance(base, owner, fmt.Sprintf("FORMAL%02d", i), &pb.TokenBalance{Balance: "1"})
	}

	recorder := &formalRentSweepCursorRecorder{StateView: base}
	runtime := rent.NewRuntime(
		rentStateViewStore{sv: recorder},
		rent.WithPolicy(rent.Policy{
			Token: rent.FeeToken, ExemptMin: big.NewInt(10),
			ExemptAssets: []rent.ExemptAsset{}, RentPerBlock: big.NewInt(1),
			RecycleAccount: rent.RecycleFundAccount,
		}),
		rent.WithRuntimeSweepBudget(pageSize, pageSize),
	)
	if err := runtime.ProcessBlock(height, nil); err != nil {
		t.Fatal(err)
	}
	cursors := recorder.cursors[keys.KeyBalancePrefix(owner)]
	if len(cursors) < 2 || cursors[0] != "" {
		t.Fatalf("balance sweep did not exercise multiple ordered pages: %q", cursors)
	}
	for i := 1; i < len(cursors); i++ {
		if cursors[i] == "" || cursors[i] <= cursors[i-1] {
			t.Fatalf("balance sweep cursor did not advance strictly: %q", cursors)
		}
	}
	if _, exists, err := recorder.Get(keys.KeyAccount(owner)); err != nil || exists {
		t.Fatalf("sweep did not complete: exists=%v err=%v", exists, err)
	}
}
