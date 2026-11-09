package consensus

import (
	chainconfig "dex/config"
	"strings"
	"testing"
	"time"
)

func TestNewRuntimeConfig(t *testing.T) {
	protocol := chainconfig.ConsensusConfig{
		K: 21, AlphaPreference: 11, AlphaConfidence: 16, Beta: 25,
		PollInterval: 175 * time.Millisecond,
		QueryTimeout: 7 * time.Second, MaxConcurrentQueries: 9,
		ProposalInterval: 6 * time.Second,
	}
	window := chainconfig.WindowConfig{
		Enabled: true,
		Stages:  []chainconfig.WindowStage{{Duration: time.Second, Probability: 0.25}},
	}
	got := NewRuntimeConfig(protocol, window)
	if got.Consensus.K != 21 || got.Consensus.AlphaPreference != 11 || got.Consensus.AlphaConfidence != 16 || got.Consensus.Beta != 25 ||
		got.Consensus.PollInterval != 175*time.Millisecond ||
		got.Consensus.QueryTimeout != 7*time.Second || got.Consensus.MaxConcurrentQueries != 9 ||
		got.Node.ProposalInterval != 6*time.Second {
		t.Fatalf("chain consensus config was not propagated: consensus=%+v node=%+v", got.Consensus, got.Node)
	}
	if got.Consensus.NumHeights != 10 || got.Consensus.BlocksPerHeight != 5 {
		t.Fatalf("simulation-only defaults must remain package-owned: %+v", got.Consensus)
	}
	if !windowConfigurationsEqual(got.Window, window) {
		t.Fatalf("chain window config was not propagated: got=%+v want=%+v", got.Window, window)
	}
}

func TestRuntimeWindowCompatibility(t *testing.T) {
	runtime := chainconfig.DefaultConfig().Window
	store := runtime
	store.Stages = append([]chainconfig.WindowStage(nil), store.Stages...)
	if err := validateRuntimeWindowCompatibility(runtime, store); err != nil {
		t.Fatalf("identical window configs rejected: %v", err)
	}

	store.Stages[1].Duration++
	if err := validateRuntimeWindowCompatibility(runtime, store); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("window config mismatch was accepted: %v", err)
	}

	localOverride := chainconfig.WindowConfig{
		Enabled: true,
		Stages:  []chainconfig.WindowStage{{Duration: time.Second, Probability: 0.5}},
	}
	if err := validateRuntimeWindowCompatibility(localOverride, localOverride); err == nil || !strings.Contains(err.Error(), "protocol network profile") {
		t.Fatalf("matching local overrides bypassed protocol profile: %v", err)
	}
}

func TestRuntimeWindowConfigIsCloned(t *testing.T) {
	protocol := chainconfig.DefaultConfig().Consensus
	window := chainconfig.DefaultConfig().Window
	runtime := NewRuntimeConfig(protocol, window)
	original := runtime.Window.Stages[0]
	window.Stages[0].Duration++
	if runtime.Window.Stages[0] != original {
		t.Fatal("runtime window config retained caller-owned stage slice")
	}
}

func TestDefaultConsensusThresholds(t *testing.T) {
	got := DefaultConfig().Consensus
	if got.K != 15 || got.AlphaPreference != 8 || got.AlphaConfidence != 12 || got.Beta != 15 {
		t.Fatalf("unexpected runtime consensus defaults: %+v", got)
	}
}
