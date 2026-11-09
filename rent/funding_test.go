package rent

import (
	"dex/keys"
	"dex/pb"
	"encoding/json"
	"errors"
	"math/big"
	"testing"

	"google.golang.org/protobuf/proto"
)

func testRentState(t *testing.T, store memStore, owner string, height, expiry uint64, nominal string) {
	t.Helper()
	data, err := json.Marshal(&RentState{Owner: owner, LastSettledHeight: height, ExpireHeight: expiry, NominalFB: nominal})
	if err != nil {
		t.Fatal(err)
	}
	store[keys.KeyRentState(owner)] = data
	store[keys.KeyRentExpiry(expiry, owner)] = []byte(owner)
	store[keys.KeyAccount(owner)] = []byte("account")
}

func loadTestRentState(t *testing.T, store memStore, owner string) RentState {
	t.Helper()
	var state RentState
	if err := json.Unmarshal(store[keys.KeyRentState(owner)], &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestExternalRentExemptionPreservesOldDebt(t *testing.T) {
	for _, kind := range []string{"frozen", "claim"} {
		t.Run(kind, func(t *testing.T) {
			store := memStore{}
			owner := "fbc1qexempt"
			asset := DefaultPolicy().ExemptAssets[0]
			if kind == "frozen" {
				data, err := proto.Marshal(&pb.TokenBalanceRecord{Address: owner, Token: asset.Token, Balance: &pb.TokenBalance{OrderFrozenBalance: asset.Minimum.String()}})
				if err != nil {
					t.Fatal(err)
				}
				store[keys.KeyBalance(owner, asset.Token)] = data
			} else if err := SetClaim(store, owner, asset.Token, "future_position_margin", "pos1", asset.Minimum); err != nil {
				t.Fatal(err)
			}
			testRentState(t, store, owner, 1, 2, "0")
			runtime := NewRuntime(store)
			settled, err := runtime.SettleRent(owner, 2)
			if err != nil {
				t.Fatal(err)
			}
			if settled.Projection.Mode != RentModeExemptDebt || settled.Projection.UnpaidRentFB != "100" {
				t.Fatalf("old debt was erased by exemption: %+v", settled.Projection)
			}
			if _, exists := store[keys.KeyRentState(owner)]; !exists {
				t.Fatal("exempt debt must retain rent state")
			}
			status, err := NewService(store).RentStatus(owner, 20)
			if err != nil {
				t.Fatal(err)
			}
			if status.State != "needs_funding" || status.PendingRentFB != "100" || status.ExemptAsset != asset.Token || status.ExemptAssetMin != asset.Minimum.String() || status.ExemptAssetTotal != asset.Minimum.String() {
				t.Fatalf("unexpected API exemption %+v", status)
			}
		})
	}
}

func TestExternalRentThresholdsAreIndependentAndExact(t *testing.T) {
	store := memStore{}
	owner := "fbc1qseparate"
	policy := DefaultPolicy()
	for _, asset := range policy.ExemptAssets[:2] {
		amount := new(big.Int).Mul(asset.Minimum, big.NewInt(6))
		amount.Div(amount, big.NewInt(10))
		writeTestBalance(t, store, owner, asset.Token, amount.String())
	}
	writeTestBalance(t, store, owner, "BTC", "999999999999999999999")
	status, err := NewService(store).RentStatus(owner, 1)
	if err != nil || status.State == "exempt" {
		t.Fatalf("two subthreshold assets or a symbol must not exempt: %+v %v", status, err)
	}
	asset := policy.ExemptAssets[0]
	writeTestBalance(t, store, owner, asset.Token, new(big.Int).Sub(asset.Minimum, big.NewInt(1)).String())
	evaluation, err := NewService(store).IsExempt(owner, policy)
	if err != nil || evaluation.ExemptAsset != "" {
		t.Fatalf("one unit below threshold exempt: %+v %v", evaluation, err)
	}
	writeTestBalance(t, store, owner, asset.Token, asset.Minimum.String())
	evaluation, err = NewService(store).IsExempt(owner, policy)
	if err != nil || evaluation.ExemptAsset != asset.Token {
		t.Fatalf("exact threshold must exempt: %+v %v", evaluation, err)
	}
}

func TestValidFBLeaseNeverAttemptsFunding(t *testing.T) {
	store := memStore{}
	owner := "fbc1qvalid"
	writeTestBalance(t, store, owner, FeeToken, "1000")
	testRentState(t, store, owner, 1, 11, "1000")
	runtime := NewRuntime(store, WithFundingAttempt(func(string, uint64) (FundingResult, error) {
		t.Fatal("valid lease must not sell other assets")
		return FundingResult{}, nil
	}))
	if err := runtime.ProcessBlock(10, []string{owner}); err != nil {
		t.Fatal(err)
	}
	if state := loadTestRentState(t, store, owner); state.ExpireHeight != 11 {
		t.Fatalf("unchanged lease was renewed: %+v", state)
	}
}

func TestExpiredRentRenewsOnlyFromNewlyReceivedFB(t *testing.T) {
	store := memStore{}
	owner := "fbc1qrenew"
	writeTestBalance(t, store, owner, FeeToken, "1000")
	testRentState(t, store, owner, 1, 11, "1000")
	attempts := 0
	runtime := NewRuntime(store, WithFundingAttempt(func(got string, height uint64) (FundingResult, error) {
		attempts++
		if got != owner || height != 11 {
			t.Fatalf("unexpected funding target %s/%d", got, height)
		}
		writeTestBalance(t, store, owner, FeeToken, "1200")
		return FundingResult{ReceivedFB: big.NewInt(200)}, nil
	}))
	if err := runtime.ProcessBlock(11, nil); err != nil {
		t.Fatal(err)
	}
	state := loadTestRentState(t, store, owner)
	if attempts != 1 || state.NominalFB != "1200" || state.ExpireHeight != 23 || state.LastSettledHeight != 11 {
		t.Fatalf("wrong renewal: attempts=%d state=%+v", attempts, state)
	}
	if _, exists := store[keys.KeyRentExpiry(11, owner)]; exists {
		t.Fatal("old expiry index retained")
	}
	if err := runtime.ProcessBlock(12, []string{owner}); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || loadTestRentState(t, store, owner).ExpireHeight != 23 {
		t.Fatal("old nominal FB was incorrectly given a second lease")
	}
}

func TestFundingRefreshesLaterMakerCandidateAndNewTouchedOwner(t *testing.T) {
	store := memStore{}
	alice, bob, maker := "fbc1qa", "fbc1qb", "fbc1qmaker"
	asset := DefaultPolicy().ExemptAssets[0]
	for _, owner := range []string{alice, bob} {
		store[keys.KeyAccount(owner)] = []byte("account")
	}
	attempts := 0
	runtime := NewRuntime(store, WithFundingAttempt(func(owner string, height uint64) (FundingResult, error) {
		attempts++
		if owner != alice {
			t.Fatalf("later newly exempt candidate must not be sold: %s", owner)
		}
		writeTestBalance(t, store, alice, FeeToken, "1000")
		writeTestBalance(t, store, bob, asset.Token, asset.Minimum.String())
		writeTestBalance(t, store, maker, FeeToken, "500")
		return FundingResult{ReceivedFB: big.NewInt(1000), TouchedOwners: []string{bob, maker}}, nil
	}))
	if err := runtime.ProcessBlock(1, []string{bob, alice}); err != nil {
		t.Fatal(err)
	}
	if attempts != 1 || runtime.LastStats().SweptAccounts != 0 {
		t.Fatalf("stale plan applied: attempts=%d stats=%+v", attempts, runtime.LastStats())
	}
	if _, exists := store[keys.KeyAccount(bob)]; !exists {
		t.Fatal("newly exempt maker was swept")
	}
	if state := loadTestRentState(t, store, maker); state.ExpireHeight != 6 {
		t.Fatalf("newly touched maker was not evaluated: %+v", state)
	}
}

func TestFundingAttemptsMakerThatLosesInitialExemption(t *testing.T) {
	store := memStore{}
	owner, maker := "fbc1qa", "fbc1qb"
	asset := DefaultPolicy().ExemptAssets[0]
	store[keys.KeyAccount(owner)] = []byte("account")
	store[keys.KeyAccount(maker)] = []byte("account")
	writeTestBalance(t, store, maker, FeeToken, DefaultRentExemptMinRaw)
	attempts := make(map[string]int)
	runtime := NewRuntime(store, WithFundingAttempt(func(target string, height uint64) (FundingResult, error) {
		attempts[target]++
		if attempts[target] > 1 {
			t.Fatalf("owner attempted twice in one block: %s", target)
		}
		switch target {
		case owner:
			writeTestBalance(t, store, owner, FeeToken, "1000")
			writeTestBalance(t, store, maker, FeeToken, "0")
			writeTestBalance(t, store, maker, asset.Token, "100")
			return FundingResult{ReceivedFB: big.NewInt(1000), TouchedOwners: []string{maker}}, nil
		case maker:
			writeTestBalance(t, store, maker, FeeToken, "500")
			return FundingResult{ReceivedFB: big.NewInt(500), TouchedOwners: []string{owner, maker}}, nil
		default:
			t.Fatalf("unexpected funding owner %s", target)
			return FundingResult{}, nil
		}
	}))
	if err := runtime.ProcessBlock(1, []string{maker, owner}); err != nil {
		t.Fatal(err)
	}
	if attempts[owner] != 1 || attempts[maker] != 1 || runtime.LastStats().SweptAccounts != 0 {
		t.Fatalf("maker missed funding attempt: attempts=%v stats=%+v", attempts, runtime.LastStats())
	}
	if state := loadTestRentState(t, store, maker); state.ExpireHeight != 6 {
		t.Fatalf("wrong maker lease %+v", state)
	}
}

func TestFailedFundingKeepsOriginalSweepAndPropagatesErrors(t *testing.T) {
	for _, callbackError := range []bool{false, true} {
		t.Run(map[bool]string{false: "no_liquidity", true: "storage_error"}[callbackError], func(t *testing.T) {
			store := memStore{}
			owner := "fbc1qfail"
			asset := DefaultPolicy().ExemptAssets[0]
			writeTestBalance(t, store, owner, asset.Token, "100")
			store[keys.KeyAccount(owner)] = []byte("account")
			failure := errors.New("storage failure")
			runtime := NewRuntime(store, WithFundingAttempt(func(string, uint64) (FundingResult, error) {
				if callbackError {
					return FundingResult{}, failure
				}
				return FundingResult{}, nil
			}))
			err := runtime.ProcessBlock(1, []string{owner})
			if callbackError {
				if !errors.Is(err, failure) {
					t.Fatalf("callback error lost: %v", err)
				}
				if _, exists := store[keys.KeyAccount(owner)]; !exists {
					t.Fatal("storage error was treated as a sweep")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := testBalanceTotal(t, store, RecycleFundAccount, asset.Token); got != "100" {
				t.Fatalf("original sweep did not recycle unsold asset: %s", got)
			}
		})
	}
}

func TestFundingCannotTurnUnfreezingIntoNewRent(t *testing.T) {
	store := memStore{}
	owner := "fbc1qunfreeze"
	data, _ := proto.Marshal(&pb.TokenBalanceRecord{Address: owner, Token: FeeToken, Balance: &pb.TokenBalance{OrderFrozenBalance: "1000"}})
	store[keys.KeyBalance(owner, FeeToken)] = data
	testRentState(t, store, owner, 1, 11, "1000")
	runtime := NewRuntime(store, WithFundingAttempt(func(string, uint64) (FundingResult, error) {
		writeTestBalance(t, store, owner, FeeToken, "1000")
		return FundingResult{ReceivedFB: big.NewInt(1000)}, nil
	}))
	if err := runtime.ProcessBlock(11, nil); err == nil {
		t.Fatal("unfreezing existing FB must not be accepted as new rent")
	}
}

func TestFBExemptionSkipsExternalAssetPrefetch(t *testing.T) {
	store := memStore{}
	owner := "fbc1qfunded"
	writeTestBalance(t, store, owner, FeeToken, DefaultRentExemptMinRaw)
	runtime := NewRuntime(store)
	if err := runtime.ProcessBlock(1, []string{owner}); err != nil {
		t.Fatal(err)
	}
	if got := runtime.LastStats().PrefetchedAssetKeys; got != 3 {
		t.Fatalf("FB-exempt owner prefetched external assets: %d keys", got)
	}
}

func TestCompleteOwnerMarkersSkipEmptyClaimAndExternalAssetPrefetch(t *testing.T) {
	store := memStore{}
	owner := "fbc1qmarkerprefetch"
	store[keys.KeyAccount(owner)] = []byte("account")
	MarkOwnerInitialized(store, owner)
	if _, exists := store[keys.KeyRentOwnerMarker(owner, OwnerMarkerInit)]; !exists {
		t.Fatal("fixture did not write complete owner marker")
	}
	runtime := NewRuntime(store)
	if err := runtime.ProcessBlock(1, []string{owner}); err != nil {
		t.Fatal(err)
	}
	                                                                     
	                                                                          
	                                                                         
	                                                                          
	if got := runtime.LastStats().PrefetchedAssetKeys; got != 3 {
		t.Fatalf("complete empty owner prefetched %d asset keys, want 3", got)
	}
}
