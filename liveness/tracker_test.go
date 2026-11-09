package liveness

import (
	"dex/keys"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

                                                                                      
                                              
type mockStore struct {
	data map[string][]byte
}

func newMockStore() *mockStore { return &mockStore{data: make(map[string][]byte)} }
func (s *mockStore) Get(key string) ([]byte, error) {
	return s.data[key], nil
}
func (s *mockStore) Set(key string, val []byte) {
	cp := make([]byte, len(val))
	copy(cp, val)
	s.data[key] = cp
}
func (s *mockStore) Del(key string) { delete(s.data, key) }
func (s *mockStore) Scan(prefix string) (map[string][]byte, error) {
	out := make(map[string][]byte)
	for k, v := range s.data {
		if strings.HasPrefix(k, prefix) {
			out[k] = v
		}
	}
	return out, nil
}

                                                         
  
                        
                                                                                    
                                                                         
func defaultTestCfg() Config {
	c := DefaultConfig()
	c.MinSamples = 3
	c.MinReporters = 3
	c.Threshold = 0.5
	c.WindowFloor = 5
	c.FreezeBlocks = 5
	c.OfflineGraceBlocks = 1                                                             
	c.MinStake = 100
	c.SlashPerReport = 10
	return c
}

                                                                                      
                                                                                                  
func newTrackerWithStore(cfg Config) (*Tracker, *mockStore) {
	tr := NewTracker(cfg)
	store := newMockStore()
	tr.SetStore(store)
	return tr, store
}

func TestUnregisterMinersBatchPrevalidatesBeforeMutation(t *testing.T) {
	cfg := defaultTestCfg()
	tracker, store := newTrackerWithStore(cfg)
	if _, err := tracker.RegisterMinerAtHeight(store, "miner-a", cfg.MinStake, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := tracker.RegisterMinerAtHeight(store, "miner-b", cfg.MinStake, 1); err != nil {
		t.Fatal(err)
	}

	tracker.mu.Lock()
	badIndex := tracker.addrToIndex["miner-b"]
	tracker.miners[badIndex] = nil
	tracker.mu.Unlock()
	if err := tracker.UnregisterMinersBatch(store, []string{"miner-a", "miner-b"}); err == nil {
		t.Fatal("inconsistent second miner must reject the whole batch")
	}
	if !tracker.IsRegistered("miner-a") {
		t.Fatal("first miner was removed before full-batch validation completed")
	}
	tracker.mu.RLock()
	firstIndex := tracker.addrToIndex["miner-a"]
	tracker.mu.RUnlock()
	if _, exists := store.data[keys.KeyLivenessMiner(firstIndex)]; !exists {
		t.Fatal("first miner persistent state was deleted on failed batch validation")
	}
}

func TestRegisterAndQuery(t *testing.T) {
	tr, sv := newTrackerWithStore(defaultTestCfg())

	idx, err := tr.RegisterMinerAtHeight(sv, "alice", 1000, 1)
	if err != nil {
		t.Fatal(err)
	}
	if idx != 0 {
		t.Fatalf("expected idx 0, got %d", idx)
	}

	if tr.IsActive("alice") {
		t.Fatal("new miner should be in probation, not active")
	}
	if !tr.IsActiveOrProbation("alice") {
		t.Fatal("new miner should be in probation")
	}
	if tr.IsActiveOrProbation("unregistered-observer") {
		t.Fatal("an address absent from the verified registry must not receive consensus authority")
	}
	if tr.GetActiveCount() != 0 {
		t.Fatal("active count should be 0")
	}
}

func TestConsensusVoterSnapshotSurvivesReload(t *testing.T) {
	tr, sv := newTrackerWithStore(defaultTestCfg())
	if _, err := tr.RegisterMinerAtHeight(sv, "genesis-miner", 1000, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.RegisterMinerAtHeight(sv, "probation-miner", 1000, 1); err != nil {
		t.Fatal(err)
	}
	tr.ProcessBlock(sv, 7, nil, nil)

	height, count := tr.GetConsensusVoterSnapshot()
	if height != 7 || count != 1 {
		t.Fatalf("live voter snapshot = (%d,%d), want strict Active (7,1)", height, count)
	}

	reloaded := NewTracker(defaultTestCfg())
	reloaded.SetStore(sv)
	height, count = reloaded.GetConsensusVoterSnapshot()
	if height != 7 || count != 1 {
		t.Fatalf("reloaded voter snapshot = (%d,%d), want strict Active (7,1)", height, count)
	}
}

func TestConsensusVoterSnapshotPublishesMembershipAtomically(t *testing.T) {
	tr, sv := newTrackerWithStore(defaultTestCfg())
	for _, miner := range []string{"genesis-1", "genesis-2", "genesis-3", "genesis-4", "genesis-5"} {
		if _, err := tr.RegisterMinerAtHeight(sv, miner, 1000, 0); err != nil {
			t.Fatal(err)
		}
	}
	if height, count := tr.GetConsensusVoterSnapshot(); height != 0 || count != 5 {
		t.Fatalf("genesis snapshot = (%d,%d), want (0,5)", height, count)
	}
	tr.ProcessBlock(sv, 1, nil, nil)
	if height, count := tr.GetConsensusVoterSnapshot(); height != 1 || count != 5 {
		t.Fatalf("initial snapshot = (%d,%d), want (1,5)", height, count)
	}

	                                                                           
	                                                                           
	                                                
	if _, err := tr.RegisterMinerAtHeight(sv, "probation-miner", 1000, 2); err != nil {
		t.Fatal(err)
	}
	if height, count := tr.GetConsensusVoterSnapshot(); height != 1 || count != 5 {
		t.Fatalf("mid-commit snapshot = (%d,%d), want unchanged (1,5)", height, count)
	}
	tr.ProcessBlock(sv, 2, nil, nil)
	if height, count := tr.GetConsensusVoterSnapshot(); height != 2 || count != 5 {
		t.Fatalf("published snapshot = (%d,%d), want Active-only (2,5)", height, count)
	}
}

func TestProbationPromotion(t *testing.T) {
	tr, sv := newTrackerWithStore(defaultTestCfg())

	                                                                                                  
	                                                                                      
	tr.RegisterMinerAtHeight(sv, "alice", 1000, 1)

	for h := uint64(2); h <= 6; h++ {
		reporter := []string{"reporter-a", "reporter-b", "reporter-c"}[int(h)%3]
		tr.ProcessBlockWithReporter(sv, h, reporter, []uint32{0}, nil)
	}

	if !tr.IsActive("alice") {
		t.Fatal("alice should be promoted to active after probation")
	}
	if tr.GetActiveCount() != 1 {
		t.Fatal("active count should be 1")
	}
}

func TestProbationPromotionRequiresReachabilityEvidence(t *testing.T) {
	cfg := defaultTestCfg()
	tr, sv := newTrackerWithStore(cfg)
	tr.RegisterMinerAtHeight(sv, "alice", 1000, 1)
	for h := uint64(2); h <= 7; h++ {
		tr.ProcessBlock(sv, h, nil, nil)
	}
	if tr.IsActive("alice") {
		t.Fatal("age alone must not activate a probation miner")
	}

	tr, sv = newTrackerWithStore(cfg)
	tr.RegisterMinerAtHeight(sv, "bob", 1000, 1)
	for h := uint64(2); h <= 6; h++ {
		reporter := []string{"reporter-a", "reporter-b", "reporter-c"}[int(h)%3]
		if h <= 4 {
			tr.ProcessBlockWithReporter(sv, h, reporter, []uint32{0}, []uint32{0})
		} else {
			tr.ProcessBlockWithReporter(sv, h, reporter, []uint32{0}, nil)
		}
	}
	if tr.IsActive("bob") {
		t.Fatal("offline ratio above activation threshold must block probation promotion")
	}
	if info := tr.GetMinerByAddress("bob"); info == nil || info.Stake != 1000 || info.TotalSlashed != 0 {
		t.Fatalf("unsigned reports mutated probation stake: %+v", info)
	}
}

func TestUnsignedReportsDoNotSlashOrKick(t *testing.T) {
	cfg := defaultTestCfg()
	cfg.MinStake = 100
	cfg.SlashPerReport = 50
	cfg.MinSamples = 10                                                                
	cfg.Threshold = 0.8
	cfg.PauseRatio = 2.0                         
	cfg.MinActiveMiners = 0                                           

	tr, sv := newTrackerWithStore(cfg)
	                                                   
	tr.RegisterMinerAtHeight(sv, "bob", 200, 0)

	if !tr.IsActive("bob") {
		t.Fatal("bob should be active (genesis)")
	}

	for height := uint64(1); height <= 3; height++ {
		kicked, slashed := tr.ProcessBlock(sv, height, []uint32{0}, []uint32{0})
		if len(kicked) != 0 || len(slashed) != 0 {
			t.Fatalf("height %d: unsigned report punished bob: kicked=%v slashed=%v", height, kicked, slashed)
		}
	}
	if info := tr.GetMinerByAddress("bob"); info == nil || info.State != StateActive || info.Stake != 200 || info.TotalSlashed != 0 {
		t.Fatalf("unsigned reports mutated bob: %+v", info)
	}
}

func TestUnsignedReportsNeverNeedPauseProtection(t *testing.T) {
	cfg := defaultTestCfg()
	cfg.PauseRatio = 0.5
	cfg.MinSamples = 1
	cfg.Threshold = 0.3
	cfg.MinActiveMiners = 0

	tr, sv := newTrackerWithStore(cfg)
	tr.RegisterMinerAtHeight(sv, "a", 10000, 0)
	tr.RegisterMinerAtHeight(sv, "b", 10000, 0)

	kicked, slashed := tr.ProcessBlock(sv, 1, []uint32{0, 1}, []uint32{0, 1})
	if len(kicked) != 0 || len(slashed) != 0 {
		t.Fatalf("unsigned reports must be observational regardless of pause settings: kicked=%v slashed=%v", kicked, slashed)
	}
}

func TestRemoveMiner(t *testing.T) {
	tr, sv := newTrackerWithStore(defaultTestCfg())
	tr.RegisterMinerAtHeight(sv, "carol", 1000, 0)

	if err := tr.RemoveMiner(sv, "carol", 10); err != nil {
		t.Fatal(err)
	}

	if tr.IsActiveOrProbation("carol") {
		t.Fatal("carol should be frozen after removal")
	}
}

func TestReRegisterAfterFreeze(t *testing.T) {
	cfg := defaultTestCfg()
	cfg.FreezeBlocks = 5
	tr, sv := newTrackerWithStore(cfg)

	                                 
	tr.RegisterMinerAtHeight(sv, "dave", 1000, 0)
	tr.RemoveMiner(sv, "dave", 2)

	                                 
	_, err := tr.RegisterMinerAtHeight(sv, "dave", 1000, 3)
	if err == nil {
		t.Fatal("should not allow re-register during freeze")
	}

	                                    
	_, err = tr.RegisterMinerAtHeight(sv, "dave", 1000, 8)
	if err != nil {
		t.Fatalf("should allow re-register after freeze: %v", err)
	}
}

func TestPersistAndRestore(t *testing.T) {
	store := newMockStore()
	cfg := defaultTestCfg()

	tr := NewTracker(cfg)
	tr.SetStore(store)
	                                   
	tr.RegisterMinerAtHeight(store, "eve", 5000, 0)

	if !tr.IsActive("eve") {
		t.Fatal("eve should be active (genesis)")
	}

	                                                   
	tr2 := NewTracker(cfg)
	tr2.SetStore(store)                                 

	if tr2.GetActiveCount() != 1 {
		t.Fatalf("restored active count: expected 1, got %d", tr2.GetActiveCount())
	}
	m := tr2.GetMinerByAddress("eve")
	if m == nil {
		t.Fatal("eve should be restored from DB")
	}
	if m.State != StateActive {
		t.Fatalf("eve state: expected Active, got %d", m.State)
	}
}

func TestExpiredReportCountersPersisted(t *testing.T) {
	cfg := defaultTestCfg()
	cfg.WindowFloor = 2
	cfg.WindowCeil = 2
	cfg.SlashPerReport = 0
	tr, sv := newTrackerWithStore(cfg)

	for _, addr := range []string{"a", "b", "c"} {
		if _, err := tr.RegisterMinerAtHeight(sv, addr, 1000, 0); err != nil {
			t.Fatalf("register %s: %v", addr, err)
		}
	}
	tr.ProcessBlock(sv, 1, []uint32{0}, nil)
	if _, ok := sv.data[keys.KeyLivenessCounter(0)]; !ok {
		t.Fatal("counter 0 should be persisted before expiry")
	}
	tr.ProcessBlock(sv, 2, []uint32{1}, nil)
	tr.ProcessBlock(sv, 3, []uint32{2}, nil)
	if _, ok := sv.data[keys.KeyLivenessCounter(0)]; ok {
		t.Fatal("counter 0 should be deleted after its only report expired")
	}
}

func TestReloadRebuildsCountersFromReportHistory(t *testing.T) {
	cfg := defaultTestCfg()
	cfg.WindowFloor = 2
	cfg.WindowCeil = 2
	cfg.SlashPerReport = 0
	tr, sv := newTrackerWithStore(cfg)
	for _, addr := range []string{"a", "b"} {
		if _, err := tr.RegisterMinerAtHeight(sv, addr, 1000, 0); err != nil {
			t.Fatalf("register %s: %v", addr, err)
		}
	}
	tr.ProcessBlock(sv, 1, []uint32{0}, nil)
	stale, err := json.Marshal(CounterInfo{Samples: 99})
	if err != nil {
		t.Fatal(err)
	}
	sv.data[keys.KeyLivenessCounter(0)] = stale

	reloaded := NewTracker(cfg)
	reloaded.SetStore(sv)
	if got := reloaded.GetSampleCountForTest(0); got != 1 {
		t.Fatalf("reload should rebuild counter from reports, got %d", got)
	}
}

func TestUnsignedReportsIgnorePunitiveConfig(t *testing.T) {
	cfg := defaultTestCfg()
	cfg.MinStake = 100
	cfg.SlashPerReport = 200                                                 
	cfg.MinSamples = 100
	cfg.PauseRatio = 2.0
	cfg.MinActiveMiners = 2

	tr, sv := newTrackerWithStore(cfg)
	tr.RegisterMinerAtHeight(sv, "a", 500, 0)
	tr.RegisterMinerAtHeight(sv, "b", 500, 0)

	if tr.GetActiveCount() != 2 {
		t.Fatalf("expected 2 active, got %d", tr.GetActiveCount())
	}

	                                                                     
	                                                          
	kicked, slashed := tr.ProcessBlock(sv, 1, []uint32{0}, []uint32{0})
	if len(kicked) != 0 || len(slashed) != 0 {
		t.Fatalf("unsigned report punished at floor: kicked=%v slashed=%v", kicked, slashed)
	}
	mA := tr.GetMinerByAddress("a")
	if mA.Stake != 500 || mA.TotalSlashed != 0 {
		t.Fatalf("unsigned report changed stake at floor: %+v", mA)
	}

	                                                     
	tr.RegisterMinerAtHeight(sv, "c", 500, 0)
	if tr.GetActiveCount() != 3 {
		t.Fatalf("expected 3 active, got %d", tr.GetActiveCount())
	}

	for _, height := range []uint64{3, 4} {
		kicked, slashed = tr.ProcessBlock(sv, height, []uint32{0}, []uint32{0})
		if len(kicked) != 0 || len(slashed) != 0 {
			t.Fatalf("height %d: unsigned report punished above floor: kicked=%v slashed=%v", height, kicked, slashed)
		}
	}
	if mA = tr.GetMinerByAddress("a"); mA == nil || mA.State != StateActive || mA.Stake != 500 || mA.TotalSlashed != 0 {
		t.Fatalf("unsigned reports mutated a: %+v", mA)
	}
}

func TestGetActiveMiners(t *testing.T) {
	cfg := defaultTestCfg()
	tr, sv := newTrackerWithStore(cfg)

	tr.RegisterMinerAtHeight(sv, "x", 1000, 0)
	tr.RegisterMinerAtHeight(sv, "y", 1000, 0)

	miners := tr.GetActiveMiners()
	if !reflect.DeepEqual(miners, []string{"x", "y"}) {
		t.Fatalf("expected deterministic order [x y], got %v", miners)
	}

	               
	tr.RemoveMiner(sv, "x", 1)
	miners = tr.GetActiveMiners()
	if len(miners) != 1 || miners[0] != "y" {
		t.Fatalf("expected [y], got %v", miners)
	}
}

                                                                                               
                                                                                                           
func TestReloadFromDBRestoresReportHistory(t *testing.T) {
	store := newMockStore()
	cfg := defaultTestCfg()
	cfg.WindowFloor = 5
	cfg.MinSamples = 3
	cfg.Threshold = 0.5
	cfg.MinActiveMiners = 0
	cfg.PauseRatio = 2.0

	                                                    
	trA := NewTracker(cfg)
	trA.SetStore(store)
	trA.RegisterMinerAtHeight(store, "alice", 10000, 0)

	                                                  
	trA.ProcessBlock(store, 1, []uint32{0}, []uint32{0})
	trA.ProcessBlock(store, 2, []uint32{0}, []uint32{0})
	trA.ProcessBlock(store, 3, []uint32{0}, []uint32{0})

	counterAfter3_A := trA.GetOfflineCountForTest(0)
	samplesAfter3_A := trA.GetSampleCountForTest(0)

	                                                                                            
	trB := NewTracker(cfg)
	trB.SetStore(store)                                                 

	counterAfter3_B := trB.GetOfflineCountForTest(0)
	samplesAfter3_B := trB.GetSampleCountForTest(0)
	if counterAfter3_A != counterAfter3_B {
		t.Fatalf("offlineCount mismatch after reload: A=%d B=%d", counterAfter3_A, counterAfter3_B)
	}
	if samplesAfter3_A != samplesAfter3_B {
		t.Fatalf("sampleCount mismatch after reload: A=%d B=%d", samplesAfter3_A, samplesAfter3_B)
	}

	                                                                              
	kickedA, _ := trA.ProcessBlock(store, 6, []uint32{0}, []uint32{0})
	kickedB, _ := trB.ProcessBlock(store, 6, []uint32{0}, []uint32{0})

	finalOffA := trA.GetOfflineCountForTest(0)
	finalOffB := trB.GetOfflineCountForTest(0)
	if finalOffA != finalOffB {
		t.Fatalf("offlineCount diverged after expiry: A=%d B=%d (reportHistory not restored)", finalOffA, finalOffB)
	}
	finalSampA := trA.GetSampleCountForTest(0)
	finalSampB := trB.GetSampleCountForTest(0)
	if finalSampA != finalSampB {
		t.Fatalf("sampleCount diverged after expiry: A=%d B=%d", finalSampA, finalSampB)
	}

	if len(kickedA) != len(kickedB) {
		t.Fatalf("kick divergence: A kicked %d, B kicked %d", len(kickedA), len(kickedB))
	}
}
