package witness_test

import (
	"reflect"
	"testing"

	"dex/pb"
	"dex/witness"
	"google.golang.org/protobuf/proto"
)

func TestComputeArbitrationVoteOutcomeIsPureAndDeterministic(t *testing.T) {
	svc := witness.NewService(witness.DefaultConfig())
	defer svc.Stop()

	req := &pb.RechargeRequest{
		RequestId: "req_pure_arbitration",
		Status:    pb.RechargeRequestStatus_RECHARGE_CHALLENGED,
	}
	ch := &pb.ChallengeRecord{
		ChallengeId:      "challenge_pure_arbitration",
		RequestId:        req.RequestId,
		Status:           pb.ChallengeStatus_CHALLENGE_ACTIVE,
		Arbitrators:      []string{"arb1", "arb2", "arb3", "arb4"},
		ArbitrationRound: 1, BallotRound: 1, DeadlineHeight: 10000,
		OrdinaryCommitteeSizes: []uint32{4, 8, 16}, OrdinaryThresholdBps: 7500,
		PassCount: 2,
		ArbitrationVotes: []*pb.WitnessVote{
			{RequestId: req.RequestId, WitnessAddress: "arb1", VoteType: pb.WitnessVoteType_VOTE_PASS, BallotRound: 1},
			{RequestId: req.RequestId, WitnessAddress: "arb2", VoteType: pb.WitnessVoteType_VOTE_PASS, BallotRound: 1},
		},
	}
	vote := &pb.WitnessVote{
		RequestId:      req.RequestId,
		WitnessAddress: "arb4",
		VoteType:       pb.WitnessVoteType_VOTE_PASS,
		TxId:           "arb_vote_4",
		BallotRound:    1,
	}

	svc.LoadRequest(req)
	svc.LoadChallenge(ch)
	first, err := svc.ComputeArbitrationVoteOutcome(ch, req, vote, nil, 120)
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.ComputeArbitrationVoteOutcome(ch, req, vote, nil, 120)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("pure arbitration outcome changed across identical calls:\nfirst=%+v\nsecond=%+v", first, second)
	}
	if first.Request == nil || first.Request.Status != pb.RechargeRequestStatus_RECHARGE_FINALIZED {
		t.Fatalf("expected decisive PASS to finalize recharge, got %+v", first.Request)
	}
	if first.Request.FinalizeHeight != 120 {
		t.Fatalf("finalize height must equal executing height: got=%d want=120", first.Request.FinalizeHeight)
	}
	if first.Challenge == nil || first.Challenge.PassCount != 3 || len(first.Challenge.ArbitrationVotes) != 3 {
		t.Fatalf("unexpected updated challenge: %+v", first.Challenge)
	}

	storedReq, err := svc.GetRequest(req.RequestId)
	if err != nil || storedReq.Status != pb.RechargeRequestStatus_RECHARGE_CHALLENGED {
		t.Fatalf("pure computation mutated service request: req=%+v err=%v", storedReq, err)
	}
	storedCh, err := svc.GetChallenge(ch.ChallengeId)
	if err != nil || storedCh.PassCount != 2 || len(storedCh.ArbitrationVotes) != 2 {
		t.Fatalf("pure computation mutated service challenge: challenge=%+v err=%v", storedCh, err)
	}
	select {
	case event := <-svc.Events():
		t.Fatalf("pure computation emitted event: %+v", event)
	default:
	}
}

func TestComputeArbitrationVoteOutcomeRejectsInactiveAndLateVotes(t *testing.T) {
	svc := witness.NewService(witness.DefaultConfig())
	defer svc.Stop()
	req := &pb.RechargeRequest{RequestId: "req_arbitration_boundary", Status: pb.RechargeRequestStatus_RECHARGE_CHALLENGED}
	baseChallenge := &pb.ChallengeRecord{
		ChallengeId:      "challenge_arbitration_boundary",
		RequestId:        req.RequestId,
		Status:           pb.ChallengeStatus_CHALLENGE_ACTIVE,
		DeadlineHeight:   100,
		Arbitrators:      []string{"arb1", "arb2", "arb3", "arb4"},
		ArbitrationRound: 1, BallotRound: 7,
		OrdinaryCommitteeSizes: []uint32{4, 8, 16}, OrdinaryThresholdBps: 7500,
	}
	vote := &pb.WitnessVote{RequestId: req.RequestId, WitnessAddress: "arb1", VoteType: pb.WitnessVoteType_VOTE_PASS, BallotRound: 7}

	inactive := proto.Clone(baseChallenge).(*pb.ChallengeRecord)
	inactive.Status = pb.ChallengeStatus_CHALLENGE_PENDING_SELECTION
	if _, err := svc.ComputeArbitrationVoteOutcome(inactive, req, vote, nil, 100); err == nil {
		t.Fatal("inactive challenge accepted an arbitration vote")
	}
	if _, err := svc.ComputeArbitrationVoteOutcome(baseChallenge, req, vote, nil, 101); err == nil {
		t.Fatal("vote after challenge deadline was accepted")
	}
	if _, err := svc.ComputeArbitrationVoteOutcome(baseChallenge, req, vote, nil, 100); err != nil {
		t.Fatalf("vote at the inclusive deadline must remain valid: %v", err)
	}
}

func TestCommittedWitnessSyncClearsTerminalPendingTasks(t *testing.T) {
	svc := witness.NewService(witness.DefaultConfig())
	defer svc.Stop()

	info := &pb.WitnessInfo{
		Address:      "witness_committed_sync",
		StakeAmount:  "1000",
		PendingTasks: []string{"req_committed_sync", "challenge_committed_sync"},
	}
	svc.LoadWitness(info)
	svc.LoadRequest(&pb.RechargeRequest{
		RequestId:         "req_committed_sync",
		Status:            pb.RechargeRequestStatus_RECHARGE_CHALLENGED,
		SelectedWitnesses: []string{info.Address},
	})

	svc.SyncCommittedRequest(&pb.RechargeRequest{
		RequestId:         "req_committed_sync",
		Status:            pb.RechargeRequestStatus_RECHARGE_FINALIZED,
		SelectedWitnesses: []string{info.Address},
	})
	svc.SyncCommittedChallenge(&pb.ChallengeRecord{
		ChallengeId: "challenge_committed_sync",
		Status:      pb.ChallengeStatus_CHALLENGE_FINALIZED,
		Finalized:   true,
		Arbitrators: []string{info.Address},
	})

	updated, err := svc.GetWitnessInfo(info.Address)
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.PendingTasks) != 0 {
		t.Fatalf("terminal committed sync left pending tasks: %v", updated.PendingTasks)
	}
	if _, err := svc.GetRequest("req_committed_sync"); err == nil {
		t.Fatal("terminal committed request remained in active service cache")
	}
}
