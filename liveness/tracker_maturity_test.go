package liveness

import (
	"bytes"
	"dex/keys"
	"reflect"
	"testing"
)

func TestGenesisMinerStartsVaultMature(t *testing.T) {
	cfg := defaultTestCfg()
	tr := NewTrackerWithVaultMaturity(cfg, 10)
	sv := newMockStore()
	tr.SetStore(sv)
	if _, err := tr.RegisterMinerAtHeight(sv, "genesis", 1000, 0); err != nil {
		t.Fatal(err)
	}
	m := tr.GetMinerByAddress("genesis")
	if m == nil || m.State != StateActive || m.MaturedStake != 1000 || m.PendingStake != 0 || m.VaultEligibleHeight != 0 {
		t.Fatalf("genesis maturity = %+v", m)
	}
}

func TestProbationActivationAndVaultMaturityAreSeparate(t *testing.T) {
	cfg := defaultTestCfg()
	cfg.WindowFloor = 3
	cfg.WindowCeil = 3
	cfg.MinSamples = 3
	cfg.MinReporters = 3
	tr := NewTrackerWithVaultMaturity(cfg, 5)
	sv := newMockStore()
	tr.SetStore(sv)
	if _, err := tr.RegisterMinerAtHeight(sv, "joiner", 1000, 1); err != nil {
		t.Fatal(err)
	}

	for i, reporter := range []string{"r-a", "r-b", "r-c"} {
		tr.ProcessBlockWithReporter(sv, uint64(i+2), reporter, []uint32{0}, nil)
	}
	m := tr.GetMinerByAddress("joiner")
	if m == nil || m.State != StateActive || m.ActiveSinceHeight != 4 || m.VaultEligibleHeight != 9 {
		t.Fatalf("activation metadata = %+v", m)
	}
	if m.MaturedStake != 0 || m.PendingStake != 1000 || m.PendingStakeMatureHeight != 6 {
		t.Fatalf("stake must remain pending independently of activation: %+v", m)
	}

	tr.ProcessBlock(sv, 6, nil, nil)
	m = tr.GetMinerByAddress("joiner")
	if m.MaturedStake != 1000 || m.PendingStake != 0 || tr.VaultEligibilityChangedAt() != 6 {
		t.Fatalf("stake maturity at height 6 = %+v changedAt=%d", m, tr.VaultEligibilityChangedAt())
	}
	tr.ProcessBlock(sv, 8, nil, nil)
	if tr.VaultEligibilityChangedAt() != 6 {
		t.Fatalf("eligibility changed early at %d", tr.VaultEligibilityChangedAt())
	}
	tr.ProcessBlock(sv, 9, nil, nil)
	if tr.VaultEligibilityChangedAt() != 9 {
		t.Fatalf("active maturity boundary not published at height 9: %d", tr.VaultEligibilityChangedAt())
	}
}

func TestTopUpsMatureIndependentlyAndSurviveReload(t *testing.T) {
	cfg := defaultTestCfg()
	tr := NewTrackerWithVaultMaturity(cfg, 5)
	sv := newMockStore()
	tr.SetStore(sv)
	if _, err := tr.RegisterMinerAtHeight(sv, "miner", 100, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.RegisterMinerAtHeight(sv, "miner", 150, 2); err != nil {
		t.Fatal(err)
	}
	m := tr.GetMinerByAddress("miner")
	if m.MaturedStake != 100 || m.PendingStake != 50 || m.PendingStakeMatureHeight != 7 ||
		!reflect.DeepEqual(m.PendingStakeBatches, []StakeMaturityBatch{{Amount: 50, MatureHeight: 7}}) {
		t.Fatalf("first top-up = %+v", m)
	}
	if _, err := tr.RegisterMinerAtHeight(sv, "miner", 180, 4); err != nil {
		t.Fatal(err)
	}
	m = tr.GetMinerByAddress("miner")
	wantBatches := []StakeMaturityBatch{{Amount: 50, MatureHeight: 7}, {Amount: 30, MatureHeight: 9}}
	if m.MaturedStake != 100 || m.PendingStake != 80 || m.PendingStakeMatureHeight != 7 ||
		!reflect.DeepEqual(m.PendingStakeBatches, wantBatches) {
		t.Fatalf("second top-up changed an earlier maturity boundary: %+v", m)
	}

	                                                                           
	                                                                         
	minerKey := keys.KeyLivenessMiner(m.Index)
	rawBefore := append([]byte(nil), sv.data[minerKey]...)
	reloaded := NewTrackerWithVaultMaturity(cfg, 5)
	reloaded.SetStore(sv)
	m = reloaded.GetMinerByAddress("miner")
	if !reflect.DeepEqual(m.PendingStakeBatches, wantBatches) || m.PendingStake != 80 || m.PendingStakeMatureHeight != 7 {
		t.Fatalf("reloaded maturity schedule = %+v", m)
	}
	if err := reloaded.UpdateMinerStake(sv, "miner", 180, 4); err != nil {
		t.Fatal(err)
	}
	if rawAfter := sv.data[minerKey]; !bytes.Equal(rawBefore, rawAfter) {
		t.Fatalf("canonical maturity serialization changed after reload:\nbefore=%s\nafter=%s", rawBefore, rawAfter)
	}
	tr = reloaded

	tr.ProcessBlock(sv, 7, nil, nil)
	if m = tr.GetMinerByAddress("miner"); m.MaturedStake != 150 || m.PendingStake != 30 ||
		m.PendingStakeMatureHeight != 9 ||
		!reflect.DeepEqual(m.PendingStakeBatches, []StakeMaturityBatch{{Amount: 30, MatureHeight: 9}}) {
		t.Fatalf("first top-up did not mature independently: %+v", m)
	}
	tr.ProcessBlock(sv, 9, nil, nil)
	if m = tr.GetMinerByAddress("miner"); m.MaturedStake != 180 || m.PendingStake != 0 ||
		m.PendingStakeMatureHeight != 0 || len(m.PendingStakeBatches) != 0 {
		t.Fatalf("second top-up did not mature at its own deadline: %+v", m)
	}
	if tr.VaultEligibilityChangedAt() != 9 {
		t.Fatalf("matured stake refresh height=%d want 9", tr.VaultEligibilityChangedAt())
	}
}

func TestStakeDecreaseConsumesPendingBeforeMatured(t *testing.T) {
	cfg := defaultTestCfg()
	tr := NewTrackerWithVaultMaturity(cfg, 5)
	sv := newMockStore()
	tr.SetStore(sv)
	_, _ = tr.RegisterMinerAtHeight(sv, "miner", 100, 0)
	_, _ = tr.RegisterMinerAtHeight(sv, "miner", 150, 2)
	_, _ = tr.RegisterMinerAtHeight(sv, "miner", 180, 4)
	if err := tr.UpdateMinerStake(sv, "miner", 130, 5); err != nil {
		t.Fatal(err)
	}
	m := tr.GetMinerByAddress("miner")
	if m.MaturedStake != 100 || m.PendingStake != 30 || m.PendingStakeMatureHeight != 7 ||
		!reflect.DeepEqual(m.PendingStakeBatches, []StakeMaturityBatch{{Amount: 30, MatureHeight: 7}}) ||
		m.MaturedStake+m.PendingStake > m.Stake {
		t.Fatalf("stake buckets violate locked total after decrease: %+v", m)
	}
}

func TestSetVaultMaturityBlocksOnlyAffectsFutureBatches(t *testing.T) {
	cfg := defaultTestCfg()
	tr := NewTrackerWithVaultMaturity(cfg, 10)
	sv := newMockStore()
	tr.SetStore(sv)
	_, _ = tr.RegisterMinerAtHeight(sv, "miner", 100, 0)
	_, _ = tr.RegisterMinerAtHeight(sv, "miner", 110, 1)

	tr.SetVaultMaturityBlocks(5)
	_, _ = tr.RegisterMinerAtHeight(sv, "miner", 130, 2)

	m := tr.GetMinerByAddress("miner")
	want := []StakeMaturityBatch{{Amount: 20, MatureHeight: 7}, {Amount: 10, MatureHeight: 11}}
	if !reflect.DeepEqual(m.PendingStakeBatches, want) || m.PendingStake != 30 || m.PendingStakeMatureHeight != 7 {
		t.Fatalf("maturity update rewrote an existing batch: %+v", m)
	}
}

func TestIndependentReportersKickWithoutSlashingAndSurviveReload(t *testing.T) {
	cfg := defaultTestCfg()
	cfg.WindowFloor = 10
	cfg.WindowCeil = 10
	cfg.MinSamples = 4
	cfg.MinReporters = 3
	cfg.Threshold = 0.75
	sv := newMockStore()
	tr := NewTracker(cfg)
	tr.SetStore(sv)
	_, _ = tr.RegisterMinerAtHeight(sv, "victim", 1000, 0)

	for i, reporter := range []string{"r-a", "r-b", "r-c"} {
		h := uint64(i + 1)
		kicked, slashed := tr.ProcessBlockWithReporter(sv, h, reporter, []uint32{0}, []uint32{0})
		if len(kicked) != 0 || len(slashed) != 0 {
			t.Fatalf("premature membership change at %d: kicked=%v slashed=%v", h, kicked, slashed)
		}
	}

	reloaded := NewTracker(cfg)
	reloaded.SetStore(sv)
	kicked, slashed := reloaded.ProcessBlockWithReporter(sv, 4, "r-a", []uint32{0}, []uint32{0})
	if len(kicked) != 0 || len(slashed) != 0 || reloaded.GetMinerByAddress("victim").OfflineSinceHeight != 4 {
		t.Fatalf("first sufficient evidence must begin grace: kicked=%v slashed=%v", kicked, slashed)
	}
	reloaded = NewTracker(cfg)
	reloaded.SetStore(sv)
	kicked, slashed = reloaded.ProcessBlockWithReporter(sv, 5, "r-b", []uint32{0}, []uint32{0})
	if len(kicked) != 1 || kicked[0] != 0 || len(slashed) != 0 {
		t.Fatalf("kick result after reload: kicked=%v slashed=%v", kicked, slashed)
	}
	m := reloaded.GetMinerByAddress("victim")
	if m == nil || m.State != StateFrozen || !m.Kicked || m.Stake != 1000 || m.TotalSlashed != 0 {
		t.Fatalf("offline removal must preserve stake: %+v", m)
	}
}

func TestReporterCannotAccuseItselfOffline(t *testing.T) {
	cfg := defaultTestCfg()
	cfg.MinSamples = 1
	cfg.MinReporters = 1
	cfg.Threshold = 0
	tr, sv := newTrackerWithStore(cfg)
	_, _ = tr.RegisterMinerAtHeight(sv, "self", 1000, 0)
	kicked, _ := tr.ProcessBlockWithReporter(sv, 1, "self", []uint32{0}, []uint32{0})
	if len(kicked) != 0 || tr.GetOfflineCountForTest(0) != 0 || !tr.IsActive("self") {
		t.Fatalf("self accusation was accepted: kicked=%v offline=%d", kicked, tr.GetOfflineCountForTest(0))
	}
}
