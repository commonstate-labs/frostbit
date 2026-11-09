package rent

import (
	"dex/keys"
	"dex/pb"
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"runtime"
	"testing"

	"google.golang.org/protobuf/proto"
)

type memStore map[string][]byte

func (m memStore) Get(key string) ([]byte, bool, error) {
	v, ok := m[key]
	return v, ok, nil
}

func (m memStore) GetMany(keys []string) (map[string][]byte, error) {
	out := make(map[string][]byte, len(keys))
	for _, k := range keys {
		if v, ok := m[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}

func (m memStore) Scan(prefix string, limit int) (map[string][]byte, error) {
	out := make(map[string][]byte)
	for k, v := range m {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			if limit > 0 && len(out) >= limit {
				break
			}
			out[k] = v
		}
	}
	return out, nil
}

func (m memStore) Set(key string, value []byte) {
	m[key] = value
}

func (m memStore) Del(key string) {
	delete(m, key)
}

type countingStore struct {
	memStore
	setCount  map[string]int
	scanCount map[string]int
}

func (m *countingStore) Set(key string, value []byte) {
	m.memStore.Set(key, value)
	m.setCount[key]++
}

func (m *countingStore) Scan(prefix string, limit int) (map[string][]byte, error) {
	if m.scanCount != nil {
		m.scanCount[prefix]++
	}
	return m.memStore.Scan(prefix, limit)
}

type captureClaimCleaner struct {
	claimsSeen int
}

func (c *captureClaimCleaner) PrepareAccountSweep(owner string, height uint64) error {
	return nil
}

func (c *captureClaimCleaner) PrepareAccountSweepWithClaims(owner string, height uint64, claims map[string][]byte) error {
	c.claimsSeen = len(claims)
	return nil
}

func TestNominalFBAssetSumsBalanceBucketsAndClaims(t *testing.T) {
	store := memStore{}
	record := &pb.TokenBalanceRecord{
		Address: "alice",
		Token:   FeeToken,
		Balance: &pb.TokenBalance{
			Balance:              "100",
			MinerLockedBalance:   "20",
			WitnessLockedBalance: "30",
			OrderFrozenBalance:   "40",
		},
	}
	data, err := proto.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	store[keys.KeyBalance("alice", FeeToken)] = data
	if err := SetClaim(store, "alice", FeeToken, "future_margin", "pos1", big.NewInt(7)); err != nil {
		t.Fatal(err)
	}
	if err := SetClaim(store, "alice", FeeToken, "challenge_stake", "c1", big.NewInt(3)); err != nil {
		t.Fatal(err)
	}

	got, err := NewService(store).NominalFBAsset("alice")
	if err != nil {
		t.Fatal(err)
	}
	if got.BalanceBuckets != "190" || got.IndexedClaims != "10" || got.Total != "200" {
		t.Fatalf("unexpected summary: %+v", got)
	}
}

func TestSetClaimRetainsMarkersWhenClaimRemoved(t *testing.T) {
	store := memStore{}
	owner := "fbc1qclaimmarkers"
	if err := SetClaim(store, owner, FeeToken, "future_margin", "pos1", big.NewInt(7)); err != nil {
		t.Fatal(err)
	}
	if _, ok := store[keys.KeyAssetClaimMarker(owner, FeeToken)]; !ok {
		t.Fatal("asset claim marker should be set")
	}
	if _, ok := store[keys.KeyRentOwnerMarker(owner, OwnerMarkerClaim)]; !ok {
		t.Fatal("rent owner claim marker should be set")
	}
	if got := string(store[keys.KeyAssetClaimTotal(owner, FeeToken)]); got != "7" {
		t.Fatalf("claim total = %q, want 7", got)
	}

	if err := SetClaim(store, owner, FeeToken, "future_margin", "pos1", big.NewInt(0)); err != nil {
		t.Fatal(err)
	}
	if _, ok := store[keys.KeyAssetClaim(owner, FeeToken, "future_margin", "pos1")]; ok {
		t.Fatal("claim should be deleted")
	}
	if _, ok := store[keys.KeyAssetClaimMarker(owner, FeeToken)]; !ok {
		t.Fatal("asset claim marker should be conservatively retained")
	}
	if _, ok := store[keys.KeyRentOwnerMarker(owner, OwnerMarkerClaim)]; !ok {
		t.Fatal("rent owner claim marker should be conservatively retained")
	}
	if _, ok := store[keys.KeyAssetClaimTotal(owner, FeeToken)]; ok {
		t.Fatal("claim total should be deleted when last claim is removed")
	}
}

func TestSetClaimRetainsMarkersAcrossClaimDeletes(t *testing.T) {
	store := memStore{}
	owner := "fbc1qclaimmarkersmulti"
	for _, item := range []struct {
		token string
		id    string
	}{
		{FeeToken, "pos1"},
		{FeeToken, "pos2"},
		{"USDT", "pos3"},
	} {
		if err := SetClaim(store, owner, item.token, "future_margin", item.id, big.NewInt(7)); err != nil {
			t.Fatal(err)
		}
	}

	if err := SetClaim(store, owner, FeeToken, "future_margin", "pos1", big.NewInt(0)); err != nil {
		t.Fatal(err)
	}
	if _, ok := store[keys.KeyAssetClaimMarker(owner, FeeToken)]; !ok {
		t.Fatal("FB marker should remain while another FB claim exists")
	}
	if _, ok := store[keys.KeyRentOwnerMarker(owner, OwnerMarkerClaim)]; !ok {
		t.Fatal("owner claim marker should remain while claims exist")
	}
	if got := string(store[keys.KeyAssetClaimTotal(owner, FeeToken)]); got != "7" {
		t.Fatalf("FB claim total = %q, want 7", got)
	}

	if err := SetClaim(store, owner, FeeToken, "future_margin", "pos2", big.NewInt(0)); err != nil {
		t.Fatal(err)
	}
	if _, ok := store[keys.KeyAssetClaimMarker(owner, FeeToken)]; !ok {
		t.Fatal("FB marker should be conservatively retained")
	}
	if _, ok := store[keys.KeyAssetClaimMarker(owner, "USDT")]; !ok {
		t.Fatal("USDT marker should remain while USDT claim exists")
	}
	if _, ok := store[keys.KeyRentOwnerMarker(owner, OwnerMarkerClaim)]; !ok {
		t.Fatal("owner claim marker should remain while another token claim exists")
	}
	if _, ok := store[keys.KeyAssetClaimTotal(owner, FeeToken)]; ok {
		t.Fatal("FB claim total should be deleted after last FB claim")
	}
	if got := string(store[keys.KeyAssetClaimTotal(owner, "USDT")]); got != "7" {
		t.Fatalf("USDT claim total = %q, want 7", got)
	}

	if err := SetClaim(store, owner, "USDT", "future_margin", "pos3", big.NewInt(0)); err != nil {
		t.Fatal(err)
	}
	if _, ok := store[keys.KeyAssetClaimMarker(owner, "USDT")]; !ok {
		t.Fatal("USDT marker should be conservatively retained")
	}
	if _, ok := store[keys.KeyRentOwnerMarker(owner, OwnerMarkerClaim)]; !ok {
		t.Fatal("owner claim marker should be conservatively retained")
	}
	if _, ok := store[keys.KeyAssetClaimTotal(owner, "USDT")]; ok {
		t.Fatal("USDT claim total should be deleted after last USDT claim")
	}
}

func TestRuntimeSchedulesAndSweepsExpiredAccount(t *testing.T) {
	store := memStore{}
	owner := "fbc1qalice"
	writeTestBalance(t, store, owner, FeeToken, "1000")
	writeTestBalance(t, store, owner, "USDT", "50")
	if err := SetClaim(store, owner, "BTC", "future_margin", "pos1", big.NewInt(9)); err != nil {
		t.Fatal(err)
	}
	store[keys.KeyAccount(owner)] = []byte("account")

	rt := NewRuntime(store, WithPolicy(Policy{
		Token: FeeToken, ExemptMin: big.NewInt(10000),
		RentPerBlock: big.NewInt(100), RecycleAccount: RecycleFundAccount,
	}))
	if err := rt.ProcessBlock(10, []string{owner}); err != nil {
		t.Fatal(err)
	}
	var st RentState
	if err := json.Unmarshal(store[keys.KeyRentState(owner)], &st); err != nil {
		t.Fatal(err)
	}
	if st.ExpireHeight != 20 || st.NominalFB != "1000" {
		t.Fatalf("unexpected rent state: %+v", st)
	}
	if err := rt.ProcessBlock(20, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := store[keys.KeyAccount(owner)]; ok {
		t.Fatalf("account should be swept")
	}
	if _, ok := store[keys.KeyBalance(owner, FeeToken)]; ok {
		t.Fatalf("FB balance should be swept")
	}
	if got := testBalanceTotal(t, store, RecycleFundAccount, FeeToken); got != "1000" {
		t.Fatalf("unexpected recycle FB: %s", got)
	}
	if got := testBalanceTotal(t, store, RecycleFundAccount, "USDT"); got != "50" {
		t.Fatalf("unexpected recycle USDT: %s", got)
	}
	if _, ok := store[keys.KeyAssetClaim(owner, "BTC", "future_margin", "pos1")]; ok {
		t.Fatalf("claim index should be deleted")
	}
	if _, ok := store[keys.KeyAssetClaimTotal(owner, "BTC")]; ok {
		t.Fatalf("claim total should be deleted")
	}
	if got := testBalanceTotal(t, store, RecycleFundAccount, "BTC"); got != "0" {
		t.Fatalf("claim index must not mint recycle BTC: %s", got)
	}
}

func TestRuntimeAggregatesRecycleBalanceWrites(t *testing.T) {
	store := &countingStore{memStore: memStore{}, setCount: map[string]int{}}
	owners := []string{"fbc1qrecyclea", "fbc1qrecycleb", "fbc1qrecyclec"}
	for _, owner := range owners {
		writeTestBalance(t, store.memStore, owner, FeeToken, "100")
		stateData, err := json.Marshal(&RentState{Owner: owner, LastSettledHeight: 1, ExpireHeight: 10, NominalFB: "100"})
		if err != nil {
			t.Fatal(err)
		}
		store.memStore[keys.KeyRentState(owner)] = stateData
		store.memStore[keys.KeyRentExpiry(10, owner)] = []byte(owner)
		store.memStore[keys.KeyAccount(owner)] = []byte("account")
	}

	rt := NewRuntime(store, WithPolicy(Policy{
		Token: FeeToken, ExemptMin: big.NewInt(10000),
		RentPerBlock: big.NewInt(100), RecycleAccount: RecycleFundAccount,
	}))
	if err := rt.ProcessBlock(10, nil); err != nil {
		t.Fatal(err)
	}
	if got := testBalanceTotal(t, store.memStore, RecycleFundAccount, FeeToken); got != "300" {
		t.Fatalf("unexpected recycle FB: %s", got)
	}
	recycleKey := keys.KeyBalance(RecycleFundAccount, FeeToken)
	if got := store.setCount[recycleKey]; got != 1 {
		t.Fatalf("recycle balance should be flushed once, got %d writes", got)
	}
	if got := rt.LastStats().RecycleFlushes; got != 1 {
		t.Fatalf("recycle flushes = %d, want 1", got)
	}
}

func TestRuntimeFastSweepsInitializedSimpleAccount(t *testing.T) {
	store := &countingStore{memStore: memStore{}, setCount: map[string]int{}, scanCount: map[string]int{}}
	owner := "fbc1qfastsimple"
	writeTestBalance(t, store.memStore, owner, FeeToken, "100")
	stateData, err := json.Marshal(&RentState{Owner: owner, LastSettledHeight: 1, ExpireHeight: 10, NominalFB: "100"})
	if err != nil {
		t.Fatal(err)
	}
	store.memStore[keys.KeyRentState(owner)] = stateData
	store.memStore[keys.KeyRentExpiry(10, owner)] = []byte(owner)
	store.memStore[keys.KeyAccount(owner)] = []byte("account")
	store.memStore[keys.KeyRentOwnerMarker(owner, OwnerMarkerInit)] = []byte{1}

	rt := NewRuntime(store, WithPolicy(Policy{
		Token: FeeToken, ExemptMin: big.NewInt(10000),
		RentPerBlock: big.NewInt(100), RecycleAccount: RecycleFundAccount,
	}))
	if err := rt.ProcessBlock(10, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.memStore[keys.KeyAccount(owner)]; ok {
		t.Fatalf("account should be swept")
	}
	if _, ok := store.memStore[keys.KeyBalance(owner, FeeToken)]; ok {
		t.Fatalf("FB balance should be swept")
	}
	if _, ok := store.memStore[keys.KeyRentOwnerMarker(owner, OwnerMarkerInit)]; ok {
		t.Fatalf("rent owner marker should be cleared")
	}
	if got := testBalanceTotal(t, store.memStore, RecycleFundAccount, FeeToken); got != "100" {
		t.Fatalf("unexpected recycle FB: %s", got)
	}
	if got := store.scanCount[keys.KeyBalancePrefix(owner)]; got != 0 {
		t.Fatalf("fast sweep should not scan owner balances, got %d", got)
	}
	if got := store.scanCount[keys.KeyAssetClaimOwnerPrefix(owner)]; got != 0 {
		t.Fatalf("fast sweep should not scan owner claims, got %d", got)
	}
}

func TestRuntimeMarkerFallsBackForComplexAccount(t *testing.T) {
	store := &countingStore{memStore: memStore{}, setCount: map[string]int{}, scanCount: map[string]int{}}
	owner := "fbc1qfastcomplex"
	writeTestBalance(t, store.memStore, owner, FeeToken, "100")
	writeTestBalance(t, store.memStore, owner, "USDT", "50")
	stateData, err := json.Marshal(&RentState{Owner: owner, LastSettledHeight: 1, ExpireHeight: 10, NominalFB: "100"})
	if err != nil {
		t.Fatal(err)
	}
	store.memStore[keys.KeyRentState(owner)] = stateData
	store.memStore[keys.KeyRentExpiry(10, owner)] = []byte(owner)
	store.memStore[keys.KeyAccount(owner)] = []byte("account")
	store.memStore[keys.KeyRentOwnerMarker(owner, OwnerMarkerInit)] = []byte{1}
	store.memStore[keys.KeyRentOwnerMarker(owner, OwnerMarkerNonFBBalance)] = []byte{1}

	rt := NewRuntime(store, WithPolicy(Policy{
		Token: FeeToken, ExemptMin: big.NewInt(10000),
		RentPerBlock: big.NewInt(100), RecycleAccount: RecycleFundAccount,
	}))
	if err := rt.ProcessBlock(10, nil); err != nil {
		t.Fatal(err)
	}
	if got := testBalanceTotal(t, store.memStore, RecycleFundAccount, "USDT"); got != "50" {
		t.Fatalf("unexpected recycle USDT: %s", got)
	}
	if got := store.scanCount[keys.KeyBalancePrefix(owner)]; got == 0 {
		t.Fatalf("complex marker should fall back to balance scan")
	}
}

func TestRuntimeReusesSweptOwnerClaimScan(t *testing.T) {
	store := &countingStore{memStore: memStore{}, setCount: map[string]int{}, scanCount: map[string]int{}}
	owner := "fbc1qclaimreuse"
	writeTestBalance(t, store.memStore, owner, FeeToken, "100")
	stateData, err := json.Marshal(&RentState{Owner: owner, LastSettledHeight: 1, ExpireHeight: 10, NominalFB: "100"})
	if err != nil {
		t.Fatal(err)
	}
	claimKey := keys.KeyAssetClaim(owner, "BTC", "external", "source1")
	store.memStore[keys.KeyRentState(owner)] = stateData
	store.memStore[keys.KeyRentExpiry(10, owner)] = []byte(owner)
	store.memStore[claimKey] = []byte("1")

	cleaner := &captureClaimCleaner{}
	rt := NewRuntime(store, WithCleaner(cleaner), WithPolicy(Policy{
		Token: FeeToken, ExemptMin: big.NewInt(10000),
		RentPerBlock: big.NewInt(100), RecycleAccount: RecycleFundAccount,
	}))
	if err := rt.ProcessBlock(10, nil); err != nil {
		t.Fatal(err)
	}
	if cleaner.claimsSeen != 1 {
		t.Fatalf("claim-aware cleaner saw %d claims, want 1", cleaner.claimsSeen)
	}
	prefix := keys.KeyAssetClaimOwnerPrefix(owner)
	if got := store.scanCount[prefix]; got != 1 {
		t.Fatalf("owner claims should be scanned once, got %d", got)
	}
	if _, ok := store.memStore[claimKey]; ok {
		t.Fatalf("claim should be swept")
	}
}

func TestRuntimeClearsRentStateWhenAccountBecomesExempt(t *testing.T) {
	store := memStore{}
	owner := "fbc1qalice"
	writeTestBalance(t, store, owner, FeeToken, "1000")
	rt := NewRuntime(store, WithPolicy(Policy{
		Token: FeeToken, ExemptMin: big.NewInt(10000),
		RentPerBlock: big.NewInt(100), RecycleAccount: RecycleFundAccount,
	}))
	if err := rt.ProcessBlock(1, []string{owner}); err != nil {
		t.Fatal(err)
	}
	                                                                           
	                                                   
	writeTestBalance(t, store, owner, FeeToken, "10100")
	if err := rt.ProcessBlock(2, []string{owner}); err != nil {
		t.Fatal(err)
	}
	if _, ok := store[keys.KeyRentState(owner)]; ok {
		t.Fatalf("rent state should be cleared")
	}
}

func TestRentStatusReportsAccountLifecycle(t *testing.T) {
	store := memStore{}
	owner := "fbc1qrentstatus"
	writeTestBalance(t, store, owner, FeeToken, "1000")
	stateData, err := json.Marshal(&RentState{
		Owner: owner, LastSettledHeight: 10, ExpireHeight: 20, NominalFB: "1000",
	})
	if err != nil {
		t.Fatal(err)
	}
	store[keys.KeyRentState(owner)] = stateData

	status, err := NewService(store).RentStatus(owner, 12)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "renting" || status.ExpireHeight != 20 || status.BlocksRemaining != 8 {
		t.Fatalf("unexpected renting status: %+v", status)
	}
	if status.AsOfHeight != 12 || status.LastSettledHeight != 10 || status.LastAccruedHeight != 10 {
		t.Fatalf("legacy rent cursor fallback is incorrect: %+v", status)
	}

	threshold, _ := new(big.Int).SetString(DefaultRentExemptMinRaw, 10)
	writeTestBalance(t, store, owner, FeeToken, new(big.Int).Add(threshold, big.NewInt(300)).String())
	status, err = NewService(store).RentStatus(owner, 13)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "exempt" || status.ExpireHeight != 0 {
		t.Fatalf("unexpected exempt status: %+v", status)
	}

	writeTestBalance(t, store, owner, FeeToken, "0")
	status, err = NewService(store).RentStatus(owner, 14)
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "needs_funding" || status.UnpaidRentFB != "400" || status.ExpireHeight != 14 {
		t.Fatalf("unexpected unfunded status: %+v", status)
	}
}

func TestRentStatusProjectionKeepsPersistedCursors(t *testing.T) {
	store := memStore{}
	owner := "fbc1qrentstatuscursor"
	writeTestBalance(t, store, owner, FeeToken, "1000")
	putAccrualState(t, store, RentState{
		Version: RentStateVersion, Owner: owner,
		LastSettledHeight: 10, LastAccruedHeight: 11,
		Mode: RentModeCharging, ExpireHeight: 21, NominalFB: "1000",
		Work: &RentWork{NextActionHeight: 21, Phase: RentWorkSettle},
	})
	before := cloneMemStore(store)

	first, err := NewService(store).RentStatus(owner, 15)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewService(store).RentStatus(owner, 16)
	if err != nil {
		t.Fatal(err)
	}
	if first.AsOfHeight != 15 || second.AsOfHeight != 16 ||
		first.LastSettledHeight != 10 || second.LastSettledHeight != 10 ||
		first.LastAccruedHeight != 11 || second.LastAccruedHeight != 11 {
		t.Fatalf("query height replaced persisted rent cursors: first=%+v second=%+v", first, second)
	}
	if first.PendingRentFB != "400" || second.PendingRentFB != "500" ||
		first.SpendableFB != "600" || second.SpendableFB != "500" {
		t.Fatalf("projection did not advance with as-of height: first=%+v second=%+v", first, second)
	}
	if !reflect.DeepEqual(store, before) {
		t.Fatal("rent status query mutated consensus state")
	}
}

func TestRuntimePrefetchesNominalAssetReads(t *testing.T) {
	store := memStore{}
	ownerA := "fbc1qa"
	ownerB := "fbc1qb"
	writeTestBalance(t, store, ownerA, FeeToken, "100")
	writeTestBalance(t, store, ownerB, FeeToken, "200")
	if err := SetClaim(store, ownerA, FeeToken, "future_margin", "pos1", big.NewInt(7)); err != nil {
		t.Fatal(err)
	}

	rt := NewRuntime(store, WithPolicy(Policy{
		Token: FeeToken, ExemptMin: big.NewInt(10000),
		RentPerBlock: big.NewInt(100), RecycleAccount: RecycleFundAccount,
	}))
	if err := rt.ProcessBlock(10, []string{ownerA, ownerB}); err != nil {
		t.Fatal(err)
	}
	stats := rt.LastStats()
	if stats.PrefetchedAssetKeys != 6 || stats.BalanceCacheHits != 4 || stats.ClaimMarkerCacheHits != 2 || stats.ClaimTotalCacheHits != 1 {
		t.Fatalf("unexpected prefetch stats: %+v", stats)
	}
	if stats.ClaimScans != 0 || stats.ClaimScanKeys != 0 {
		t.Fatalf("unexpected claim scan stats: %+v", stats)
	}
	var st RentState
	if err := json.Unmarshal(store[keys.KeyRentState(ownerA)], &st); err != nil {
		t.Fatal(err)
	}
	if st.NominalFB != "107" {
		t.Fatalf("unexpected nominal FB for ownerA: %+v", st)
	}
}

func TestRuntimeParallelSettlePlansWriteDeterministicStates(t *testing.T) {
	oldProcs := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(oldProcs)

	store := memStore{}
	owners := make([]string, 0, parallelSettleMinOwners+16)
	for i := parallelSettleMinOwners + 15; i >= 0; i-- {
		owner := fmt.Sprintf("fbc1qparallel%05d", i)
		owners = append(owners, owner)
		writeTestBalance(t, store, owner, FeeToken, "1000")
	}
	rt := NewRuntime(store, WithPolicy(Policy{
		Token: FeeToken, ExemptMin: big.NewInt(10000),
		RentPerBlock: big.NewInt(100), RecycleAccount: RecycleFundAccount,
	}))
	if err := rt.ProcessBlock(1, owners); err != nil {
		t.Fatal(err)
	}
	stats := rt.LastStats()
	if stats.Owners != len(owners) || stats.WrittenStates != len(owners) || stats.SweptAccounts != 0 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
	for _, owner := range owners {
		var st RentState
		if err := json.Unmarshal(store[keys.KeyRentState(owner)], &st); err != nil {
			t.Fatalf("rent state %s: %v", owner, err)
		}
		if st.Owner != owner || st.LastSettledHeight != 1 || st.ExpireHeight != 11 || st.NominalFB != "1000" {
			t.Fatalf("unexpected state for %s: %+v", owner, st)
		}
		if string(store[keys.KeyRentExpiry(11, owner)]) != owner {
			t.Fatalf("missing expiry index for %s", owner)
		}
	}
}

func writeTestBalance(t *testing.T, store memStore, owner, token, amount string) {
	t.Helper()
	data, err := proto.Marshal(&pb.TokenBalanceRecord{
		Address: owner,
		Token:   token,
		Balance: &pb.TokenBalance{Balance: amount},
	})
	if err != nil {
		t.Fatal(err)
	}
	store[keys.KeyBalance(owner, token)] = data
}

func testBalanceTotal(t *testing.T, store memStore, owner, token string) string {
	t.Helper()
	data, ok := store[keys.KeyBalance(owner, token)]
	if !ok {
		return "0"
	}
	var record pb.TokenBalanceRecord
	if err := proto.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	return TokenBalanceTotal(record.Balance).String()
}
