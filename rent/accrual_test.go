package rent

import (
	iface "dex/interfaces"
	"dex/keys"
	"dex/pb"
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"sort"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
)

func putAccrualState(t *testing.T, store memStore, state RentState) {
	t.Helper()
	data, err := json.Marshal(&state)
	if err != nil {
		t.Fatal(err)
	}
	store[keys.KeyRentState(state.Owner)] = data
	if key := rentStateWorkKey(&state); key != "" {
		store[key] = []byte(state.Owner)
	}
}

func cloneMemStore(in memStore) memStore {
	out := make(memStore, len(in))
	for key, value := range in {
		out[key] = append([]byte(nil), value...)
	}
	return out
}

func TestProjectRentIsReadOnlyAndReportsSpendableBalance(t *testing.T) {
	store := memStore{}
	owner := "fbc1qprojectrent"
	writeTestBalance(t, store, owner, FeeToken, "1000")
	putAccrualState(t, store, RentState{
		Version: RentStateVersion, Owner: owner, LastSettledHeight: 9, LastAccruedHeight: 10,
		Mode: RentModeCharging, ExpireHeight: 20, NominalFB: "1000",
		Work: &RentWork{NextActionHeight: 20, Phase: RentWorkSettle},
	})
	before := cloneMemStore(store)

	projection, err := NewService(store).ProjectRent(owner, 15, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if projection.PendingRentFB != "500" || projection.PayableFB != "500" || projection.UnpaidRentFB != "0" {
		t.Fatalf("unexpected projected rent: %+v", projection)
	}
	if projection.AvailableFB != "1000" || projection.SpendableFB != "500" || projection.EffectiveAvailableFB != "500" {
		t.Fatalf("unexpected spendable projection: %+v", projection)
	}
	if projection.ExpireHeight != 20 || projection.Mode != RentModeCharging {
		t.Fatalf("unexpected projected lifecycle: %+v", projection)
	}
	if projection.Height != 15 || projection.LastSettledHeight != 9 || projection.LastAccruedHeight != 10 {
		t.Fatalf("projection confused query height with persisted cursors: %+v", projection)
	}
	if !reflect.DeepEqual(store, before) {
		t.Fatal("ProjectRent mutated consensus state")
	}
}

func TestProjectRentWithoutStateHasNoPersistedCursor(t *testing.T) {
	store := memStore{}
	owner := "fbc1qprojectrentnew"
	writeTestBalance(t, store, owner, FeeToken, "1000")

	projection, err := NewService(store).ProjectRent(owner, 15, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if projection.Height != 15 || projection.LastSettledHeight != 0 || projection.LastAccruedHeight != 0 || projection.NewlyAccruedFB != "0" {
		t.Fatalf("new account reported a persisted rent cursor: %+v", projection)
	}
}

func TestProjectRentRejectsMalformedLockedBalance(t *testing.T) {
	store := memStore{}
	owner := "fbc1qmalformedrentbalance"
	record, err := proto.Marshal(&pb.TokenBalanceRecord{
		Address: owner, Token: FeeToken,
		Balance: &pb.TokenBalance{Balance: "100", OrderFrozenBalance: "not-an-amount"},
	})
	if err != nil {
		t.Fatal(err)
	}
	store[keys.KeyBalance(owner, FeeToken)] = record
	before := cloneMemStore(store)
	if _, err := NewService(store).ProjectRent(owner, 10, DefaultPolicy()); err == nil {
		t.Fatal("malformed locked balance was treated as zero")
	}
	if !reflect.DeepEqual(store, before) {
		t.Fatal("failed projection mutated state")
	}
}

func TestSettleRentTransfersExactlyOncePerHeight(t *testing.T) {
	store := memStore{}
	owner := "fbc1qsettlerent"
	writeTestBalance(t, store, owner, FeeToken, "1000")
	putAccrualState(t, store, RentState{
		Version: RentStateVersion, Owner: owner, LastAccruedHeight: 10,
		Mode: RentModeCharging, ExpireHeight: 20, NominalFB: "1000",
		Work: &RentWork{NextActionHeight: 20, Phase: RentWorkSettle},
	})
	runtime := NewRuntime(store)
	first, err := runtime.SettleRent(owner, 15)
	if err != nil {
		t.Fatal(err)
	}
	if first.PaidFB != "500" || testBalanceTotal(t, store, owner, FeeToken) != "500" || testBalanceTotal(t, store, RecycleFundAccount, FeeToken) != "500" {
		t.Fatalf("unexpected first settlement: %+v owner=%s fund=%s", first, testBalanceTotal(t, store, owner, FeeToken), testBalanceTotal(t, store, RecycleFundAccount, FeeToken))
	}
	if first.Projection.LastSettledHeight != 15 || first.Projection.LastAccruedHeight != 15 {
		t.Fatalf("settlement projection did not expose its newly persisted cursors: %+v", first.Projection)
	}
	second, err := runtime.SettleRent(owner, 15)
	if err != nil {
		t.Fatal(err)
	}
	if second.PaidFB != "0" || testBalanceTotal(t, store, owner, FeeToken) != "500" || testBalanceTotal(t, store, RecycleFundAccount, FeeToken) != "500" {
		t.Fatalf("same height charged twice: %+v", second)
	}
	state := loadTestRentState(t, store, owner)
	if state.LastAccruedHeight != 15 || state.UnpaidFB != "0" || state.ExpireHeight != 20 || state.Work == nil || state.Work.NextActionHeight != 20 {
		t.Fatalf("unexpected persisted state: %+v", state)
	}
}

func TestSameHeightCreditPaysDebtWithoutSecondAccrual(t *testing.T) {
	store := memStore{}
	owner := "fbc1qsameheightcredit"
	putAccrualState(t, store, RentState{
		Version: RentStateVersion, Owner: owner, LastAccruedHeight: 15,
		UnpaidFB: "500", Mode: RentModeNeedsFunding, ExpireHeight: 15, NominalFB: "0",
		Work: &RentWork{NextActionHeight: 15, Phase: RentWorkFunding},
	})
	runtime := NewRuntime(store)
	writeTestBalance(t, store, owner, FeeToken, "300")
	partial, err := runtime.RefreshRentEligibility(owner, 15)
	if err != nil {
		t.Fatal(err)
	}
	if partial.PaidFB != "300" || partial.Projection.UnpaidRentFB != "200" || partial.Projection.NewlyAccruedFB != "0" {
		t.Fatalf("unexpected partial payment: %+v", partial)
	}
	writeTestBalance(t, store, owner, FeeToken, "500")
	final, err := runtime.RefreshRentEligibility(owner, 15)
	if err != nil {
		t.Fatal(err)
	}
	if final.PaidFB != "200" || final.Projection.UnpaidRentFB != "0" || final.Projection.SpendableFB != "300" || final.Projection.NewlyAccruedFB != "0" {
		t.Fatalf("unexpected final payment: %+v", final)
	}
	if testBalanceTotal(t, store, RecycleFundAccount, FeeToken) != "500" {
		t.Fatal("rent fund did not receive exactly the debt")
	}
}

func TestExemptCreditStopsNewAccrualButKeepsOldDebt(t *testing.T) {
	store := memStore{}
	owner := "fbc1qexemptdebt"
	asset := DefaultPolicy().ExemptAssets[0]
	writeTestBalance(t, store, owner, asset.Token, asset.Minimum.String())
	putAccrualState(t, store, RentState{
		Version: RentStateVersion, Owner: owner, LastAccruedHeight: 10,
		Mode: RentModeCharging, ExpireHeight: 10, NominalFB: "0",
		Work: &RentWork{NextActionHeight: 10, Phase: RentWorkSettle},
	})
	runtime := NewRuntime(store)
	settled, err := runtime.SettleRent(owner, 15)
	if err != nil {
		t.Fatal(err)
	}
	if settled.Projection.Mode != RentModeExemptDebt || settled.Projection.UnpaidRentFB != "500" || settled.Projection.RequiredFundingFB != "500" {
		t.Fatalf("exempt asset erased old debt: %+v", settled.Projection)
	}
	later, err := NewService(store).ProjectRent(owner, 25, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if later.NewlyAccruedFB != "0" || later.PendingRentFB != "500" || later.Mode != RentModeExemptDebt {
		t.Fatalf("exempt debt kept accruing: %+v", later)
	}
	writeTestBalance(t, store, owner, FeeToken, "300")
	partial, err := runtime.SettleRent(owner, 25)
	if err != nil {
		t.Fatal(err)
	}
	if partial.PaidFB != "300" || partial.Projection.UnpaidRentFB != "200" || partial.Projection.RequiredFundingFB != "200" || partial.Projection.NewlyAccruedFB != "0" {
		t.Fatalf("unexpected exempt debt payment: %+v", partial)
	}
}

func TestExemptDebtConversionCompletesNewLeaseInSameBlock(t *testing.T) {
	base := memStore{}
	store := orderedMemStore{memStore: base}
	owner := "fbc1qexemptdebtrequeue"
	asset := DefaultPolicy().ExemptAssets[0]
	base[keys.KeyAccount(owner)] = []byte("account")
	writeTestBalance(t, base, owner, asset.Token, asset.Minimum.String())
	putAccrualState(t, base, RentState{
		Version: RentStateVersion, Owner: owner, LastAccruedHeight: 10,
		UnpaidFB: "500", Mode: RentModeExemptDebt, ExpireHeight: 10, NominalFB: "0",
		Work: &RentWork{NextActionHeight: 10, Phase: RentWorkFunding},
	})
	attempts := 0
	runtime := NewRuntime(store, WithFundingAttempt(func(got string, height uint64, required *big.Int, _ *RentWork) (FundingResult, error) {
		attempts++
		if got != owner || height != 10 || required == nil {
			t.Fatalf("unexpected funding target owner=%s height=%d required=%v", got, height, required)
		}
		switch attempts {
		case 1:
			if required.String() != "500" {
				t.Fatalf("first required=%v", required)
			}
			writeTestBalance(t, base, owner, asset.Token, new(big.Int).Sub(asset.Minimum, big.NewInt(1)).String())
			writeTestBalance(t, base, owner, FeeToken, "500")
			return FundingResult{ReceivedFB: big.NewInt(500)}, nil
		case 2:
			if required.String() != DefaultActivationFeeRaw {
				t.Fatalf("second required=%v", required)
			}
			writeTestBalance(t, base, owner, FeeToken, DefaultActivationFeeRaw)
			return FundingResult{ReceivedFB: mustAmount(DefaultActivationFeeRaw)}, nil
		default:
			t.Fatalf("unexpected funding attempt %d", attempts)
			return FundingResult{}, nil
		}
	}))
	if err := runtime.ProcessBlock(10, nil); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("attempts=%d want=2", attempts)
	}
	if _, exists := base[keys.KeyAccount(owner)]; !exists {
		t.Fatal("real conversion progress was mistaken for confirmed no liquidity")
	}
	state := loadTestRentState(t, base, owner)
	if state.Mode != RentModeCharging || state.UnpaidFB != "0" || state.Work == nil ||
		state.Work.NextActionHeight <= 11 {
		t.Fatalf("same-block funding did not establish a new lease: %+v", state)
	}
	if testBalanceTotal(t, base, RecycleFundAccount, FeeToken) != "500" {
		t.Fatal("old debt was not paid before the new funding target")
	}
}

func TestFundingCallbackInvalidatesPrefetchForSkippedMakerSettlement(t *testing.T) {
	base := memStore{}
	store := orderedMemStore{memStore: base}
	ownerA := "fbc1qcachea"
	ownerB := "fbc1qcacheb"
	for _, owner := range []string{ownerA, ownerB} {
		base[keys.KeyAccount(owner)] = []byte("account")
	}
	putAccrualState(t, base, RentState{
		Version: RentStateVersion, Owner: ownerA, LastAccruedHeight: 10,
		Mode: RentModeNeedsFunding, ExpireHeight: 10, NominalFB: "0",
		Work: &RentWork{NextActionHeight: 10, Phase: RentWorkFunding},
	})
	writeTestBalance(t, base, ownerB, FeeToken, "400")
	putAccrualState(t, base, RentState{
		Version: RentStateVersion, Owner: ownerB, LastAccruedHeight: 5,
		Mode: RentModeCharging, ExpireHeight: 20, NominalFB: "400",
		Work: &RentWork{NextActionHeight: 20, Phase: RentWorkSettle},
	})
	attempts := make(map[string]int)
	runtime := NewRuntime(store, WithFundingAttempt(func(target string, height uint64, required *big.Int, _ *RentWork) (FundingResult, error) {
		attempts[target]++
		switch target {
		case ownerA:
			                                                                    
			                                           
			hidden, err := NewRuntime(store).SettleRent(ownerB, height)
			if err != nil {
				t.Fatal(err)
			}
			if hidden.PaidFB != "0" || hidden.Projection.UnpaidRentFB != "100" {
				t.Fatalf("unexpected hidden maker settlement: %+v", hidden)
			}
			if required == nil || required.String() != DefaultActivationFeeRaw {
				t.Fatalf("owner A required=%v", required)
			}
			writeTestBalance(t, base, ownerA, FeeToken, DefaultActivationFeeRaw)
			return FundingResult{ReceivedFB: mustAmount(DefaultActivationFeeRaw)}, nil
		case ownerB:
			return FundingResult{}, nil
		default:
			t.Fatalf("unexpected target %s", target)
			return FundingResult{}, nil
		}
	}))
	if err := runtime.ProcessBlock(10, []string{ownerA, ownerB}); err != nil {
		t.Fatal(err)
	}
	if attempts[ownerA] != 1 || attempts[ownerB] != 1 {
		t.Fatalf("attempts=%v", attempts)
	}
	if got := testBalanceTotal(t, base, RecycleFundAccount, FeeToken); got != "400" {
		t.Fatalf("skipped maker rent was charged more than once: %s", got)
	}
	if _, exists := base[keys.KeyAccount(ownerB)]; exists {
		t.Fatal("confirmed no-liquidity maker was not swept in the same block")
	}
	if _, exists := base[keys.KeyRentState(ownerB)]; exists {
		t.Fatal("swept maker retained rent state")
	}
}

type orderedMemStore struct{ memStore }

func (m orderedMemStore) ScanRentOrderedAfter(prefix, startAfter string, limit int) ([]iface.KVPair, error) {
	keysFound := make([]string, 0)
	for key := range m.memStore {
		if strings.HasPrefix(key, prefix) && (startAfter == "" || key > startAfter) {
			keysFound = append(keysFound, key)
		}
	}
	sort.Strings(keysFound)
	if limit > 0 && len(keysFound) > limit {
		keysFound = keysFound[:limit]
	}
	out := make([]iface.KVPair, 0, len(keysFound))
	for _, key := range keysFound {
		out = append(out, iface.KVPair{Key: key, Value: m.memStore[key]})
	}
	return out, nil
}

func TestDueQueueDrainsEveryOwnerAcrossPagesInSameBlock(t *testing.T) {
	base := memStore{}
	owners := []string{"fbc1qduea", "fbc1qdueb", "fbc1qduec"}
	for i, owner := range owners {
		base[keys.KeyAccount(owner)] = []byte("account")
		writeTestBalance(t, base, owner, FeeToken, "100")
		putAccrualState(t, base, RentState{
			Version: RentStateVersion, Owner: owner, LastAccruedHeight: 1,
			Mode: RentModeCharging, ExpireHeight: uint64(i + 2), NominalFB: "100",
			Work: &RentWork{NextActionHeight: uint64(i + 2), Phase: RentWorkSettle},
		})
	}
	store := orderedMemStore{memStore: base}
	attempts := make(map[string]int)
	runtime := NewRuntime(store, WithRuntimeScanLimit(2), WithFundingAttempt(func(owner string, _ uint64, _ *big.Int, _ *RentWork) (FundingResult, error) {
		attempts[owner]++
		return FundingResult{}, nil
	}))
	if err := runtime.ProcessBlock(10, nil); err != nil {
		t.Fatal(err)
	}
	for _, owner := range owners {
		if attempts[owner] != 1 {
			t.Fatalf("owner %s attempts=%d", owner, attempts[owner])
		}
		if _, exists := base[keys.KeyAccount(owner)]; exists {
			t.Fatalf("owner %s survived same-block due drain", owner)
		}
	}
	if got := runtime.LastStats().DueOwners; got != len(owners) {
		t.Fatalf("due owners=%d want=%d", got, len(owners))
	}
}

func TestDueFundingDrainsMoreThanSixtyFourOwnersInSameBlock(t *testing.T) {
	base := memStore{}
	store := orderedMemStore{memStore: base}
	const ownerCount = 130
	owners := make([]string, 0, ownerCount)
	for i := 0; i < ownerCount; i++ {
		owner := fmt.Sprintf("fbc1qfairqueue%03d", i)
		owners = append(owners, owner)
		base[keys.KeyAccount(owner)] = []byte("account")
		putAccrualState(t, base, RentState{
			Version: RentStateVersion, Owner: owner, LastAccruedHeight: 1,
			UnpaidFB: "100", Mode: RentModeNeedsFunding, ExpireHeight: 1, NominalFB: "0",
			Work: &RentWork{NextActionHeight: 1, Phase: RentWorkFunding},
		})
	}

	attempts := make(map[string]int, ownerCount)
	order := make([]string, 0, ownerCount)
	runtime := NewRuntime(store, WithFundingAttempt(func(owner string, _ uint64, _ *big.Int, _ *RentWork) (FundingResult, error) {
		attempts[owner]++
		order = append(order, owner)
		return FundingResult{}, nil
	}))
	if err := runtime.ProcessBlock(10, nil); err != nil {
		t.Fatal(err)
	}
	if len(order) != ownerCount {
		t.Fatalf("same-block funding attempts=%d want=%d", len(order), ownerCount)
	}
	if stats := runtime.LastStats(); stats.DueOwners != ownerCount || stats.SweepOwnerBudget != DefaultSweepOwnerBudget {
		t.Fatalf("owner page size became a block cap: %+v", stats)
	}
	for i, owner := range owners {
		if attempts[owner] != 1 || order[i] != owner {
			t.Fatalf("owner %s attempts=%d order[%d]=%s", owner, attempts[owner], i, order[i])
		}
		if _, exists := base[keys.KeyAccount(owner)]; exists {
			t.Fatalf("owner %s survived same-block drain", owner)
		}
	}
	assertNoDueAtOrBefore(t, base, 10)
	if got := countKeysWithPrefix(base, keys.KeyRentExpiryAllPrefix()); got != 0 {
		t.Fatalf("same-block drain retained %d rent work keys", got)
	}
}

func TestDueOrderingIsIndependentOfOwnerPageSize(t *testing.T) {
	build := func() memStore {
		base := memStore{}
		for _, item := range []struct {
			owner string
			due   uint64
		}{
			{"fbc1qpageordera", 1},
			{"fbc1qpageorderb", 2},
		} {
			base[keys.KeyAccount(item.owner)] = []byte("account")
			putAccrualState(t, base, RentState{
				Version: RentStateVersion, Owner: item.owner, LastAccruedHeight: item.due,
				Mode: RentModeNeedsFunding, ExpireHeight: item.due, NominalFB: "0",
				Work: &RentWork{NextActionHeight: item.due, Phase: RentWorkFunding},
			})
		}
		return base
	}
	run := func(pageSize int) (memStore, []string) {
		base := build()
		store := orderedMemStore{memStore: base}
		order := make([]string, 0, 3)
		created := false
		runtime := NewRuntime(store,
			WithRuntimeSweepBudget(pageSize, 10),
			WithFundingAttempt(func(owner string, _ uint64, _ *big.Int, _ *RentWork) (FundingResult, error) {
				order = append(order, owner)
				if owner == "fbc1qpageordera" && !created {
					created = true
					inserted := "fbc1qpageorderc"
					base[keys.KeyAccount(inserted)] = []byte("account")
					putAccrualState(t, base, RentState{
						Version: RentStateVersion, Owner: inserted, LastAccruedHeight: 1,
						Mode: RentModeNeedsFunding, ExpireHeight: 1, NominalFB: "0",
						Work: &RentWork{NextActionHeight: 1, Phase: RentWorkFunding},
					})
					return FundingResult{TouchedOwners: []string{inserted}}, nil
				}
				return FundingResult{}, nil
			}),
		)
		if err := runtime.ProcessBlock(10, nil); err != nil {
			t.Fatalf("page size %d: %v", pageSize, err)
		}
		return base, order
	}

	oneState, oneOrder := run(1)
	wideState, wideOrder := run(DefaultSweepOwnerBudget)
	wantOrder := []string{"fbc1qpageordera", "fbc1qpageorderb", "fbc1qpageorderc"}
	if !reflect.DeepEqual(oneOrder, wantOrder) || !reflect.DeepEqual(wideOrder, wantOrder) {
		t.Fatalf("page-size-dependent due order: one=%v wide=%v", oneOrder, wideOrder)
	}
	if !reflect.DeepEqual(oneState, wideState) {
		t.Fatalf("page-size-dependent final state: one=%v wide=%v", oneState, wideState)
	}
}

func TestDynamicCanonicalDueWorkKeepsGlobalKeyOrder(t *testing.T) {
	run := func(pageSize int) []string {
		base := memStore{}
		ownerA := "fbc1qdynamicordera"
		ownerB := "fbc1qzzdynamicorder"
		ownerC := "fbc1q00dynamicorder"
		for _, item := range []struct {
			owner string
			due   uint64
		}{{ownerA, 1}, {ownerB, 10}} {
			base[keys.KeyAccount(item.owner)] = []byte("account")
			putAccrualState(t, base, RentState{
				Version: RentStateVersion, Owner: item.owner, LastAccruedHeight: item.due,
				Mode: RentModeNeedsFunding, ExpireHeight: item.due, NominalFB: "0",
				Work: &RentWork{NextActionHeight: item.due, Phase: RentWorkFunding},
			})
		}
		store := orderedMemStore{memStore: base}
		order := make([]string, 0, 3)
		created := false
		var runtime *Runtime
		runtime = NewRuntime(store,
			WithRuntimeSweepBudget(pageSize, 10),
			WithFundingAttempt(func(owner string, _ uint64, _ *big.Int, _ *RentWork) (FundingResult, error) {
				order = append(order, owner)
				if owner == ownerA && !created {
					created = true
					base[keys.KeyAccount(ownerC)] = []byte("account")
					if err := runtime.writeAccrualState(&RentState{
						Version: RentStateVersion, Owner: ownerC, LastAccruedHeight: 10,
						Mode: RentModeNeedsFunding, ExpireHeight: 10, NominalFB: "0",
						Work: &RentWork{NextActionHeight: 10, Phase: RentWorkFunding},
					}, nil); err != nil {
						return FundingResult{}, err
					}
				}
				return FundingResult{}, nil
			}),
		)
		if err := runtime.ProcessBlock(10, nil); err != nil {
			t.Fatalf("page size %d: %v", pageSize, err)
		}
		return order
	}

	want := []string{"fbc1qdynamicordera", "fbc1q00dynamicorder", "fbc1qzzdynamicorder"}
	if one, wide := run(1), run(DefaultSweepOwnerBudget); !reflect.DeepEqual(one, want) || !reflect.DeepEqual(wide, want) {
		t.Fatalf("dynamic due order changed with page size: one=%v wide=%v want=%v", one, wide, want)
	}
}

func TestTouchedAndOlderDueOwnersAllCompleteInSameBlock(t *testing.T) {
	base := memStore{}
	store := orderedMemStore{memStore: base}
	const dueCount = DefaultSweepOwnerBudget + 1
	dueOwners := make([]string, 0, dueCount)
	for i := 0; i < dueCount; i++ {
		owner := fmt.Sprintf("fbc1qoldqueue%03d", i)
		dueOwners = append(dueOwners, owner)
		base[keys.KeyAccount(owner)] = []byte("account")
		putAccrualState(t, base, RentState{
			Version: RentStateVersion, Owner: owner, LastAccruedHeight: 1,
			UnpaidFB: "100", Mode: RentModeNeedsFunding, ExpireHeight: 1, NominalFB: "0",
			Work: &RentWork{NextActionHeight: 1, Phase: RentWorkFunding},
		})
	}
	touched := "fbc1q000touched"
	base[keys.KeyAccount(touched)] = []byte("account")

	order := make([]string, 0, dueCount+1)
	runtime := NewRuntime(store, WithFundingAttempt(func(owner string, _ uint64, _ *big.Int, _ *RentWork) (FundingResult, error) {
		order = append(order, owner)
		return FundingResult{}, nil
	}))
	if err := runtime.ProcessBlock(10, []string{touched}); err != nil {
		t.Fatal(err)
	}
	if len(order) != dueCount+1 {
		t.Fatalf("same-block attempts=%d want=%d", len(order), dueCount+1)
	}
	for i := 0; i < dueCount; i++ {
		if order[i] != dueOwners[i] {
			t.Fatalf("attempt %d=%s want old due %s", i, order[i], dueOwners[i])
		}
	}
	if order[len(order)-1] != touched {
		t.Fatalf("new touched due work ran before older work: order tail=%s", order[len(order)-1])
	}
	for _, owner := range append(dueOwners, touched) {
		if _, exists := base[keys.KeyAccount(owner)]; exists {
			t.Fatalf("owner %s survived same-block drain", owner)
		}
	}
	assertNoDueAtOrBefore(t, base, 10)
	if got := countKeysWithPrefix(base, keys.KeyRentExpiryAllPrefix()); got != 0 {
		t.Fatalf("same-block touched drain retained %d rent work keys", got)
	}
}

func TestTouchedSweepingOwnerCompletesCleanupInSameBlock(t *testing.T) {
	base := memStore{}
	store := orderedMemStore{memStore: base}
	owner := "fbc1qtouchedsweeping"
	base[keys.KeyAccount(owner)] = []byte("account")
	writeTestBalance(t, base, owner, FeeToken, "5000")
	putAccrualState(t, base, RentState{
		Version: RentStateVersion, Owner: owner, LastAccruedHeight: 5,
		Mode: RentModeSweeping, ExpireHeight: 5, NominalFB: "5000",
		Work: &RentWork{NextActionHeight: 10, Phase: RentWorkSweepBalances},
	})

	runtime := NewRuntime(store, WithRuntimeSweepBudget(1, 1))
	if err := runtime.ProcessBlock(6, []string{owner}); err != nil {
		t.Fatal(err)
	}
	if _, exists := base[keys.KeyAccount(owner)]; exists {
		t.Fatal("touched sweeping owner survived same-block cleanup")
	}
	if got := testBalanceTotal(t, base, RecycleFundAccount, FeeToken); got != "5000" {
		t.Fatalf("recycled FB=%s want=5000", got)
	}
	assertNoDueAtOrBefore(t, base, 6)
}

func TestDeferredFundingIsRejectedAsIncomplete(t *testing.T) {
	base := memStore{}
	owner := "fbc1qcursor"
	base[keys.KeyAccount(owner)] = []byte("account")
	putAccrualState(t, base, RentState{
		Version: RentStateVersion, Owner: owner, LastAccruedHeight: 5,
		UnpaidFB: "100", Mode: RentModeNeedsFunding, ExpireHeight: 5, NominalFB: "0",
		Work: &RentWork{NextActionHeight: 5, Phase: RentWorkFunding, Cursor: "page-1"},
	})
	store := orderedMemStore{memStore: base}
	attempts := 0
	runtime := NewRuntime(store, WithFundingAttempt(func(_ string, height uint64, required *big.Int, work *RentWork) (FundingResult, error) {
		attempts++
		if height != 5 || required == nil || required.String() != "1000100" {
			t.Fatalf("required funding=%v", required)
		}
		if work == nil || work.Cursor != "page-1" {
			t.Fatalf("unexpected work: %+v", work)
		}
		return FundingResult{Deferred: true, NextActionHeight: height + 100, Phase: RentWorkFunding, Cursor: "page-2"}, nil
	}))
	err := runtime.ProcessBlock(5, nil)
	if err == nil || !strings.Contains(err.Error(), "incomplete deferred work") {
		t.Fatalf("deferred callback was accepted: %v", err)
	}
	if attempts != 1 {
		t.Fatalf("deferred callback retried %d times", attempts)
	}
}

func TestImmediateSweepRemovesRescheduledCurrentIndex(t *testing.T) {
	base := memStore{}
	owner := "fbc1qstaleexpiry"
	base[keys.KeyAccount(owner)] = []byte("account")
	putAccrualState(t, base, RentState{
		Version: RentStateVersion, Owner: owner, LastAccruedHeight: 5,
		UnpaidFB: "100", Mode: RentModeNeedsFunding, ExpireHeight: 5, NominalFB: "0",
		Work: &RentWork{NextActionHeight: 5, Phase: RentWorkFunding},
	})
	runtime := NewRuntime(orderedMemStore{memStore: base})
	if err := runtime.ProcessBlock(10, nil); err != nil {
		t.Fatal(err)
	}
	if _, exists := base[keys.KeyAccount(owner)]; exists {
		t.Fatal("account was not swept")
	}
	if _, exists := base[keys.KeyRentState(owner)]; exists {
		t.Fatal("rent state was not removed")
	}
	for key := range base {
		if strings.HasPrefix(key, keys.KeyRentExpiryAllPrefix()) && strings.HasSuffix(key, "_"+owner) {
			t.Fatalf("sweep retained stale work index %s", key)
		}
	}
}

func TestSweepingProjectionPaysOldDebtButNeverBecomesSpendable(t *testing.T) {
	store := memStore{}
	owner := "fbc1qsweepingprojection"
	writeTestBalance(t, store, owner, FeeToken, "500")
	putAccrualState(t, store, RentState{
		Version: RentStateVersion, Owner: owner, LastAccruedHeight: 10,
		UnpaidFB: "100", Mode: RentModeSweeping, ExpireHeight: 10, NominalFB: "500",
		Work: &RentWork{NextActionHeight: 11, Phase: RentWorkSweepBalances},
	})
	projection, err := NewService(store).ProjectRent(owner, 20, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if projection.NewlyAccruedFB != "0" || projection.PayableFB != "100" || projection.UnpaidRentFB != "0" ||
		projection.EffectiveAvailableFB != "400" || projection.SpendableFB != "0" || projection.Mode != RentModeSweeping {
		t.Fatalf("unexpected sweeping projection: %+v", projection)
	}
	settled, err := NewRuntime(store).SettleRent(owner, 20)
	if err != nil {
		t.Fatal(err)
	}
	if settled.PaidFB != "100" || testBalanceTotal(t, store, owner, FeeToken) != "400" ||
		testBalanceTotal(t, store, RecycleFundAccount, FeeToken) != "100" {
		t.Fatalf("unexpected sweeping settlement: %+v", settled)
	}
	state := loadTestRentState(t, store, owner)
	if state.Mode != RentModeSweeping || state.Work == nil || state.Work.Phase != RentWorkSweepBalances {
		t.Fatalf("sweep progress was lost after debt payment: %+v", state)
	}
	status, err := NewService(store).RentStatus(owner, 20)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "sweeping" || status.Work == nil || status.Work.Phase != RentWorkSweepBalances || status.SpendableFB != "0" {
		t.Fatalf("query did not expose sweeping progress: %+v", status)
	}
}

func TestPagedSweepDrainsEverySourceInSameBlock(t *testing.T) {
	base := memStore{}
	store := orderedMemStore{memStore: base}
	owner := "fbc1qpagedsweep"
	base[keys.KeyAccount(owner)] = []byte("account")
	putAccrualState(t, base, RentState{
		Version: RentStateVersion, Owner: owner, LastAccruedHeight: 5,
		UnpaidFB: "0", Mode: RentModeSweeping, ExpireHeight: 5, NominalFB: "0",
		Work: &RentWork{NextActionHeight: 5, Phase: RentWorkSweepClaims},
	})
	for i, token := range []string{"CLAIM-A", "CLAIM-B", "CLAIM-C"} {
		if err := SetClaim(store, owner, token, "external", string(rune('a'+i)), big.NewInt(int64(i+1))); err != nil {
			t.Fatal(err)
		}
	}
	for token, amount := range map[string]string{"A": "10", "B": "20", "C": "30"} {
		writeTestBalance(t, base, owner, token, amount)
	}
	if err := SetClaim(store, owner, "CLAIM-0", "external", "late", big.NewInt(4)); err != nil {
		t.Fatal(err)
	}
	writeTestBalance(t, base, owner, "A0", "40")

	runtime := NewRuntime(store, WithRuntimeScanLimit(2))
	if err := runtime.ProcessBlock(5, nil); err != nil {
		t.Fatal(err)
	}
	if _, exists := base[keys.KeyAccount(owner)]; exists {
		t.Fatal("paged sweep did not finish in the due block")
	}
	for _, prefix := range []string{
		keys.KeyAssetClaimOwnerPrefix(owner),
		keys.KeyAssetClaimMarkerOwnerPrefix(owner),
		keys.KeyAssetClaimTotalOwnerPrefix(owner),
		keys.KeyBalancePrefix(owner),
	} {
		if got := countKeysWithPrefix(base, prefix); got != 0 {
			t.Fatalf("sweep retained %d rows under %s", got, prefix)
		}
	}
	for token, want := range map[string]string{"A": "10", "A0": "40", "B": "20", "C": "30"} {
		if got := testBalanceTotal(t, base, RecycleFundAccount, token); got != want {
			t.Fatalf("recycle %s=%s want=%s", token, got, want)
		}
	}
	for key := range base {
		if strings.HasPrefix(key, keys.KeyRentExpiryAllPrefix()) && strings.HasSuffix(key, "_"+owner) {
			t.Fatalf("completed paged sweep retained work index %s", key)
		}
	}
	if stats := runtime.LastStats(); stats.SweepKeysScanned <= 2 {
		t.Fatalf("scan limit acted as a block cap: %+v", stats)
	}
}

func TestSweepOwnerAndKeyLimitsArePageSizesNotBlockCaps(t *testing.T) {
	base := memStore{}
	store := orderedMemStore{memStore: base}
	owners := []string{
		"fbc1qsweepbudget0", "fbc1qsweepbudget1", "fbc1qsweepbudget2",
		"fbc1qsweepbudget3", "fbc1qsweepbudget4",
	}
	for _, owner := range owners {
		base[keys.KeyAccount(owner)] = []byte("account")
		writeTestBalance(t, base, owner, "USDT", "10")
		putAccrualState(t, base, RentState{
			Version: RentStateVersion, Owner: owner, LastAccruedHeight: 1,
			Mode: RentModeSweeping, ExpireHeight: 1, NominalFB: "0",
			Work: &RentWork{NextActionHeight: 1, Phase: RentWorkSweepClaims},
		})
	}
	runtime := NewRuntime(store, WithRuntimeSweepBudget(2, 2))
	if err := runtime.ProcessBlock(1, nil); err != nil {
		t.Fatal(err)
	}
	for _, owner := range owners {
		if _, exists := base[keys.KeyAccount(owner)]; exists {
			t.Fatalf("owner %s survived same-block paged sweep", owner)
		}
	}
	if got := testBalanceTotal(t, base, RecycleFundAccount, "USDT"); got != "50" {
		t.Fatalf("paged budget lost value: recycle=%s", got)
	}
	stats := runtime.LastStats()
	if stats.SweepOwnersProcessed != len(owners) || stats.SweepOwnerBudget != 2 || stats.SweepKeyBudget != 2 {
		t.Fatalf("page sizes became block caps: %+v", stats)
	}
	assertNoDueAtOrBefore(t, base, 1)
}

func TestStaleOldDueKeyCannotPullFutureSweepForward(t *testing.T) {
	base := memStore{}
	store := orderedMemStore{memStore: base}
	owner := "fbc1qfuturecleanup"
	base[keys.KeyAccount(owner)] = []byte("account")
	writeTestBalance(t, base, owner, "USDT", "10")
	putAccrualState(t, base, RentState{
		Version: RentStateVersion, Owner: owner, LastAccruedHeight: 5,
		Mode: RentModeSweeping, ExpireHeight: 5, NominalFB: "0",
		Work: &RentWork{NextActionHeight: 20, Phase: RentWorkSweepClaims},
	})
	stateBefore := append([]byte(nil), base[keys.KeyRentState(owner)]...)
	base[keys.KeyRentExpiry(1, owner)] = []byte(owner)
	mismatchedKey := keys.KeyRentExpiry(2, "fbc1qwrongindexowner")
	base[mismatchedKey] = []byte(owner)

	runtime := NewRuntime(store)
	if err := runtime.ProcessBlock(10, nil); err != nil {
		t.Fatal(err)
	}
	if _, exists := base[keys.KeyRentExpiry(1, owner)]; exists {
		t.Fatal("stale old due key was retained")
	}
	if _, exists := base[mismatchedKey]; exists {
		t.Fatal("owner-mismatched due key was retained")
	}
	if !reflect.DeepEqual(base[keys.KeyRentState(owner)], stateBefore) {
		t.Fatal("stale due key rewrote future RentState")
	}
	if _, exists := base[keys.KeyRentExpiry(20, owner)]; !exists {
		t.Fatal("canonical future work key was removed")
	}
	if _, exists := base[keys.KeyAccount(owner)]; !exists {
		t.Fatal("stale due key swept a future owner")
	}
	if got := testBalanceTotal(t, base, RecycleFundAccount, "USDT"); got != "0" {
		t.Fatalf("stale due key recycled future balance: %s", got)
	}
}

func assertNoDueAtOrBefore(t *testing.T, store memStore, height uint64) {
	t.Helper()
	for key := range store {
		if !strings.HasPrefix(key, keys.KeyRentExpiryAllPrefix()) {
			continue
		}
		dueHeight, ok := parseRentExpiryHeight(key)
		if !ok {
			t.Fatalf("malformed rent due key %s", key)
		}
		if dueHeight <= height {
			t.Fatalf("height %d retained due key %s", height, key)
		}
	}
}

func countKeysWithPrefix(store memStore, prefix string) int {
	count := 0
	for key := range store {
		if strings.HasPrefix(key, prefix) {
			count++
		}
	}
	return count
}
