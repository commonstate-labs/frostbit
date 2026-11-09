package witness_test

import (
	"errors"
	"testing"

	"dex/pb"
	"dex/witness"
)

func TestVoteManagerRejectsUnknownVoteTypeWithoutConsumingSlot(t *testing.T) {
	manager := witness.NewVoteManager(witness.DefaultConfig())
	const requestID = "request_invalid_vote_type"
	const address = "witness_invalid_vote_type"
	manager.SetSelectedWitnesses(requestID, []string{address})

	invalid := &pb.WitnessVote{
		RequestId:      requestID,
		WitnessAddress: address,
		VoteType:       pb.WitnessVoteType(127),
	}
	if err := manager.AddVote(invalid); !errors.Is(err, witness.ErrInvalidVote) {
		t.Fatalf("unknown vote type error = %v, want %v", err, witness.ErrInvalidVote)
	}
	if votes := manager.GetVotes(requestID); len(votes) != 0 {
		t.Fatalf("unknown vote consumed a slot: %+v", votes)
	}

	valid := &pb.WitnessVote{
		RequestId:      requestID,
		WitnessAddress: address,
		VoteType:       pb.WitnessVoteType_VOTE_PASS,
	}
	if err := manager.AddVote(valid); err != nil {
		t.Fatalf("valid vote after rejected unknown enum: %v", err)
	}
	if votes := manager.GetVotes(requestID); len(votes) != 1 || votes[0].VoteType != pb.WitnessVoteType_VOTE_PASS {
		t.Fatalf("valid vote was not recorded after rejection: %+v", votes)
	}
}

func TestChallengeManagerRejectsUnknownVoteTypeWithoutConsumingSlot(t *testing.T) {
	cm, challengeID := newCMWithChallenge(t, []string{"arb1"})
	invalid := &pb.WitnessVote{WitnessAddress: "arb1", VoteType: pb.WitnessVoteType(127)}
	if err := cm.AddArbitrationVote(challengeID, invalid); !errors.Is(err, witness.ErrInvalidVote) {
		t.Fatalf("unknown arbitration vote error = %v, want %v", err, witness.ErrInvalidVote)
	}
	record, err := cm.GetChallenge(challengeID)
	if err != nil {
		t.Fatal(err)
	}
	if len(record.ArbitrationVotes) != 0 || record.PassCount != 0 || record.FailCount != 0 {
		t.Fatalf("unknown arbitration vote mutated challenge: %+v", record)
	}

	if err := cm.AddArbitrationVoteAtHeight(challengeID, &pb.WitnessVote{
		RequestId:      record.RequestId,
		WitnessAddress: "arb1",
		VoteType:       pb.WitnessVoteType_VOTE_PASS,
		BallotRound:    record.BallotRound,
	}, record.DeadlineHeight); err != nil {
		t.Fatalf("valid arbitration vote after rejected unknown enum: %v", err)
	}
}

func TestComputeArbitrationVoteOutcomeRejectsUnknownVoteType(t *testing.T) {
	svc := witness.NewService(witness.DefaultConfig())
	defer svc.Stop()

	req := &pb.RechargeRequest{
		RequestId: "request_invalid_arbitration_enum",
		Status:    pb.RechargeRequestStatus_RECHARGE_CHALLENGED,
	}
	challenge := &pb.ChallengeRecord{
		ChallengeId:        "challenge_invalid_arbitration_enum",
		RequestId:          req.RequestId,
		Status:             pb.ChallengeStatus_CHALLENGE_ACTIVE,
		DeadlineHeight:     100,
		ConsensusThreshold: 100,
		Arbitrators:        []string{"arb1"},
	}
	vote := &pb.WitnessVote{
		RequestId:      req.RequestId,
		WitnessAddress: "arb1",
		VoteType:       pb.WitnessVoteType(127),
	}

	if _, err := svc.ComputeArbitrationVoteOutcome(challenge, req, vote, nil, 100); !errors.Is(err, witness.ErrInvalidVote) {
		t.Fatalf("unknown pure arbitration vote error = %v, want %v", err, witness.ErrInvalidVote)
	}
	if len(challenge.ArbitrationVotes) != 0 || challenge.PassCount != 0 || challenge.FailCount != 0 {
		t.Fatalf("rejected pure arbitration vote mutated input: %+v", challenge)
	}
}
