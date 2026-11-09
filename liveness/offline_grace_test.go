package liveness

import (
	"dex/keys"
	"dex/protocol"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func graceTestTracker(t *testing.T, grace uint64, window int) (*Tracker, *mockStore) {
	t.Helper()
	cfg := defaultTestCfg()
	cfg.OfflineGraceBlocks = grace
	cfg.WindowFloor, cfg.WindowCeil = window, window
	tr, sv := newTrackerWithStore(cfg)
	if _, err := tr.RegisterMinerAtHeight(sv, "victim", 1000, 0); err != nil {
		t.Fatal(err)
	}
	return tr, sv
}

func reportOffline(t *testing.T, tr *Tracker, sv WriteView, h uint64, wantKick bool) {
	t.Helper()
	kicked, slashed := tr.ProcessBlockWithReporter(sv, h, fmt.Sprintf("r-%d", h%3), []uint32{0}, []uint32{0})
	if (len(kicked) != 0) != wantKick || len(slashed) != 0 {
		t.Fatalf("height %d kicked=%v slashed=%v wantKick=%v", h, kicked, slashed, wantKick)
	}
}

func TestOfflineGraceDefaultBoundaryUsesFreshIndependentEvidence(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.OfflineGraceBlocks != protocol.OfflineGraceBlocks || protocol.OfflineGraceBlocks != 17280 {
		t.Fatal("wrong protocol grace")
	}
	cfg.OfflineGraceBlocks = 0                                                    
	cfg.MinStake = 100
	tr, sv := newTrackerWithStore(cfg)
	_, _ = tr.RegisterMinerAtHeight(sv, "victim", 1000, 0)
	for h := uint64(1); h <= 10; h++ {
		reportOffline(t, tr, sv, h, false)
	}
	if got := tr.GetMinerByAddress("victim").OfflineSinceHeight; got != 10 {
		t.Fatalf("since=%d want 10", got)
	}
	                                                                           
	                                                                         
	for h := protocol.OfflineGraceBlocks; h < protocol.OfflineGraceBlocks+10; h++ {
		reportOffline(t, tr, sv, h, false)
	}
	reportOffline(t, tr, sv, protocol.OfflineGraceBlocks+10, true)
	m := tr.GetMinerByAddress("victim")
	if m.State != StateFrozen || !m.Kicked || m.Stake != 1000 || m.TotalSlashed != 0 {
		t.Fatalf("membership-only removal changed stake: %+v", m)
	}
}

func TestOfflineGraceUnknownAndExpiryDoNotResetOrAuthorizeKick(t *testing.T) {
	tr, sv := graceTestTracker(t, 3, 5)
	for h := uint64(1); h <= 3; h++ {
		reportOffline(t, tr, sv, h, false)
	}
	if got := tr.GetMinerByAddress("victim").OfflineSinceHeight; got != 3 {
		t.Fatalf("since=%d", got)
	}
	tr.ProcessBlockWithReporter(sv, 20, "r-0", nil, nil)
	if m := tr.GetMinerByAddress("victim"); m.State != StateActive || m.OfflineSinceHeight != 3 {
		t.Fatalf("unknown expired evidence changed membership/timer: %+v", m)
	}
	                                                                              
	tr.ProcessBlockWithOnlineReporter(sv, 21, "", []uint32{0}, nil, []uint32{0})
	tr.ProcessBlockWithReporter(sv, 22, "r-1", []uint32{0}, nil)
	if got := tr.GetMinerByAddress("victim").OfflineSinceHeight; got != 3 {
		t.Fatalf("unproven recovery cleared since: %d", got)
	}
	for h := uint64(23); h <= 25; h++ {
		reportOffline(t, tr, sv, h, h == 25)
	}
}

func TestOfflineGraceOnlineAndProposerTakePriorityThenRestartFullGrace(t *testing.T) {
	for _, proposer := range []bool{false, true} {
		t.Run(fmt.Sprintf("proposer=%v", proposer), func(t *testing.T) {
			tr, sv := graceTestTracker(t, 3, 20)
			for h := uint64(1); h <= 5; h++ {
				reportOffline(t, tr, sv, h, false)
			}
			reporter, online := "r-0", []uint32{0}
			if proposer {
				reporter, online = "victim", nil
			}
			kicked, _ := tr.ProcessBlockWithOnlineReporter(sv, 6, reporter, []uint32{0}, nil, online)
			if len(kicked) != 0 || tr.GetMinerByAddress("victim").OfflineSinceHeight != 0 {
				t.Fatal("positive did not win at kick boundary")
			}
			reportOffline(t, tr, sv, 7, false)
			if got := tr.GetMinerByAddress("victim").OfflineSinceHeight; got != 7 {
				t.Fatalf("restart since=%d", got)
			}
			reportOffline(t, tr, sv, 9, false)
			reportOffline(t, tr, sv, 10, true)
		})
	}
}

func TestOfflineGracePersistsWithoutBackdatingAndReplaysOnline(t *testing.T) {
	tr, sv := graceTestTracker(t, 3, 20)
	for h := uint64(1); h <= 3; h++ {
		reportOffline(t, tr, sv, h, false)
	}
	cfg := tr.cfg
	reloaded := NewTracker(cfg)
	reloaded.SetStore(sv)
	if got := reloaded.GetMinerByAddress("victim").OfflineSinceHeight; got != 3 {
		t.Fatalf("restart lost timer %d", got)
	}
	reloaded.ProcessBlockWithOnlineReporter(sv, 6, "r-0", []uint32{0}, nil, []uint32{0})
	var report BlockReport
	if err := json.Unmarshal(sv.data[keys.KeyLivenessReport(6)], &report); err != nil || len(report.Online) != 1 {
		t.Fatalf("online report not persisted: %+v %v", report, err)
	}
	reloaded = NewTracker(cfg)
	reloaded.SetStore(sv)
	                                                                               
	kicked, _ := reloaded.ProcessBlockWithReporter(sv, 6, "r-0", []uint32{0}, nil)
	if len(kicked) != 0 || reloaded.GetMinerByAddress("victim").OfflineSinceHeight != 0 {
		t.Fatal("same-height reload lost explicit positive")
	}
	                                                                             
	m := reloaded.GetMinerByAddress("victim")
	m.OfflineSinceHeight = 0
	raw, _ := json.Marshal(m)
	sv.Set(keys.KeyLivenessMiner(0), raw)
	reloaded = NewTracker(cfg)
	reloaded.SetStore(sv)
	reportOffline(t, reloaded, sv, 7, false)
	if got := reloaded.GetMinerByAddress("victim").OfflineSinceHeight; got != 7 {
		t.Fatalf("legacy timer backdated to %d", got)
	}
}

type graceCountingStore struct {
	*mockStore
	reads, scans, writes, minerWrites int
}

func (s *graceCountingStore) Get(k string) ([]byte, error) { s.reads++; return s.mockStore.Get(k) }
func (s *graceCountingStore) Scan(p string) (map[string][]byte, error) {
	s.scans++
	return s.mockStore.Scan(p)
}
func (s *graceCountingStore) Set(k string, v []byte) {
	s.writes++
	if strings.HasPrefix(k, strings.TrimSuffix(keys.KeyLivenessMiner(0), "0")) {
		s.minerWrites++
	}
	s.mockStore.Set(k, v)
}

func TestOfflineGraceRuntimeIOIsIndependentOfRegistrySize(t *testing.T) {
	for _, n := range []int{20, 100, 10000} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			cfg := defaultTestCfg()
			cfg.MinSamples = 1
			cfg.MinReporters = 1
			cfg.OfflineGraceBlocks = 100
			tr := NewTracker(cfg)
			sv := &graceCountingStore{mockStore: newMockStore()}
			tr.SetStore(sv)
			for i := 0; i < n; i++ {
				_, _ = tr.RegisterMinerAtHeight(sv, fmt.Sprintf("miner-%05d", i), 1000, 0)
			}
			sv.reads, sv.scans, sv.writes, sv.minerWrites = 0, 0, 0, 0
			tr.ProcessBlockWithReporter(sv, 1, "r", []uint32{0}, []uint32{0})
			if sv.reads != 0 || sv.scans != 0 || sv.minerWrites != 1 {
				t.Fatalf("N=%d first offline IO reads=%d scans=%d minerWrites=%d", n, sv.reads, sv.scans, sv.minerWrites)
			}
			sv.writes, sv.minerWrites = 0, 0
			tr.ProcessBlockWithReporter(sv, 2, "r", []uint32{0}, []uint32{0})
			if sv.minerWrites != 0 || sv.writes != 3 || sv.reads != 0 || sv.scans != 0 {
				t.Fatalf("N=%d unchanged timer IO reads=%d scans=%d writes=%d minerWrites=%d", n, sv.reads, sv.scans, sv.writes, sv.minerWrites)
			}
		})
	}
}
