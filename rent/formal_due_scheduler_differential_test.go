package rent

import (
	"dex/formal/itftrace"
	"dex/keys"
	"dex/pb"
	"encoding/json"
	"math/big"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	iface "dex/interfaces"

	"google.golang.org/protobuf/proto"
)

const formalDueSchedulerTraceEnv = "DEX_FORMAL_ITF_RENT_DUE_SCHEDULER"

type formalDueScan struct {
	startAfter string
	limit      int
	rows       int
}

type formalDueStore struct {
	memStore
	scans []formalDueScan
}

func (s *formalDueStore) ScanRentOrderedAfter(prefix, startAfter string, limit int) ([]iface.KVPair, error) {
	keysFound := make([]string, 0)
	for key := range s.memStore {
		if strings.HasPrefix(key, prefix) && (startAfter == "" || key > startAfter) {
			keysFound = append(keysFound, key)
		}
	}
	sort.Strings(keysFound)
	if limit > 0 && len(keysFound) > limit {
		keysFound = keysFound[:limit]
	}
	rows := make([]iface.KVPair, 0, len(keysFound))
	for _, key := range keysFound {
		rows = append(rows, iface.KVPair{Key: key, Value: cloneBytes(s.memStore[key])})
	}
	if prefix == keys.KeyRentExpiryAllPrefix() {
		s.scans = append(s.scans, formalDueScan{startAfter: startAfter, limit: limit, rows: len(rows)})
	}
	return rows, nil
}

type formalDueRun struct {
	state           memStore
	processed       []string
	dueOwners       int
	scans           []formalDueScan
	recycleFB       string
	insertedEarlier bool
}

func TestFormalRentDueSchedulerPageInvariant(t *testing.T) {
	runs := make([]formalDueRun, 0, 3)
	for _, pageSize := range []int{1, 2, DefaultSweepOwnerBudget} {
		runs = append(runs, runFormalDueSchedulerProduction(t, pageSize))
	}
	wantOrder := []string{formalDueAlice, formalDueCarol, formalDueBob}
	for i, run := range runs {
		if !reflect.DeepEqual(run.processed, wantOrder) {
			t.Fatalf("run %d processing order=%v want=%v", i, run.processed, wantOrder)
		}
		if run.dueOwners != 3 {
			t.Fatalf("run %d due owners=%d want=3", i, run.dueOwners)
		}
		if run.recycleFB != "3" {
			t.Fatalf("run %d recycle FB=%s want=3", i, run.recycleFB)
		}
		if !run.insertedEarlier {
			t.Fatalf("run %d did not exercise an insertion behind the completed scan cursor", i)
		}
		assertFormalNoDueAtOrBefore(t, run.state, formalDueHeight)
		if got, wantMax := len(run.scans), formalDueMaxScans(3, []int{1, 2, DefaultSweepOwnerBudget}[i]); got > wantMax {
			t.Fatalf("run %d ordered scans=%d want<=%d: %+v", i, got, wantMax, run.scans)
		}
	}
	for i := 1; i < len(runs); i++ {
		if !reflect.DeepEqual(runs[0].state, runs[i].state) {
			t.Fatalf("page-size-dependent final state: page1=%v page%d=%v", runs[0].state, i+1, runs[i].state)
		}
	}
}

func TestFormalRentDueSchedulerITFDifferential(t *testing.T) {
	path := os.Getenv(formalDueSchedulerTraceEnv)
	if path == "" {
		t.Skip("rent due-scheduler ITF path is set by formal/run-go-differential.ps1")
	}
	trace, err := itftrace.Load(path)
	if err != nil {
		t.Fatalf("load Quint rent due-scheduler trace: %v", err)
	}
	if len(trace.States) < 2 {
		t.Fatalf("rent due-scheduler trace states=%d want>=2", len(trace.States))
	}
	final := trace.States[len(trace.States)-1]
	phase, err := final.Tag("phase")
	if err != nil {
		t.Fatal(err)
	}
	processed, err := final.SetTags("processedOwners")
	if err != nil {
		t.Fatal(err)
	}
	storeDue, err := final.SetTags("storeDueKeys")
	if err != nil {
		t.Fatal(err)
	}
	blockers, err := final.SetTags("blockers")
	if err != nil {
		t.Fatal(err)
	}
	recycle, err := final.Int("recycle")
	if err != nil {
		t.Fatal(err)
	}
	remaining, err := final.Int("remainingObligations")
	if err != nil {
		t.Fatal(err)
	}
	insertedEarlier, err := final.Bool("insertedEarlier")
	if err != nil {
		t.Fatal(err)
	}
	if phase != "Complete" || len(processed) != 3 || len(storeDue) != 0 || len(blockers) != 0 ||
		recycle != 3 || remaining != 0 || !insertedEarlier {
		t.Fatalf("unexpected final model state: phase=%s processed=%v due=%v blockers=%v recycle=%d remaining=%d insertedEarlier=%v",
			phase, processed, storeDue, blockers, recycle, remaining, insertedEarlier)
	}

	production := runFormalDueSchedulerProduction(t, 1)
	if len(production.processed) != len(processed) || production.recycleFB != big.NewInt(recycle).String() ||
		production.dueOwners != len(processed) || !production.insertedEarlier {
		t.Fatalf("model/production mismatch: model processed=%v recycle=%d; production=%+v", processed, recycle, production)
	}
	assertFormalNoDueAtOrBefore(t, production.state, formalDueHeight)
}

func TestFormalRentDueSchedulerRejectsUntrackedInsertion(t *testing.T) {
	base := memStore{}
	seedFormalDueOwner(t, base, formalDueAlice, 1)
	store := &formalDueStore{memStore: base}
	runtime := NewRuntime(store,
		WithPolicy(formalDuePolicy()),
		WithRuntimeSweepBudget(1, 1),
		WithFundingAttempt(func(owner string, _ uint64, _ *big.Int, _ *RentWork) (FundingResult, error) {
			if owner == formalDueAlice {
				                                                                      
				                                                                     
				                                                    
				base[keys.KeyRentExpiry(formalDueHeight, "fbc1qformalrogue")] = []byte("fbc1qformalrogue")
			}
			return FundingResult{}, nil
		}),
	)
	err := runtime.ProcessBlock(formalDueHeight, nil)
	if err == nil || !strings.Contains(err.Error(), "untracked due work") {
		t.Fatalf("untracked same-block due insertion was accepted: %v", err)
	}
}

func TestFormalRentDueSchedulerRefreshesQueuedCanonicalValue(t *testing.T) {
	var baseline memStore
	for _, pageSize := range []int{1, DefaultSweepOwnerBudget} {
		base := memStore{}
		seedFormalDueOwner(t, base, formalDueAlice, 1)
		seedFormalDueOwner(t, base, formalDueBob, formalDueHeight)
		bobKey := keys.KeyRentExpiry(formalDueHeight, formalDueBob)
		                                                                          
		                                                                            
		                                                                              
		                                                                
		base[bobKey] = []byte("fbc1qformalwrongvalue")
		processed := make([]string, 0, 2)
		runtime := NewRuntime(&formalDueStore{memStore: base},
			WithPolicy(formalDuePolicy()),
			WithRuntimeSweepBudget(pageSize, 1),
			WithFundingAttempt(func(owner string, _ uint64, _ *big.Int, _ *RentWork) (FundingResult, error) {
				processed = append(processed, owner)
				if owner == formalDueAlice {
					return FundingResult{TouchedOwners: []string{formalDueBob}}, nil
				}
				return FundingResult{}, nil
			}),
		)
		if err := runtime.ProcessBlock(formalDueHeight, nil); err != nil {
			t.Fatalf("page size %d: %v", pageSize, err)
		}
		if want := []string{formalDueAlice, formalDueBob}; !reflect.DeepEqual(processed, want) {
			t.Fatalf("page size %d processed=%v want=%v", pageSize, processed, want)
		}
		assertFormalNoDueAtOrBefore(t, base, formalDueHeight)
		if _, exists := base[keys.KeyRentState(formalDueBob)]; exists {
			t.Fatalf("page size %d retained Bob's due RentState after losing its canonical index", pageSize)
		}
		if baseline == nil {
			baseline = cloneMemStore(base)
		} else if !reflect.DeepEqual(baseline, memStore(base)) {
			t.Fatalf("canonical value refresh is page-size dependent: page1=%v page%d=%v", baseline, pageSize, base)
		}
	}
}

func TestFormalRentMarkerFastPathRequiresCompletenessProof(t *testing.T) {
	const (
		owner = "fbc1qformallegacymarker"
		asset = "FORMAL.USDT"
	)
	base := memStore{
		keys.KeyAccount(owner):                                   []byte("account"),
		keys.KeyAssetClaimTotal(owner, asset):                    []byte("10"),
		keys.KeyAssetClaim(owner, asset, "position", "formal-1"): []byte("10"),
	}
	policy := formalDuePolicy()
	policy.ExemptAssets = []ExemptAsset{{Token: asset, Minimum: big.NewInt(10)}}
	runtime := NewRuntime(&formalDueStore{memStore: base}, WithPolicy(policy))
	if err := runtime.ProcessBlock(formalDueHeight, []string{owner}); err != nil {
		t.Fatal(err)
	}
	if _, exists := base[keys.KeyAccount(owner)]; !exists {
		t.Fatal("legacy owner without OwnerMarkerInit was swept despite an exact-threshold claim total")
	}
	if _, exists := base[keys.KeyAssetClaimTotal(owner, asset)]; !exists {
		t.Fatal("legacy external claim total was discarded by an incomplete marker fast path")
	}
	if _, exists := base[keys.KeyRentState(owner)]; exists {
		t.Fatal("externally exempt legacy owner retained rent state")
	}
}

func TestFormalRentFundingProgressWitness(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result FundingResult
	}{
		{name: "progress flag", result: FundingResult{Deferred: true, Progress: true}},
		{name: "removed-key claim", result: FundingResult{Deferred: true, RemovedWorkKeys: []string{"formal/blocker/fabricated"}}},
		{name: "fresh cursor", result: FundingResult{Deferred: true, Phase: "unbounded", Cursor: "fresh-cursor"}},
	} {
		t.Run(tc.name+" cannot defer scheduler work", func(t *testing.T) {
			base := memStore{}
			seedFormalDueOwner(t, base, formalDueAlice, formalDueHeight)
			attempts := 0
			runtime := NewRuntime(base,
				WithPolicy(formalDuePolicy()),
				WithFundingAttempt(func(string, uint64, *big.Int, *RentWork) (FundingResult, error) {
					attempts++
					return tc.result, nil
				}),
			)
			err := runtime.ProcessBlock(formalDueHeight, nil)
			if err == nil || !strings.Contains(err.Error(), "incomplete deferred work") || attempts != 1 {
				t.Fatalf("%s created scheduler continuation: attempts=%d err=%v", tc.name, attempts, err)
			}
		})
	}

	t.Run("callback drains finite blockers before returning", func(t *testing.T) {
		base := memStore{}
		seedFormalDueOwner(t, base, formalDueAlice, formalDueHeight)
		blockers := []string{"formal/blocker/1", "formal/blocker/2"}
		for _, blocker := range blockers {
			base[blocker] = []byte("present")
		}
		attempt := 0
		runtime := NewRuntime(base,
			WithPolicy(formalDuePolicy()),
			WithFundingAttempt(func(string, uint64, *big.Int, *RentWork) (FundingResult, error) {
				attempt++
				removed := make([]string, 0, len(blockers))
				for _, blocker := range blockers {
					delete(base, blocker)
					removed = append(removed, blocker)
				}
				writeFormalDueBalance(t, base, formalDueAlice, mustAmount(DefaultActivationFeeRaw).Int64())
				return FundingResult{ReceivedFB: mustAmount(DefaultActivationFeeRaw), RemovedWorkKeys: removed}, nil
			}),
		)
		if err := runtime.ProcessBlock(formalDueHeight, nil); err != nil {
			t.Fatalf("finite blocker run: %v", err)
		}
		if attempt != 1 {
			t.Fatalf("funding attempts=%d want=1 complete callback", attempt)
		}
		for _, blocker := range blockers {
			if _, exists := base[blocker]; exists {
				t.Fatalf("blocker %q survived successful run", blocker)
			}
		}
		if got := testBalanceTotal(t, base, RecycleFundAccount, FeeToken); got != "1" {
			t.Fatalf("recycled rent=%s want=1", got)
		}
		if got := testBalanceTotal(t, base, formalDueAlice, FeeToken); got != DefaultActivationFeeRaw {
			t.Fatalf("funded owner balance=%s want=%s", got, DefaultActivationFeeRaw)
		}
	})
}

const (
	formalDueHeight = uint64(10)
	formalDueAlice  = "fbc1qformalduea"
	formalDueBob    = "fbc1qformalduez"
	formalDueCarol  = "fbc1qformalduec"
)

func runFormalDueSchedulerProduction(t *testing.T, pageSize int) formalDueRun {
	t.Helper()
	base := memStore{}
	seedFormalDueOwner(t, base, formalDueAlice, 1)
	seedFormalDueOwner(t, base, formalDueBob, formalDueHeight)
	                                                                  
	                                                              
	base[keys.KeyRentExpiry(2, formalDueBob)] = []byte(formalDueBob)
	store := &formalDueStore{memStore: base}
	processed := make([]string, 0, 3)
	inserted := false
	runtime := NewRuntime(store,
		WithPolicy(formalDuePolicy()),
		WithRuntimeSweepBudget(pageSize, 1),
		WithFundingAttempt(func(owner string, _ uint64, _ *big.Int, _ *RentWork) (FundingResult, error) {
			processed = append(processed, owner)
			if owner != formalDueAlice || inserted {
				return FundingResult{}, nil
			}
			inserted = true
			base[keys.KeyAccount(formalDueCarol)] = []byte("account")
			writeFormalDueBalance(t, base, formalDueCarol, 1)
			state := RentState{
				Version: RentStateVersion, Owner: formalDueCarol,
				LastAccruedHeight: formalDueHeight, UnpaidFB: "1",
				Mode: RentModeNeedsFunding, ExpireHeight: formalDueHeight, NominalFB: "1",
				Work: &RentWork{NextActionHeight: formalDueHeight, Phase: RentWorkFunding},
			}
			encoded, err := json.Marshal(&state)
			if err != nil {
				t.Fatal(err)
			}
			                                                                       
			                                                                        
			                                                               
			base[keys.KeyRentState(formalDueCarol)] = encoded
			return FundingResult{TouchedOwners: []string{formalDueCarol}}, nil
		}),
	)
	if err := runtime.ProcessBlock(formalDueHeight, nil); err != nil {
		t.Fatalf("page size %d: %v", pageSize, err)
	}
	state := cloneMemStore(base)
	return formalDueRun{
		state: state, processed: append([]string(nil), processed...),
		dueOwners:       runtime.LastStats().DueOwners,
		scans:           append([]formalDueScan(nil), store.scans...),
		recycleFB:       testBalanceTotal(t, base, RecycleFundAccount, FeeToken),
		insertedEarlier: keys.KeyRentExpiry(formalDueHeight, formalDueCarol) < keys.KeyRentExpiry(formalDueHeight, formalDueBob),
	}
}

func seedFormalDueOwner(t *testing.T, store memStore, owner string, dueHeight uint64) {
	t.Helper()
	store[keys.KeyAccount(owner)] = []byte("account")
	writeFormalDueBalance(t, store, owner, 1)
	putAccrualState(t, store, RentState{
		Version: RentStateVersion, Owner: owner,
		LastAccruedHeight: formalDueHeight, UnpaidFB: "1",
		Mode: RentModeNeedsFunding, ExpireHeight: dueHeight, NominalFB: "1",
		Work: &RentWork{NextActionHeight: dueHeight, Phase: RentWorkFunding},
	})
}

func writeFormalDueBalance(t *testing.T, store memStore, owner string, amount int64) {
	t.Helper()
	raw, err := proto.Marshal(&pb.TokenBalanceRecord{
		Address: owner, Token: FeeToken,
		Balance: &pb.TokenBalance{Balance: big.NewInt(amount).String()},
	})
	if err != nil {
		t.Fatal(err)
	}
	store[keys.KeyBalance(owner, FeeToken)] = raw
}

func formalDuePolicy() Policy {
	return Policy{
		Token: FeeToken, ExemptMin: big.NewInt(10), RentPerBlock: big.NewInt(1),
		RecycleAccount: RecycleFundAccount,
	}
}

func formalDueMaxScans(initialRows, pageSize int) int {
	pages := (initialRows + pageSize - 1) / pageSize
	                                                                         
	                                                                           
	                                                               
	return pages + 2
}

func assertFormalNoDueAtOrBefore(t *testing.T, store memStore, height uint64) {
	t.Helper()
	for key := range store {
		if !strings.HasPrefix(key, keys.KeyRentExpiryAllPrefix()) {
			continue
		}
		dueHeight, valid := parseRentExpiryHeight(key)
		if !valid {
			t.Fatalf("malformed due key %q", key)
		}
		if dueHeight <= height {
			t.Fatalf("height %d retained due key %s", height, key)
		}
	}
}
