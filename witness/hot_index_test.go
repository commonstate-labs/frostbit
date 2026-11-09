package witness

import (
	"testing"

	"dex/pb"
)

func TestServiceHotIndexesTrackRequestState(t *testing.T) {
	svc := NewService(DefaultConfig())

	svc.LoadRequest(&pb.RechargeRequest{
		RequestId:         "req_hot_index",
		Status:            pb.RechargeRequestStatus_RECHARGE_VOTING,
		DeadlineHeight:    10,
		SelectedWitnesses: []string{"w1"},
	})

	svc.mu.Lock()
	if got := svc.dueVotingRequestIDsLocked(9); len(got) != 0 {
		t.Fatalf("expected no due voting request before deadline, got %v", got)
	}
	if got := svc.dueVotingRequestIDsLocked(10); len(got) != 1 || got[0] != "req_hot_index" {
		t.Fatalf("expected due voting request, got %v", got)
	}
	svc.mu.Unlock()

	svc.LoadRequest(&pb.RechargeRequest{
		RequestId:    "req_hot_index",
		Status:       pb.RechargeRequestStatus_RECHARGE_SHELVED,
		ShelveHeight: 11,
	})

	svc.mu.Lock()
	if got := svc.dueVotingRequestIDsLocked(100); len(got) != 0 {
		t.Fatalf("expected voting index cleared after shelve, got %v", got)
	}
	if got := svc.shelvedRequestIDsLocked(); len(got) != 1 || got[0] != "req_hot_index" {
		t.Fatalf("expected shelved request index, got %v", got)
	}
	svc.mu.Unlock()

	svc.LoadRequest(&pb.RechargeRequest{
		RequestId: "req_hot_index",
		Status:    pb.RechargeRequestStatus_RECHARGE_REJECTED,
	})

	if _, err := svc.GetRequest("req_hot_index"); err == nil {
		t.Fatalf("expected terminal request to be pruned from active memory")
	}
	svc.mu.Lock()
	if got := svc.dueVotingRequestIDsLocked(100); len(got) != 0 {
		t.Fatalf("expected voting index empty after terminal prune, got %v", got)
	}
	if got := svc.shelvedRequestIDsLocked(); len(got) != 0 {
		t.Fatalf("expected shelved index empty after terminal prune, got %v", got)
	}
	svc.mu.Unlock()
}

func TestChallengeManagerHotIndexesTrackChallengeState(t *testing.T) {
	cfg := DefaultConfig()
	cm := NewChallengeManager(cfg)
	const challengeID = "challenge_hot_index"

	if _, err := cm.CreateChallenge(challengeID, "req_hot_index", "challenger", "100", "test", 100, []string{"a1", "a2", "a3"}); err != nil {
		t.Fatalf("CreateChallenge: %v", err)
	}
	if got := cm.GetDueActiveChallenges(99 + cfg.ArbitrationPeriodBlocks); len(got) != 0 {
		t.Fatalf("expected no due active challenge before deadline, got %d", len(got))
	}
	if got := cm.GetDueActiveChallenges(100 + cfg.ArbitrationPeriodBlocks); len(got) != 0 {
		t.Fatalf("deadline remains open for votes, got %v", got)
	}
	if got := cm.GetDueActiveChallenges(101 + cfg.ArbitrationPeriodBlocks); len(got) != 1 || got[0].ChallengeId != challengeID {
		t.Fatalf("expected due active challenge, got %v", got)
	}

	expanded := &pb.ChallengeRecord{
		ChallengeId:            challengeID,
		RequestId:              "req_hot_index",
		Status:                 pb.ChallengeStatus_CHALLENGE_ACTIVE,
		ArbitrationRound:       1,
		Arbitrators:            []string{"b1", "b2", "b3"},
		DeadlineHeight:         200 + cfg.ArbitrationPeriodBlocks,
		BallotRound:            2,
		OrdinaryCommitteeSizes: []uint32{4, 8, 16},
		OrdinaryThresholdBps:   7500,
	}
	cm.LoadChallenge(expanded)
	if got := cm.GetDueActiveChallenges(100 + cfg.ArbitrationPeriodBlocks); len(got) != 0 {
		t.Fatalf("expected old active deadline cleared after reload, got %d", len(got))
	}
	if got := cm.GetDueActiveChallenges(201 + cfg.ArbitrationPeriodBlocks); len(got) != 1 || got[0].ChallengeId != challengeID {
		t.Fatalf("expected reloaded active challenge in due index, got %v", got)
	}

	for _, address := range expanded.Arbitrators {
		if err := cm.AddArbitrationVoteAtHeight(challengeID, &pb.WitnessVote{RequestId: expanded.RequestId, WitnessAddress: address, VoteType: pb.WitnessVoteType_VOTE_PASS, BallotRound: expanded.BallotRound}, 220); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := cm.FinalizeChallenge(challengeID, true, 230); err != nil {
		t.Fatalf("FinalizeChallenge: %v", err)
	}
	if got := cm.GetActiveChallenges(); len(got) != 0 {
		t.Fatalf("expected finalized challenge removed from hot active indexes, got %d", len(got))
	}
}
