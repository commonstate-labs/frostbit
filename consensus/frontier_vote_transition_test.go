package consensus

import (
	"dex/types"
	"reflect"
	"testing"
)

func TestVerifiedFrontierVoteTransitionReachesDistinctPeerQuorum(t *testing.T) {
	query := formalFrontierTransitionQuery()
	dispatched := formalFrontierPeers("peer-3", "peer-1", "peer-2")
	responses := make(map[types.NodeID]AcceptedFrontierVote)

	for i, peer := range []types.NodeID{"peer-1", "peer-2", "peer-3"} {
		before := cloneFrontierResponses(responses)
		result := applyVerifiedFrontierVote(frontierVoteTransitionInput{
			query:        query,
			alpha:        3,
			roundStarted: true,
			dispatched:   dispatched,
			responses:    responses,
			vote:         formalFrontierOKVote(peer, query, "block-2"),
		})
		if !reflect.DeepEqual(responses, before) {
			t.Fatal("pure transition mutated the input response map")
		}
		if !result.accepted {
			t.Fatalf("vote %d was not accepted", i+1)
		}
		responses = result.responses
		if i < 2 && result.decision != frontierVotePending {
			t.Fatalf("vote %d decision=%d want pending", i+1, result.decision)
		}
		if i == 2 {
			if result.decision != frontierVoteQuorum || result.targetID != "block-2" {
				t.Fatalf("decision=%d target=%q want quorum block-2", result.decision, result.targetID)
			}
			wantSupporters := []types.NodeID{"peer-1", "peer-2", "peer-3"}
			if !reflect.DeepEqual(result.supporters, wantSupporters) {
				t.Fatalf("supporters=%v want=%v", result.supporters, wantSupporters)
			}
		}
	}
}

func TestVerifiedFrontierVoteTransitionRejectsDuplicateAndUnsampledPeer(t *testing.T) {
	query := formalFrontierTransitionQuery()
	dispatched := formalFrontierPeers("peer-1", "peer-2", "peer-3")
	existing := map[types.NodeID]AcceptedFrontierVote{
		"peer-1": formalFrontierOKVote("peer-1", query, "block-2"),
	}

	for _, vote := range []AcceptedFrontierVote{
		formalFrontierOKVote("peer-1", query, "block-2"),
		formalFrontierOKVote("peer-4", query, "block-2"),
	} {
		result := applyVerifiedFrontierVote(frontierVoteTransitionInput{
			query:        query,
			alpha:        3,
			roundStarted: true,
			dispatched:   dispatched,
			responses:    existing,
			vote:         vote,
		})
		if result.accepted || result.decision != frontierVotePending || !reflect.DeepEqual(result.responses, existing) {
			t.Fatalf("unexpected result for peer %s: %+v", vote.Peer, result)
		}
	}
}

func TestVerifiedFrontierVoteTransitionSplitTargetsHaveNoQuorum(t *testing.T) {
	query := formalFrontierTransitionQuery()
	peers := []types.NodeID{"peer-1", "peer-2", "peer-3", "peer-4"}
	dispatched := formalFrontierPeers(peers...)
	responses := make(map[types.NodeID]AcceptedFrontierVote)
	var result frontierVoteTransitionResult
	for i, peer := range peers {
		target := "block-a"
		if i >= 2 {
			target = "block-b"
		}
		result = applyVerifiedFrontierVote(frontierVoteTransitionInput{
			query:        query,
			alpha:        3,
			roundStarted: true,
			dispatched:   dispatched,
			responses:    responses,
			vote:         formalFrontierOKVote(peer, query, target),
		})
		responses = result.responses
	}
	if result.decision != frontierVoteNoQuorum || result.targetID != "" || len(result.supporters) != 0 {
		t.Fatalf("split vote result=%+v", result)
	}
}

func TestVerifiedFrontierVoteTransitionAggregatesAnchorConflictAcrossIDs(t *testing.T) {
	query := formalFrontierTransitionQuery()
	peers := []types.NodeID{"peer-1", "peer-2", "peer-3"}
	dispatched := formalFrontierPeers(peers...)
	responses := make(map[types.NodeID]AcceptedFrontierVote)
	observed := []string{"branch-b", "branch-a", "branch-b"}
	var result frontierVoteTransitionResult
	for i, peer := range peers {
		result = applyVerifiedFrontierVote(frontierVoteTransitionInput{
			query:        query,
			alpha:        3,
			roundStarted: true,
			dispatched:   dispatched,
			responses:    responses,
			vote: AcceptedFrontierVote{
				Peer:             peer,
				Status:           AcceptedFrontierStatusAnchorMismatch,
				ObservedAnchorID: observed[i],
				TargetHeight:     query.TargetHeight,
			},
		})
		responses = result.responses
	}
	wantReport := []string{"branch-a:1", "branch-b:2"}
	if result.decision != frontierVoteConflict || result.conflictTotal != 3 ||
		!reflect.DeepEqual(result.conflictReport, wantReport) {
		t.Fatalf("conflict result=%+v wantReport=%v", result, wantReport)
	}
}

func formalFrontierTransitionQuery() AcceptedFrontierQuery {
	return AcceptedFrontierQuery{
		AnchorHeight:  1,
		AnchorBlockID: "block-1",
		TargetHeight:  2,
	}
}

func formalFrontierOKVote(peer types.NodeID, query AcceptedFrontierQuery, targetID string) AcceptedFrontierVote {
	return AcceptedFrontierVote{
		Peer:             peer,
		Status:           AcceptedFrontierStatusOK,
		AcceptedHeight:   query.TargetHeight,
		ObservedAnchorID: query.AnchorBlockID,
		TargetHeight:     query.TargetHeight,
		TargetBlockID:    targetID,
	}
}

func formalFrontierPeers(peers ...types.NodeID) map[types.NodeID]struct{} {
	out := make(map[types.NodeID]struct{}, len(peers))
	for _, peer := range peers {
		out[peer] = struct{}{}
	}
	return out
}
