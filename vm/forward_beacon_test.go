                            
                                                                      
                                                                                                                                                
                                                                                                                                     
package vm

import (
	"bytes"
	"reflect"
	"testing"

	"dex/keys"
	"dex/pb"
	"dex/witness"

	"google.golang.org/protobuf/proto"
)

                                                                

func TestDeriveForwardBeacon_SameInputSameOutput(t *testing.T) {
	sv := newMemStateView()
	sv.Set(keys.KeyBlockHashByHeight(100), []byte("block_100_hash"))
	sv.Set(keys.KeyBlockHashByHeight(101), []byte("block_101_hash"))
	sv.Set(keys.KeyBlockHashByHeight(102), []byte("block_102_hash"))

	a := deriveForwardBeacon(sv, 100, 3, "ETH", "0xnative")
	b := deriveForwardBeacon(sv, 100, 3, "ETH", "0xnative")
	if !bytes.Equal(a, b) {
		t.Fatalf("deterministic expected; a=%x b=%x", a, b)
	}
}

func TestDeriveForwardBeacon_DifferentFutureHashChangesBeacon(t *testing.T) {
	sv1 := newMemStateView()
	sv1.Set(keys.KeyBlockHashByHeight(100), []byte("hash_A"))
	sv1.Set(keys.KeyBlockHashByHeight(101), []byte("hash_B"))
	sv1.Set(keys.KeyBlockHashByHeight(102), []byte("hash_C"))

	sv2 := newMemStateView()
	sv2.Set(keys.KeyBlockHashByHeight(100), []byte("hash_A"))
	sv2.Set(keys.KeyBlockHashByHeight(101), []byte("hash_B"))
	sv2.Set(keys.KeyBlockHashByHeight(102), []byte("hash_C_mutated"))

	a := deriveForwardBeacon(sv1, 100, 3, "ETH", "0xnative")
	b := deriveForwardBeacon(sv2, 100, 3, "ETH", "0xnative")
	if bytes.Equal(a, b) {
		t.Fatal("expected different beacon when any future block hash changes")
	}
}

func TestDeriveForwardBeacon_NativeTxHashBinding(t *testing.T) {
	sv := newMemStateView()
	sv.Set(keys.KeyBlockHashByHeight(100), []byte("h1"))
	sv.Set(keys.KeyBlockHashByHeight(101), []byte("h2"))
	sv.Set(keys.KeyBlockHashByHeight(102), []byte("h3"))

	a := deriveForwardBeacon(sv, 100, 3, "ETH", "0xnativeA")
	b := deriveForwardBeacon(sv, 100, 3, "ETH", "0xnativeB")
	if bytes.Equal(a, b) {
		t.Fatal("native_tx_hash binding broken: same beacon for different native txs")
	}
}

                                                                             
func TestDeriveForwardBeacon_MissingFutureHashIsDeterministic(t *testing.T) {
	sv := newMemStateView()
	sv.Set(keys.KeyBlockHashByHeight(100), []byte("h1"))
	                  

	a := deriveForwardBeacon(sv, 100, 3, "ETH", "0xnative")
	b := deriveForwardBeacon(sv, 100, 3, "ETH", "0xnative")
	if !bytes.Equal(a, b) {
		t.Fatal("missing hash path should still be deterministic")
	}
}

                                                                       

                                                                                          
func seedBalance(sv StateView, addr, available, witnessLocked string) {
	SetBalance(sv, addr, "FB", &pb.TokenBalance{
		Balance:              available,
		WitnessLockedBalance: witnessLocked,
	})
}

                                                      
func readBalance(sv StateView, addr string) *pb.TokenBalance {
	return GetBalance(sv, addr, "FB")
}

func TestApplySlashAndRefundToState_SlashReducesWitnessLockedBalance(t *testing.T) {
	sv := newMemStateView()
	seedBalance(sv, "wrong1", "0", "1000000")

	outcome := &witness.ArbitrationVoteOutcome{
		SlashedWitnesses: []*pb.WitnessInfo{
			{Address: "wrong1", StakeAmount: "0", Status: pb.WitnessStatus_WITNESS_SLASHED, SlashedCount: 1},
		},
		SlashAmounts: map[string]string{"wrong1": "500000"},
	}

	if _, err := applySlashAndRefundToState(sv, outcome); err != nil {
		t.Fatal(err)
	}

	infoData, ok, _ := sv.Get(keys.KeyWitnessInfo("wrong1"))
	if !ok {
		t.Fatal("WitnessInfo not written")
	}
	var info pb.WitnessInfo
	if err := proto.Unmarshal(infoData, &info); err != nil {
		t.Fatal(err)
	}
	if info.Status != pb.WitnessStatus_WITNESS_SLASHED {
		t.Errorf("expected SLASHED, got %v", info.Status)
	}

	bal := readBalance(sv, "wrong1")
	if bal.WitnessLockedBalance != "500000" {
		t.Errorf("expected locked=500000 after slashing 500000, got %q", bal.WitnessLockedBalance)
	}
}

func TestApplySlashAndRefundToState_ChallengerRefundAddsToFBBalance(t *testing.T) {
	sv := newMemStateView()
	seedBalance(sv, "0xChallenger", "100000", "0")

	outcome := &witness.ArbitrationVoteOutcome{
		ChallengerAddr:   "0xChallenger",
		ChallengerRefund: "120000",                               
	}
	if _, err := applySlashAndRefundToState(sv, outcome); err != nil {
		t.Fatal(err)
	}

	bal := readBalance(sv, "0xChallenger")
	                           
	if bal.Balance != "220000" {
		t.Errorf("expected balance=220000, got %q", bal.Balance)
	}
}

func TestApplySlashAndRefundToState_SystemChallengerSkipsRefund(t *testing.T) {
	sv := newMemStateView()
	seedBalance(sv, "system", "0", "0")

	outcome := &witness.ArbitrationVoteOutcome{
		ChallengerAddr:   "system",
		ChallengerRefund: "120000",
	}
	if _, err := applySlashAndRefundToState(sv, outcome); err != nil {
		t.Fatal(err)
	}

	bal := readBalance(sv, "system")
	if bal.Balance != "0" {
		t.Errorf("system challenger should not receive refund, got %q", bal.Balance)
	}
}

func TestApplySlashAndRefundToState_SlashNeverNegative(t *testing.T) {
	                                                              
	sv := newMemStateView()
	seedBalance(sv, "wrong1", "0", "300000")

	outcome := &witness.ArbitrationVoteOutcome{
		SlashedWitnesses: []*pb.WitnessInfo{{Address: "wrong1", StakeAmount: "0", SlashedCount: 1}},
		SlashAmounts:     map[string]string{"wrong1": "500000"},                 
	}
	if _, err := applySlashAndRefundToState(sv, outcome); err != nil {
		t.Fatal(err)
	}

	bal := readBalance(sv, "wrong1")
	if bal.WitnessLockedBalance != "0" {
		t.Errorf("expected clamped to 0, got %q", bal.WitnessLockedBalance)
	}
}

func TestApplyRechargeRejectedReleasesNativeTxLock(t *testing.T) {
	sv := newMemStateView()
	req := &pb.RechargeRequest{ArbitrationProtocolVersion: 1, FeeEscrow: "0",
		RequestId:    "req_reject_release_native",
		Status:       pb.RechargeRequestStatus_RECHARGE_CHALLENGED,
		NativeChain:  "eth",
		NativeTxHash: "0xnative-retry",
	}
	data, err := proto.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyRechargeRequest(req.RequestId), data)
	nativeKey := keys.KeyRechargeRequestByNativeTx(req.NativeChain, req.NativeTxHash)
	sv.Set(nativeKey, []byte(req.RequestId))

	if err := applyRechargeRejected(sv, req, 1); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := sv.Get(nativeKey); ok {
		t.Fatal("rejected recharge should release native tx lock for retry/review")
	}
}

func TestApplyArbitrationTimeouts_ThreeRoundsThenFixedCourtRetries(t *testing.T) {
	cfg := witness.DefaultConfig()
	cfg.OrdinaryCommitteeSizes = []uint32{4, 8, 16}                           
	cfg.OrdinaryThresholdBps = 7500
	cfg.ArbitrationPeriodBlocks = 10000
	svc := witness.NewService(cfg)
	defer svc.Stop()
	sv := newMemStateView()
	seedStateSelectionWitnesses(t, sv, "arb1", "arb2", "arb3", "arb4", "arb5", "arb6", "arb7", "arb8")
	req := &pb.RechargeRequest{FeeEscrow: "0",
		RequestId: "req_fixed_court", Status: pb.RechargeRequestStatus_RECHARGE_CHALLENGED,
		ChallengeId: "ch_fixed_court", NativeChain: "eth", NativeTxHash: "0xfixed-court",
		ArbitrationProtocolVersion: 1,
	}
	initial := &pb.ChallengeRecord{ChallengeId: req.ChallengeId, RequestId: req.RequestId,
		Status: pb.ChallengeStatus_CHALLENGE_PENDING_SELECTION, SelectionHeight: 10,
		StakeAmount: "1000000000", RequiredStake: "1000000000"}
	x := &Executor{WitnessService: svc}
	ch, err := x.startArbitrationRoundFromState(sv, initial, req, 10)
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyChallengeTimeoutIndexBackfillDone(), []byte("1"))
	nativeKey := keys.KeyRechargeRequestByNativeTx(req.NativeChain, req.NativeTxHash)
	sv.Set(nativeKey, []byte(req.RequestId))
	var frozen []string
	var snapshotHeight uint64
	for step := 0; step < 6; step++ {
		oldRound, oldBallot, oldDeadline := ch.ArbitrationRound, ch.BallotRound, ch.DeadlineHeight
		                                                                         
		ch.ArbitrationVotes = []*pb.WitnessVote{}
		for _, a := range ch.Arbitrators[:2] {
			vote := &pb.WitnessVote{RequestId: req.RequestId, WitnessAddress: a,
				VoteType: pb.WitnessVoteType_VOTE_PASS, BallotRound: ch.BallotRound}
			ch.ArbitrationVotes = append(ch.ArbitrationVotes, vote)
			if ch.ArbitrationStage == pb.ArbitrationStage_ARBITRATION_ORDINARY {
				ch.OrdinaryVotes = witness.LastDirectionalPositions(ch.OrdinaryVotes, vote)
			}
			data, _ := proto.Marshal(vote)
			sv.Set(keys.KeyArbitrationVoteRound(ch.ChallengeId, ch.BallotRound, a), data)
		}
		ch.PassCount, ch.FailCount = 2, 0
		if err := setChallengeRecordWithTimeoutIndexes(sv, ch, cfg.BeaconMixBlocks, 0, 0); err != nil {
			t.Fatal(err)
		}
		before, _, _ := sv.Get(keys.KeyChallengeRecord(ch.ChallengeId))
		if err := x.applyArbitrationTimeouts(sv, oldDeadline); err != nil {
			t.Fatal(err)
		}
		atDeadline, _, _ := sv.Get(keys.KeyChallengeRecord(ch.ChallengeId))
		if !bytes.Equal(before, atDeadline) {
			t.Fatal("deadline block prematurely advanced the round")
		}
		if err := x.applyArbitrationTimeouts(sv, oldDeadline+1); err != nil {
			t.Fatal(err)
		}
		raw, ok, err := sv.Get(keys.KeyChallengeRecord(ch.ChallengeId))
		if err != nil || !ok {
			t.Fatalf("challenge missing: %v", err)
		}
		next := &pb.ChallengeRecord{}
		if err := proto.Unmarshal(raw, next); err != nil {
			t.Fatal(err)
		}
		if next.Finalized || next.Settled || next.Status != pb.ChallengeStatus_CHALLENGE_ACTIVE || next.StakeAmount != "1000000000" {
			t.Fatalf("timeout settled or lost principal: %+v", next)
		}
		if next.BallotRound != oldBallot+1 || next.DeadlineHeight != oldDeadline+10001 || len(next.ArbitrationVotes) != 0 || next.PassCount != 0 || next.FailCount != 0 {
			t.Fatalf("round/ballot/deadline transition wrong: %+v", next)
		}
		if step < 2 {
			if next.ArbitrationStage != pb.ArbitrationStage_ARBITRATION_ORDINARY || next.ArbitrationRound != oldRound+1 {
				t.Fatalf("ordinary expansion skipped a round: %+v", next)
			}
			wantQ := uint32(6)
			if step == 1 {
				wantQ = 12
			}
			if q, err := witness.ArbitrationQuorum(next); err != nil || q != wantQ {
				t.Fatalf("planned quorum lowered to availability: q=%d err=%v", q, err)
			}
		} else if next.ArbitrationStage != pb.ArbitrationStage_ARBITRATION_FINAL_COURT || !next.OrdinaryVotesFrozen {
			t.Fatalf("third timeout did not enter/follow final court: %+v", next)
		}
		if step == 0 && len(next.FinalCourt) != 0 {
			t.Fatal("court frozen before third round")
		}
		if step == 1 {
			frozen = append([]string(nil), next.FinalCourt...)
			snapshotHeight = oldDeadline + 1
			if !reflect.DeepEqual(frozen, []string{"arb1", "arb2", "arb3", "arb4"}) || next.CourtSnapshotHeight != snapshotHeight {
				t.Fatalf("third-round stake/address ranking snapshot wrong: %+v", next)
			}
			                                                                 
			seedStateSelectionWitnesses(t, sv, "new1", "new2", "new3", "new4")
			for _, a := range frozen {
				data, _, _ := sv.Get(keys.KeyWitnessInfo(a))
				info := &pb.WitnessInfo{}
				if err := proto.Unmarshal(data, info); err != nil {
					t.Fatal(err)
				}
				info.Status = pb.WitnessStatus_WITNESS_UNSTAKING
				data, _ = proto.Marshal(info)
				sv.Set(keys.KeyWitnessInfo(a), data)
			}
		}
		if step >= 2 && (!reflect.DeepEqual(next.Arbitrators, frozen) || !reflect.DeepEqual(next.FinalCourt, frozen) || next.CourtSnapshotHeight != snapshotHeight) {
			t.Fatalf("court membership drifted after selection: %+v", next)
		}
		for _, v := range ch.ArbitrationVotes {
			if _, ok, _ := sv.Get(keys.KeyArbitrationVoteRound(ch.ChallengeId, oldBallot, v.WitnessAddress)); ok {
				t.Fatal("old round vote key survived timeout")
			}
		}
		if _, ok, _ := sv.Get(keys.KeyChallengeTimeoutDue(oldDeadline+1, ch.ChallengeId)); ok {
			t.Fatal("old timeout index survived")
		}
		if raw, ok, _ := sv.Get(keys.KeyChallengeTimeoutDue(next.DeadlineHeight+1, ch.ChallengeId)); !ok || string(raw) != ch.ChallengeId {
			t.Fatal("next timeout index missing")
		}
		if _, ok, _ := sv.Get(keys.KeyArchiveChallengeRecord(ch.ChallengeId)); ok {
			t.Fatal("waiting arbitration was archived")
		}
		if raw, ok, _ := sv.Get(nativeKey); !ok || string(raw) != req.RequestId {
			t.Fatal("waiting arbitration released native transaction lock")
		}
		stored := loadRechargeRequestAtKey(sv, keys.KeyRechargeRequest(req.RequestId))
		if stored == nil || stored.Status != pb.RechargeRequestStatus_RECHARGE_ARBITRATION {
			t.Fatal("waiting recharge reached terminal status")
		}
		for _, a := range []string{"arb1", "arb2", "arb3", "arb4", "arb5", "arb6", "arb7", "arb8"} {
			if got := GetBalance(sv, a, "FB").WitnessLockedBalance; got != "10000000000" {
				t.Fatalf("offline member %s punished: %s", a, got)
			}
		}
		ch = next
	}
}

func TestApplyArbitrationTimeouts_BelowQuorumExpandsWithoutPerBlockRetry(t *testing.T) {
	cfg := witness.DefaultConfig()
	cfg.OrdinaryCommitteeSizes = []uint32{4, 8, 16}
	cfg.OrdinaryThresholdBps = 7500
	svc := witness.NewService(cfg)
	defer svc.Stop()
	sv := newMemStateView()
	seedStateSelectionWitnesses(t, sv, "arb1", "arb2", "arb3")
	req := &pb.RechargeRequest{FeeEscrow: "0", RequestId: "req_active_expand", ChallengeId: "ch_active_expand",
		Status: pb.RechargeRequestStatus_RECHARGE_CHALLENGED, ArbitrationProtocolVersion: 1}
	x := &Executor{WitnessService: svc}
	ch, err := x.startArbitrationRoundFromState(sv, &pb.ChallengeRecord{ChallengeId: req.ChallengeId, RequestId: req.RequestId}, req, 100)
	if err != nil {
		t.Fatal(err)
	}
	ch.ArbitrationVotes = []*pb.WitnessVote{{RequestId: req.RequestId, WitnessAddress: ch.Arbitrators[0],
		VoteType: pb.WitnessVoteType_VOTE_PASS, BallotRound: ch.BallotRound}}
	ch.OrdinaryVotes = witness.LastDirectionalPositions(nil, ch.ArbitrationVotes[0])
	ch.PassCount = 1
	oldDue := ch.DeadlineHeight + 1
	if err := setChallengeRecordWithTimeoutIndexes(sv, ch, cfg.BeaconMixBlocks, 0, 0); err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyChallengeTimeoutIndexBackfillDone(), []byte("1"))
	if err := x.applyArbitrationTimeouts(sv, oldDue); err != nil {
		t.Fatal(err)
	}
	raw, ok, _ := sv.Get(keys.KeyChallengeRecord(ch.ChallengeId))
	updated := &pb.ChallengeRecord{}
	if !ok {
		t.Fatal("challenge missing after expansion")
	}
	if err := proto.Unmarshal(raw, updated); err != nil {
		t.Fatal(err)
	}
	if updated.Status != pb.ChallengeStatus_CHALLENGE_ACTIVE || updated.ArbitrationRound != 2 || len(updated.Arbitrators) != 3 {
		t.Fatalf("ordinary expansion with fewer candidates is invalid: %+v", updated)
	}
	if q, err := witness.ArbitrationQuorum(updated); err != nil || q != 6 {
		t.Fatalf("planned eight-member quorum changed: %d, %v", q, err)
	}
	if updated.PassCount != 0 || updated.FailCount != 0 || len(updated.ArbitrationVotes) != 0 || len(updated.OrdinaryVotes) != 1 {
		t.Fatal("round reset lost stage positions or retained current ballots")
	}
	if _, ok, _ := sv.Get(keys.KeyChallengeTimeoutDue(oldDue, ch.ChallengeId)); ok {
		t.Fatal("current timeout index survived")
	}
	if raw, ok, _ := sv.Get(keys.KeyChallengeTimeoutDue(updated.DeadlineHeight+1, ch.ChallengeId)); !ok || string(raw) != ch.ChallengeId {
		t.Fatal("next full-period timeout missing")
	}
	if _, ok, _ := sv.Get(keys.KeyChallengeTimeoutDue(oldDue+1, ch.ChallengeId)); ok {
		t.Fatal("below-quorum case retried every block")
	}
}

func TestEnsureChallengeTimeoutDueIndexBackfillUsesRecordDueHeight(t *testing.T) {
	cfg := witness.DefaultConfig()
	svc := witness.NewService(cfg)
	defer svc.Stop()

	sv := newMemStateView()
	ch := &pb.ChallengeRecord{
		ChallengeId:    "ch_backfill_deterministic",
		RequestId:      "req_backfill_deterministic",
		Status:         pb.ChallengeStatus_CHALLENGE_ACTIVE,
		DeadlineHeight: 100,
		Arbitrators:    []string{"arb1", "arb2", "arb3"},
	}
	chData, err := proto.Marshal(ch)
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyChallengeRecord(ch.ChallengeId), chData)

	x := &Executor{WitnessService: svc}
	if err := x.ensureChallengeTimeoutDueIndex(sv, 200, svc.MaxShelveWaitBlocks()); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := sv.Get(keys.KeyChallengeTimeoutDue(101, ch.ChallengeId)); !ok {
		t.Fatal("expected backfill to use record deadline due height")
	}
	if _, ok, _ := sv.Get(keys.KeyChallengeTimeoutDue(200, ch.ChallengeId)); ok {
		t.Fatal("backfill must not encode local first-run height into due index")
	}
	if raw, ok, _ := sv.Get(keys.KeyChallengeTimeoutIndexBackfillDone()); !ok || string(raw) != "1" {
		t.Fatalf("expected deterministic backfill marker, ok=%v raw=%q", ok, string(raw))
	}
}

func TestWitnessChallengeTxHandler_PersistsActivatedChallengeWithoutServiceCache(t *testing.T) {
	cfg := witness.DefaultConfig()
	cfg.SelectionBeaconDepth = 6
	svc := witness.NewService(cfg)
	defer svc.Stop()
	sv := newMemStateView()
	seedStateSelectionWitnesses(t, sv, "w1", "w2", "w3", "w4")
	req := &pb.RechargeRequest{FeeEscrow: "0", RequestId: "req_challenge_persist", Status: pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD,
		DeadlineHeight: 42, SelectedWitnesses: []string{"w1", "w2", "w3", "w4"}, ArbitrationProtocolVersion: 1}
	assignments := []WitnessCaseAssignment{}
	for _, a := range req.SelectedWitnesses {
		assignments = append(assignments, WitnessCaseAssignment{Address: a, Role: WitnessCaseRoleOriginal, Amount: WitnessCaseBondRaw})
	}
	if err := reserveWitnessCaseCollateral(sv, req, assignments, cfg.MinStakeAmount); err != nil {
		t.Fatal(err)
	}
	if err := setRechargeRequestWithIndexes(sv, req, cfg.BeaconMixBlocks); err != nil {
		t.Fatal(err)
	}
	accData, err := proto.Marshal(&pb.Account{Address: "challenger"})
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyAccount("challenger"), accData)
	seedBalance(sv, "challenger", "100000000000", "0")
	handler := &WitnessChallengeTxHandler{witnessSvc: svc}
	tx := &pb.AnyTx{Content: &pb.AnyTx_WitnessChallengeTx{WitnessChallengeTx: &pb.WitnessChallengeTx{
		Base:      &pb.BaseMessage{TxId: "ch_persist_001", FromAddress: "challenger", ExecutedHeight: 26},
		RequestId: req.RequestId, StakeAmount: "1000000000", Reason: "test", Evidence: "evidence",
	}}}
	ops, receipt, err := handler.DryRun(tx, sv)
	if err != nil || receipt == nil || receipt.Status != "SUCCEED" {
		t.Fatalf("DryRun failed: receipt=%+v err=%v", receipt, err)
	}
	if len(ops) != 0 {
		t.Fatal("handler already wrote StateView but returned duplicate write operations")
	}
	stored := loadRechargeRequestAtKey(sv, keys.KeyRechargeRequest(req.RequestId))
	if stored == nil || stored.Status != pb.RechargeRequestStatus_RECHARGE_CHALLENGED || stored.ChallengeId != "ch_persist_001" {
		t.Fatalf("activated request not durably written: %+v", stored)
	}
	data, ok, err := sv.Get(keys.KeyChallengeRecord(stored.ChallengeId))
	if err != nil || !ok {
		t.Fatalf("challenge not durably written: %v", err)
	}
	ch := &pb.ChallengeRecord{}
	if err := proto.Unmarshal(data, ch); err != nil {
		t.Fatal(err)
	}
	if ch.SelectionHeight != 32 || ch.RequestId != req.RequestId || ch.Status != pb.ChallengeStatus_CHALLENGE_PENDING_SELECTION || ch.RequiredStake != "1000000000" || ch.StakeAmount != "1000000000" {
		t.Fatalf("activation used live 400 FB stake instead of reserved 40 FB or lost scheduling: %+v", ch)
	}
	if got := GetBalance(sv, "challenger", "FB").Balance; got != "99000000000" {
		t.Fatalf("principal not actually deducted: %s", got)
	}
	if _, ok, _ := sv.Get(keys.KeyRechargeChallengeDue(43, req.RequestId)); ok {
		t.Fatal("activated request retained publicity finalization index")
	}
	due := ch.SelectionHeight + cfg.BeaconMixBlocks - 1
	if raw, ok, _ := sv.Get(keys.KeyChallengeSelectionDue(due, ch.ChallengeId)); !ok || string(raw) != ch.ChallengeId {
		t.Fatal("activation selection index missing")
	}
	if _, ok, _ := sv.Get(keys.KeyChallengeContribution(ch.ChallengeId, "ch_persist_001")); !ok {
		t.Fatal("actual contribution missing")
	}
}
