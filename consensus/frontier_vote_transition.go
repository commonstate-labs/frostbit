package consensus

import (
	"dex/types"
	"fmt"
	"sort"
)

type frontierVoteDecisionKind uint8

const (
	frontierVotePending frontierVoteDecisionKind = iota
	frontierVoteConflict
	frontierVoteQuorum
	frontierVoteNoQuorum
)

                                                                              
                                                                            
                                            
type frontierVoteTransitionInput struct {
	query        AcceptedFrontierQuery
	alpha        int
	roundStarted bool
	dispatched   map[types.NodeID]struct{}
	responses    map[types.NodeID]AcceptedFrontierVote
	vote         AcceptedFrontierVote
}

                                                                        
                                                                             
                                                                               
type frontierVoteTransitionResult struct {
	accepted       bool
	responses      map[types.NodeID]AcceptedFrontierVote
	decision       frontierVoteDecisionKind
	targetID       string
	supporters     []types.NodeID
	conflictTotal  int
	conflictReport []string
}

                                                                          
                                  
  
                 
                                                                        
                                                                      
  
                  
                                 
                                                           
                                                                          
func applyVerifiedFrontierVote(input frontierVoteTransitionInput) frontierVoteTransitionResult {
	result := frontierVoteTransitionResult{
		responses: cloneFrontierResponses(input.responses),
		decision:  frontierVotePending,
	}
	if input.alpha <= 0 || input.vote.Peer == "" || input.vote.TargetHeight != input.query.TargetHeight {
		return result
	}
	if _, ok := input.dispatched[input.vote.Peer]; !ok {
		return result
	}
	if _, duplicate := result.responses[input.vote.Peer]; duplicate {
		return result
	}
	result.accepted = true
	result.responses[input.vote.Peer] = input.vote

	supportByID := make(map[string][]types.NodeID)
	anchorConflictByID := make(map[string]int)
	for peer, response := range result.responses {
		if response.Status == AcceptedFrontierStatusOK &&
			response.AcceptedHeight >= input.query.TargetHeight &&
			response.TargetHeight == input.query.TargetHeight &&
			response.ObservedAnchorID == input.query.AnchorBlockID && response.TargetBlockID != "" {
			supportByID[response.TargetBlockID] = append(supportByID[response.TargetBlockID], peer)
			continue
		}
		if response.Status == AcceptedFrontierStatusAnchorMismatch && response.ObservedAnchorID != "" &&
			response.ObservedAnchorID != input.query.AnchorBlockID {
			anchorConflictByID[response.ObservedAnchorID]++
			result.conflictTotal++
		}
	}

	if result.conflictTotal >= input.alpha {
		result.decision = frontierVoteConflict
		observedIDs := make([]string, 0, len(anchorConflictByID))
		for observedID := range anchorConflictByID {
			observedIDs = append(observedIDs, observedID)
		}
		sort.Strings(observedIDs)
		result.conflictReport = make([]string, 0, len(observedIDs))
		for _, observedID := range observedIDs {
			result.conflictReport = append(result.conflictReport,
				fmt.Sprintf("%s:%d", observedID, anchorConflictByID[observedID]))
		}
		return result
	}

	targetIDs := make([]string, 0, len(supportByID))
	for targetID := range supportByID {
		targetIDs = append(targetIDs, targetID)
	}
	sort.Strings(targetIDs)
	for _, targetID := range targetIDs {
		peers := supportByID[targetID]
		if len(peers) < input.alpha {
			continue
		}
		sort.Slice(peers, func(i, j int) bool { return peers[i] < peers[j] })
		result.decision = frontierVoteQuorum
		result.targetID = targetID
		result.supporters = append([]types.NodeID(nil), peers...)
		return result
	}

	if input.roundStarted && len(input.dispatched) > 0 && len(result.responses) >= len(input.dispatched) {
		result.decision = frontierVoteNoQuorum
	}
	return result
}

func cloneFrontierResponses(src map[types.NodeID]AcceptedFrontierVote) map[types.NodeID]AcceptedFrontierVote {
	dst := make(map[types.NodeID]AcceptedFrontierVote, len(src)+1)
	for peer, response := range src {
		dst[peer] = response
	}
	return dst
}
