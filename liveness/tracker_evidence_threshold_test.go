package liveness

import (
	"dex/keys"
	"dex/protocol"
	"testing"
)

func TestBlockHeaderLivenessReportsProtocolModeAffectsMembershipOnly(t *testing.T) {
	if protocol.BlockHeaderLivenessReportsAreObservational {
		t.Fatal("signed multi-proposer liveness evidence must gate membership")
	}
}

func TestSingleBlockOfflineReportCannotPunish(t *testing.T) {
	cfg := defaultTestCfg()
	                                                                          
	                                   
	cfg.MinSamples = 1
	cfg.Threshold = 0
	cfg.SlashPerReport = 10_000
	cfg.MinStake = 100
	cfg.MinActiveMiners = 0
	cfg.PauseRatio = 2.0

	tr, sv := newTrackerWithStore(cfg)
	tr.RegisterMinerAtHeight(sv, "victim", 10000, 0)

	kicked, slashed := tr.ProcessBlock(sv, 1, []uint32{0}, []uint32{0})
	if len(kicked) != 0 || len(slashed) != 0 {
		t.Fatalf("unsigned report produced punishment: kicked=%v slashed=%v", kicked, slashed)
	}
	info := tr.GetMinerByAddress("victim")
	if info == nil || info.State != StateActive || info.Stake != 10000 || info.TotalSlashed != 0 {
		t.Fatalf("unsigned report mutated victim: %+v", info)
	}
}

                                                                            
                                                                          
func TestAccumulatedUnsignedOfflineReportsNeverPunish(t *testing.T) {
	cfg := defaultTestCfg()
	cfg.MinSamples = 1
	cfg.Threshold = 0
	cfg.SlashPerReport = 10_000
	cfg.MinStake = 100
	cfg.MinActiveMiners = 0
	cfg.PauseRatio = 2.0
	cfg.WindowFloor = 100

	tr, sv := newTrackerWithStore(cfg)
	tr.RegisterMinerAtHeight(sv, "honest", 10000, 0)
	tr.RegisterMinerAtHeight(sv, "victim", 10000, 0)

	const N = 25
	for h := uint64(1); h <= N; h++ {
		                                                                           
		kicked, slashed := tr.ProcessBlock(sv, h, []uint32{0, 1, 99}, []uint32{1, 1, 99})
		if len(kicked) != 0 || len(slashed) != 0 {
			t.Fatalf("height %d: unsigned reports produced punishment: kicked=%v slashed=%v", h, kicked, slashed)
		}
	}
	info := tr.GetMinerByAddress("victim")
	if info == nil || info.State != StateActive || info.Stake != 10000 || info.TotalSlashed != 0 || info.ExitHeight != 0 {
		t.Fatalf("malicious reports mutated victim: %+v", info)
	}
	tr.mu.RLock()
	_, hasUnknownSampleCounter := tr.sampleCount[99]
	_, hasUnknownOfflineCounter := tr.offlineCount[99]
	tr.mu.RUnlock()
	if hasUnknownSampleCounter || hasUnknownOfflineCounter {
		t.Fatal("unknown miner index created a diagnostic counter")
	}
	if _, exists := sv.data[keys.KeyLivenessCounter(99)]; exists {
		t.Fatal("unknown miner index created a persistent counter key")
	}
}

func TestIndependentOfflineEvidenceBlocksProbationPromotion(t *testing.T) {
	cfg := defaultTestCfg()
	cfg.WindowFloor = 5
	cfg.WindowCeil = 5
	cfg.MinSamples = 1
	cfg.MinReporters = 3
	cfg.ActivationThreshold = 0
	cfg.Threshold = 0
	cfg.SlashPerReport = 10_000
	cfg.MinStake = 100
	cfg.MinActiveMiners = 0
	cfg.PauseRatio = 2.0

	tr, sv := newTrackerWithStore(cfg)
	tr.RegisterMinerAtHeight(sv, "joiner", 10000, 1)

	for h := uint64(2); h <= 6; h++ {
		reporter := []string{"reporter-a", "reporter-b", "reporter-c"}[int(h)%3]
		kicked, slashed := tr.ProcessBlockWithReporter(sv, h, reporter, []uint32{0}, []uint32{0})
		if len(kicked) != 0 || len(slashed) != 0 {
			t.Fatalf("height %d: probation evidence must not slash or kick", h)
		}
	}
	info := tr.GetMinerByAddress("joiner")
	if info == nil || info.State != StateProbation || info.Stake != 10000 || info.TotalSlashed != 0 {
		t.Fatalf("offline probation miner should remain unchanged in probation: %+v", info)
	}
}

func TestSingleOfflineReporterCannotBlockProbationPromotion(t *testing.T) {
	cfg := defaultTestCfg()
	cfg.WindowFloor = 5
	cfg.WindowCeil = 5
	cfg.MinSamples = 5
	cfg.MinReporters = 0                                                    
	cfg.ActivationThreshold = 0.2

	tr, sv := newTrackerWithStore(cfg)
	if _, err := tr.RegisterMinerAtHeight(sv, "joiner", 10000, 1); err != nil {
		t.Fatal(err)
	}

	reports := []struct {
		reporter string
		offline  bool
	}{
		{reporter: "malicious", offline: true},
		{reporter: "honest-b"},
		{reporter: "honest-c"},
		{reporter: "malicious", offline: true},
		{reporter: "malicious", offline: true},
	}
	for i, report := range reports {
		offline := []uint32(nil)
		if report.offline {
			offline = []uint32{0}
		}
		tr.ProcessBlockWithReporter(sv, uint64(i+2), report.reporter, []uint32{0}, offline)
	}

	info := tr.GetMinerByAddress("joiner")
	if info == nil || info.State != StateActive || info.ActiveSinceHeight != 6 {
		t.Fatalf("one offline reporter blocked promotion despite three sample reporters: %+v", info)
	}
	if got := tr.GetOfflineCountForTest(0); got != 3 {
		t.Fatalf("diagnostic offline count=%d want 3", got)
	}
}

func TestUnreachableProbationNeverPromotesUnderAdversarialReporterSchedule(t *testing.T) {
	cfg := defaultTestCfg()
	cfg.WindowFloor = 10
	cfg.WindowCeil = 10
	cfg.MinSamples = 10
	cfg.MinReporters = 3
	cfg.ActivationThreshold = 0.2

	tr, sv := newTrackerWithStore(cfg)
	if _, err := tr.RegisterMinerAtHeight(sv, "unreachable", 10000, 1); err != nil {
		t.Fatal(err)
	}
	for height := uint64(2); height <= 31; height++ {
		reporter := []string{"reporter-a", "reporter-b", "reporter-c"}[int(height)%3]
		tr.ProcessBlockWithReporter(sv, height, reporter, []uint32{0}, []uint32{0})
		if tr.IsActive("unreachable") {
			t.Fatalf("fully unreachable probation miner promoted at height %d", height)
		}
	}
	info := tr.GetMinerByAddress("unreachable")
	if info == nil || info.State != StateProbation || info.Stake != 10000 || info.TotalSlashed != 0 {
		t.Fatalf("unreachable probation state = %+v", info)
	}
}

                                                                            
                                                          
func TestDuplicateOfflineInOneBlockDedupedToSingleObservation(t *testing.T) {
	cfg := defaultTestCfg()
	cfg.MinSamples = 1
	cfg.Threshold = 0.5
	cfg.SlashPerReport = 0
	cfg.MinStake = 0
	cfg.MinActiveMiners = 0
	cfg.PauseRatio = 2.0
	cfg.WindowFloor = 100

	tr, sv := newTrackerWithStore(cfg)
	tr.RegisterMinerAtHeight(sv, "honest", 10000, 0)
	tr.RegisterMinerAtHeight(sv, "v", 10000, 0)

	                                                                                               
	dup := make([]uint32, 100)
	for i := range dup {
		dup[i] = 1
	}
	tr.ProcessBlock(sv, 1, append([]uint32{0}, dup...), dup)

	                                                            
	tr.mu.RLock()
	gotSample := tr.sampleCount[1]
	gotOffline := tr.offlineCount[1]
	tr.mu.RUnlock()
	if gotSample != 1 {
		t.Fatalf("dedup failed: sampleCount[victim]=%d want 1", gotSample)
	}
	if gotOffline != 1 {
		t.Fatalf("dedup failed: offlineCount[victim]=%d want 1", gotOffline)
	}
}
