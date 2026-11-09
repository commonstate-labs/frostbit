package config

import (
	"dex/protocol"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestWitnessChallengeDefaultsAndArbitrationValidation(t *testing.T) {
	want := strconv.FormatInt(protocol.MinChallengeContributionRaw, 10)
	if DefaultWitnessConfig().ChallengeStakeAmount != want {
		t.Fatal("default challenge minimum differs from protocol floor")
	}
	for _, path := range []string{
		"witness_default.json", "env/dev/witness_default.json", "env/test/witness_default.json",
		"env/prod/witness_default.json", "env/local20/witness_default.json",
	} {
		cfg, err := LoadWitnessConfig(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if cfg.ChallengeStakeAmount != want {
			t.Fatalf("%s: minimum=%s want=%s", path, cfg.ChallengeStakeAmount, want)
		}
	}
	for _, tc := range []struct {
		name, body string
		wantErr    bool
	}{
		{"three rounds required", `{"ordinaryCommitteeSizes":[4,8]}`, true},
		{"increasing size", `{"ordinaryCommitteeSizes":[4,8,8]}`, true},
		{"minimum four", `{"ordinaryCommitteeSizes":[3,8,16]}`, true},
		{"strict majority", `{"ordinaryThresholdBps":5000}`, true},
		{"threshold over full committee", `{"ordinaryThresholdBps":10001}`, true},
		{"period fixed", `{"arbitrationPeriodBlocks":100}`, true},
		{"production rejects acceleration", `{"arbitrationPeriodBlocks":20}`, true},
		{"explicit production rejects acceleration", `{"arbitrationTimingProfile":"prod","arbitrationPeriodBlocks":20}`, true},
		{"dev acceleration", `{"arbitrationTimingProfile":"dev","arbitrationPeriodBlocks":20}`, false},
		{"local20 acceleration", `{"arbitrationTimingProfile":"local20","arbitrationPeriodBlocks":20}`, false},
		{"dev production duration", `{"arbitrationTimingProfile":"dev","arbitrationPeriodBlocks":10000}`, false},
		{"dev rejects arbitrary duration", `{"arbitrationTimingProfile":"dev","arbitrationPeriodBlocks":30}`, true},
		{"unknown profile", `{"arbitrationTimingProfile":"local2O","arbitrationPeriodBlocks":10000}`, true},
		{"parameterized policy", `{"ordinaryCommitteeSizes":[8,16,32],"ordinaryThresholdBps":8000}`, false},
		{"defaults", `{}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "witness.json")
			if err := os.WriteFile(path, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadWitnessConfig(path)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestWitnessArbitrationTimingProfiles(t *testing.T) {
	for _, tc := range []struct {
		path, profile string
		period        uint64
	}{
		{"witness_default.json", "", 10000},
		{"env/prod/witness_default.json", "", 10000},
		{"env/test/witness_default.json", "", 10000},
		{"env/dev/witness_default.json", "dev", 20},
		{"env/local20/witness_default.json", "local20", 20},
	} {
		cfg, err := LoadWitnessConfig(tc.path)
		if err != nil || cfg.ArbitrationTimingProfile != tc.profile || cfg.ArbitrationPeriodBlocks != tc.period {
			t.Fatalf("%s: profile=%q period=%d err=%v", tc.path, cfg.ArbitrationTimingProfile, cfg.ArbitrationPeriodBlocks, err)
		}
	}
}

func TestLoadFromDirArbitrationTimingCannotSilentlyFallbackOrBypass(t *testing.T) {
	for _, tc := range []struct {
		name, witnessJSON, configJSON string
		wantErr                       bool
		wantPeriod                    uint64
	}{
		{"runtime directory preserves explicit local20", `{"arbitrationTimingProfile":"local20","arbitrationPeriodBlocks":20}`, `{}`, false, 20},
		{"invalid witness is not silently ignored", `{"arbitrationPeriodBlocks":20}`, `{}`, true, 0},
		{"malformed witness is not silently ignored", `{`, `{}`, true, 0},
		{"config override cannot bypass production", `{}`, `{"Witness":{"arbitrationPeriodBlocks":20}}`, true, 0},
		{"config override cannot remove dev opt-in", `{"arbitrationTimingProfile":"local20","arbitrationPeriodBlocks":20}`, `{"Witness":{"arbitrationTimingProfile":""}}`, true, 0},
		{"explicit config override", `{}`, `{"Witness":{"arbitrationTimingProfile":"dev","arbitrationPeriodBlocks":20}}`, false, 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			                                                                               
			dir := filepath.Join(t.TempDir(), "runtime")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			for name, body := range map[string]string{"witness_default.json": tc.witnessJSON, "config.json": tc.configJSON} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			cfg, err := LoadFromDir(dir)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if err == nil && cfg.Witness.ArbitrationPeriodBlocks != tc.wantPeriod {
				t.Fatalf("effective period=%d want=%d", cfg.Witness.ArbitrationPeriodBlocks, tc.wantPeriod)
			}
		})
	}
}

func TestWitnessChallengePeriodProfiles(t *testing.T) {
	if got := DefaultWitnessConfig().ChallengePeriodBlocks; got != protocol.DefaultRechargeChallengePeriodBlocks {
		t.Fatalf("default challenge period=%d want=%d", got, protocol.DefaultRechargeChallengePeriodBlocks)
	}
	for _, path := range []string{"witness_default.json", "env/prod/witness_default.json"} {
		cfg, err := LoadWitnessConfig(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if cfg.ChallengePeriodBlocks != protocol.DefaultRechargeChallengePeriodBlocks {
			t.Fatalf("%s: challenge period=%d want=%d", path, cfg.ChallengePeriodBlocks, protocol.DefaultRechargeChallengePeriodBlocks)
		}
	}

	                                                                           
	                                                                         
	for _, path := range []string{"env/dev/witness_default.json", "env/test/witness_default.json", "env/local20/witness_default.json"} {
		cfg, err := LoadWitnessConfig(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if cfg.ChallengePeriodBlocks != 20 {
			t.Fatalf("%s: accelerated challenge period=%d want=20", path, cfg.ChallengePeriodBlocks)
		}
	}
}
