package vm

import (
	"testing"

	"dex/keys"
	"dex/pb"
	"dex/witness"

	"google.golang.org/protobuf/proto"
)

func TestRechargeVoteGraceDueTransitionsPassAfterClosingBlock(t *testing.T) {
	cfg := witness.DefaultConfig()
	cfg.ChallengePeriodBlocks = 20
	svc := witness.NewService(cfg)
	x := &Executor{WitnessService: svc}
	sv := NewMockStateView()

	req := &pb.RechargeRequest{
		RequestId:             "req_grace_due_pass",
		Status:                pb.RechargeRequestStatus_RECHARGE_CONSENSUS_PASS,
		DecisionHeight:        100,
		VoteAcceptUntilHeight: 101,
		DeadlineHeight:        120,
		SelectedWitnesses:     []string{"w1", "w2", "w3", "w4"},
		Votes: []*pb.WitnessVote{
			testVMGraceVote("req_grace_due_pass", "w1", pb.WitnessVoteType_VOTE_PASS),
			testVMGraceVote("req_grace_due_pass", "w2", pb.WitnessVoteType_VOTE_PASS),
			testVMGraceVote("req_grace_due_pass", "w3", pb.WitnessVoteType_VOTE_PASS),
			testVMGraceVote("req_grace_due_pass", "w4", pb.WitnessVoteType_VOTE_FAIL),
		},
		PassCount: 3,
		FailCount: 1,
	}
	if err := setRechargeRequestWithIndexes(sv, req, 1); err != nil {
		t.Fatalf("set locked request: %v", err)
	}
	graceKey := keys.KeyRechargeVoteGraceDue(101, req.RequestId)
	if _, ok, _ := sv.Get(graceKey); !ok {
		t.Fatalf("missing vote grace due index")
	}

	if err := x.applyRechargeVoteGraceClosures(sv, 100, 1); err != nil {
		t.Fatalf("close before due: %v", err)
	}
	before := loadRechargeRequestAtKey(sv, keys.KeyRechargeRequest(req.RequestId))
	if before == nil || before.Status != pb.RechargeRequestStatus_RECHARGE_CONSENSUS_PASS {
		t.Fatalf("request closed before inclusive deadline: %+v", before)
	}

	if err := x.applyRechargeVoteGraceClosures(sv, 101, 1); err != nil {
		t.Fatalf("close at due height: %v", err)
	}
	after := loadRechargeRequestAtKey(sv, keys.KeyRechargeRequest(req.RequestId))
	if after == nil || after.Status != pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD {
		t.Fatalf("want challenge period, got %+v", after)
	}
	if after.DeadlineHeight != 121 {
		t.Fatalf("challenge period must start after grace close: deadline=%d", after.DeadlineHeight)
	}
	if after.PassCount != 3 || after.FailCount != 1 || len(after.Votes) != 4 {
		t.Fatalf("grace votes lost at close: pass=%d fail=%d votes=%d", after.PassCount, after.FailCount, len(after.Votes))
	}
	if _, ok, _ := sv.Get(graceKey); ok {
		t.Fatalf("vote grace due index not removed")
	}
	if _, ok, _ := sv.Get(keys.KeyRechargeChallengeDue(122, req.RequestId)); !ok {
		t.Fatalf("challenge finalization due index not created")
	}
}

func TestRechargeVoteGraceDueRejectsFailAndArchivesVotes(t *testing.T) {
	cfg := witness.DefaultConfig()
	svc := witness.NewService(cfg)
	x := &Executor{WitnessService: svc}
	sv := NewMockStateView()

	req := &pb.RechargeRequest{
		RequestId:             "req_grace_due_fail",
		Status:                pb.RechargeRequestStatus_RECHARGE_CONSENSUS_FAIL,
		DecisionHeight:        50,
		VoteAcceptUntilHeight: 51,
		DeadlineHeight:        80,
		SelectedWitnesses:     []string{"w1", "w2", "w3"},
		Votes: []*pb.WitnessVote{
			testVMGraceVote("req_grace_due_fail", "w1", pb.WitnessVoteType_VOTE_FAIL),
			testVMGraceVote("req_grace_due_fail", "w2", pb.WitnessVoteType_VOTE_FAIL),
		},
		FailCount: 2,
	}
	if err := setRechargeRequestWithIndexes(sv, req, 1); err != nil {
		t.Fatalf("set locked request: %v", err)
	}
	if err := x.applyRechargeVoteGraceClosures(sv, 51, 1); err != nil {
		t.Fatalf("close fail grace: %v", err)
	}

	hot := loadRechargeRequestAtKey(sv, keys.KeyRechargeRequest(req.RequestId))
	if hot == nil || hot.Status != pb.RechargeRequestStatus_RECHARGE_REJECTED {
		t.Fatalf("want rejected hot marker, got %+v", hot)
	}
	if hot.FinalizeHeight != 51 || hot.DecisionHeight != 50 || hot.VoteAcceptUntilHeight != 51 {
		t.Fatalf("rejection audit heights lost: finalize=%d decision=%d accept=%d", hot.FinalizeHeight, hot.DecisionHeight, hot.VoteAcceptUntilHeight)
	}

	rawArchive, ok, _ := sv.Get(keys.KeyArchiveRechargeRequest(req.RequestId))
	if !ok {
		t.Fatalf("missing rejected request archive")
	}
	var archived pb.RechargeRequest
	if err := proto.Unmarshal(rawArchive, &archived); err != nil {
		t.Fatalf("unmarshal archive: %v", err)
	}
	if len(archived.Votes) != 2 || archived.FailCount != 2 {
		t.Fatalf("archived grace votes lost: votes=%d fail=%d", len(archived.Votes), archived.FailCount)
	}
}

func testVMGraceVote(requestID, address string, voteType pb.WitnessVoteType) *pb.WitnessVote {
	return &pb.WitnessVote{
		RequestId:      requestID,
		WitnessAddress: address,
		VoteType:       voteType,
		TxId:           requestID + "_" + address,
	}
}
