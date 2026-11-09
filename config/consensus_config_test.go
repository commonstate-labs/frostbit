package config

import (
	"dex/protocol"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultConsensusConfig(t *testing.T) {
	cfg := DefaultConfig()
	got := cfg.Consensus
	if got.K != 15 || got.AlphaPreference != 8 || got.AlphaConfidence != 12 || got.Beta != 15 {
		t.Fatalf(
			"unexpected consensus thresholds: K=%d AlphaPreference=%d AlphaConfidence=%d Beta=%d",
			got.K, got.AlphaPreference, got.AlphaConfidence, got.Beta,
		)
	}
	if got.PollInterval != 25*time.Millisecond || got.QueryTimeout != 4*time.Second || got.MaxConcurrentQueries != 8 || got.ProposalInterval != 5*time.Second {
		t.Fatalf("unexpected consensus timing: %+v", got)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config must be valid: %v", err)
	}
}

func TestLivenessMinerStakeMatchesProtocolMinimum(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Liveness.MinStake != protocol.MinMinerStakeRaw {
		t.Fatalf("default liveness min stake=%d want=%d", cfg.Liveness.MinStake, protocol.MinMinerStakeRaw)
	}
	cfg.Liveness.MinStake--
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "protocol miner minimum") {
		t.Fatalf("mismatched liveness min stake was accepted: %v", err)
	}
}

func TestLivenessConsensusCriticalProfileCannotBeLocallyOverridden(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"freeze blocks", func(cfg *Config) { cfg.Liveness.FreezeBlocks++ }},
		{"offline grace", func(cfg *Config) { cfg.Liveness.OfflineGraceBlocks++ }},
		{"slash amount", func(cfg *Config) { cfg.Liveness.SlashPerReport++ }},
		{"offline threshold", func(cfg *Config) { cfg.Liveness.Threshold -= 0.1 }},
		{"minimum samples", func(cfg *Config) { cfg.Liveness.MinSamples++ }},
		{"minimum active miners", func(cfg *Config) { cfg.Liveness.MinActiveMiners++ }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			tt.mutate(cfg)
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "protocol network profile") {
				t.Fatalf("local liveness override was accepted: %v", err)
			}
		})
	}
}

func TestConsensusConfigJSONOverrideKeepsDefaults(t *testing.T) {
	cfg := DefaultConfig()
	if err := json.Unmarshal([]byte(`{"Consensus":{"Beta":20}}`), cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Consensus.K != 15 || cfg.Consensus.AlphaPreference != 8 || cfg.Consensus.AlphaConfidence != 12 || cfg.Consensus.Beta != 20 {
		t.Fatalf("unexpected merged consensus config: %+v", cfg.Consensus)
	}
	if cfg.Consensus.PollInterval != DefaultConsensusPollInterval {
		t.Fatalf("JSON override lost default poll interval: %v", cfg.Consensus.PollInterval)
	}
}

func TestLoadFromFileRejectsInvalidConsensusConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"Consensus":{"Beta":0}}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadFromFile(path)
	if err == nil || !strings.Contains(err.Error(), "Consensus.Beta") {
		t.Fatalf("expected invalid beta error, got %v", err)
	}
}

func TestConsensusThresholdValidation(t *testing.T) {
	tests := []struct {
		name            string
		k               int
		alphaPreference int
		alphaConfidence int
		want            string
	}{
		{
			name:            "preference must be a strict majority",
			k:               15,
			alphaPreference: 7,
			alphaConfidence: 12,
			want:            "Consensus.AlphaPreference",
		},
		{
			name:            "confidence must be a strict majority",
			k:               15,
			alphaPreference: 8,
			alphaConfidence: 7,
			want:            "Consensus.AlphaConfidence",
		},
		{
			name:            "preference cannot exceed confidence",
			k:               15,
			alphaPreference: 13,
			alphaConfidence: 12,
			want:            "less than or equal",
		},
		{
			name:            "confidence cannot exceed K",
			k:               15,
			alphaPreference: 8,
			alphaConfidence: 16,
			want:            "Consensus.AlphaConfidence",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Consensus.K = tt.k
			cfg.Consensus.AlphaPreference = tt.alphaPreference
			cfg.Consensus.AlphaConfidence = tt.alphaConfidence
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestWindowConfigValidation(t *testing.T) {
	tests := []struct {
		name   string
		window WindowConfig
		want   string
	}{
		{
			name:   "enabled without stages",
			window: WindowConfig{Enabled: true},
			want:   "no stages",
		},
		{
			name: "zero duration before final stage",
			window: WindowConfig{Enabled: true, Stages: []WindowStage{
				{Duration: 0, Probability: 0.1},
				{Duration: time.Second, Probability: 0.2},
			}},
			want: "final window stage",
		},
		{
			name: "invalid probability",
			window: WindowConfig{Enabled: true, Stages: []WindowStage{
				{Duration: time.Second, Probability: 1.1},
			}},
			want: "probability",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Window = tt.window
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("invalid window configuration was accepted: %v", err)
			}
		})
	}

	cfg := DefaultConfig()
	cfg.Window.Enabled = false
	cfg.Window.Stages = nil
	if err := ValidateWindowConfig(cfg.Window); err != nil {
		t.Fatalf("disabled single-window structure should be valid: %v", err)
	}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "protocol network profile") {
		t.Fatalf("production config accepted a local window-profile override: %v", err)
	}
}

func TestWindowConsensusProfileCannotBeLocallyOverridden(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{"enabled", func(cfg *Config) { cfg.Window.Enabled = false }},
		{"stage count", func(cfg *Config) { cfg.Window.Stages = cfg.Window.Stages[:len(cfg.Window.Stages)-1] }},
		{"duration", func(cfg *Config) { cfg.Window.Stages[0].Duration++ }},
		{"probability", func(cfg *Config) { cfg.Window.Stages[0].Probability += 0.01 }},
		{"expected proposers", func(cfg *Config) { cfg.Window.Stages[0].ExpectedProposers++ }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			tt.mutate(cfg)
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "protocol network profile") {
				t.Fatalf("local window override was accepted: %v", err)
			}
		})
	}
}

func TestLoadFromFileRejectsLegacyConsensusAlpha(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"Consensus":{"Alpha":12}}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadFromFile(path)
	if err == nil || !strings.Contains(err.Error(), "legacy Consensus.Alpha") {
		t.Fatalf("expected legacy Alpha migration error, got %v", err)
	}
}

func TestLoadFromFileRejectsInvalidConsensusPollInterval(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"Consensus":{"PollInterval":0}}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadFromFile(path)
	if err == nil || !strings.Contains(err.Error(), "Consensus.PollInterval") {
		t.Fatalf("expected invalid poll interval error, got %v", err)
	}
}

func TestEnvironmentConfigsUseValidConsensusDefaults(t *testing.T) {
	for _, env := range []string{"dev", "local20", "prod", "test"} {
		t.Run(env, func(t *testing.T) {
			cfg, err := LoadFromDir(filepath.Join("env", env))
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Consensus.K != 15 || cfg.Consensus.AlphaPreference != 8 ||
				cfg.Consensus.AlphaConfidence != 12 || cfg.Consensus.Beta != 15 {
				t.Fatalf("unexpected consensus thresholds: %+v", cfg.Consensus)
			}
		})
	}
}
