package vm

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"

	"dex/keys"
	"dex/pb"
	"dex/witness"
	"google.golang.org/protobuf/proto"
)

func lifecycleFixture(t *testing.T, count int) (StateView, *Executor, *pb.RechargeRequest) {
	t.Helper()
	sv := newMemStateView()
	seedChallengeSettlementFBToken(t, sv, "1000000000000")
	for i := 1; i <= count; i++ {
		a := fmt.Sprintf("w%02d", i)
		stake := fmt.Sprint(10000000000 + int64(count-i)*100000000)
		settlementPut(t, sv, keys.KeyWitnessInfo(a), &pb.WitnessInfo{Address: a, StakeAmount: stake, Status: pb.WitnessStatus_WITNESS_ACTIVE})
		SetBalance(sv, a, FeeToken, &pb.TokenBalance{Balance: "100000", WitnessLockedBalance: stake})
	}
	SetBalance(sv, "payer", FeeToken, &pb.TokenBalance{Balance: "1000"})
	SetBalance(sv, "challenger", FeeToken, &pb.TokenBalance{Balance: "2000000000"})
	settlementPut(t, sv, keys.KeyAccount("challenger"), &pb.Account{Address: "challenger"})
	req := &pb.RechargeRequest{RequestId: "lifecycle", RequesterAddress: "payer", RechargeFee: "101", Status: pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD, DeadlineHeight: 100, PassCount: 4}
	if err := collectWitnessServiceFee(sv, req, false); err != nil {
		t.Fatal(err)
	}
	var assignments []WitnessCaseAssignment
	for i := 1; i <= 4; i++ {
		a := fmt.Sprintf("w%02d", i)
		req.SelectedWitnesses = append(req.SelectedWitnesses, a)
		req.OriginalPositions = append(req.OriginalPositions, &pb.WitnessVote{RequestId: req.RequestId, WitnessAddress: a, VoteType: pb.WitnessVoteType_VOTE_PASS})
		assignments = append(assignments, WitnessCaseAssignment{Address: a, Role: WitnessCaseRoleOriginal, Amount: WitnessCaseBondRaw})
	}
	if err := reserveWitnessCaseCollateral(sv, req, assignments, "3200000000"); err != nil {
		t.Fatal(err)
	}
	if err := setRechargeRequestWithIndexes(sv, req, 3); err != nil {
		t.Fatal(err)
	}
	svc := witness.NewService(witness.DefaultConfig())
	t.Cleanup(svc.Stop)
	return sv, &Executor{WitnessService: svc}, req
}

func lifecycleFund(t *testing.T, sv StateView, x *Executor, requestID string) *pb.ChallengeRecord {
	t.Helper()
	tx := &pb.AnyTx{Content: &pb.AnyTx_WitnessChallengeTx{WitnessChallengeTx: &pb.WitnessChallengeTx{Base: &pb.BaseMessage{TxId: "fund-case", FromAddress: "challenger", ExecutedHeight: 10}, RequestId: requestID, StakeAmount: "1000000000"}}}
	_, receipt, err := (&WitnessChallengeTxHandler{witnessSvc: x.WitnessService}).DryRun(tx, sv)
	if err != nil || receipt == nil || receipt.Status != "SUCCEED" {
		t.Fatalf("fund: %+v %v", receipt, err)
	}
	ch := loadChallengeRecordAtKey(sv, keys.KeyChallengeRecord("fund-case"))
	if ch == nil || ch.RequiredStake != "1000000000" || ch.Status != pb.ChallengeStatus_CHALLENGE_PENDING_SELECTION {
		t.Fatalf("funded case: %+v", ch)
	}
	return ch
}

func lifecycleBallot(t *testing.T, sv StateView, x *Executor, ch *pb.ChallengeRecord, a string, direction pb.WitnessVoteType, height uint64, success bool) {
	t.Helper()
	tx := &pb.AnyTx{Content: &pb.AnyTx_ArbitrationVoteTx{ArbitrationVoteTx: &pb.ArbitrationVoteTx{Base: &pb.BaseMessage{TxId: fmt.Sprintf("vote-%d-%s", ch.BallotRound, a), FromAddress: a, ExecutedHeight: height}, ChallengeId: ch.ChallengeId, BallotRound: ch.BallotRound, Vote: &pb.WitnessVote{RequestId: ch.RequestId, WitnessAddress: a, VoteType: direction}}}}
	before := proto.Clone(tx)
	_, receipt, err := (&ArbitrationVoteTxHandler{witnessSvc: x.WitnessService}).DryRun(tx, sv)
	if (err == nil && receipt != nil && receipt.Status == "SUCCEED") != success {
		t.Fatalf("ballot %s round %d: %+v %v", a, ch.BallotRound, receipt, err)
	}
	if !proto.Equal(before, tx) {
		t.Fatal("ballot handler mutated block body")
	}
}

func TestArbitrationLifecycleThreeRoundsFixedCourtAndRestart(t *testing.T) {
	sv, x, req := lifecycleFixture(t, 8)
	sv, initialRead := settlementBaseline(sv)
	ch := lifecycleFund(t, sv, x, req.RequestId)
	height := challengeSelectionReadyHeight(ch, 3)
	if err := x.applyPendingArbitrationSelections(sv, height); err != nil {
		t.Fatal(err)
	}
	ch = loadChallengeRecordAtKey(sv, keys.KeyChallengeRecord(ch.ChallengeId))
	if ch.ArbitrationRound != 1 || ch.BallotRound != 1 || len(ch.Arbitrators) != 4 || ch.DeadlineHeight != height+10000 {
		t.Fatalf("first round: %+v", ch)
	}
	first := ch.Arbitrators[0]
	lifecycleBallot(t, sv, x, ch, first, pb.WitnessVoteType_VOTE_PASS, height, true)
	if err := x.applyArbitrationTimeouts(sv, ch.DeadlineHeight); err != nil {
		t.Fatal(err)
	}
	if got := loadChallengeRecordAtKey(sv, keys.KeyChallengeRecord(ch.ChallengeId)); got.BallotRound != 1 {
		t.Fatal("deadline is inclusive")
	}
	height = ch.DeadlineHeight + 1
	if err := x.applyArbitrationTimeouts(sv, height); err != nil {
		t.Fatal(err)
	}
	ch = loadChallengeRecordAtKey(sv, keys.KeyChallengeRecord(ch.ChallengeId))
	if ch.ArbitrationRound != 2 || len(ch.Arbitrators) != 8 || ch.PassCount != 0 {
		t.Fatalf("second round: %+v", ch)
	}
	lifecycleBallot(t, sv, x, ch, first, pb.WitnessVoteType_VOTE_FAIL, height, true)
	lifecycleBallot(t, sv, x, ch, "w05", pb.WitnessVoteType_VOTE_FAIL, height, first != "w05")
	height = ch.DeadlineHeight + 1
	if err := x.applyArbitrationTimeouts(sv, height); err != nil {
		t.Fatal(err)
	}
	ch = loadChallengeRecordAtKey(sv, keys.KeyChallengeRecord(ch.ChallengeId))
	court := []string{"w01", "w02", "w03", "w04"}
	if ch.ArbitrationRound != 3 || !reflect.DeepEqual(ch.FinalCourt, court) || ch.CourtSnapshotHeight != height {
		t.Fatalf("third round snapshot: %+v", ch)
	}
	if q, _ := witness.ArbitrationQuorum(ch); q != 12 {
		t.Fatalf("candidate shortage lowered quorum: %d", q)
	}
	                                                                               
	var exiting pb.WitnessInfo
	b, _, _ := sv.Get(keys.KeyWitnessInfo("w01"))
	if err := proto.Unmarshal(b, &exiting); err != nil {
		t.Fatal(err)
	}
	exiting.Status = pb.WitnessStatus_WITNESS_UNSTAKING
	settlementPut(t, sv, keys.KeyWitnessInfo("w01"), &exiting)
	height = ch.DeadlineHeight + 1
	if err := x.applyArbitrationTimeouts(sv, height); err != nil {
		t.Fatal(err)
	}
	ch = loadChallengeRecordAtKey(sv, keys.KeyChallengeRecord(ch.ChallengeId))
	if ch.ArbitrationStage != pb.ArbitrationStage_ARBITRATION_FINAL_COURT || !ch.OrdinaryVotesFrozen || !reflect.DeepEqual(ch.Arbitrators, court) {
		t.Fatalf("court entry: %+v", ch)
	}
	oldRound := proto.Clone(ch).(*pb.ChallengeRecord)
	for i, a := range court {
		d := pb.WitnessVoteType_VOTE_FAIL
		if i < 2 {
			d = pb.WitnessVoteType_VOTE_PASS
		}
		lifecycleBallot(t, sv, x, ch, a, d, height, true)
	}
	for retry := 0; retry < 20; retry++ {
		ch = loadChallengeRecordAtKey(sv, keys.KeyChallengeRecord(ch.ChallengeId))
		if ch.Finalized || !reflect.DeepEqual(ch.FinalCourt, court) || ch.StakeAmount != "1000000000" {
			t.Fatal("unresolved round changed custody or court")
		}
		height = ch.DeadlineHeight + 1
		if err := x.applyArbitrationTimeouts(sv, height); err != nil {
			t.Fatal(err)
		}
	}
	lifecycleBallot(t, sv, x, oldRound, "w01", pb.WitnessVoteType_VOTE_PASS, height, false)
	                                                                                                  
	x.WitnessService.Stop()
	x.WitnessService = witness.NewService(witness.DefaultConfig())
	t.Cleanup(x.WitnessService.Stop)
	ch = loadChallengeRecordAtKey(sv, keys.KeyChallengeRecord(ch.ChallengeId))
	for _, a := range court[:3] {
		lifecycleBallot(t, sv, x, ch, a, pb.WitnessVoteType_VOTE_FAIL, height, true)
	}
	final := loadChallengeRecordAtKey(sv, keys.KeyChallengeRecord(ch.ChallengeId))
	closed := loadRechargeRequestAtKey(sv, keys.KeyRechargeRequest(req.RequestId))
	if !final.Settled || !final.ChallengeSuccess || final.StakeAmount != "0" || closed.Status != pb.RechargeRequestStatus_RECHARGE_REJECTED || !closed.FeeSettled {
		t.Fatalf("incomplete settlement: %+v / %+v", final, closed)
	}
	for i := 1; i <= 8; i++ {
		var info pb.WitnessInfo
		b, _, _ := sv.Get(keys.KeyWitnessInfo(fmt.Sprintf("w%02d", i)))
		_ = proto.Unmarshal(b, &info)
		if info.ActiveCaseCount != 0 || info.ReservedStakeAmount != "0" {
			t.Fatalf("orphan case duty: %+v", &info)
		}
	}
	if GetBalance(sv, WitnessFeePoolAccount, FeeToken).Balance != "0" {
		t.Fatal("fee escrow not drained")
	}
	AuditGlobalConservationDiff(sv.Diff(), height, initialRead)
	snapshot := sv.Snapshot()
	lifecycleBallot(t, sv, x, ch, "w04", pb.WitnessVoteType_VOTE_FAIL, height, false)
	if sv.Snapshot() != snapshot {
		t.Fatal("closed case replay changed state")
	}
}

func TestArbitrationLifecycleCourtShortageWaitsAndPreservesFrozenPositions(t *testing.T) {
	sv, x, req := lifecycleFixture(t, 4)
	ch := lifecycleFund(t, sv, x, req.RequestId)
	if err := x.applyPendingArbitrationSelections(sv, challengeSelectionReadyHeight(ch, 3)); err != nil {
		t.Fatal(err)
	}
	for round := 1; round <= 2; round++ {
		ch = loadChallengeRecordAtKey(sv, keys.KeyChallengeRecord(ch.ChallengeId))
		if round == 2 {
			var info pb.WitnessInfo
			b, _, _ := sv.Get(keys.KeyWitnessInfo("w04"))
			_ = proto.Unmarshal(b, &info)
			info.Status = pb.WitnessStatus_WITNESS_UNSTAKING
			settlementPut(t, sv, keys.KeyWitnessInfo("w04"), &info)
		}
		if err := x.applyArbitrationTimeouts(sv, ch.DeadlineHeight+1); err != nil {
			t.Fatal(err)
		}
	}
	ch = loadChallengeRecordAtKey(sv, keys.KeyChallengeRecord(ch.ChallengeId))
	if ch.ArbitrationRound != 3 || ch.Status != pb.ChallengeStatus_CHALLENGE_PENDING_SELECTION || ch.Finalized || len(ch.FinalCourt) != 0 {
		t.Fatalf("court shortage must wait: %+v", ch)
	}
	before := GetBalance(sv, "challenger", FeeToken).Balance
	if err := x.applyPendingArbitrationSelections(sv, challengeSelectionReadyHeight(ch, 3)); err != nil {
		t.Fatal(err)
	}
	if GetBalance(sv, "challenger", FeeToken).Balance != before {
		t.Fatal("waiting refunded or charged principal")
	}
}

func TestArbitrationFundingExpiryRefundsBeforeFinalization(t *testing.T) {
	sv, x, req := lifecycleFixture(t, 12)
	                                                                          
	for i := 5; i <= 12; i++ {
		a := fmt.Sprintf("w%02d", i)
		if err := reserveWitnessCaseCollateral(sv, req, []WitnessCaseAssignment{{Address: a, Role: WitnessCaseRoleOriginal, Amount: WitnessCaseBondRaw}}, "3200000000"); err != nil {
			t.Fatal(err)
		}
	}
	if err := setRechargeRequestWithIndexes(sv, req, 3); err != nil {
		t.Fatal(err)
	}
	sv, read := settlementBaseline(sv)
	tx := &pb.AnyTx{Content: &pb.AnyTx_WitnessChallengeTx{WitnessChallengeTx: &pb.WitnessChallengeTx{Base: &pb.BaseMessage{TxId: "partial", FromAddress: "challenger", ExecutedHeight: 99}, RequestId: req.RequestId, StakeAmount: "1000000000"}}}
	_, r, err := (&WitnessChallengeTxHandler{witnessSvc: x.WitnessService}).DryRun(tx, sv)
	if err != nil || r.Status != "SUCCEED" {
		t.Fatalf("fund %+v %v", r, err)
	}
	req = loadRechargeRequestAtKey(sv, keys.KeyRechargeRequest(req.RequestId))
	ch := loadChallengeRecordAtKey(sv, keys.KeyChallengeRecord(req.ChallengeId))
	if ch.Status != pb.ChallengeStatus_CHALLENGE_FUNDING || ch.RequiredStake != "1200000000" || req.Status != pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD {
		t.Fatalf("partial: %+v", ch)
	}
	if err := refundUnfundedRechargeChallenge(sv, req, 100, 3); err == nil {
		t.Fatal("refunded before deadline")
	}
	if err := refundUnfundedRechargeChallenge(sv, req, 101, 3); err != nil {
		t.Fatal(err)
	}
	if GetBalance(sv, "challenger", FeeToken).Balance != "2000000000" || GetBalance(sv, WitnessFeePoolAccount, FeeToken).Balance != "101" {
		t.Fatal("principal refund affected witness fee escrow")
	}
	snap := sv.Snapshot()
	if err := refundUnfundedRechargeChallenge(sv, req, 102, 3); err != nil || sv.Snapshot() != snap {
		t.Fatal("refund not idempotent", err)
	}
	AuditGlobalConservationDiff(sv.Diff(), 102, read)
}

func TestArbitrationSelectionFailureRollsBackIndexAndCollateral(t *testing.T) {
	sv, x, req := lifecycleFixture(t, 4)
	ch := lifecycleFund(t, sv, x, req.RequestId)
	before, _, _ := sv.Get(keys.KeyRechargeRequest(req.RequestId))
	                                                                                    
	SetBalance(sv, "w04", FeeToken, &pb.TokenBalance{WitnessLockedBalance: "0"})
	snap := sv.Snapshot()
	err := x.applyPendingArbitrationSelections(sv, challengeSelectionReadyHeight(ch, 3))
	if err == nil || sv.Snapshot() != snap {
		t.Fatal("corrupt backing was silently retried or partially committed", err)
	}
	after, _, _ := sv.Get(keys.KeyRechargeRequest(req.RequestId))
	if !bytes.Equal(before, after) {
		t.Fatal("failed selection changed collateral")
	}
	if _, exists, _ := sv.Get(challengeSelectionDueKeyForRecord(ch, 3)); !exists {
		t.Fatal("failed selection lost due index")
	}
}

func TestWitnessZeroGraceRejectionDoesNotRestoreReleasedCollateral(t *testing.T) {
	sv, x, req := lifecycleFixture(t, 4)
	x.WitnessService.Stop()
	cfg := witness.DefaultConfig()
	cfg.VoteGracePeriodBlocks = 0
	x.WitnessService = witness.NewService(cfg)
	t.Cleanup(x.WitnessService.Stop)
	req.Status, req.PassCount, req.FailCount = pb.RechargeRequestStatus_RECHARGE_VOTING, 0, 2
	req.OriginalPositions = nil
	for _, a := range []string{"w01", "w02"} {
		req.OriginalPositions = append(req.OriginalPositions, &pb.WitnessVote{RequestId: req.RequestId, WitnessAddress: a, VoteType: pb.WitnessVoteType_VOTE_FAIL})
	}
	req.Votes = req.OriginalPositions
	if err := setRechargeRequestWithIndexes(sv, req, 3); err != nil {
		t.Fatal(err)
	}
	tx := &pb.AnyTx{Content: &pb.AnyTx_WitnessVoteTx{WitnessVoteTx: &pb.WitnessVoteTx{Base: &pb.BaseMessage{TxId: "final-original-fail", FromAddress: "w03", ExecutedHeight: 20}, Vote: &pb.WitnessVote{RequestId: req.RequestId, WitnessAddress: "w03", VoteType: pb.WitnessVoteType_VOTE_FAIL}}}}
	beforeTx := proto.Clone(tx)
	ops, receipt, err := (&WitnessVoteTxHandler{witnessSvc: x.WitnessService}).DryRun(tx, sv)
	if err != nil || receipt == nil || receipt.Status != "SUCCEED" {
		t.Fatalf("vote %+v %v", receipt, err)
	}
	for _, op := range ops {
		if err := applyWriteOpWithRechargeIndexes(sv, op, 3, 0, 20); err != nil {
			t.Fatal(err)
		}
	}
	closed := loadRechargeRequestAtKey(sv, keys.KeyRechargeRequest(req.RequestId))
	if closed.Status != pb.RechargeRequestStatus_RECHARGE_REJECTED || !closed.FeeSettled {
		t.Fatalf("not settled: %+v", closed)
	}
	for _, a := range req.SelectedWitnesses {
		var info pb.WitnessInfo
		b, _, _ := sv.Get(keys.KeyWitnessInfo(a))
		if err := proto.Unmarshal(b, &info); err != nil {
			t.Fatal(err)
		}
		if info.ActiveCaseCount != 0 || info.ReservedStakeAmount != "0" {
			t.Fatalf("old stats restored a released duty: %+v", &info)
		}
	}
	if !proto.Equal(tx, beforeTx) {
		t.Fatal("original vote handler changed signed payload")
	}
}

func TestWitnessStalePublicityEventCannotDetachFundedChallenge(t *testing.T) {
	sv, x, req := lifecycleFixture(t, 4)
	stale := proto.Clone(req).(*pb.RechargeRequest)
	ch := lifecycleFund(t, sv, x, req.RequestId)
	if err := persistRechargeRequest(sv, stale, 3); err != nil {
		t.Fatal(err)
	}
	current := loadRechargeRequestAtKey(sv, keys.KeyRechargeRequest(req.RequestId))
	if current.Status != pb.RechargeRequestStatus_RECHARGE_CHALLENGED || current.ChallengeId != ch.ChallengeId {
		t.Fatalf("stale event reopened/detached case: %+v", current)
	}
	if _, active, err := rechargeActivatedChallengeInState(sv, current); err != nil || !active {
		t.Fatal("funded case no longer gates finalization", err)
	}
}

func TestWitnessLegacyVersionCannotEnterNewCollateralOrFunding(t *testing.T) {
	sv, x, req := lifecycleFixture(t, 4)
	req.ArbitrationProtocolVersion = 0
	req.CaseCollateral = nil
	settlementPut(t, sv, keys.KeyRechargeRequest(req.RequestId), req)
	snapshot := sv.Snapshot()
	if _, err := x.selectOriginalWitnessesFromState(sv, req, nil, 20); err == nil {
		t.Fatal("legacy selection silently created new liability")
	}
	tx := &pb.AnyTx{Content: &pb.AnyTx_WitnessChallengeTx{WitnessChallengeTx: &pb.WitnessChallengeTx{Base: &pb.BaseMessage{TxId: "legacy-fund", FromAddress: "challenger", ExecutedHeight: 20}, RequestId: req.RequestId, StakeAmount: WitnessCaseBondRaw}}}
	_, receipt, err := (&WitnessChallengeTxHandler{witnessSvc: x.WitnessService}).DryRun(tx, sv)
	if err != nil || receipt == nil || receipt.Status != "FAILED" || sv.Snapshot() != snapshot {
		t.Fatalf("legacy funding changed state: %+v %v", receipt, err)
	}
}
