package witness

import (
	"errors"
	"testing"

	"dex/pb"
)

func TestProcessVoteLocksPassAndAcceptsOneBlockGrace(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ConsensusThreshold = 60
	cfg.VoteGracePeriodBlocks = 1
	svc := NewService(cfg)
	svc.SetCurrentHeight(99)

	req := &pb.RechargeRequest{
		RequestId:         "req_vote_grace_pass",
		Status:            pb.RechargeRequestStatus_RECHARGE_VOTING,
		CreateHeight:      90,
		DeadlineHeight:    120,
		SelectedWitnesses: []string{"w1", "w2", "w3", "w4", "w5"},
	}
	svc.LoadRequest(req)

	for _, addr := range []string{"w1", "w2", "w3"} {
		if _, err := svc.ProcessVoteAtHeight(testWitnessVote(req.RequestId, addr, pb.WitnessVoteType_VOTE_PASS), 100); err != nil {
			t.Fatalf("vote %s: %v", addr, err)
		}
	}

	locked, err := svc.GetRequest(req.RequestId)
	if err != nil {
		t.Fatalf("get locked request: %v", err)
	}
	if locked.Status != pb.RechargeRequestStatus_RECHARGE_CONSENSUS_PASS {
		t.Fatalf("want locked pass, got %v", locked.Status)
	}
	if locked.DecisionHeight != 100 || locked.VoteAcceptUntilHeight != 101 {
		t.Fatalf("unexpected decision window: decision=%d acceptUntil=%d", locked.DecisionHeight, locked.VoteAcceptUntilHeight)
	}
	originalDeadline := locked.DeadlineHeight

	if _, err := svc.ProcessVoteAtHeight(testWitnessVote(req.RequestId, "w4", pb.WitnessVoteType_VOTE_FAIL), 101); err != nil {
		t.Fatalf("last grace-block vote: %v", err)
	}
	afterLate, err := svc.GetRequest(req.RequestId)
	if err != nil {
		t.Fatalf("get request after late vote: %v", err)
	}
	if afterLate.Status != pb.RechargeRequestStatus_RECHARGE_CONSENSUS_PASS {
		t.Fatalf("late fail vote reversed locked result: %v", afterLate.Status)
	}
	if afterLate.PassCount != 3 || afterLate.FailCount != 1 || len(afterLate.Votes) != 4 {
		t.Fatalf("late vote not recorded: pass=%d fail=%d votes=%d", afterLate.PassCount, afterLate.FailCount, len(afterLate.Votes))
	}
	if afterLate.DecisionHeight != 100 || afterLate.VoteAcceptUntilHeight != 101 || afterLate.DeadlineHeight != originalDeadline {
		t.Fatalf("late vote changed locked heights: decision=%d accept=%d deadline=%d", afterLate.DecisionHeight, afterLate.VoteAcceptUntilHeight, afterLate.DeadlineHeight)
	}

	if _, err := svc.ProcessVoteAtHeight(testWitnessVote(req.RequestId, "w5", pb.WitnessVoteType_VOTE_PASS), 102); !errors.Is(err, ErrVotingPeriodExpired) {
		t.Fatalf("want overdue vote rejection, got %v", err)
	}
}

func TestProcessVoteLocksFailAndCapsGraceAtVotingDeadline(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ConsensusThreshold = 60
	cfg.VoteGracePeriodBlocks = 1
	svc := NewService(cfg)

	req := &pb.RechargeRequest{
		RequestId:         "req_vote_grace_fail",
		Status:            pb.RechargeRequestStatus_RECHARGE_VOTING,
		CreateHeight:      190,
		DeadlineHeight:    200,
		SelectedWitnesses: []string{"w1", "w2", "w3", "w4"},
	}
	svc.LoadRequest(req)

	for _, addr := range []string{"w1", "w2", "w3"} {
		if _, err := svc.ProcessVoteAtHeight(testWitnessVote(req.RequestId, addr, pb.WitnessVoteType_VOTE_FAIL), 200); err != nil {
			t.Fatalf("vote %s: %v", addr, err)
		}
	}
	locked, err := svc.GetRequest(req.RequestId)
	if err != nil {
		t.Fatalf("get locked request: %v", err)
	}
	if locked.Status != pb.RechargeRequestStatus_RECHARGE_CONSENSUS_FAIL {
		t.Fatalf("want locked fail, got %v", locked.Status)
	}
	if locked.DecisionHeight != 200 || locked.VoteAcceptUntilHeight != 200 {
		t.Fatalf("grace must be capped at voting deadline: decision=%d acceptUntil=%d", locked.DecisionHeight, locked.VoteAcceptUntilHeight)
	}
	if _, err := svc.ProcessVoteAtHeight(testWitnessVote(req.RequestId, "w4", pb.WitnessVoteType_VOTE_PASS), 200); err != nil {
		t.Fatalf("same-height trailing vote must be accepted: %v", err)
	}
	if locked.Status != pb.RechargeRequestStatus_RECHARGE_CONSENSUS_FAIL {
		t.Fatalf("trailing pass vote reversed locked fail result: %v", locked.Status)
	}
}

func testWitnessVote(requestID, witnessAddress string, voteType pb.WitnessVoteType) *pb.WitnessVote {
	return &pb.WitnessVote{
		RequestId:      requestID,
		WitnessAddress: witnessAddress,
		VoteType:       voteType,
		TxId:           requestID + "_" + witnessAddress,
	}
}
