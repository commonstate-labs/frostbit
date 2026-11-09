package vm

import (
	"dex/keys"
	"dex/pb"
	"dex/rent"
	"encoding/json"
	"fmt"
	"math/big"
	"math/bits"
	"reflect"
	"sort"
	"strings"
	"testing"
)

const (
	overlayRentDueOwnerCount = 257
	overlayRentDueHeight     = uint64(10)
	overlayRentSeedKeyCount  = 257
	overlayRentMovedOwners   = 128
)

type overlayRentScanProbe struct {
	base               map[string][]byte
	expiryScanCalls    int
	expiryRowsReturned int
	activitySeqScans   int
	orderedScanCalls   map[string]int
	orderedRows        map[string]int
}

func TestOrderedFirstPageScansStayLinearAsTombstonesAccumulate(t *testing.T) {
	const rowCount = 257
	const prefix = "rent_funding_book/"

	for _, keysOnly := range []bool{false, true} {
		name := "pairs"
		if keysOnly {
			name = "keys"
		}
		t.Run(name, func(t *testing.T) {
			base := make(map[string][]byte, rowCount)
			for i := 0; i < rowCount; i++ {
				base[fmt.Sprintf("%s%04d", prefix, i)] = []byte{1}
			}
			probe := &overlayRentScanProbe{base: base}
			sv := newOverlayRentSchedulerStateView(probe)

			for i := 0; i < rowCount; i++ {
				var key string
				if keysOnly {
					page, err := scanStateViewOrderedKeysAfter(sv, prefix, "", 1, false)
					if err != nil {
						t.Fatalf("scan first key page %d: %v", i, err)
					}
					if len(page) != 1 {
						t.Fatalf("first key page %d len=%d", i, len(page))
					}
					key = page[0]
				} else {
					page, err := scanStateViewOrderedAfter(sv, prefix, "", 1, false)
					if err != nil {
						t.Fatalf("scan first pair page %d: %v", i, err)
					}
					if len(page) != 1 {
						t.Fatalf("first pair page %d len=%d", i, len(page))
					}
					key = page[0].Key
				}
				want := fmt.Sprintf("%s%04d", prefix, i)
				if key != want {
					t.Fatalf("first page %d key=%q want=%q", i, key, want)
				}
				sv.Del(key)
			}

			if keysOnly {
				if page, err := scanStateViewOrderedKeysAfter(sv, prefix, "", 1, false); err != nil {
					t.Fatalf("scan exhausted key book: %v", err)
				} else if len(page) != 0 {
					t.Fatalf("exhausted key book returned %v", page)
				}
			} else if page, err := scanStateViewOrderedAfter(sv, prefix, "", 1, false); err != nil {
				t.Fatalf("scan exhausted pair book: %v", err)
			} else if len(page) != 0 {
				t.Fatalf("exhausted pair book returned %v", page)
			}
			if got, max := probe.orderedRows[prefix], 2*rowCount; got > max {
				t.Fatalf("first-page base rows=%d want<=%d", got, max)
			}
			if got, max := probe.orderedScanCalls[prefix], bits.Len(uint(rowCount))+1; got > max {
				t.Fatalf("first-page base scans=%d want<=%d", got, max)
			}
			t.Logf("first-page scans=%d rows=%d", probe.orderedScanCalls[prefix], probe.orderedRows[prefix])
		})
	}
}

func (p *overlayRentScanProbe) pairs(prefix, startAfter string, limit int, reverse, countExpiry bool) []KVPair {
	orderedKeys := make([]string, 0)
	for key := range p.base {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		if startAfter != "" {
			if reverse && key >= startAfter {
				continue
			}
			if !reverse && key <= startAfter {
				continue
			}
		}
		orderedKeys = append(orderedKeys, key)
	}
	sort.Strings(orderedKeys)
	if reverse {
		for left, right := 0, len(orderedKeys)-1; left < right; left, right = left+1, right-1 {
			orderedKeys[left], orderedKeys[right] = orderedKeys[right], orderedKeys[left]
		}
	}
	if limit > 0 && len(orderedKeys) > limit {
		orderedKeys = orderedKeys[:limit]
	}
	pairs := make([]KVPair, 0, len(orderedKeys))
	for _, key := range orderedKeys {
		pairs = append(pairs, KVPair{Key: key, Value: cloneBytes(p.base[key])})
	}
	if countExpiry && prefix == keys.KeyRentExpiryAllPrefix() {
		p.expiryScanCalls++
		p.expiryRowsReturned += len(pairs)
	}
	if countExpiry {
		if p.orderedScanCalls == nil {
			p.orderedScanCalls = make(map[string]int)
			p.orderedRows = make(map[string]int)
		}
		p.orderedScanCalls[prefix]++
		p.orderedRows[prefix] += len(pairs)
	}
	return pairs
}

func TestRentSchedulerOverlayTombstonesRemainLinearAcrossPageSizes(t *testing.T) {
	base, owners, futureKey := overlayRentSchedulerFixture(t)
	immutableBase := cloneBytesMap(base)
	var reference map[string][]byte

	for _, pageSize := range []int{1, 7, 64} {
		t.Run(fmt.Sprintf("page_%d", pageSize), func(t *testing.T) {
			probe := &overlayRentScanProbe{base: base}
			sv := newOverlayRentSchedulerStateView(probe)
			runtime := rent.NewRuntime(
				rentStateViewStore{sv: sv},
				rent.WithPolicy(rent.Policy{
					Token:          rent.FeeToken,
					ExemptMin:      big.NewInt(10),
					ExemptAssets:   []rent.ExemptAsset{},
					RentPerBlock:   big.NewInt(1),
					RecycleAccount: rent.RecycleFundAccount,
				}),
				rent.WithRuntimeSweepBudget(pageSize, pageSize),
			)

			if err := runtime.ProcessBlock(overlayRentDueHeight, nil); err != nil {
				t.Fatalf("process block: %v", err)
			}
			if got := runtime.LastStats().DueOwners; got != overlayRentDueOwnerCount {
				t.Fatalf("due owners=%d want=%d", got, overlayRentDueOwnerCount)
			}
			if got := runtime.LastStats().SweptAccounts; got != overlayRentDueOwnerCount {
				t.Fatalf("swept accounts=%d want=%d", got, overlayRentDueOwnerCount)
			}

			for _, owner := range owners {
				for _, key := range []string{
					keys.KeyAccount(owner),
					keys.KeyRentState(owner),
					keys.KeyRentExpiry(overlayRentDueHeight, owner),
					keys.KeyRentOwnerMarker(owner, rent.OwnerMarkerInit),
				} {
					if _, exists, err := sv.Get(key); err != nil {
						t.Fatalf("get cleaned key %q: %v", key, err)
					} else if exists {
						t.Fatalf("due owner %s retained key %q", owner, key)
					}
				}
			}
			if value, exists, err := sv.Get(futureKey); err != nil {
				t.Fatalf("get future sentinel: %v", err)
			} else if !exists || string(value) != "fbc1qrentoverlayfuture" {
				t.Fatalf("future sentinel changed: exists=%v value=%q", exists, value)
			}

			                                                                 
			                                                                    
			                                                             
			maxCalls := (overlayRentDueOwnerCount+pageSize-1)/pageSize +
				2*bits.Len(uint(overlayRentDueOwnerCount+1)) + 2
			if probe.expiryScanCalls > maxCalls {
				t.Fatalf("rent expiry ordered scans=%d want<=%d", probe.expiryScanCalls, maxCalls)
			}
			                                                                         
			                                                                          
			                                                                       
			maxRows := 2 * (overlayRentDueOwnerCount + 1)
			if probe.expiryRowsReturned > maxRows {
				t.Fatalf("rent expiry rows returned=%d want<=%d", probe.expiryRowsReturned, maxRows)
			}
			t.Logf("rent expiry ordered scans=%d rows=%d", probe.expiryScanCalls, probe.expiryRowsReturned)

			finalState := overlayRentEffectiveState(base, sv.Diff())
			if reference == nil {
				reference = finalState
			} else if !reflect.DeepEqual(finalState, reference) {
				t.Fatal("final effective state depends on rent due page size")
			}
			if !reflect.DeepEqual(base, immutableBase) {
				t.Fatal("StateView callbacks mutated the immutable base fixture")
			}
		})
	}
}

func TestRentSchedulerPreseededOverlayTombstonesRemainLinear(t *testing.T) {
	base, owners, futureKey := overlayRentSchedulerFixture(t)
	removedKey := keys.KeyRentExpiry(overlayRentDueHeight+2, "fbc1qrentoverlayremoved")
	base[removedKey] = []byte("fbc1qrentoverlayremoved")
	immutableBase := cloneBytesMap(base)
	var reference map[string][]byte

	for _, pageSize := range []int{1, 7, 64} {
		t.Run(fmt.Sprintf("page_%d", pageSize), func(t *testing.T) {
			probe := &overlayRentScanProbe{base: base}
			sv := newOverlayRentSchedulerStateView(probe)
			seedOverlayRentExpiryChanges(t, sv, base, owners, removedKey)
			if got := countOverlayRentExpiryOps(sv.Diff()); got != overlayRentSeedKeyCount {
				t.Fatalf("preseeded rent expiry overlay keys=%d want=%d", got, overlayRentSeedKeyCount)
			}

			runtime := rent.NewRuntime(
				rentStateViewStore{sv: sv},
				rent.WithPolicy(overlayRentTestPolicy()),
				rent.WithRuntimeSweepBudget(pageSize, pageSize),
			)
			if err := runtime.ProcessBlock(overlayRentDueHeight, nil); err != nil {
				t.Fatalf("process block: %v", err)
			}
			if got := runtime.LastStats().DueOwners; got != overlayRentDueOwnerCount {
				t.Fatalf("due owners=%d want=%d", got, overlayRentDueOwnerCount)
			}
			if got := runtime.LastStats().SweptAccounts; got != overlayRentDueOwnerCount {
				t.Fatalf("swept accounts=%d want=%d", got, overlayRentDueOwnerCount)
			}
			for i, owner := range owners {
				ownerKeys := []string{
					keys.KeyAccount(owner),
					keys.KeyRentState(owner),
					keys.KeyRentExpiry(overlayRentDueHeight, owner),
				}
				if i < overlayRentMovedOwners {
					ownerKeys = append(ownerKeys, keys.KeyRentExpiry(overlayRentDueHeight-1, owner))
				}
				for _, key := range ownerKeys {
					if _, exists, err := sv.Get(key); err != nil {
						t.Fatalf("get cleaned key %q: %v", key, err)
					} else if exists {
						t.Fatalf("due owner %s retained key %q", owner, key)
					}
				}
			}
			if value, exists, err := sv.Get(futureKey); err != nil {
				t.Fatalf("get future sentinel: %v", err)
			} else if !exists || string(value) != "fbc1qrentoverlayfuture" {
				t.Fatalf("future sentinel changed: exists=%v value=%q", exists, value)
			}
			if _, exists, err := sv.Get(removedKey); err != nil {
				t.Fatalf("get preseed tombstone: %v", err)
			} else if exists {
				t.Fatalf("preseed tombstone %q was resurrected", removedKey)
			}

			maxCalls := (overlayRentDueOwnerCount+pageSize-1)/pageSize +
				2*bits.Len(uint(overlayRentDueOwnerCount+overlayRentSeedKeyCount+2)) + 2
			                                                                          
			                                                                          
			                                                            
			maxRows := 4 * (overlayRentDueOwnerCount + overlayRentSeedKeyCount + 2)
			t.Logf("preseeded rent expiry ordered scans=%d rows=%d limits=(%d,%d)",
				probe.expiryScanCalls, probe.expiryRowsReturned, maxCalls, maxRows)
			if probe.expiryScanCalls > maxCalls {
				t.Errorf("rent expiry ordered scans=%d want<=%d", probe.expiryScanCalls, maxCalls)
			}
			if probe.expiryRowsReturned > maxRows {
				t.Errorf("rent expiry rows returned=%d want<=%d", probe.expiryRowsReturned, maxRows)
			}

			finalState := overlayRentEffectiveState(base, sv.Diff())
			if reference == nil {
				reference = finalState
			} else if !reflect.DeepEqual(finalState, reference) {
				t.Fatal("final effective state depends on rent due page size")
			}
			if !reflect.DeepEqual(base, immutableBase) {
				t.Fatal("StateView callbacks mutated the immutable base fixture")
			}
		})
	}
}

func TestRentSchedulerLocal20DueScaleRemainsLinear(t *testing.T) {
	for _, dueOwners := range []int{3721, 3722, 3723} {
		for _, pageSize := range []int{63, 64, 65} {
			t.Run(fmt.Sprintf("owners_%d_page_%d", dueOwners, pageSize), func(t *testing.T) {
				base, _, futureKey := overlayRentSchedulerFixtureCount(t, dueOwners)
				probe := &overlayRentScanProbe{base: base}
				sv := newOverlayRentSchedulerStateView(probe)
				runtime := rent.NewRuntime(
					rentStateViewStore{sv: sv},
					rent.WithPolicy(overlayRentTestPolicy()),
					rent.WithRuntimeSweepBudget(pageSize, pageSize),
				)
				if err := runtime.ProcessBlock(overlayRentDueHeight, nil); err != nil {
					t.Fatalf("process local20-scale due set: %v", err)
				}
				if stats := runtime.LastStats(); stats.DueOwners != dueOwners || stats.SweptAccounts != dueOwners {
					t.Fatalf("local20-scale due work incomplete: %+v", stats)
				}
				if _, exists, err := sv.Get(futureKey); err != nil || !exists {
					t.Fatalf("future sentinel changed: exists=%v err=%v", exists, err)
				}
				maxCalls := (dueOwners+pageSize-1)/pageSize + 2*bits.Len(uint(dueOwners+1)) + 2
				maxRows := 3 * (dueOwners + 1)
				if probe.expiryScanCalls > maxCalls || probe.expiryRowsReturned > maxRows {
					t.Fatalf("local20-scale expiry work is not linear: calls=%d/%d rows=%d/%d",
						probe.expiryScanCalls, maxCalls, probe.expiryRowsReturned, maxRows)
				}
				t.Logf("local20-scale due owners=%d page=%d scans=%d rows=%d",
					dueOwners, pageSize, probe.expiryScanCalls, probe.expiryRowsReturned)
			})
		}
	}
}

func TestRentRuntimeLocal20MarkersCollapseExternalAssetPrefetch(t *testing.T) {
	const (
		touchedOwners = 5005
		dueOwners     = 3722
		pageSize      = 64
	)
	base, owners, _ := overlayRentSchedulerFixtureCount(t, dueOwners)
	for i := dueOwners; i < touchedOwners; i++ {
		owner := fmt.Sprintf("fbc1qrentoverlay%06d", i)
		owners = append(owners, owner)
		base[keys.KeyAccount(owner)] = []byte("account")
		base[keys.KeyRentOwnerMarker(owner, rent.OwnerMarkerInit)] = []byte{1}
		balance, err := MarshalProto(&pb.TokenBalanceRecord{
			Address: owner,
			Token:   rent.FeeToken,
			Balance: &pb.TokenBalance{Balance: "10"},
		})
		if err != nil {
			t.Fatal(err)
		}
		base[keys.KeyBalance(owner, rent.FeeToken)] = balance
	}

	policy := rent.DefaultPolicy()
	policy.ExemptMin = big.NewInt(10)
	policy.RentPerBlock = big.NewInt(1)
	probe := &overlayRentScanProbe{base: base}
	sv := newOverlayRentSchedulerStateView(probe)
	runtime := rent.NewRuntime(
		rentStateViewStore{sv: sv},
		rent.WithPolicy(policy),
		rent.WithRuntimeSweepBudget(pageSize, pageSize),
	)
	if err := runtime.ProcessBlock(overlayRentDueHeight, owners); err != nil {
		t.Fatalf("process local20 marker scale: %v", err)
	}
	stats := runtime.LastStats()
	if stats.DueOwners != dueOwners || stats.SweptAccounts != dueOwners {
		t.Fatalf("local20 marker work incomplete: %+v", stats)
	}
	                                                                             
	                                                                              
	                                                                             
	                                                    
	maxAssetKeys := 2*touchedOwners + dueOwners
	if stats.PrefetchedAssetKeys > maxAssetKeys {
		t.Fatalf("prefetched asset keys=%d want<=%d", stats.PrefetchedAssetKeys, maxAssetKeys)
	}
	maxMarkerKeys := touchedOwners * 5
	if stats.PrefetchedMarkerKeys > maxMarkerKeys {
		t.Fatalf("prefetched marker keys=%d want<=%d", stats.PrefetchedMarkerKeys, maxMarkerKeys)
	}
	t.Logf("local20 markers touched=%d due=%d assetKeys=%d markerKeys=%d expiryScans=%d expiryRows=%d",
		touchedOwners, dueOwners, stats.PrefetchedAssetKeys, stats.PrefetchedMarkerKeys,
		probe.expiryScanCalls, probe.expiryRowsReturned)
}

func TestRentSweepOverlayPagesRemainLinear(t *testing.T) {
	const balanceCount = 257
	owner := "fbc1qrentsweepoverlay000000000000000000000"
	seed := newMemStateView()
	seed.Set(keys.KeyAccount(owner), []byte("account"))
	state := rent.RentState{
		Version:           rent.RentStateVersion,
		Owner:             owner,
		LastSettledHeight: overlayRentDueHeight,
		LastAccruedHeight: overlayRentDueHeight,
		UnpaidFB:          "1",
		Mode:              rent.RentModeNeedsFunding,
		ExpireHeight:      overlayRentDueHeight,
		NominalFB:         "0",
		Work: &rent.RentWork{
			NextActionHeight: overlayRentDueHeight,
			Phase:            rent.RentWorkFunding,
		},
	}
	encoded, err := json.Marshal(&state)
	if err != nil {
		t.Fatal(err)
	}
	seed.Set(keys.KeyRentState(owner), encoded)
	seed.Set(keys.KeyRentExpiry(overlayRentDueHeight, owner), []byte(owner))
	rent.MarkOwnerInitialized(seed, owner)
	rent.MarkOwnerActivity(seed, owner, rent.OwnerMarkerNonFBBalance)
	for i := 0; i < balanceCount; i++ {
		SetBalance(seed, owner, fmt.Sprintf("ASSET%03d", i), &pb.TokenBalance{Balance: "1"})
	}
	base := overlayRentEffectiveState(nil, seed.Diff())
	prefix := keys.KeyBalancePrefix(owner)

	for _, pageSize := range []int{1, 7, 64} {
		t.Run(fmt.Sprintf("page_%d", pageSize), func(t *testing.T) {
			probe := &overlayRentScanProbe{base: base}
			sv := newOverlayRentSchedulerStateView(probe)
			runtime := rent.NewRuntime(
				rentStateViewStore{sv: sv},
				rent.WithPolicy(overlayRentTestPolicy()),
				rent.WithRuntimeSweepBudget(pageSize, pageSize),
			)
			if err := runtime.ProcessBlock(overlayRentDueHeight, nil); err != nil {
				t.Fatalf("process sweep: %v", err)
			}
			if _, exists, err := sv.Get(keys.KeyAccount(owner)); err != nil || exists {
				t.Fatalf("swept account remains: exists=%v err=%v", exists, err)
			}
			maxCalls := (balanceCount+pageSize-1)/pageSize + 1
			maxRows := balanceCount + maxCalls
			if calls, rows := probe.orderedScanCalls[prefix], probe.orderedRows[prefix]; calls > maxCalls || rows > maxRows {
				t.Fatalf("balance sweep is not linear: calls=%d/%d rows=%d/%d", calls, maxCalls, rows, maxRows)
			}
			t.Logf("balance sweep page=%d scans=%d rows=%d", pageSize, probe.orderedScanCalls[prefix], probe.orderedRows[prefix])
		})
	}
}

func seedOverlayRentExpiryChanges(t *testing.T, sv StateView, base map[string][]byte, owners []string, removedKey string) {
	t.Helper()
	for _, owner := range owners[:overlayRentMovedOwners] {
		var state rent.RentState
		if err := json.Unmarshal(base[keys.KeyRentState(owner)], &state); err != nil {
			t.Fatalf("decode rent state for %s: %v", owner, err)
		}
		state.ExpireHeight = overlayRentDueHeight - 1
		state.Work.NextActionHeight = overlayRentDueHeight - 1
		encoded, err := json.Marshal(&state)
		if err != nil {
			t.Fatalf("encode rent state for %s: %v", owner, err)
		}
		sv.Del(keys.KeyRentExpiry(overlayRentDueHeight, owner))
		sv.Set(keys.KeyRentState(owner), encoded)
		sv.Set(keys.KeyRentExpiry(overlayRentDueHeight-1, owner), []byte(owner))
	}
	sv.Del(removedKey)
}

func countOverlayRentExpiryOps(diff []WriteOp) int {
	count := 0
	for _, op := range diff {
		if strings.HasPrefix(op.Key, keys.KeyRentExpiryAllPrefix()) {
			count++
		}
	}
	return count
}

func overlayRentTestPolicy() rent.Policy {
	return rent.Policy{
		Token:          rent.FeeToken,
		ExemptMin:      big.NewInt(10),
		ExemptAssets:   []rent.ExemptAsset{},
		RentPerBlock:   big.NewInt(1),
		RecycleAccount: rent.RecycleFundAccount,
	}
}

func overlayRentSchedulerFixture(t *testing.T) (map[string][]byte, []string, string) {
	return overlayRentSchedulerFixtureCount(t, overlayRentDueOwnerCount)
}

func overlayRentSchedulerFixtureCount(t *testing.T, ownerCount int) (map[string][]byte, []string, string) {
	t.Helper()
	base := make(map[string][]byte, ownerCount*4+1)
	owners := make([]string, 0, ownerCount)
	for i := 0; i < ownerCount; i++ {
		owner := fmt.Sprintf("fbc1qrentoverlay%06d", i)
		owners = append(owners, owner)
		state := rent.RentState{
			Version:           rent.RentStateVersion,
			Owner:             owner,
			LastSettledHeight: overlayRentDueHeight,
			LastAccruedHeight: overlayRentDueHeight,
			UnpaidFB:          "1",
			Mode:              rent.RentModeNeedsFunding,
			ExpireHeight:      overlayRentDueHeight,
			NominalFB:         "0",
			Work: &rent.RentWork{
				NextActionHeight: overlayRentDueHeight,
				Phase:            rent.RentWorkFunding,
			},
		}
		encoded, err := json.Marshal(&state)
		if err != nil {
			t.Fatal(err)
		}
		base[keys.KeyAccount(owner)] = []byte("account")
		base[keys.KeyRentState(owner)] = encoded
		base[keys.KeyRentExpiry(overlayRentDueHeight, owner)] = []byte(owner)
		base[keys.KeyRentOwnerMarker(owner, rent.OwnerMarkerInit)] = []byte{1}
	}
	futureOwner := "fbc1qrentoverlayfuture"
	futureKey := keys.KeyRentExpiry(overlayRentDueHeight+1, futureOwner)
	base[futureKey] = []byte(futureOwner)
	return base, owners, futureKey
}

func newOverlayRentSchedulerStateView(probe *overlayRentScanProbe) StateView {
	read := func(key string) ([]byte, error) {
		return cloneBytes(probe.base[key]), nil
	}
	batchRead := func(requested []string) (map[string][]byte, error) {
		out := make(map[string][]byte, len(requested))
		for _, key := range requested {
			if value, exists := probe.base[key]; exists {
				out[key] = cloneBytes(value)
			}
		}
		return out, nil
	}
	scan := func(prefix string) (map[string][]byte, error) {
		if strings.HasPrefix(prefix, keys.KeyUserActivityFamilyPrefix()) {
			probe.activitySeqScans++
		}
		out := make(map[string][]byte)
		for key, value := range probe.base {
			if strings.HasPrefix(key, prefix) {
				out[key] = cloneBytes(value)
			}
		}
		return out, nil
	}
	scanLimit := func(prefix string, limit int) (map[string][]byte, error) {
		return overlayRentPairsToMap(probe.pairs(prefix, "", limit, false, false)), nil
	}
	scanLimitReverse := func(prefix string, limit int) (map[string][]byte, error) {
		return overlayRentPairsToMap(probe.pairs(prefix, "", limit, true, false)), nil
	}
	scanOrdered := func(prefix string, limit int, reverse bool) ([]KVPair, error) {
		return probe.pairs(prefix, "", limit, reverse, true), nil
	}
	scanAfter := func(prefix, startAfter string, limit int, reverse bool) ([]KVPair, error) {
		return probe.pairs(prefix, startAfter, limit, reverse, true), nil
	}
	return NewStateViewWithLimitScanOrderedAndBatch(
		read, batchRead, scan, scanLimit, scanLimitReverse, scanOrdered, scanAfter,
	)
}

func overlayRentPairsToMap(pairs []KVPair) map[string][]byte {
	out := make(map[string][]byte, len(pairs))
	for _, pair := range pairs {
		out[pair.Key] = cloneBytes(pair.Value)
	}
	return out
}

func overlayRentEffectiveState(base map[string][]byte, diff []WriteOp) map[string][]byte {
	out := cloneBytesMap(base)
	for _, op := range diff {
		if op.Del {
			delete(out, op.Key)
			continue
		}
		out[op.Key] = cloneBytes(op.Value)
	}
	return out
}
