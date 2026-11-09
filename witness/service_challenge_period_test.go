package witness

import (
	"testing"

	"dex/pb"
)

func TestCheckChallengePeriod_SkipsActivatedChallenge(t *testing.T) {
	svc := NewService(DefaultConfig())
	defer svc.Stop()

	svc.LoadRequest(&pb.RechargeRequest{
		RequestId:      "req_challenge_period_guard",
		Status:         pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD,
		DeadlineHeight: 42,
		ChallengeId:    "ch_challenge_period_guard",
	})
	svc.LoadChallenge(&pb.ChallengeRecord{
		ChallengeId:     "ch_challenge_period_guard",
		RequestId:       "req_challenge_period_guard",
		Status:          pb.ChallengeStatus_CHALLENGE_PENDING_SELECTION,
		SelectionHeight: 26,
	})

	svc.SetCurrentHeight(43)
	svc.checkChallengePeriod()

	req, err := svc.GetRequest("req_challenge_period_guard")
	if err != nil {
		t.Fatal(err)
	}
	if req.Status != pb.RechargeRequestStatus_RECHARGE_CHALLENGED {
		t.Fatalf("expected request upgraded to CHALLENGED, got %v", req.Status)
	}
	if req.FinalizeHeight != 0 {
		t.Fatalf("expected request not finalized, got FinalizeHeight=%d", req.FinalizeHeight)
	}

	select {
	case ev := <-svc.Events():
		if ev != nil && ev.Type == EventRechargeFinalized {
			t.Fatalf("unexpected finalized event for activated challenge: %+v", ev)
		}
	default:
	}
}

func TestCheckChallengePeriodDoesNotMintMemoryRewards(t *testing.T) {
	svc := NewService(DefaultConfig())
	defer svc.Stop()
	req := &pb.RechargeRequest{RequestId: "fee-from-escrow-only", Status: pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD, DeadlineHeight: 42, RechargeFee: "999999999999999", SelectedWitnesses: []string{"original"}, Votes: []*pb.WitnessVote{{RequestId: "fee-from-escrow-only", WitnessAddress: "original", VoteType: pb.WitnessVoteType_VOTE_PASS}}}
	svc.LoadRequest(req)
	svc.LoadWitness(&pb.WitnessInfo{Address: "original", StakeAmount: "3200000000", PendingReward: "0", Status: pb.WitnessStatus_WITNESS_ACTIVE})
	svc.SetCurrentHeight(43)
	svc.checkChallengePeriod()
	svc.checkChallengePeriod()
	info, err := svc.GetWitnessInfo("original")
	if err != nil || info.PendingReward != "0" || info.StakeAmount != "3200000000" {
		t.Fatalf("runtime public-period reminder created reward: %+v %v", info, err)
	}
}
