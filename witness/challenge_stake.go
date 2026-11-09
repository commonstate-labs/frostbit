package witness

import (
	"dex/protocol"
	"fmt"
	"math/big"
)

const DefaultChallengeStakeRatioBps uint32 = 1000

type ChallengeStakeQuote struct {
	RequiredChallengeStake   string `json:"required_challenge_stake"`
	SelectedWitnessStake     string `json:"selected_witness_stake"`
	SlashableWitnessStake    string `json:"slashable_witness_stake"`
	ChallengeStakeRatioBps   uint32 `json:"challenge_stake_ratio_bps"`
	MinChallengeStakeAmount  string `json:"min_challenge_stake_amount"`
	MinChallengeContribution string `json:"min_challenge_contribution"`
	ArbitrationBaseCost      string `json:"arbitration_base_cost"`
	MaxChallengeStakeAmount  string `json:"max_challenge_stake_amount"`
}

func CalculateChallengeStakeRequirement(cfg *Config, originalReservedAmounts []string) (*ChallengeStakeQuote, error) {
	total := new(big.Int)
	for _, raw := range originalReservedAmounts {
		amount, err := parseNonNegativeInt(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid original case reservation: %w", err)
		}
		total.Add(total, amount)
	}
	                                                                                
	required := new(big.Int).Add(total, big.NewInt(9))
	required.Div(required, big.NewInt(10))
	minimum := big.NewInt(protocol.MinChallengeContributionRaw)
	if required.Cmp(minimum) < 0 {
		required.Set(minimum)
	}
	return &ChallengeStakeQuote{RequiredChallengeStake: required.String(), SelectedWitnessStake: total.String(),
		SlashableWitnessStake: total.String(), ChallengeStakeRatioBps: 1000, MinChallengeStakeAmount: minimum.String(),
		MinChallengeContribution: minimum.String(), ArbitrationBaseCost: "0", MaxChallengeStakeAmount: "0"}, nil
}

func parseNonNegativeInt(raw string) (*big.Int, error) {
	if raw == "" {
		return big.NewInt(0), nil
	}
	v, ok := new(big.Int).SetString(raw, 10)
	if !ok || v.Sign() < 0 {
		return nil, fmt.Errorf("%q", raw)
	}
	return v, nil
}

func maxBigInt(values ...*big.Int) *big.Int {
	out := big.NewInt(0)
	for _, v := range values {
		if v != nil && v.Cmp(out) > 0 {
			out = new(big.Int).Set(v)
		}
	}
	return out
}
