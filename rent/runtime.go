package rent

import (
	"dex/keys"
	"dex/pb"
	"encoding/json"
	"fmt"
	"math/big"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
)

const (
	RecycleFundAccount      = "fbc1000000000000000000000000000000000000000"
	DefaultRentExemptMinRaw = "1000000000"
	DefaultRentPerBlockRaw  = "100"
	DefaultActivationFeeRaw = "1000000"
	parallelSettleMinOwners = 128
	parallelSettleMaxWork   = 8
	DefaultSweepOwnerBudget = 64
	DefaultSweepKeyBudget   = 10000
)

type MutableStore interface {
	Store
	Writer
}

type Cleaner interface {
	PrepareAccountSweep(owner string, height uint64) error
}

type ClaimAwareCleaner interface {
	PrepareAccountSweepWithClaims(owner string, height uint64, claims map[string][]byte) error
}

                                                                       
                                                                            
                                                                            
                             
type ClaimPageCleaner interface {
	PrepareAccountSweepClaimPage(owner string, height uint64, claims map[string][]byte) error
}

                                                                              
                                                                             
type SweepFinalizer interface {
	FinalizeAccountSweep(owner string, height uint64) error
}

type Policy struct {
	Token          string
	ExemptMin      *big.Int
	ExemptAssets   []ExemptAsset
	RentPerBlock   *big.Int
	RecycleAccount string
}

type RentState struct {
	                                                                         
	                                                                         
	                                                                      
	Version           uint32    `json:"version,omitempty"`
	Owner             string    `json:"owner"`
	LastSettledHeight uint64    `json:"last_settled_height,omitempty"`                             
	LastAccruedHeight uint64    `json:"last_accrued_height,omitempty"`
	UnpaidFB          string    `json:"unpaid_fb,omitempty"`
	Mode              string    `json:"mode,omitempty"`
	ExpireHeight      uint64    `json:"expire_height,omitempty"`                                         
	NominalFB         string    `json:"nominal_fb"`
	Work              *RentWork `json:"work,omitempty"`
}

type RentWork struct {
	NextActionHeight uint64 `json:"next_action_height"`
	Phase            string `json:"phase,omitempty"`
	Cursor           string `json:"cursor,omitempty"`
}

type Runtime struct {
	store            MutableStore
	policy           Policy
	scanLimit        int
	cleaner          Cleaner
	fundingAttempt   func(string, uint64, *big.Int, *RentWork) (FundingResult, error)
	stateRead        map[string]cachedRentState
	balanceRead      map[string]cachedRead
	claimMarkerRead  map[string]cachedRead
	claimTotalRead   map[string]cachedRead
	ownerMarkerRead  map[string]map[string]bool
	recycleTotals    map[string]*big.Int
	sweepOwnerBudget int
	sweepKeyBudget   int
	stats            ProcessBlockStats
	serviceStats     ServiceStats
	activeDueQueue   *dueRentQueue
	activeDueHeight  uint64
}

type RuntimeOption func(*Runtime)

                                                                            
                                                                         
type FundingResult struct {
	ReceivedFB    *big.Int
	TouchedOwners []string
	                                                                          
	                                                                            
	                              
	RemovedWorkKeys []string
	                                                                          
	                                                                             
	                                                                         
	                                                                    
	Deferred bool
	Progress bool
	                                                                            
	                                                                              
	NextActionHeight uint64
	Phase            string
	Cursor           string
}

type cachedRentState struct {
	state *RentState
}

type ProcessBlockStats struct {
	DueOwnersDuration       time.Duration
	PrefetchStatesDuration  time.Duration
	PrefetchAssetsDuration  time.Duration
	PrefetchMarkersDuration time.Duration
	NominalAssetDuration    time.Duration
	ClaimScanDuration       time.Duration
	SweepDuration           time.Duration
	SweepClaimScanDuration  time.Duration
	SweepCleanerDuration    time.Duration
	SweepBalanceDuration    time.Duration
	SweepClaimDuration      time.Duration
	RecycleFlushDuration    time.Duration
	SettleLoopDuration      time.Duration
	TouchedOwners           int
	DueOwners               int
	Owners                  int
	PrefetchedAssetKeys     int
	PrefetchedMarkerKeys    int
	BalanceCacheHits        int
	ClaimMarkerCacheHits    int
	ClaimTotalCacheHits     int
	ClaimScans              int
	ClaimScanKeys           int
	SweptAccounts           int
	ExemptAccounts          int
	WrittenStates           int
	UnchangedStates         int
	RecycleFlushes          int
	SweepOwnerBudget        int
	SweepKeyBudget          int
	SweepOwnersProcessed    int
	SweepKeysScanned        int
	SweepBudgetDeferrals    int
}

func DefaultPolicy() Policy {
	return Policy{
		Token:          FeeToken,
		ExemptMin:      mustAmount(DefaultRentExemptMinRaw),
		ExemptAssets:   defaultExemptAssets(),
		RentPerBlock:   mustAmount(DefaultRentPerBlockRaw),
		RecycleAccount: RecycleFundAccount,
	}
}

func WithPolicy(policy Policy) RuntimeOption {
	return func(r *Runtime) { r.policy = policy }
}

func WithCleaner(cleaner Cleaner) RuntimeOption {
	return func(r *Runtime) { r.cleaner = cleaner }
}

                                                                              
                                                                              
                                                                    
func WithFundingAttempt(attempt any) RuntimeOption {
	return func(r *Runtime) {
		switch fn := attempt.(type) {
		case func(string, uint64, *big.Int, *RentWork) (FundingResult, error):
			r.fundingAttempt = fn
		case func(string, uint64, *big.Int) (FundingResult, error):
			r.fundingAttempt = func(owner string, height uint64, required *big.Int, _ *RentWork) (FundingResult, error) {
				return fn(owner, height, required)
			}
		case func(string, uint64) (FundingResult, error):
			r.fundingAttempt = func(owner string, height uint64, _ *big.Int, _ *RentWork) (FundingResult, error) {
				return fn(owner, height)
			}
		case nil:
			r.fundingAttempt = nil
		default:
			panic("rent: unsupported funding callback")
		}
	}
}

func WithRuntimeScanLimit(limit int) RuntimeOption {
	return func(r *Runtime) {
		if limit > 0 {
			r.scanLimit = limit
		}
	}
}

                                                                               
                                                                              
                                                             
func WithRuntimeSweepBudget(ownerLimit, keyLimit int) RuntimeOption {
	return func(r *Runtime) {
		if ownerLimit > 0 {
			r.sweepOwnerBudget = ownerLimit
		}
		if keyLimit > 0 {
			r.sweepKeyBudget = keyLimit
		}
	}
}

func NewRuntime(store MutableStore, opts ...RuntimeOption) *Runtime {
	r := &Runtime{
		store: store, policy: DefaultPolicy(), scanLimit: DefaultScanLimit,
		sweepOwnerBudget: DefaultSweepOwnerBudget, sweepKeyBudget: DefaultSweepKeyBudget,
	}
	for _, opt := range opts {
		opt(r)
	}
	r.policy = normalizePolicy(r.policy)
	return r
}

func (r *Runtime) processLeaseBlockLegacy(height uint64, touchedOwners []string) error {
	if r == nil || r.store == nil {
		return fmt.Errorf("rent: nil runtime store")
	}
	r.stats = ProcessBlockStats{TouchedOwners: len(touchedOwners)}
	r.serviceStats = ServiceStats{}
	r.recycleTotals = make(map[string]*big.Int)
	defer func() { r.recycleTotals = nil }()
	dueStart := time.Now()
	owners, err := r.dueOwners(height)
	r.stats.DueOwnersDuration = time.Since(dueStart)
	if err != nil {
		return err
	}
	r.stats.DueOwners = len(owners)
	for _, owner := range touchedOwners {
		owners[owner] = struct{}{}
	}
	sorted := sortedOwners(owners)
	r.stats.Owners = len(sorted)
	statePrefetchStart := time.Now()
	if err := r.prefetchStates(sorted); err != nil {
		return err
	}
	r.stats.PrefetchStatesDuration = time.Since(statePrefetchStart)
	assetPrefetchStart := time.Now()
	if err := r.prefetchNominalAssetReads(sorted); err != nil {
		return err
	}
	r.stats.PrefetchAssetsDuration = time.Since(assetPrefetchStart)
	markerPrefetchStart := time.Now()
	if err := r.prefetchOwnerMarkers(sorted); err != nil {
		return err
	}
	r.stats.PrefetchMarkersDuration = time.Since(markerPrefetchStart)
	settleStart := time.Now()
	plans, err := r.planSettleAccounts(sorted, height)
	if err != nil {
		return err
	}
	if r.fundingAttempt != nil {
		attempted, err := r.fundSweepCandidates(plans, owners, height)
		if err != nil {
			return err
		}
		if attempted {
			sorted = sortedOwners(owners)
			r.stats.Owners = len(sorted)
			if err := r.prefetchStates(sorted); err != nil {
				return err
			}
			if err := r.prefetchNominalAssetReads(sorted); err != nil {
				return err
			}
			if err := r.prefetchOwnerMarkers(sorted); err != nil {
				return err
			}
			plans, err = r.planSettleAccounts(sorted, height)
			if err != nil {
				return err
			}
		}
	}
	for i := range plans {
		if err := r.applySettlePlan(&plans[i], height); err != nil {
			return err
		}
	}
	r.stats.SettleLoopDuration = time.Since(settleStart)
	flushStart := time.Now()
	if err := r.flushRecycleBalances(); err != nil {
		return err
	}
	r.stats.RecycleFlushDuration = time.Since(flushStart)
	r.stats.ClaimScanDuration = r.serviceStats.ClaimScanDuration
	r.stats.BalanceCacheHits = r.serviceStats.BalanceCacheHits
	r.stats.ClaimMarkerCacheHits = r.serviceStats.ClaimMarkerHits
	r.stats.ClaimTotalCacheHits = r.serviceStats.ClaimTotalHits
	r.stats.ClaimScans = r.serviceStats.ClaimScans
	r.stats.ClaimScanKeys = r.serviceStats.ClaimScanKeys
	return nil
}

func (r *Runtime) clearReadCaches() {
	r.stateRead = nil
	r.balanceRead = nil
	r.claimMarkerRead = nil
	r.claimTotalRead = nil
	r.ownerMarkerRead = nil
}

func (r *Runtime) invalidateOwnerReadCaches(owner string) {
	r.invalidateRentSettlementReadCaches(owner)
	for _, asset := range r.policy.ExemptAssets {
		delete(r.balanceRead, keys.KeyBalance(owner, asset.Token))
		delete(r.claimMarkerRead, keys.KeyAssetClaimMarker(owner, asset.Token))
		delete(r.claimTotalRead, keys.KeyAssetClaimTotal(owner, asset.Token))
	}
	delete(r.claimMarkerRead, keys.KeyAssetClaimMarker(owner, r.policy.Token))
	delete(r.claimTotalRead, keys.KeyAssetClaimTotal(owner, r.policy.Token))
	delete(r.ownerMarkerRead, owner)
}

                                                                          
                                                                            
                                                                     
func (r *Runtime) invalidateRentSettlementReadCaches(owner string) {
	if r.stateRead != nil {
		delete(r.stateRead, owner)
	}
	delete(r.balanceRead, keys.KeyBalance(owner, r.policy.Token))
}

                                                                           
                                                                           
func (r *Runtime) fundSweepCandidates(plans []settlePlan, owners map[string]struct{}, height uint64) (bool, error) {
	attempted := false
	queued := make(map[string]struct{})
	attemptedOwners := make(map[string]struct{})
	for _, initial := range plans {
		if initial.action == settlePlanSweep {
			queued[initial.owner] = struct{}{}
		}
	}
	queue := sortedOwners(queued)
	for cursor := 0; cursor < len(queue); cursor++ {
		owner := queue[cursor]
		delete(queued, owner)
		if _, done := attemptedOwners[owner]; done {
			continue
		}
		r.clearReadCaches()
		current := r.planSettleAccount(owner, height)
		if current.err != nil {
			return attempted, current.err
		}
		if current.action != settlePlanSweep {
			continue
		}
		attempted = true
		attemptedOwners[owner] = struct{}{}
		result, err := r.fundingAttempt(owner, height, nil, nil)
		if err != nil {
			return attempted, err
		}
		newCandidates := make(map[string]struct{})
		for _, touched := range result.TouchedOwners {
			if IsRentManagedAccount(touched) {
				owners[touched] = struct{}{}
				if _, done := attemptedOwners[touched]; !done {
					if _, waiting := queued[touched]; !waiting {
						newCandidates[touched] = struct{}{}
					}
				}
			}
		}
		                                                                     
		                                                                          
		                                                                      
		for _, touched := range sortedOwners(newCandidates) {
			queued[touched] = struct{}{}
			queue = append(queue, touched)
		}
		if result.ReceivedFB == nil || result.ReceivedFB.Sign() == 0 {
			continue
		}
		if result.ReceivedFB.Sign() < 0 {
			return attempted, fmt.Errorf("rent: negative funding receipt for %s", owner)
		}
		r.clearReadCaches()
		funded := r.planSettleAccount(owner, height)
		if funded.err != nil {
			return attempted, funded.err
		}
		increase := new(big.Int).Sub(funded.nominal, current.nominal)
		if result.ReceivedFB.Cmp(increase) > 0 {
			return attempted, fmt.Errorf("rent: funding receipt exceeds nominal FB increase for %s", owner)
		}
		if funded.action == settlePlanClearExempt {
			r.clearState(owner, funded.old)
		} else {
			                                                                     
			                                                                         
			r.writeFundedState(owner, height, funded.nominal, result.ReceivedFB, funded.old)
		}
	}
	return attempted, nil
}

func (r *Runtime) LastStats() ProcessBlockStats {
	if r == nil {
		return ProcessBlockStats{}
	}
	return r.stats
}

type settlePlanAction uint8

const (
	settlePlanNoop settlePlanAction = iota
	settlePlanClearExempt
	settlePlanWriteState
	settlePlanSweep
	settlePlanUnchanged
)

type settlePlan struct {
	owner        string
	old          *RentState
	nominal      *big.Int
	action       settlePlanAction
	serviceStats ServiceStats
	err          error
}

func (r *Runtime) planSettleAccounts(owners []string, height uint64) ([]settlePlan, error) {
	if len(owners) == 0 {
		return nil, nil
	}
	planStart := time.Now()
	plans := make([]settlePlan, len(owners))
	workers := rentParallelWorkers(len(owners))
	if workers <= 1 {
		for i, owner := range owners {
			plans[i] = r.planSettleAccount(owner, height)
		}
	} else {
		chunk := (len(owners) + workers - 1) / workers
		var wg sync.WaitGroup
		for i := 0; i < workers; i++ {
			start := i * chunk
			if start >= len(owners) {
				break
			}
			end := start + chunk
			if end > len(owners) {
				end = len(owners)
			}
			wg.Add(1)
			go func(start, end int) {
				defer wg.Done()
				for j := start; j < end; j++ {
					plans[j] = r.planSettleAccount(owners[j], height)
				}
			}(start, end)
		}
		wg.Wait()
	}
	r.stats.NominalAssetDuration += time.Since(planStart)
	for i := range plans {
		r.mergeServiceStats(plans[i].serviceStats)
		if plans[i].err != nil {
			return nil, plans[i].err
		}
	}
	return plans, nil
}

func (r *Runtime) planSettleAccount(owner string, height uint64) settlePlan {
	owner = strings.TrimSpace(owner)
	plan := settlePlan{owner: owner, action: settlePlanNoop}
	if !IsRentManagedAccount(owner) {
		return plan
	}
	st, err := r.readState(owner)
	if err != nil {
		plan.err = err
		return plan
	}
	plan.old = st
	serviceStats := ServiceStats{}
	evaluation, err := NewService(
		r.store,
		WithScanLimit(r.scanLimit),
		WithClaimMarkerFastPath(),
		withPrefetchedReads(r.balanceRead, r.claimMarkerRead, r.claimTotalRead),
		withServiceStats(&serviceStats),
	).evaluateAccountRent(owner, height, r.policy, st)
	plan.serviceStats = serviceStats
	if err != nil {
		plan.err = err
		return plan
	}
	nominal, ok := new(big.Int).SetString(evaluation.NominalFB.Total, 10)
	if !ok {
		plan.err = fmt.Errorf("rent: invalid nominal total %q", evaluation.NominalFB.Total)
		return plan
	}
	plan.nominal = nominal
	switch {
	case evaluation.ExemptAsset != "":
		plan.action = settlePlanClearExempt
	case evaluation.NeedsFunding:
		plan.action = settlePlanSweep
	case st != nil && st.NominalFB == nominal.String():
		plan.action = settlePlanUnchanged
	default:
		plan.action = settlePlanWriteState
	}
	return plan
}

func (r *Runtime) applySettlePlan(plan *settlePlan, height uint64) error {
	if plan == nil {
		return nil
	}
	switch plan.action {
	case settlePlanNoop:
		return nil
	case settlePlanClearExempt:
		r.stats.ExemptAccounts++
		r.clearState(plan.owner, plan.old)
		return nil
	case settlePlanSweep:
		return r.SweepAccount(plan.owner, height, plan.old)
	case settlePlanUnchanged:
		r.stats.UnchangedStates++
		return nil
	case settlePlanWriteState:
		r.stats.WrittenStates++
		r.writeState(plan.owner, height, plan.nominal, plan.old)
		return nil
	default:
		return fmt.Errorf("rent: unknown settle plan action %d for %s", plan.action, plan.owner)
	}
}

func rentParallelWorkers(items int) int {
	if items < parallelSettleMinOwners {
		return 1
	}
	workers := runtime.GOMAXPROCS(0)
	if workers > parallelSettleMaxWork {
		workers = parallelSettleMaxWork
	}
	if workers < 2 || items < workers*2 {
		return 1
	}
	return workers
}

func (r *Runtime) mergeServiceStats(stats ServiceStats) {
	r.serviceStats.BalanceReads += stats.BalanceReads
	r.serviceStats.BalanceCacheHits += stats.BalanceCacheHits
	r.serviceStats.BalanceReadDuration += stats.BalanceReadDuration
	r.serviceStats.ClaimMarkerReads += stats.ClaimMarkerReads
	r.serviceStats.ClaimMarkerHits += stats.ClaimMarkerHits
	r.serviceStats.ClaimMarkerDuration += stats.ClaimMarkerDuration
	r.serviceStats.ClaimTotalReads += stats.ClaimTotalReads
	r.serviceStats.ClaimTotalHits += stats.ClaimTotalHits
	r.serviceStats.ClaimTotalDuration += stats.ClaimTotalDuration
	r.serviceStats.ClaimScans += stats.ClaimScans
	r.serviceStats.ClaimScanKeys += stats.ClaimScanKeys
	r.serviceStats.ClaimScanDuration += stats.ClaimScanDuration
}

func (r *Runtime) settleAccountLegacy(owner string, height uint64) error {
	owner = strings.TrimSpace(owner)
	if !IsRentManagedAccount(owner) {
		return nil
	}
	r.clearReadCaches()
	plans, err := r.planSettleAccounts([]string{owner}, height)
	if err != nil {
		return err
	}
	if r.fundingAttempt != nil {
		owners := map[string]struct{}{owner: {}}
		attempted, err := r.fundSweepCandidates(plans, owners, height)
		if err != nil {
			return err
		}
		if attempted {
			r.clearReadCaches()
			plans, err = r.planSettleAccounts(sortedOwners(owners), height)
			if err != nil {
				return err
			}
		}
	}
	for i := range plans {
		if err := r.applySettlePlan(&plans[i], height); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) SweepAccount(owner string, height uint64, old *RentState) error {
	if !IsRentManagedAccount(owner) {
		return nil
	}
	sweepStart := time.Now()
	defer func() { r.stats.SweepDuration += time.Since(sweepStart) }()
	state := cloneRentState(old)
	if state == nil {
		var err error
		state, err = r.readState(owner)
		if err != nil {
			return err
		}
	}
	if state == nil {
		return fmt.Errorf("rent: sweep owner %s has no rent state", owner)
	}
	r.stats.SweepOwnersProcessed++
	if r.canFastSweepOwner(owner) {
		balanceStart := time.Now()
		if err := r.sweepPrimaryBalance(owner); err != nil {
			return err
		}
		r.stats.SweepBalanceDuration += time.Since(balanceStart)
		if finalizer, ok := r.cleaner.(SweepFinalizer); ok {
			if err := finalizer.FinalizeAccountSweep(owner, height); err != nil {
				return err
			}
		}
		r.store.Del(keys.KeyAccount(owner))
		r.clearState(owner, state)
		ClearOwnerMarkers(r.store, owner)
		r.stats.SweptAccounts++
		return nil
	}

	if state.Mode != RentModeSweeping || state.Work == nil || !isSweepPhase(state.Work.Phase) {
		previous := cloneRentState(state)
		state.Version = RentStateVersion
		state.Mode = RentModeSweeping
		state.Work = &RentWork{NextActionHeight: height, Phase: RentWorkSweepClaims}
		if err := r.writeAccrualState(state, previous); err != nil {
			return err
		}
	}

	                                                                            
	                                                                         
	                                                                         
	                             
	phase := RentWorkSweepClaims
	cleanerPrepared := state.Work != nil && state.Work.Cursor == "cleaner_prepared"
	phaseCursor := ""
	for {
		switch phase {
		case RentWorkSweep, RentWorkSweepClaims:
			claimScanStart := time.Now()
			claims, nextCursor, hasMore, err := r.scanSweepPage(keys.KeyAssetClaimOwnerPrefix(owner), phaseCursor)
			r.stats.SweepClaimScanDuration += time.Since(claimScanStart)
			if err != nil {
				return err
			}
			cleanerStart := time.Now()
			if pageCleaner, ok := r.cleaner.(ClaimPageCleaner); ok {
				err = pageCleaner.PrepareAccountSweepClaimPage(owner, height, claims)
			} else if claimCleaner, ok := r.cleaner.(ClaimAwareCleaner); ok {
				err = claimCleaner.PrepareAccountSweepWithClaims(owner, height, claims)
			} else if r.cleaner != nil && !cleanerPrepared {
				err = r.cleaner.PrepareAccountSweep(owner, height)
				cleanerPrepared = true
			}
			r.stats.SweepCleanerDuration += time.Since(cleanerStart)
			if err != nil {
				return err
			}
			claimStart := time.Now()
			for _, key := range sortedKeys(claims) {
				r.store.Del(key)
			}
			r.stats.SweepClaimDuration += time.Since(claimStart)
			if hasMore {
				phaseCursor = nextCursor
				continue
			}
			phase = RentWorkSweepBalances
			phaseCursor = ""
		case RentWorkSweepBalances:
			balanceStart := time.Now()
			balances, nextCursor, hasMore, err := r.scanSweepPage(keys.KeyBalancePrefix(owner), phaseCursor)
			if err == nil {
				err = r.sweepBalancePage(owner, balances)
			}
			r.stats.SweepBalanceDuration += time.Since(balanceStart)
			if err != nil {
				return err
			}
			if hasMore {
				phaseCursor = nextCursor
				continue
			}
			phase = RentWorkSweepClaimMarkers
			phaseCursor = ""
		case RentWorkSweepClaimMarkers:
			claimStart := time.Now()
			markers, nextCursor, hasMore, err := r.scanSweepPage(keys.KeyAssetClaimMarkerOwnerPrefix(owner), phaseCursor)
			if err == nil {
				for _, key := range sortedKeys(markers) {
					r.store.Del(key)
				}
			}
			r.stats.SweepClaimDuration += time.Since(claimStart)
			if err != nil {
				return err
			}
			if hasMore {
				phaseCursor = nextCursor
				continue
			}
			phase = RentWorkSweepClaimTotals
			phaseCursor = ""
		case RentWorkSweepClaimTotals:
			claimStart := time.Now()
			totals, nextCursor, hasMore, err := r.scanSweepPage(keys.KeyAssetClaimTotalOwnerPrefix(owner), phaseCursor)
			if err == nil {
				for _, key := range sortedKeys(totals) {
					r.store.Del(key)
				}
			}
			r.stats.SweepClaimDuration += time.Since(claimStart)
			if err != nil {
				return err
			}
			if hasMore {
				phaseCursor = nextCursor
				continue
			}
			phase = RentWorkSweepFinalize
			phaseCursor = ""
		case RentWorkSweepFinalize:
			if finalizer, ok := r.cleaner.(SweepFinalizer); ok {
				cleanerStart := time.Now()
				err := finalizer.FinalizeAccountSweep(owner, height)
				r.stats.SweepCleanerDuration += time.Since(cleanerStart)
				if err != nil {
					return err
				}
			}
			r.store.Del(keys.KeyAccount(owner))
			r.clearState(owner, state)
			ClearOwnerMarkers(r.store, owner)
			r.stats.SweptAccounts++
			return nil
		default:
			return fmt.Errorf("rent: unknown sweep phase %q for %s", phase, owner)
		}
	}
}

func (r *Runtime) prefetchNominalAssetReads(owners []string) error {
	if len(owners) == 0 {
		return nil
	}
	getter, ok := r.store.(BatchGetter)
	if !ok {
		return nil
	}
	if r.balanceRead == nil {
		r.balanceRead = make(map[string]cachedRead)
	}
	if r.claimMarkerRead == nil {
		r.claimMarkerRead = make(map[string]cachedRead)
	}
	if r.claimTotalRead == nil {
		r.claimTotalRead = make(map[string]cachedRead)
	}

	type readTarget struct {
		key   string
		cache map[string]cachedRead
	}
	readTargets := func(targets []readTarget) error {
		if len(targets) == 0 {
			return nil
		}
		keysToRead := make([]string, 0, len(targets))
		for _, target := range targets {
			keysToRead = append(keysToRead, target.key)
		}
		values, err := getter.GetMany(keysToRead)
		if err != nil {
			return err
		}
		r.stats.PrefetchedAssetKeys += len(keysToRead)
		for _, target := range targets {
			data, exists := values[target.key]
			target.cache[target.key] = cachedRead{data: cloneBytes(data), exists: exists}
		}
		return nil
	}

	                                                                            
	                                                          
	baseTargets := make([]readTarget, 0, len(owners)*2)
	for _, owner := range owners {
		balanceKey := keys.KeyBalance(owner, r.policy.Token)
		if _, cached := r.balanceRead[balanceKey]; !cached {
			baseTargets = append(baseTargets, readTarget{balanceKey, r.balanceRead})
		}
		markerKey := keys.KeyAssetClaimMarker(owner, r.policy.Token)
		if _, cached := r.claimMarkerRead[markerKey]; !cached {
			baseTargets = append(baseTargets, readTarget{markerKey, r.claimMarkerRead})
		}
	}
	if err := readTargets(baseTargets); err != nil {
		return err
	}
	baseClaimTargets := make([]readTarget, 0, len(owners))
	for _, owner := range owners {
		markerKey := keys.KeyAssetClaimMarker(owner, r.policy.Token)
		totalKey := keys.KeyAssetClaimTotal(owner, r.policy.Token)
		if _, cached := r.claimTotalRead[totalKey]; cached {
			continue
		}
		markers, markerCached := r.ownerMarkerRead[owner]
		markerComplete := markerCached && markers[OwnerMarkerInit]
		if r.claimMarkerRead[markerKey].exists || !markerComplete {
			baseClaimTargets = append(baseClaimTargets, readTarget{totalKey, r.claimTotalRead})
		} else {
			r.claimTotalRead[totalKey] = cachedRead{}
		}
	}
	if err := readTargets(baseClaimTargets); err != nil {
		return err
	}
	for _, owner := range owners {
		markers, markerCached := r.ownerMarkerRead[owner]
		if markerCached && markers[OwnerMarkerInit] {
			continue
		}
		markerKey := keys.KeyAssetClaimMarker(owner, r.policy.Token)
		totalKey := keys.KeyAssetClaimTotal(owner, r.policy.Token)
		if !r.claimMarkerRead[markerKey].exists && r.claimTotalRead[totalKey].exists {
			                                                                       
			                                                                   
			r.claimMarkerRead[markerKey] = cachedRead{data: []byte{1}, exists: true}
		}
	}

	                                                                       
	                                                                             
	                                                  
	if len(r.policy.ExemptAssets) > 0 {
		service := NewService(r.store, WithClaimMarkerFastPath(), withPrefetchedReads(r.balanceRead, r.claimMarkerRead, r.claimTotalRead))
		lowOwners := make([]string, 0, len(owners))
		for _, owner := range owners {
			summary, err := service.NominalAsset(owner, r.policy.Token)
			if err != nil {
				return err
			}
			if parseAmountString(summary.Total).Cmp(r.policy.ExemptMin) < 0 {
				lowOwners = append(lowOwners, owner)
			}
		}
		assetTargets := make([]readTarget, 0, len(lowOwners)*len(r.policy.ExemptAssets)*2)
		for _, owner := range lowOwners {
			markers, markerCached := r.ownerMarkerRead[owner]
			markerComplete := markerCached && markers[OwnerMarkerInit]
			skipExternal := markerComplete && !markers[OwnerMarkerNonFBBalance] && !markers[OwnerMarkerClaim]
			for _, asset := range r.policy.ExemptAssets {
				balanceKey := keys.KeyBalance(owner, asset.Token)
				markerKey := keys.KeyAssetClaimMarker(owner, asset.Token)
				totalKey := keys.KeyAssetClaimTotal(owner, asset.Token)
				if skipExternal {
					r.balanceRead[balanceKey] = cachedRead{}
					r.claimMarkerRead[markerKey] = cachedRead{}
					r.claimTotalRead[totalKey] = cachedRead{}
					continue
				}
				if _, cached := r.balanceRead[balanceKey]; !cached {
					assetTargets = append(assetTargets, readTarget{balanceKey, r.balanceRead})
				}
				if _, cached := r.claimMarkerRead[markerKey]; !cached {
					assetTargets = append(assetTargets, readTarget{markerKey, r.claimMarkerRead})
				}
			}
		}
		if err := readTargets(assetTargets); err != nil {
			return err
		}
		externalClaimTargets := make([]readTarget, 0)
		for _, owner := range lowOwners {
			for _, asset := range r.policy.ExemptAssets {
				markerKey := keys.KeyAssetClaimMarker(owner, asset.Token)
				totalKey := keys.KeyAssetClaimTotal(owner, asset.Token)
				if _, cached := r.claimTotalRead[totalKey]; cached {
					continue
				}
				markers, markerCached := r.ownerMarkerRead[owner]
				markerComplete := markerCached && markers[OwnerMarkerInit]
				if r.claimMarkerRead[markerKey].exists || !markerComplete {
					externalClaimTargets = append(externalClaimTargets, readTarget{totalKey, r.claimTotalRead})
				} else {
					r.claimTotalRead[totalKey] = cachedRead{}
				}
			}
		}
		if err := readTargets(externalClaimTargets); err != nil {
			return err
		}
		for _, owner := range lowOwners {
			markers, markerCached := r.ownerMarkerRead[owner]
			if markerCached && markers[OwnerMarkerInit] {
				continue
			}
			for _, asset := range r.policy.ExemptAssets {
				markerKey := keys.KeyAssetClaimMarker(owner, asset.Token)
				totalKey := keys.KeyAssetClaimTotal(owner, asset.Token)
				if !r.claimMarkerRead[markerKey].exists && r.claimTotalRead[totalKey].exists {
					r.claimMarkerRead[markerKey] = cachedRead{data: []byte{1}, exists: true}
				}
			}
		}
	}
	return nil
}

func (r *Runtime) prefetchOwnerMarkers(owners []string) error {
	if len(owners) == 0 {
		return nil
	}
	getter, ok := r.store.(BatchGetter)
	if !ok {
		return nil
	}
	if r.ownerMarkerRead == nil {
		r.ownerMarkerRead = make(map[string]map[string]bool)
	}
	keysToRead := make([]string, 0, len(owners)*len(ownerMarkerKinds))
	ownerByKey := make(map[string]string, len(owners)*len(ownerMarkerKinds))
	kindByKey := make(map[string]string, len(owners)*len(ownerMarkerKinds))
	for _, owner := range owners {
		if _, cached := r.ownerMarkerRead[owner]; cached {
			continue
		}
		r.ownerMarkerRead[owner] = map[string]bool{}
		for _, kind := range ownerMarkerKinds {
			k := keys.KeyRentOwnerMarker(owner, kind)
			keysToRead = append(keysToRead, k)
			ownerByKey[k] = owner
			kindByKey[k] = kind
		}
	}
	if len(keysToRead) == 0 {
		return nil
	}
	values, err := getter.GetMany(keysToRead)
	if err != nil {
		return err
	}
	r.stats.PrefetchedMarkerKeys += len(keysToRead)
	for k := range values {
		owner := ownerByKey[k]
		kind := kindByKey[k]
		if owner != "" && kind != "" {
			r.ownerMarkerRead[owner][kind] = true
		}
	}
	return nil
}

func (r *Runtime) ownerMarker(owner, kind string) bool {
	if r.ownerMarkerRead != nil {
		if cached, ok := r.ownerMarkerRead[owner]; ok {
			return cached[kind]
		}
	}
	data, exists, err := r.store.Get(keys.KeyRentOwnerMarker(owner, kind))
	return err == nil && exists && len(data) > 0
}

func (r *Runtime) canFastSweepOwner(owner string) bool {
	if !r.ownerMarker(owner, OwnerMarkerInit) {
		return false
	}
	for _, kind := range []string{OwnerMarkerSpotOrder, OwnerMarkerFutureOrder, OwnerMarkerClaim, OwnerMarkerNonFBBalance} {
		if r.ownerMarker(owner, kind) {
			return false
		}
	}
	return true
}

func (r *Runtime) dueOwners(height uint64) (map[string]struct{}, error) {
	kvs, err := r.store.Scan(keys.KeyRentExpiryPrefix(height), r.scanLimit)
	if err != nil {
		return nil, err
	}
	owners := make(map[string]struct{}, len(kvs))
	prefix := keys.KeyRentExpiryPrefix(height)
	for k, v := range kvs {
		owner := strings.TrimSpace(string(v))
		if owner == "" {
			owner = strings.TrimPrefix(k, prefix)
		}
		if IsRentManagedAccount(owner) {
			owners[owner] = struct{}{}
		}
	}
	return owners, nil
}

func (r *Runtime) readState(owner string) (*RentState, error) {
	if r.stateRead != nil {
		if cached, ok := r.stateRead[owner]; ok {
			if cached.state == nil {
				return nil, nil
			}
			cp := *cached.state
			return &cp, nil
		}
	}
	data, exists, err := r.store.Get(keys.KeyRentState(owner))
	if err != nil || !exists || len(data) == 0 {
		return nil, err
	}
	var st RentState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("rent: parse state %s: %w", owner, err)
	}
	return &st, nil
}

func (r *Runtime) prefetchStates(owners []string) error {
	if len(owners) == 0 {
		return nil
	}
	getter, ok := r.store.(BatchGetter)
	if !ok {
		return nil
	}
	if r.stateRead == nil {
		r.stateRead = make(map[string]cachedRentState)
	}
	stateKeys := make([]string, 0, len(owners))
	ownerByKey := make(map[string]string, len(owners))
	for _, owner := range owners {
		if _, cached := r.stateRead[owner]; cached {
			continue
		}
		k := keys.KeyRentState(owner)
		stateKeys = append(stateKeys, k)
		ownerByKey[k] = owner
		r.stateRead[owner] = cachedRentState{}
	}
	if len(stateKeys) == 0 {
		return nil
	}
	values, err := getter.GetMany(stateKeys)
	if err != nil {
		return err
	}
	for k, data := range values {
		if len(data) == 0 {
			continue
		}
		owner := ownerByKey[k]
		if owner == "" {
			continue
		}
		var st RentState
		if err := json.Unmarshal(data, &st); err != nil {
			return fmt.Errorf("rent: parse state %s: %w", owner, err)
		}
		cp := st
		r.stateRead[owner] = cachedRentState{state: &cp}
	}
	return nil
}

func (r *Runtime) writeState(owner string, height uint64, remaining *big.Int, old *RentState) {
	r.writeFundedState(owner, height, remaining, remaining, old)
}

func (r *Runtime) writeFundedState(owner string, height uint64, nominal, credit *big.Int, old *RentState) {
	expire := height + ceilDivUint64(credit, r.policy.RentPerBlock)
	st := RentState{Owner: owner, LastSettledHeight: height, ExpireHeight: expire, NominalFB: nominal.String()}
	if oldKey := rentStateWorkKey(old); oldKey != "" && oldKey != keys.KeyRentExpiry(expire, owner) {
		r.store.Del(oldKey)
	}
	data, _ := json.Marshal(&st)
	r.store.Set(keys.KeyRentState(owner), data)
	r.store.Set(keys.KeyRentExpiry(expire, owner), []byte(owner))
	r.observeRentWork(&st)
	if r.stateRead != nil {
		cp := st
		r.stateRead[owner] = cachedRentState{state: &cp}
	}
}

func (r *Runtime) clearState(owner string, old *RentState) {
	if key := rentStateWorkKey(old); key != "" {
		r.store.Del(key)
	}
	r.store.Del(keys.KeyRentState(owner))
	if r.stateRead != nil {
		r.stateRead[owner] = cachedRentState{}
	}
}

func (r *Runtime) scanSweepPage(prefix, startAfter string) (map[string][]byte, string, bool, error) {
	pageLimit := r.scanLimit
	if pageLimit <= 0 {
		pageLimit = DefaultScanLimit
	}
	if r.sweepKeyBudget > 0 && r.sweepKeyBudget < pageLimit {
		pageLimit = r.sweepKeyBudget
	}
	if pageLimit <= 0 {
		return nil, "", false, fmt.Errorf("rent: invalid sweep page size")
	}
	readLimit := pageLimit + 1
	result := make(map[string][]byte, pageLimit)
	if ordered, ok := r.store.(orderedRentStore); ok {
		rows, err := ordered.ScanRentOrderedAfter(prefix, startAfter, readLimit)
		if err != nil {
			return nil, "", false, err
		}
		scanned := len(rows)
		hasMore := scanned > pageLimit
		if len(rows) > pageLimit {
			rows = rows[:pageLimit]
		}
		for _, row := range rows {
			result[row.Key] = row.Value
		}
		r.stats.SweepKeysScanned += scanned
		nextCursor := ""
		if len(rows) > 0 {
			nextCursor = rows[len(rows)-1].Key
		}
		return result, nextCursor, hasMore, nil
	}

	                                                                           
	                                                                          
	                                                                           
	rows, err := r.store.Scan(prefix, 0)
	if err != nil {
		return nil, "", false, err
	}
	orderedKeys := sortedKeys(rows)
	if startAfter != "" {
		start := sort.SearchStrings(orderedKeys, startAfter)
		for start < len(orderedKeys) && orderedKeys[start] <= startAfter {
			start++
		}
		orderedKeys = orderedKeys[start:]
	}
	if len(orderedKeys) > readLimit {
		orderedKeys = orderedKeys[:readLimit]
	}
	scanned := len(orderedKeys)
	hasMore := scanned > pageLimit
	processedKeys := orderedKeys
	if len(processedKeys) > pageLimit {
		processedKeys = processedKeys[:pageLimit]
	}
	for _, key := range processedKeys {
		result[key] = rows[key]
	}
	r.stats.SweepKeysScanned += scanned
	nextCursor := ""
	if len(processedKeys) > 0 {
		nextCursor = processedKeys[len(processedKeys)-1]
	}
	return result, nextCursor, hasMore, nil
}

func (r *Runtime) sweepBalancePage(owner string, kvs map[string][]byte) error {
	prefix := keys.KeyBalancePrefix(owner)
	for _, key := range sortedKeys(kvs) {
		var record pb.TokenBalanceRecord
		if err := proto.Unmarshal(kvs[key], &record); err != nil {
			return fmt.Errorf("rent: parse balance %s: %w", key, err)
		}
		token := record.Token
		if token == "" {
			token = strings.TrimPrefix(key, prefix)
		}
		total, err := tokenBalanceTotalCanonical("sweep balance "+owner+"/"+token, record.Balance)
		if err != nil {
			return err
		}
		if total.Sign() > 0 {
			if err := r.addRecycleBalance(token, total); err != nil {
				return err
			}
		}
		r.store.Del(key)
	}
	return nil
}

func (r *Runtime) sweepBalances(owner string) error {
	kvs, err := r.store.Scan(keys.KeyBalancePrefix(owner), r.scanLimit)
	if err != nil {
		return err
	}
	return r.sweepBalancePage(owner, kvs)
}

func (r *Runtime) sweepPrimaryBalance(owner string) error {
	record, exists, err := r.readBalance(owner, r.policy.Token)
	if err != nil {
		return err
	}
	if exists {
		total, err := tokenBalanceTotalCanonical("sweep primary balance "+owner+"/"+r.policy.Token, record.Balance)
		if err != nil {
			return err
		}
		if total.Sign() > 0 {
			if err := r.addRecycleBalance(r.policy.Token, total); err != nil {
				return err
			}
		}
		r.store.Del(keys.KeyBalance(owner, r.policy.Token))
	}
	return nil
}

func (r *Runtime) scanOwnerClaims(owner string) (map[string][]byte, error) {
	return r.store.Scan(keys.KeyAssetClaimOwnerPrefix(owner), r.scanLimit)
}

func (r *Runtime) sweepClaims(owner string, claims map[string][]byte) error {
	kvs := claims
	var err error
	if kvs == nil {
		kvs, err = r.scanOwnerClaims(owner)
	}
	if err != nil {
		return err
	}
	for _, k := range sortedKeys(kvs) {
		r.store.Del(k)
	}
	markers, err := r.store.Scan(keys.KeyAssetClaimMarkerOwnerPrefix(owner), r.scanLimit)
	if err != nil {
		return err
	}
	for _, k := range sortedKeys(markers) {
		r.store.Del(k)
	}
	totals, err := r.store.Scan(keys.KeyAssetClaimTotalOwnerPrefix(owner), r.scanLimit)
	if err != nil {
		return err
	}
	for _, k := range sortedKeys(totals) {
		r.store.Del(k)
	}
	return nil
}

func (r *Runtime) addRecycleBalance(token string, amount *big.Int) error {
	if strings.TrimSpace(token) == "" || amount == nil || amount.Sign() <= 0 {
		return nil
	}
	if r.recycleTotals != nil {
		if r.recycleTotals[token] == nil {
			r.recycleTotals[token] = new(big.Int)
		}
		r.recycleTotals[token].Add(r.recycleTotals[token], amount)
		return nil
	}
	return r.addRecycleBalanceNow(token, amount)
}

func (r *Runtime) flushRecycleBalances() error {
	if len(r.recycleTotals) == 0 {
		return nil
	}
	tokens := make([]string, 0, len(r.recycleTotals))
	for token := range r.recycleTotals {
		tokens = append(tokens, token)
	}
	sort.Strings(tokens)
	for _, token := range tokens {
		if err := r.addRecycleBalanceNow(token, r.recycleTotals[token]); err != nil {
			return err
		}
		r.stats.RecycleFlushes++
	}
	return nil
}

func (r *Runtime) addRecycleBalanceNow(token string, amount *big.Int) error {
	if strings.TrimSpace(token) == "" || amount == nil || amount.Sign() <= 0 {
		return nil
	}
	record, err := r.recycleBalanceAfter(token, amount)
	if err != nil {
		return err
	}
	data, err := proto.Marshal(record)
	if err != nil {
		return err
	}
	r.store.Set(keys.KeyBalance(r.policy.RecycleAccount, token), data)
	return nil
}

func (r *Runtime) recycleBalanceAfter(token string, amount *big.Int) (*pb.TokenBalanceRecord, error) {
	if strings.TrimSpace(token) == "" || amount == nil || amount.Sign() <= 0 {
		return nil, fmt.Errorf("rent: invalid recycle balance addition")
	}
	record, _, err := r.readBalance(r.policy.RecycleAccount, token)
	if err != nil {
		return nil, err
	}
	if record.Address == "" {
		record.Address = r.policy.RecycleAccount
	}
	if record.Token == "" {
		record.Token = token
	}
	if record.Balance == nil {
		record.Balance = &pb.TokenBalance{}
	}
	available, err := amountFromCanonical("recycle fund balance", record.Balance.Balance)
	if err != nil {
		return nil, err
	}
	record.Balance.Balance = new(big.Int).Add(available, amount).String()
	return record, nil
}

func (r *Runtime) readBalance(owner, token string) (*pb.TokenBalanceRecord, bool, error) {
	key := keys.KeyBalance(owner, token)
	data, exists, ok := cachedGet(r.balanceRead, key)
	var err error
	if !ok {
		data, exists, err = r.store.Get(key)
	}
	if err != nil || !exists || len(data) == 0 {
		return &pb.TokenBalanceRecord{Address: owner, Token: token, Balance: &pb.TokenBalance{}}, false, err
	}
	var record pb.TokenBalanceRecord
	if err := proto.Unmarshal(data, &record); err != nil {
		return nil, false, fmt.Errorf("rent: parse balance %s/%s: %w", owner, token, err)
	}
	if record.Balance == nil {
		record.Balance = &pb.TokenBalance{}
	}
	return &record, true, nil
}

func IsProtocolAccount(owner string) bool {
	owner = strings.TrimSpace(owner)
	return owner == "" || owner == RecycleFundAccount || strings.HasPrefix(owner, "__")
}

func IsRentManagedAccount(owner string) bool {
	owner = strings.TrimSpace(owner)
	return strings.HasPrefix(owner, "fbc1") && !IsProtocolAccount(owner)
}

func normalizePolicy(policy Policy) Policy {
	if strings.TrimSpace(policy.Token) == "" {
		policy.Token = FeeToken
	}
	if policy.ExemptMin == nil || policy.ExemptMin.Sign() <= 0 {
		policy.ExemptMin = mustAmount(DefaultRentExemptMinRaw)
	} else {
		policy.ExemptMin = new(big.Int).Set(policy.ExemptMin)
	}
	if policy.RentPerBlock == nil || policy.RentPerBlock.Sign() <= 0 {
		policy.RentPerBlock = mustAmount(DefaultRentPerBlockRaw)
	} else {
		policy.RentPerBlock = new(big.Int).Set(policy.RentPerBlock)
	}
	if strings.TrimSpace(policy.RecycleAccount) == "" {
		policy.RecycleAccount = RecycleFundAccount
	}
	if policy.ExemptAssets != nil {
		assets := make([]ExemptAsset, len(policy.ExemptAssets))
		for i, asset := range policy.ExemptAssets {
			assets[i] = ExemptAsset{Token: asset.Token}
			if asset.Minimum != nil {
				assets[i].Minimum = new(big.Int).Set(asset.Minimum)
			}
		}
		policy.ExemptAssets = assets
	}
	return policy
}

func sortedOwners(in map[string]struct{}) []string {
	out := make([]string, 0, len(in))
	for owner := range in {
		if IsRentManagedAccount(owner) {
			out = append(out, owner)
		}
	}
	sort.Strings(out)
	return out
}

func sortedKeys(in map[string][]byte) []string {
	out := make([]string, 0, len(in))
	for k := range in {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func ceilDivUint64(n, d *big.Int) uint64 {
	if n == nil || d == nil || n.Sign() <= 0 || d.Sign() <= 0 {
		return 0
	}
	q := new(big.Int).Sub(new(big.Int).Add(n, d), big.NewInt(1))
	q.Div(q, d)
	if !q.IsUint64() {
		return ^uint64(0)
	}
	return q.Uint64()
}

func mustAmount(raw string) *big.Int {
	out, ok := new(big.Int).SetString(raw, 10)
	if !ok {
		panic("invalid rent amount")
	}
	return out
}
