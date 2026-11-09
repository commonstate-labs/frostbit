package witness

import (
	"dex/pb"

	"google.golang.org/protobuf/proto"
)

                                                                              
                                                                               
                                                                               
                                                                             
                                                                      
func (s *Service) CloneForSimulation() *Service {
	if s == nil {
		return nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	cfg := DefaultConfig()
	if s.config != nil {
		copied := *s.config
		cfg = &copied
	}
	cloned := NewService(cfg)
	cloned.currentHeight = s.currentHeight

	cloned.requests = make(map[string]*pb.RechargeRequest, len(s.requests))
	for id, request := range s.requests {
		if request != nil {
			cloned.requests[id] = proto.Clone(request).(*pb.RechargeRequest)
		}
	}
	cloned.votingDue = cloneHeightStringSet(s.votingDue)
	cloned.votingDeadline = make(map[string]uint64, len(s.votingDeadline))
	for id, height := range s.votingDeadline {
		cloned.votingDeadline[id] = height
	}
	cloned.shelvedRequests = cloneStringSet(s.shelvedRequests)

	s.stakeManager.mu.RLock()
	cloned.stakeManager.witnesses = make(map[string]*pb.WitnessInfo, len(s.stakeManager.witnesses))
	for address, info := range s.stakeManager.witnesses {
		if info != nil {
			cloned.stakeManager.witnesses[address] = proto.Clone(info).(*pb.WitnessInfo)
		}
	}
	s.stakeManager.mu.RUnlock()

	s.voteManager.mu.RLock()
	cloned.voteManager.votes = make(map[string]map[string]*pb.WitnessVote, len(s.voteManager.votes))
	for requestID, votes := range s.voteManager.votes {
		copiedVotes := make(map[string]*pb.WitnessVote, len(votes))
		for address, vote := range votes {
			if vote != nil {
				copiedVotes[address] = proto.Clone(vote).(*pb.WitnessVote)
			}
		}
		cloned.voteManager.votes[requestID] = copiedVotes
	}
	cloned.voteManager.selectedWitnesses = make(map[string]map[string]bool, len(s.voteManager.selectedWitnesses))
	for requestID, selected := range s.voteManager.selectedWitnesses {
		copiedSelected := make(map[string]bool, len(selected))
		for address, value := range selected {
			copiedSelected[address] = value
		}
		cloned.voteManager.selectedWitnesses[requestID] = copiedSelected
	}
	s.voteManager.mu.RUnlock()

	s.challengeManager.mu.RLock()
	cloned.challengeManager.challenges = make(map[string]*pb.ChallengeRecord, len(s.challengeManager.challenges))
	for id, challenge := range s.challengeManager.challenges {
		if challenge != nil {
			cloned.challengeManager.challenges[id] = proto.Clone(challenge).(*pb.ChallengeRecord)
		}
	}
	cloned.challengeManager.activeByDeadline = cloneHeightStringSet(s.challengeManager.activeByDeadline)
	cloned.challengeManager.activeDeadline = make(map[string]uint64, len(s.challengeManager.activeDeadline))
	for id, height := range s.challengeManager.activeDeadline {
		cloned.challengeManager.activeDeadline[id] = height
	}
	cloned.challengeManager.requestChallenges = make(map[string]string, len(s.challengeManager.requestChallenges))
	for requestID, challengeID := range s.challengeManager.requestChallenges {
		cloned.challengeManager.requestChallenges[requestID] = challengeID
	}
	cloned.challengeManager.arbitrationVotes = make(map[string]map[string]*pb.WitnessVote, len(s.challengeManager.arbitrationVotes))
	for challengeID, votes := range s.challengeManager.arbitrationVotes {
		copiedVotes := make(map[string]*pb.WitnessVote, len(votes))
		for address, vote := range votes {
			if vote != nil {
				copiedVotes[address] = proto.Clone(vote).(*pb.WitnessVote)
			}
		}
		cloned.challengeManager.arbitrationVotes[challengeID] = copiedVotes
	}
	s.challengeManager.mu.RUnlock()

	return cloned
}

func cloneHeightStringSet(source map[uint64]map[string]struct{}) map[uint64]map[string]struct{} {
	cloned := make(map[uint64]map[string]struct{}, len(source))
	for height, values := range source {
		cloned[height] = cloneStringSet(values)
	}
	return cloned
}

func cloneStringSet(source map[string]struct{}) map[string]struct{} {
	cloned := make(map[string]struct{}, len(source))
	for value := range source {
		cloned[value] = struct{}{}
	}
	return cloned
}
