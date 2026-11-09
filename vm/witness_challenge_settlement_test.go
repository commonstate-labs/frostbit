package vm

import (
	"dex/keys"
	"dex/pb"
	"dex/witness"
	"fmt"
	"testing"

	"google.golang.org/protobuf/proto"
)

func seedChallengeSettlementFBToken(t *testing.T, sv StateView, supply string) {
	t.Helper()
	data, err := proto.Marshal(&pb.Token{Address: FeeToken, TotalSupply: supply})
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyToken(FeeToken), data)
}

func readChallengeSettlementFBTokenSupply(t *testing.T, sv StateView) string {
	t.Helper()
	data, ok, err := sv.Get(keys.KeyToken(FeeToken))
	if err != nil || !ok {
		t.Fatalf("FB token missing: ok=%v err=%v", ok, err)
	}
	var token pb.Token
	if err := proto.Unmarshal(data, &token); err != nil {
		t.Fatal(err)
	}
	return token.TotalSupply
}

func TestApplySlashAndRefundToState_FinalizedFailedChallengeBurnsStakeAndSlash(t *testing.T) {
	sv := newMemStateView()
	seedChallengeSettlementFBToken(t, sv, "1000000")
	seedBalance(sv, "wrong1", "0", "500000")

	outcome := &witness.ArbitrationVoteOutcome{
		Challenge: &pb.ChallengeRecord{
			ChallengeId: "c_failed", StakeAmount: "300000",
			Status: pb.ChallengeStatus_CHALLENGE_FINALIZED, Finalized: true,
		},
		SlashedWitnesses: []*pb.WitnessInfo{{Address: "wrong1", StakeAmount: "300000", Status: pb.WitnessStatus_WITNESS_SLASHED}},
		SlashAmounts:     map[string]string{"wrong1": "200000"},
		ChallengerAddr:   "challenger",
		ChallengerRefund: "0",
	}
	if _, err := applySlashAndRefundToState(sv, outcome); err != nil {
		t.Fatal(err)
	}
	if got := readBalance(sv, "wrong1").WitnessLockedBalance; got != "300000" {
		t.Fatalf("wrong1 locked=%s want=300000", got)
	}
	if got := readChallengeSettlementFBTokenSupply(t, sv); got != "500000" {
		t.Fatalf("FB supply=%s want=500000", got)
	}
}

func TestApplySlashAndRefundToState_SystemChallengeBurnsSlashWithoutStake(t *testing.T) {
	sv := newMemStateView()
	seedChallengeSettlementFBToken(t, sv, "1000000")
	seedBalance(sv, "wrong1", "0", "500000")

	outcome := &witness.ArbitrationVoteOutcome{
		Challenge: &pb.ChallengeRecord{
			ChallengeId: "auto_req1", StakeAmount: "0",
			Status: pb.ChallengeStatus_CHALLENGE_FINALIZED, Finalized: true,
		},
		SlashedWitnesses: []*pb.WitnessInfo{{Address: "wrong1", StakeAmount: "300000", Status: pb.WitnessStatus_WITNESS_SLASHED}},
		SlashAmounts:     map[string]string{"wrong1": "200000"},
		ChallengerAddr:   "system",
	}
	if _, err := applySlashAndRefundToState(sv, outcome); err != nil {
		t.Fatal(err)
	}
	if got := readBalance(sv, "wrong1").WitnessLockedBalance; got != "300000" {
		t.Fatalf("wrong1 locked=%s want=300000", got)
	}
	if got := readChallengeSettlementFBTokenSupply(t, sv); got != "800000" {
		t.Fatalf("FB supply=%s want=800000", got)
	}
}

func TestAttachChallengeContributorRefundsUsesActualSlash(t *testing.T) {
	sv := newMemStateView()
	seedChallengeSettlementFBToken(t, sv, "1000000")
	seedBalance(sv, "wrong1", "0", "100000")
	seedBalance(sv, "challenger", "0", "0")

	contrib := &pb.WitnessChallengeTx{Base: &pb.BaseMessage{TxId: "tx1", FromAddress: "challenger"}, StakeAmount: "300000"}
	data, err := proto.Marshal(contrib)
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyChallengeContribution("c_success", "tx1"), data)

	outcome := &witness.ArbitrationVoteOutcome{
		Challenge: &pb.ChallengeRecord{
			ChallengeId: "c_success", StakeAmount: "300000", ChallengeSuccess: true,
			Status: pb.ChallengeStatus_CHALLENGE_FINALIZED, Finalized: true,
		},
		SlashedWitnesses: []*pb.WitnessInfo{{Address: "wrong1", StakeAmount: "0", Status: pb.WitnessStatus_WITNESS_SLASHED}},
		SlashAmounts:     map[string]string{"wrong1": "500000"},
	}
	attachChallengeContributorRefunds(sv, outcome)
	if got := outcome.ChallengerRefunds["challenger"]; got != "400000" {
		t.Fatalf("refund=%s want=400000", got)
	}
	if _, err := applySlashAndRefundToState(sv, outcome); err != nil {
		t.Fatal(err)
	}
	if got := readBalance(sv, "wrong1").WitnessLockedBalance; got != "0" {
		t.Fatalf("wrong1 locked=%s want=0", got)
	}
	if got := readBalance(sv, "challenger").Balance; got != "400000" {
		t.Fatalf("challenger balance=%s want=400000", got)
	}
	if got := readChallengeSettlementFBTokenSupply(t, sv); got != "1000000" {
		t.Fatalf("FB supply=%s want=1000000", got)
	}
	if _, exists, _ := sv.Get(keys.KeyChallengeContribution("c_success", "tx1")); exists {
		t.Fatalf("finalized challenge contribution was not deleted")
	}
	archivedContrib, archived, _ := sv.Get(keys.KeyArchiveChallengeContribution("c_success", "tx1"))
	if !archived {
		t.Fatalf("finalized challenge contribution was not archived")
	}
	var archivedTx pb.WitnessChallengeTx
	if err := proto.Unmarshal(archivedContrib, &archivedTx); err != nil {
		t.Fatalf("unmarshal archived contribution: %v", err)
	}
	if archivedTx.Base == nil || archivedTx.Base.FromAddress != "challenger" || archivedTx.StakeAmount != "300000" {
		t.Fatalf("archived contribution mismatch: %+v", &archivedTx)
	}
}

func TestWitnessChallengeContributionsAccumulateAcrossSameBlock(t *testing.T) {
	sv := newMemStateView()
	req := &pb.RechargeRequest{ArbitrationProtocolVersion: 1, FeeEscrow: "0", RequestId: "req1", Status: pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD, DeadlineHeight: 100}
	                                                                
	for i := 0; i < 300; i++ {
		req.CaseCollateral = append(req.CaseCollateral, &pb.WitnessCaseCollateral{Address: fmt.Sprint("original-", i), RoleMask: WitnessCaseRoleOriginal, OriginalAmount: WitnessCaseBondRaw})
	}
	reqData, err := proto.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyRechargeRequest(req.RequestId), reqData)
	for _, addr := range []string{"alice", "bob"} {
		accData, err := proto.Marshal(&pb.Account{Address: addr})
		if err != nil {
			t.Fatal(err)
		}
		sv.Set(keys.KeyAccount(addr), accData)
		seedBalance(sv, addr, "100000000000", "0")
	}
	releaseStateViewHistory(sv)

	cfg := witness.DefaultConfig()
	cfg.ChallengeStakeAmount = "30000000000"
	svc := witness.NewService(cfg)
	defer svc.Stop()
	handler := &WitnessChallengeTxHandler{witnessSvc: svc}
	applyChallenge := func(txID, from, amount string) {
		t.Helper()
		tx := &pb.AnyTx{Content: &pb.AnyTx_WitnessChallengeTx{WitnessChallengeTx: &pb.WitnessChallengeTx{
			Base:      &pb.BaseMessage{TxId: txID, FromAddress: from, ExecutedHeight: 10},
			RequestId: req.RequestId, StakeAmount: amount, Reason: "test",
		}}}
		snap := sv.Snapshot()
		ws, rc, err := handler.DryRun(tx, sv)
		if err != nil || rc == nil || rc.Status != "SUCCEED" {
			t.Fatalf("challenge %s dryrun failed rc=%+v err=%v", txID, rc, err)
		}
		changedKeys := map[string]struct{}(nil)
		if sv.Snapshot() != snap {
			changedKeys = stateViewChangedKeysSince(sv, snap)
		}
		for _, w := range ws {
			if _, changed := changedKeys[w.Key]; changed {
				continue
			}
			if stateViewHasPendingWrite(sv, w.Key) && writeOpAlreadyInStateView(sv, w) {
				continue
			}
			if err := applyWriteOpWithRechargeIndexes(sv, w, 1, 0, 0); err != nil {
				t.Fatal(err)
			}
		}
		releaseStateViewHistory(sv)
	}

	applyChallenge("tx1", "alice", "10000000000")
	firstReq, _, _ := sv.Get(keys.KeyRechargeRequest(req.RequestId))
	var pendingReq pb.RechargeRequest
	if err := proto.Unmarshal(firstReq, &pendingReq); err != nil {
		t.Fatal(err)
	}
	if pendingReq.Status != pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD {
		t.Fatal("one valid 100 FB contribution must not activate a case requiring 300 FB")
	}
	applyChallenge("tx2", "bob", "20000000000")

	updatedReqData, ok, err := sv.Get(keys.KeyRechargeRequest(req.RequestId))
	if err != nil || !ok {
		t.Fatalf("request missing ok=%v err=%v", ok, err)
	}
	var updatedReq pb.RechargeRequest
	if err := proto.Unmarshal(updatedReqData, &updatedReq); err != nil {
		t.Fatal(err)
	}
	challengeData, ok, err := sv.Get(keys.KeyChallengeRecord(updatedReq.ChallengeId))
	if err != nil || !ok {
		t.Fatalf("challenge missing id=%s ok=%v err=%v", updatedReq.ChallengeId, ok, err)
	}
	var challenge pb.ChallengeRecord
	if err := proto.Unmarshal(challengeData, &challenge); err != nil {
		t.Fatal(err)
	}
	if challenge.StakeAmount != "30000000000" {
		t.Fatalf("challenge stake=%s want=30000000000", challenge.StakeAmount)
	}
	if updatedReq.Status != pb.RechargeRequestStatus_RECHARGE_CHALLENGED {
		t.Fatal("accumulated 300 FB must activate the case")
	}
	if got := readBalance(sv, "alice").Balance; got != "90000000000" {
		t.Fatalf("alice balance=%s want=90000000000", got)
	}
	if got := readBalance(sv, "bob").Balance; got != "80000000000" {
		t.Fatalf("bob balance=%s want=80000000000", got)
	}
}
