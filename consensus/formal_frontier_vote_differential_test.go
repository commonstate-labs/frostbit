package consensus

import (
	"dex/formal/itftrace"
	"dex/formal/refinement"
	"dex/types"
	"os"
	"reflect"
	"testing"
)

func TestFormalFrontierVoteITFDifferential(t *testing.T) {
	cases := []struct {
		name string
		env  string
	}{
		{name: "quorum", env: "DEX_FORMAL_ITF_FRONTIER_QUORUM"},
		{name: "split-no-quorum", env: "DEX_FORMAL_ITF_FRONTIER_SPLIT"},
		{name: "anchor-conflict", env: "DEX_FORMAL_ITF_FRONTIER_CONFLICT"},
	}
	if os.Getenv(cases[0].env) == "" {
		t.Skip("frontier ITF paths are set by formal/run-go-differential.ps1")
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runFormalFrontierTrace(t, os.Getenv(tc.env))
		})
	}
}

func runFormalFrontierTrace(t *testing.T, path string) {
	t.Helper()
	trace, err := itftrace.Load(path)
	if err != nil {
		t.Fatalf("load Quint frontier ITF trace: %v", err)
	}
	if len(trace.States) < 2 {
		t.Fatalf("frontier differential trace states=%d want>=2", len(trace.States))
	}

	query := formalFrontierTransitionQuery()
	dispatched := formalFrontierPeers("peer-1", "peer-2", "peer-3", "peer-4")
	responses := make(map[types.NodeID]AcceptedFrontierVote)
	modelPeers := map[string]types.NodeID{
		"P1": "peer-1",
		"P2": "peer-2",
		"P3": "peer-3",
		"P4": "peer-4",
	}
	reversePeers := map[types.NodeID]string{
		"peer-1": "P1",
		"peer-2": "P2",
		"peer-3": "P3",
		"peer-4": "P4",
	}
	decision := frontierVotePending
	targetID := ""
	assertFormalFrontierStateRelation(t, trace.States[0], responses, decision, targetID, query, reversePeers, 0)

	for i := 1; i < len(trace.States); i++ {
		before, err := refinement.FrontierFromITF(trace.States[i-1])
		if err != nil {
			t.Fatal(err)
		}
		after, err := refinement.FrontierFromITF(trace.States[i])
		if err != nil {
			t.Fatal(err)
		}
		added := oneAddedFormalFrontierPeer(t, before.Responded, after.Responded)
		peer, ok := modelPeers[added]
		if !ok {
			t.Fatalf("unknown model peer %q", added)
		}
		vote := formalFrontierVoteFromModel(peer, added, after, query)

		result := applyVerifiedFrontierVote(frontierVoteTransitionInput{
			query:        query,
			alpha:        3,
			roundStarted: true,
			dispatched:   dispatched,
			responses:    responses,
			vote:         vote,
		})
		if !result.accepted {
			t.Fatalf("pure frontier transition rejected model peer %s", added)
		}
		responses = result.responses
		decision = result.decision
		targetID = result.targetID
		assertFormalFrontierStateRelation(t, trace.States[i], responses, decision, targetID, query, reversePeers, i)
	}
}

func formalFrontierVoteFromModel(peer types.NodeID, modelPeer string, state refinement.FrontierState, query AcceptedFrontierQuery) AcceptedFrontierVote {
	vote := AcceptedFrontierVote{
		Peer:             peer,
		AcceptedHeight:   query.TargetHeight,
		ObservedAnchorID: query.AnchorBlockID,
		TargetHeight:     query.TargetHeight,
	}
	switch {
	case containsFormalString(state.SupportA, modelPeer):
		vote.Status = AcceptedFrontierStatusOK
		vote.TargetBlockID = "block-a"
	case containsFormalString(state.SupportB, modelPeer):
		vote.Status = AcceptedFrontierStatusOK
		vote.TargetBlockID = "block-b"
	case containsFormalString(state.AnchorMismatches, modelPeer):
		vote.Status = AcceptedFrontierStatusAnchorMismatch
		vote.ObservedAnchorID = "conflict-" + modelPeer
	default:
		vote.Status = AcceptedFrontierStatusUnavailable
	}
	return vote
}

func assertFormalFrontierStateRelation(
	t *testing.T,
	model itftrace.State,
	responses map[types.NodeID]AcceptedFrontierVote,
	decision frontierVoteDecisionKind,
	targetID string,
	query AcceptedFrontierQuery,
	reversePeers map[types.NodeID]string,
	step int,
) {
	t.Helper()
	expected, err := refinement.FrontierFromITF(model)
	if err != nil {
		t.Fatal(err)
	}
	actual := formalFrontierStateFromResponses(responses, decision, targetID, query, reversePeers)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("frontier state relation failed at step=%d action=%s\nactual:   %+v\nexpected: %+v",
			step, model.Action, actual, expected)
	}
}

func formalFrontierStateFromResponses(
	responses map[types.NodeID]AcceptedFrontierVote,
	decision frontierVoteDecisionKind,
	targetID string,
	query AcceptedFrontierQuery,
	reversePeers map[types.NodeID]string,
) refinement.FrontierState {
	responded := make(map[string]struct{})
	supportA := make(map[string]struct{})
	supportB := make(map[string]struct{})
	mismatches := make(map[string]struct{})
	for peer, response := range responses {
		modelPeer := reversePeers[peer]
		responded[modelPeer] = struct{}{}
		if response.Status == AcceptedFrontierStatusOK && response.AcceptedHeight >= query.TargetHeight &&
			response.TargetHeight == query.TargetHeight && response.ObservedAnchorID == query.AnchorBlockID {
			switch response.TargetBlockID {
			case "block-a":
				supportA[modelPeer] = struct{}{}
			case "block-b":
				supportB[modelPeer] = struct{}{}
			}
		}
		if response.Status == AcceptedFrontierStatusAnchorMismatch && response.ObservedAnchorID != "" &&
			response.ObservedAnchorID != query.AnchorBlockID {
			mismatches[modelPeer] = struct{}{}
		}
	}

	phase := "Polling"
	selectedTarget := "NoTarget"
	switch decision {
	case frontierVoteConflict:
		phase = "SafetyHalt"
	case frontierVoteQuorum:
		phase = "CatchUp"
		if targetID == "block-a" {
			selectedTarget = "A"
		} else if targetID == "block-b" {
			selectedTarget = "B"
		}
	case frontierVoteNoQuorum:
		phase = "Closed"
	}
	return refinement.FrontierState{
		Responded:        refinement.SortedStrings(responded),
		SupportA:         refinement.SortedStrings(supportA),
		SupportB:         refinement.SortedStrings(supportB),
		AnchorMismatches: refinement.SortedStrings(mismatches),
		Phase:            phase,
		SelectedTarget:   selectedTarget,
	}
}

func oneAddedFormalFrontierPeer(t *testing.T, before, after []string) string {
	t.Helper()
	beforeSet := make(map[string]struct{}, len(before))
	for _, peer := range before {
		beforeSet[peer] = struct{}{}
	}
	var added string
	for _, peer := range after {
		if _, exists := beforeSet[peer]; exists {
			continue
		}
		if added != "" {
			t.Fatalf("model transition added multiple peers: %s and %s", added, peer)
		}
		added = peer
	}
	if added == "" || len(after) != len(before)+1 {
		t.Fatalf("model transition did not add exactly one response: before=%v after=%v", before, after)
	}
	return added
}

func containsFormalString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
