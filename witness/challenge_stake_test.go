package witness

import (
	"dex/protocol"
	"strconv"
	"testing"
)

func TestChallengeStakeRequirementUsesFrozenOriginalReservations(t *testing.T) {
	twenty := make([]string, 20)
	for i := range twenty {
		twenty[i] = "1000000000"
	}
	for _, tc := range []struct {
		name        string
		reserved    []string
		want, total string
	}{
		{"empty original committee floor", nil, "1000000000", "0"},
		{"four original members reserve ten each", []string{"1000000000", "1000000000", "1000000000", "1000000000"}, "1000000000", "4000000000"},
		{"twenty original members", twenty, "2000000000", "20000000000"},
		{"ceiling retains final raw unit", []string{"10000000001"}, "1000000001", "10000000001"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			                                                                
			                                                              
			cfg.MinStakeAmount, cfg.ChallengeStakeAmount = "999999999999999", "90000000000"
			cfg.ChallengeArbitrationBaseCost, cfg.MaxChallengeStakeAmount = "80000000000", "1"
			cfg.ChallengeStakeRatioBps, cfg.SlashRatio = 9999, 0.01
			quote, err := CalculateChallengeStakeRequirement(cfg, tc.reserved)
			if err != nil {
				t.Fatal(err)
			}
			if quote.RequiredChallengeStake != tc.want || quote.SelectedWitnessStake != tc.total || quote.SlashableWitnessStake != tc.total {
				t.Fatalf("quote=%+v want required=%s original=%s", quote, tc.want, tc.total)
			}
			if quote.ChallengeStakeRatioBps != 1000 || quote.ArbitrationBaseCost != "0" || quote.MaxChallengeStakeAmount != "0" || quote.MinChallengeContribution != strconv.FormatInt(protocol.MinChallengeContributionRaw, 10) {
				t.Fatalf("old policy leaked into quote: %+v", quote)
			}
		})
	}
}

func TestChallengeStakeRequirementRejectsNonIntegerReservations(t *testing.T) {
	for _, raw := range []string{"-1", "1000000000.5", "1e9", "invalid"} {
		if _, err := CalculateChallengeStakeRequirement(DefaultConfig(), []string{raw}); err == nil {
			t.Fatalf("accepted invalid original reservation %q", raw)
		}
	}
}
