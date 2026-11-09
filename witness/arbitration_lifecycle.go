package witness

import (
	"dex/pb"
	"dex/protocol"
	"errors"
	"fmt"
	"math"
	"math/big"
	"sort"

	"google.golang.org/protobuf/proto"
)

var ErrInsufficientFinalCourt = errors.New("fewer than four eligible court members")

                                                                             
                                                                                 
func LastDirectionalPositions(previous []*pb.WitnessVote, vote *pb.WitnessVote) []*pb.WitnessVote {
	byAddress := make(map[string]*pb.WitnessVote)
	for _, item := range previous {
		if item != nil && item.WitnessAddress != "" && (item.VoteType == pb.WitnessVoteType_VOTE_PASS || item.VoteType == pb.WitnessVoteType_VOTE_FAIL) {
			byAddress[item.WitnessAddress] = proto.Clone(item).(*pb.WitnessVote)
		}
	}
	if vote != nil && vote.WitnessAddress != "" && (vote.VoteType == pb.WitnessVoteType_VOTE_PASS || vote.VoteType == pb.WitnessVoteType_VOTE_FAIL) {
		byAddress[vote.WitnessAddress] = proto.Clone(vote).(*pb.WitnessVote)
	}
	addresses := make([]string, 0, len(byAddress))
	for address := range byAddress {
		addresses = append(addresses, address)
	}
	sort.Strings(addresses)
	result := make([]*pb.WitnessVote, 0, len(addresses))
	for _, address := range addresses {
		result = append(result, byAddress[address])
	}
	return result
}

func OriginalPositions(req *pb.RechargeRequest) []*pb.WitnessVote {
	if req == nil {
		return nil
	}
	positions := req.OriginalPositions
	if len(positions) == 0 {
		positions = req.Votes
	}
	return LastDirectionalPositions(positions, nil)
}

func ValidateOrdinaryArbitrationPolicy(sizes []uint32, threshold uint32) error {
	if len(sizes) != 3 || threshold <= 5000 || threshold > 10000 {
		return fmt.Errorf("ordinary arbitration requires three sizes and a strict-majority threshold")
	}
	for i, size := range sizes {
		if size < 4 || (i > 0 && size <= sizes[i-1]) {
			return fmt.Errorf("ordinary arbitration sizes must increase from at least four")
		}
	}
	return nil
}

                                                                              
                                                       
func PrepareArbitrationRecord(ch *pb.ChallengeRecord, req *pb.RechargeRequest, cfg *Config) error {
	if ch == nil || req == nil || cfg == nil {
		return fmt.Errorf("missing arbitration state")
	}
	if ch.ArbitrationPeriodBlocks == 0 {
		                                                                           
		                                                                                             
		ch.ArbitrationPeriodBlocks = protocol.ArbitrationRoundBlocks
		if ch.Status == pb.ChallengeStatus_CHALLENGE_FUNDING {
			ch.ArbitrationPeriodBlocks = configuredArbitrationPeriodBlocks(cfg)
		}
	}
	if len(ch.OrdinaryCommitteeSizes) == 0 {
		if err := ValidateOrdinaryArbitrationPolicy(cfg.OrdinaryCommitteeSizes, cfg.OrdinaryThresholdBps); err != nil {
			return err
		}
		ch.OrdinaryCommitteeSizes = append([]uint32(nil), cfg.OrdinaryCommitteeSizes...)
		ch.OrdinaryThresholdBps = cfg.OrdinaryThresholdBps
	}
	if ch.ArbitrationRound == 0 {
		ch.ArbitrationRound = 1
	}
	if len(ch.OriginalVotes) == 0 {
		ch.OriginalVotes = OriginalPositions(req)
	}
	return ValidateOrdinaryArbitrationPolicy(ch.OrdinaryCommitteeSizes, ch.OrdinaryThresholdBps)
}

func configuredArbitrationPeriodBlocks(cfg *Config) uint64 {
	if cfg == nil || cfg.ArbitrationPeriodBlocks == 0 {
		return protocol.ArbitrationRoundBlocks
	}
	return cfg.ArbitrationPeriodBlocks
}

                                                                                               
func ArbitrationRoundPeriod(ch *pb.ChallengeRecord) uint64 {
	if ch == nil || ch.ArbitrationPeriodBlocks == 0 {
		return protocol.ArbitrationRoundBlocks
	}
	return ch.ArbitrationPeriodBlocks
}

func ArbitrationQuorum(ch *pb.ChallengeRecord) (uint32, error) {
	if ch == nil {
		return 0, fmt.Errorf("nil challenge")
	}
	if ch.ArbitrationStage == pb.ArbitrationStage_ARBITRATION_FINAL_COURT {
		if len(ch.FinalCourt) != 4 {
			return 0, fmt.Errorf("final court must have four frozen members")
		}
		return 3, nil
	}
	if ch.ArbitrationStage != pb.ArbitrationStage_ARBITRATION_ORDINARY || ch.ArbitrationRound < 1 || ch.ArbitrationRound > 3 {
		return 0, fmt.Errorf("invalid arbitration stage or round")
	}
	if err := ValidateOrdinaryArbitrationPolicy(ch.OrdinaryCommitteeSizes, ch.OrdinaryThresholdBps); err != nil {
		return 0, err
	}
	                                                                             
	n := uint64(ch.OrdinaryCommitteeSizes[ch.ArbitrationRound-1])
	return uint32((n*uint64(ch.OrdinaryThresholdBps) + 9999) / 10000), nil
}

func StartArbitrationRound(in *pb.ChallengeRecord, members, court []string, height, period uint64) (*pb.ChallengeRecord, error) {
	if in == nil || in.Finalized || period == 0 || in.BallotRound == math.MaxUint64 {
		return nil, fmt.Errorf("invalid arbitration round start")
	}
	                                                                                                               
	period = ArbitrationRoundPeriod(in)
	if height >= math.MaxUint64-period {
		return nil, fmt.Errorf("invalid arbitration round start")
	}
	ch := proto.Clone(in).(*pb.ChallengeRecord)
	ch.ArbitrationPeriodBlocks = period
	if ch.ArbitrationStage == pb.ArbitrationStage_ARBITRATION_FINAL_COURT {
		members = ch.FinalCourt
		ch.OrdinaryVotesFrozen = true
	} else if ch.ArbitrationRound == 3 && len(ch.FinalCourt) == 0 {
		if len(court) != 4 {
			return nil, fmt.Errorf("four eligible final court members required")
		}
		ch.FinalCourt = append([]string(nil), court...)
		ch.CourtSnapshotHeight = height
	}
	seen := make(map[string]bool)
	for _, address := range members {
		if address == "" || seen[address] {
			return nil, fmt.Errorf("invalid duplicate committee member")
		}
		seen[address] = true
	}
	if _, err := ArbitrationQuorum(ch); err != nil {
		return nil, err
	}
	if ch.ArbitrationStage == pb.ArbitrationStage_ARBITRATION_ORDINARY && uint32(len(members)) > ch.OrdinaryCommitteeSizes[ch.ArbitrationRound-1] {
		return nil, fmt.Errorf("ordinary committee exceeds planned size")
	}
	if len(ch.FinalCourt) > 0 {
		courtSeen := make(map[string]bool)
		for _, address := range ch.FinalCourt {
			if address == "" || courtSeen[address] {
				return nil, fmt.Errorf("invalid frozen court")
			}
			courtSeen[address] = true
		}
		if len(courtSeen) != 4 {
			return nil, fmt.Errorf("invalid frozen court size")
		}
	}
	ch.Arbitrators = append([]string(nil), members...)
	ch.BallotRound++
	ch.ArbitrationVotes = nil
	ch.PassCount, ch.FailCount = 0, 0
	ch.Status = pb.ChallengeStatus_CHALLENGE_ACTIVE
	ch.DeadlineHeight = height + period
	return ch, nil
}

                                                                             
                                                                      
func SelectFinalCourt(infos []*pb.WitnessInfo, minimum string) ([]string, error) {
	min, ok := new(big.Int).SetString(minimum, 10)
	if !ok || min.Sign() <= 0 {
		return nil, fmt.Errorf("invalid registration minimum")
	}
	type candidate struct {
		address string
		stake   *big.Int
	}
	candidates := make([]candidate, 0, len(infos))
	seen := make(map[string]bool)
	for _, info := range infos {
		if info == nil || info.Address == "" || info.Status != pb.WitnessStatus_WITNESS_ACTIVE || seen[info.Address] {
			continue
		}
		stake, ok := new(big.Int).SetString(info.StakeAmount, 10)
		if !ok || stake.Cmp(min) < 0 {
			continue
		}
		seen[info.Address] = true
		candidates = append(candidates, candidate{info.Address, stake})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if cmp := candidates[i].stake.Cmp(candidates[j].stake); cmp != 0 {
			return cmp > 0
		}
		return candidates[i].address < candidates[j].address
	})
	if len(candidates) < 4 {
		return nil, ErrInsufficientFinalCourt
	}
	return []string{candidates[0].address, candidates[1].address, candidates[2].address, candidates[3].address}, nil
}

                                                                                
                                                                   
func ComputeArbitrationBallotOutcome(ch *pb.ChallengeRecord, req *pb.RechargeRequest, vote *pb.WitnessVote, ballotRound, height uint64) (*ArbitrationVoteOutcome, error) {
	if ch == nil || req == nil || vote == nil || ch.RequestId != req.RequestId || vote.RequestId != req.RequestId {
		return nil, fmt.Errorf("arbitration request mismatch")
	}
	if !IsValidVoteType(vote.VoteType) {
		return nil, ErrInvalidVote
	}
	if ch.Finalized || ch.Settled || ch.Status != pb.ChallengeStatus_CHALLENGE_ACTIVE || terminalRechargeStatus(req.Status) {
		return nil, fmt.Errorf("arbitration is not active")
	}
	if ballotRound == 0 || ballotRound != ch.BallotRound || (vote.BallotRound != 0 && vote.BallotRound != ballotRound) {
		return nil, fmt.Errorf("arbitration ballot round mismatch")
	}
	if height > ch.DeadlineHeight {
		return nil, ErrArbitrationExpired
	}
	members := make(map[string]bool, len(ch.Arbitrators))
	for _, member := range ch.Arbitrators {
		if member == "" || members[member] {
			return nil, fmt.Errorf("invalid persisted committee")
		}
		members[member] = true
	}
	if ch.ArbitrationStage == pb.ArbitrationStage_ARBITRATION_FINAL_COURT {
		if len(members) != 4 || !ch.OrdinaryVotesFrozen {
			return nil, fmt.Errorf("invalid final court state")
		}
		court := make(map[string]bool)
		for _, member := range ch.FinalCourt {
			if !members[member] || court[member] {
				return nil, fmt.Errorf("current committee differs from frozen court")
			}
			court[member] = true
		}
	} else if ch.ArbitrationRound >= 1 && ch.ArbitrationRound <= 3 && len(ch.OrdinaryCommitteeSizes) == 3 && uint32(len(members)) > ch.OrdinaryCommitteeSizes[ch.ArbitrationRound-1] {
		return nil, fmt.Errorf("persisted ordinary committee exceeds planned size")
	}
	if !members[vote.WitnessAddress] {
		return nil, ErrNotArbitrator
	}
	for _, old := range ch.ArbitrationVotes {
		if old != nil && old.WitnessAddress == vote.WitnessAddress {
			return nil, ErrDuplicateVote
		}
	}
	quorum, err := ArbitrationQuorum(ch)
	if err != nil {
		return nil, err
	}
	updated := proto.Clone(ch).(*pb.ChallengeRecord)
	accepted := proto.Clone(vote).(*pb.WitnessVote)
	accepted.BallotRound = ballotRound
	updated.ArbitrationVotes = append(updated.ArbitrationVotes, accepted)
	if updated.ArbitrationStage == pb.ArbitrationStage_ARBITRATION_ORDINARY {
		if updated.OrdinaryVotesFrozen {
			return nil, fmt.Errorf("ordinary positions already frozen")
		}
		updated.OrdinaryVotes = LastDirectionalPositions(updated.OrdinaryVotes, accepted)
	}
	updated.PassCount, updated.FailCount = 0, 0
	seen := make(map[string]bool)
	for _, item := range updated.ArbitrationVotes {
		if item == nil || item.BallotRound != ballotRound || seen[item.WitnessAddress] || !members[item.WitnessAddress] || item.RequestId != req.RequestId || !IsValidVoteType(item.VoteType) {
			return nil, fmt.Errorf("invalid persisted arbitration votes")
		}
		seen[item.WitnessAddress] = true
		if item.VoteType == pb.WitnessVoteType_VOTE_PASS {
			updated.PassCount++
		}
		if item.VoteType == pb.WitnessVoteType_VOTE_FAIL {
			updated.FailCount++
		}
	}
	outcome := &ArbitrationVoteOutcome{Challenge: updated}
	if updated.PassCount < quorum && updated.FailCount < quorum {
		return outcome, nil
	}
	updated.FinalVote = pb.WitnessVoteType_VOTE_PASS
	if updated.FailCount >= quorum {
		updated.FinalVote = pb.WitnessVoteType_VOTE_FAIL
	}
	updated.Finalized = true
	updated.Status = pb.ChallengeStatus_CHALLENGE_FINALIZED
	updated.FinalizeHeight = height
	updated.OrdinaryVotesFrozen = true
	updated.ChallengeSuccess = (updated.FinalVote == pb.WitnessVoteType_VOTE_PASS) != (req.PassCount > req.FailCount)
	result := proto.Clone(req).(*pb.RechargeRequest)
	result.FinalizeHeight = height
	result.Status = pb.RechargeRequestStatus_RECHARGE_REJECTED
	if updated.FinalVote == pb.WitnessVoteType_VOTE_PASS {
		result.Status = pb.RechargeRequestStatus_RECHARGE_FINALIZED
	}
	outcome.Request = result
	return outcome, nil
}
