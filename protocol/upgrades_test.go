package protocol

import "testing"

func TestCompiledUpgradeSchedulesAreValid(t *testing.T) {
	for chainID := range compiledUpgradeSchedules {
		if err := ValidateUpgradeSchedule(chainID); err != nil {
			t.Fatalf("chain %d: %v", chainID, err)
		}
	}
}

func TestFresh51025HasNoUpgradeSchedule(t *testing.T) {
	if schedule := UpgradeSchedule(51025); len(schedule) != 0 {
		t.Fatalf("fresh chain has unexpected upgrade schedule: %+v", schedule)
	}
	for _, height := range []uint64{1, 42599, 42600, 1000000} {
		if UpgradePending(51025, height, LegacyVersion, UpgradeBTCFeeConfigV2) {
			t.Fatalf("fresh chain has pending BTC migration at height %d", height)
		}
	}
}

func TestUpgradeScheduleReturnsCopy(t *testing.T) {
	const syntheticChainID = 9901
	compiledUpgradeSchedules[syntheticChainID] = []Upgrade{{Version: 2, ActivationHeight: 42600, ID: UpgradeBTCFeeConfigV2}}
	t.Cleanup(func() { delete(compiledUpgradeSchedules, syntheticChainID) })
	first := UpgradeSchedule(syntheticChainID)
	first[0].ActivationHeight++
	second := UpgradeSchedule(syntheticChainID)
	if second[0].ActivationHeight != 42600 {
		t.Fatal("caller mutated compiled upgrade schedule")
	}
	if !UpgradePending(syntheticChainID, 42599, LegacyVersion, UpgradeBTCFeeConfigV2) ||
		UpgradePending(syntheticChainID, 42600, LegacyVersion, UpgradeBTCFeeConfigV2) {
		t.Fatal("generic pending migration boundary changed")
	}
}

func TestWitnessRechargeEconomicsIsGenesisChainRule(t *testing.T) {
	if !WitnessRechargeEconomicsActive(51025) {
		t.Fatal("launch chain must enforce economics from its first request")
	}
	for _, chainID := range []uint64{0, 1, 1001, 1002} {
		if WitnessRechargeEconomicsActive(chainID) {
			t.Fatalf("historical/local chain %d unexpectedly enforces launch economics", chainID)
		}
	}
}

func TestParseStateVersionRequiresCanonicalDecimal(t *testing.T) {
	if got, err := ParseStateVersion([]byte("2")); err != nil || got != 2 {
		t.Fatalf("parse version: got=%d err=%v", got, err)
	}
	for _, raw := range []string{"", "0", "02", " 2", "2\n", "v2"} {
		if _, err := ParseStateVersion([]byte(raw)); err == nil {
			t.Fatalf("accepted invalid version %q", raw)
		}
	}
}
