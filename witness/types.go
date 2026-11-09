                   
                                    
package witness

import (
	frostchain "dex/frost/chain"
	"dex/protocol"
	"errors"
	"strconv"
	"strings"
	"time"
)

                                                           

var (
	ErrWitnessNotFound               = errors.New("witness not found")
	ErrWitnessNotActive              = errors.New("witness is not active")
	ErrWitnessAlreadyActive          = errors.New("witness is already active")
	ErrInsufficientStake             = errors.New("insufficient stake amount")
	ErrUnstakingInProgress           = errors.New("unstaking already in progress")
	ErrPendingTasks                  = errors.New("witness has pending tasks")
	ErrShelvedArbitrations           = errors.New("witness has shelved arbitrations")
	ErrLockPeriodNotExpired          = errors.New("lock period has not expired")
	ErrNotUnstaking                  = errors.New("witness is not in unstaking state")
	ErrNothingToClaim                = errors.New("no locked stake to claim")
	ErrRequestNotFound               = errors.New("recharge request not found")
	ErrRequestAlreadyExists          = errors.New("recharge request already exists")
	ErrInvalidVote                   = errors.New("invalid vote")
	ErrDuplicateVote                 = errors.New("duplicate vote")
	ErrWitnessVoteRoundMismatch      = errors.New("witness vote round does not match request")
	ErrVotingPeriodExpired           = errors.New("voting period has expired")
	ErrNotSelectedWitness            = errors.New("not a selected witness for this request")
	ErrChallengeNotFound             = errors.New("challenge not found")
	ErrChallengeExists               = errors.New("challenge already exists")
	ErrNotInChallengePeriod          = errors.New("not in challenge period")
	ErrInsufficientChallenge         = errors.New("insufficient challenge stake")
	ErrArbitrationExpired            = errors.New("arbitration period has expired")
	ErrNotArbitrator                 = errors.New("not an arbitrator for this challenge")
	ErrConsensusNotReached           = errors.New("consensus not reached")
	ErrNativeChainNotSupport         = errors.New("native chain not supported")
	ErrInsufficientSelectedWitnesses = errors.New("insufficient selected witnesses to start witness process")
)

                                                           

                               
type Config struct {
	               
	ConsensusThreshold uint32                                       
	AbstainThreshold   uint32                                       

	                              
	VotingPeriodBlocks      uint64             
	VoteGracePeriodBlocks   uint64                                                          
	ChallengePeriodBlocks   uint64             
	ArbitrationPeriodBlocks uint64             
	UnstakeLockBlocks       uint64                      
	RetryIntervalBlocks     uint64                               
	MaxShelveWaitBlocks     uint64                                                                              

	               
	MinStakeAmount               string                      
	ChallengeStakeAmount         string                            
	ChallengeStakeRatioBps       uint32                                
	ChallengeArbitrationBaseCost string                         
	MaxChallengeStakeAmount      string                                        

	                        
	InitialWitnessCount     uint32                           
	MinSelectedWitnessCount uint32                                                               
	ExpandMultiplier        uint32                        
	OrdinaryCommitteeSizes  []uint32                                                    
	OrdinaryThresholdBps    uint32                                             

	                                                                                                           
	                                                                                                                                             
	SelectionBeaconDepth uint64                                                              
	BeaconMixBlocks      uint64                                             

	               
	SlashRatio        float64                                  
	ChallengerReward  float64                                                       
	SilenceSlashRatio float64                                                                                      
}

                                   
func DefaultConfig() *Config {
	challengeStake := strconv.FormatInt(protocol.MinChallengeContributionRaw, 10)
	return &Config{
		ConsensusThreshold:           60,
		AbstainThreshold:             20,
		VotingPeriodBlocks:           20,                      
		VoteGracePeriodBlocks:        1,
		ChallengePeriodBlocks:        protocol.DefaultRechargeChallengePeriodBlocks,
		ArbitrationPeriodBlocks:      10000,
		UnstakeLockBlocks:            20,                          
		RetryIntervalBlocks:          20,                          
		MaxShelveWaitBlocks:          200,                                                                                
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

                                                              

                                                                
type ChallengePeriodLevel int

const (
	ChallengePeriodShort  ChallengePeriodLevel = iota           
	ChallengePeriodMedium                                        
	ChallengePeriodLong                                          
)

                                                                
func GetChallengePeriodBlocks(level ChallengePeriodLevel) uint64 {
	return protocol.DefaultRechargeChallengePeriodBlocks
}

                                                                 

                                                             
                                 
func GetConsensusThreshold(round uint32) uint32 {
	switch {
	case round == 0:
		return 80
	case round == 1:
		return 70
	case round == 2:
		return 60
	default:
		return 50
	}
}

                                                                 

                              
type NativeChain struct {
	ID          string                    
	Name        string                    
	ConfirmTime time.Duration                
	MinConfirms uint32                          
}

                                           
var SupportedChains = map[string]*NativeChain{
	"BTC": {
		ID:          "BTC",
		Name:        "Bitcoin",
		ConfirmTime: 10 * time.Minute,
		MinConfirms: 6,
	},
	"ETH": {
		ID:          "ETH",
		Name:        "Ethereum",
		ConfirmTime: 15 * time.Second,
		MinConfirms: 12,
	},
	"BSC": {
		ID:          "BSC",
		Name:        "BNB Smart Chain",
		ConfirmTime: 3 * time.Second,
		MinConfirms: 15,
	},
	"TRON": {
		ID:          "TRON",
		Name:        "Tron",
		ConfirmTime: 3 * time.Second,
		MinConfirms: 19,
	},
}

                                                  
func IsSupportedChain(chainID string) bool {
	if chainID != strings.TrimSpace(chainID) {
		return false
	}
	if frostchain.IsValidChain(chainID) {
		return true
	}
	_, ok := SupportedChains[strings.ToUpper(chainID)]
	return ok
}

                                                                 

                         
type VoteStats struct {
	Total        uint32              
	Pass         uint32              
	Fail         uint32              
	Abstain      uint32           
	PassRatio    float64             
	FailRatio    float64             
	AbstainRatio float64             
}

                                        
func CalculateVoteStats(pass, fail, abstain, total uint32) *VoteStats {
	if total == 0 {
		return &VoteStats{}
	}
	return &VoteStats{
		Total:        total,
		Pass:         pass,
		Fail:         fail,
		Abstain:      abstain,
		PassRatio:    float64(pass) / float64(total) * 100,
		FailRatio:    float64(fail) / float64(total) * 100,
		AbstainRatio: float64(abstain) / float64(total) * 100,
	}
}

                               
type ConsensusResult int

const (
	ConsensusNone     ConsensusResult = iota                   
	ConsensusPass                                           
	ConsensusFail                                           
	ConsensusExpand                                               
	ConsensusConflict                                                         
)

                                        
func DetermineConsensus(stats *VoteStats, threshold uint32, isDeadline bool) ConsensusResult {
	thresholdFloat := float64(threshold)

	                                 
	if stats.PassRatio >= thresholdFloat {
		return ConsensusPass
	}

	                                 
	if stats.FailRatio >= thresholdFloat {
		return ConsensusFail
	}

	                           
	if isDeadline {
		                                                                  
		if stats.Pass > 0 && stats.Fail > 0 {
			return ConsensusConflict
		}

		                                             
		if stats.AbstainRatio > float64(100-threshold) {
			return ConsensusExpand
		}
	}

	return ConsensusNone
}

                                                           

                         
type EventType string

const (
	EventWitnessStaked                EventType = "witness_staked"
	EventWitnessUnstaking             EventType = "witness_unstaking"
	EventWitnessExited                EventType = "witness_exited"
	EventWitnessSlashed               EventType = "witness_slashed"
	EventRechargeRequested            EventType = "recharge_requested"
	EventVoteReceived                 EventType = "vote_received"
	EventConsensusReached             EventType = "consensus_reached"
	EventConsensusFailed              EventType = "consensus_failed"
	EventScopeExpanded                EventType = "scope_expanded"
	EventChallengePeriodStart         EventType = "challenge_period_start"
	EventChallengeInitiated           EventType = "challenge_initiated"
	EventArbitrationStart             EventType = "arbitration_start"
	EventArbitrationVote              EventType = "arbitration_vote"
	EventChallengeResolved            EventType = "challenge_resolved"
	EventRechargeFinalized            EventType = "recharge_finalized"
	EventRechargeRejected             EventType = "recharge_rejected"
	EventRechargeShelved              EventType = "recharge_shelved"
	EventRechargeReselectionScheduled EventType = "recharge_reselection_scheduled"
)

               
type Event struct {
	Type      EventType
	RequestID string
	Data      interface{}
	Height    uint64
	Timestamp int64
}
