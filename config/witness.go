package config

import (
	"dex/protocol"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

                                      
type WitnessConfig struct {
	Enabled bool `json:"enabled"`

	               
	ConsensusThreshold uint32 `json:"consensusThreshold"`
	AbstainThreshold   uint32 `json:"abstainThreshold"`

	                              
	VotingPeriodBlocks      uint64 `json:"votingPeriodBlocks"`
	VoteGracePeriodBlocks   uint64 `json:"voteGracePeriodBlocks"`
	ChallengePeriodBlocks   uint64 `json:"challengePeriodBlocks"`
	ArbitrationPeriodBlocks uint64 `json:"arbitrationPeriodBlocks"`
	                                                                                              
	ArbitrationTimingProfile string `json:"arbitrationTimingProfile,omitempty"`
	UnstakeLockBlocks        uint64 `json:"unstakeLockBlocks"`
	RetryIntervalBlocks      uint64 `json:"retryIntervalBlocks"`
	MaxShelveWaitBlocks      uint64 `json:"maxShelveWaitBlocks"`

	               
	MinStakeAmount               string `json:"minStakeAmount"`
	ChallengeStakeAmount         string `json:"challengeStakeAmount"`
	ChallengeStakeRatioBps       uint32 `json:"challengeStakeRatioBps"`
	ChallengeArbitrationBaseCost string `json:"challengeArbitrationBaseCost"`
	MaxChallengeStakeAmount      string `json:"maxChallengeStakeAmount"`

	                        
	InitialWitnessCount     uint32   `json:"initialWitnessCount"`
	MinSelectedWitnessCount uint32   `json:"minSelectedWitnessCount"`
	ExpandMultiplier        uint32   `json:"expandMultiplier"`
	OrdinaryCommitteeSizes  []uint32 `json:"ordinaryCommitteeSizes"`
	OrdinaryThresholdBps    uint32   `json:"ordinaryThresholdBps"`

	                                     
	SelectionBeaconDepth uint64 `json:"selectionBeaconDepth"`
	BeaconMixBlocks      uint64 `json:"beaconMixBlocks"`

	               
	SlashRatio        float64 `json:"slashRatio"`
	ChallengerReward  float64 `json:"challengerReward"`
	SilenceSlashRatio float64 `json:"silenceSlashRatio"`
}

                                                  
func (wc WitnessConfig) ToWitnessConfig() interface{} {
	                                                                                           
	                                                                                          
	return wc
}

                                                   
func DefaultWitnessConfig() WitnessConfig {
	challengeStake := strconv.FormatInt(protocol.MinChallengeContributionRaw, 10)
	return WitnessConfig{
		Enabled:                 true,
		ConsensusThreshold:      60,
		AbstainThreshold:        20,
		VotingPeriodBlocks:      100,
		VoteGracePeriodBlocks:   1,
		ChallengePeriodBlocks:   protocol.DefaultRechargeChallengePeriodBlocks,
		ArbitrationPeriodBlocks: protocol.ArbitrationRoundBlocks,
		UnstakeLockBlocks:       50,
		RetryIntervalBlocks:     20,
		                                                                                          
		MaxShelveWaitBlocks: 864000,
		                                
		MinStakeAmount:               "3200000000",           
		ChallengeStakeAmount:         challengeStake,         
		ChallengeStakeRatioBps:       1000,                                                        
		ChallengeArbitrationBaseCost: "0",
		MaxChallengeStakeAmount:      "0",
		InitialWitnessCount:          5,
		MinSelectedWitnessCount:      4,
		ExpandMultiplier:             2,
		OrdinaryCommitteeSizes:       []uint32{4, 8, 16},
		OrdinaryThresholdBps:         7500,
		SelectionBeaconDepth:         6,
		BeaconMixBlocks:              3,
		SlashRatio:                   1.0,
		ChallengerReward:             0.2,
		SilenceSlashRatio:            0,
	}
}

                                                   
func LoadWitnessConfig(path string) (WitnessConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultWitnessConfig(), nil
		}
		return WitnessConfig{}, fmt.Errorf("failed to read witness config: %w", err)
	}

	cfg := DefaultWitnessConfig()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return WitnessConfig{}, fmt.Errorf("failed to parse witness config: %w", err)
	}
	if err := validateWitnessChallengeStakeBounds(cfg); err != nil {
		return WitnessConfig{}, err
	}

	return cfg, nil
}

                                                                                
                                                                             
func validateWitnessChallengeStakeBounds(cfg WitnessConfig) error {
	switch cfg.ArbitrationTimingProfile {
	case "", "prod":
		if cfg.ArbitrationPeriodBlocks != protocol.ArbitrationRoundBlocks {
			return fmt.Errorf("production arbitrationPeriodBlocks must be %d; accelerated timing requires an explicit dev/local20 profile", protocol.ArbitrationRoundBlocks)
		}
	case "dev", "local20":
		if cfg.ArbitrationPeriodBlocks != 20 && cfg.ArbitrationPeriodBlocks != protocol.ArbitrationRoundBlocks {
			return fmt.Errorf("dev/local20 arbitrationPeriodBlocks must be 20 or %d", protocol.ArbitrationRoundBlocks)
		}
	default:
		return fmt.Errorf("unknown arbitrationTimingProfile %q", cfg.ArbitrationTimingProfile)
	}
	if len(cfg.OrdinaryCommitteeSizes) != 3 || cfg.OrdinaryThresholdBps <= 5000 || cfg.OrdinaryThresholdBps > 10000 {
		return fmt.Errorf("ordinary arbitration requires three committee sizes and a strict-majority threshold")
	}
	for i, n := range cfg.OrdinaryCommitteeSizes {
		if n < 4 || (i > 0 && n <= cfg.OrdinaryCommitteeSizes[i-1]) {
			return fmt.Errorf("ordinary committee sizes must strictly increase from at least four")
		}
	}
	return nil
}
