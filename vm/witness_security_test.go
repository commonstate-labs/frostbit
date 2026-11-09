package vm_test

import (
	"testing"

	"dex/keys"
	"dex/pb"
	"dex/vm"
	"dex/witness"

	"google.golang.org/protobuf/proto"
)

func putWitnessSecurityState(t *testing.T, db *MockDB, key string, msg proto.Message) {
	t.Helper()
	data, err := proto.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal %s: %v", key, err)
	}
	db.data[key] = data
}

func TestWitnessStakeRejectDoesNotCreateGhostWitness(t *testing.T) {
	db := NewMockDB()
	address := "fbc1ghost"
	putWitnessSecurityState(t, db, keys.KeyAccount(address), &pb.Account{Address: address})
	putWitnessSecurityState(t, db, keys.KeyBalance(address, "FB"), &pb.TokenBalanceRecord{
		Balance: &pb.TokenBalance{Balance: "1", WitnessLockedBalance: "0"},
	})

	svc := witness.NewService(witness.DefaultConfig())
	handler := &vm.WitnessStakeTxHandler{}
	handler.SetWitnessService(svc)
	tx := &pb.AnyTx{Content: &pb.AnyTx_WitnessStakeTx{WitnessStakeTx: &pb.WitnessStakeTx{
		Base:   &pb.BaseMessage{TxId: "stake_insufficient", FromAddress: address, ExecutedHeight: 10},
		Amount: svc.ConfigSnapshot().MinStakeAmount,
		Op:     pb.OrderOp_ADD,
	}}}

	ops, receipt, _ := handler.DryRun(tx, vm.NewStateView(db.Get, db.Scan))
	if receipt == nil || receipt.Status != "FAILED" || receipt.Error != "insufficient balance" {
		t.Fatalf("expected insufficient-balance failure, got receipt=%+v", receipt)
	}
	if len(ops) != 0 {
		t.Fatalf("failed stake must not produce writes, got %d", len(ops))
	}
	if info, err := svc.GetWitnessInfo(address); err == nil {
		t.Fatalf("failed stake created ghost witness: %+v", info)
	}
}

func TestWitnessUnstakePreservesOldCaseDutyUntilClaim(t *testing.T) {
	db := NewMockDB()
	address := "fbc1pending"
	stakeAmount := witness.DefaultConfig().MinStakeAmount
	putWitnessSecurityState(t, db, keys.KeyAccount(address), &pb.Account{Address: address})
	putWitnessSecurityState(t, db, keys.KeyBalance(address, "FB"), &pb.TokenBalanceRecord{
		Balance: &pb.TokenBalance{Balance: "0", WitnessLockedBalance: stakeAmount},
	})
	putWitnessSecurityState(t, db, keys.KeyWitnessInfo(address), &pb.WitnessInfo{
		Address: address, StakeAmount: stakeAmount, Status: pb.WitnessStatus_WITNESS_ACTIVE, PendingTasks: []string{"request-1"},
		ReservedStakeAmount: "1000000000", ActiveCaseCount: 1,
	})

	svc := witness.NewService(witness.DefaultConfig())
	svc.LoadWitness(&pb.WitnessInfo{Address: address, StakeAmount: stakeAmount, Status: pb.WitnessStatus_WITNESS_ACTIVE})
	handler := &vm.WitnessStakeTxHandler{}
	handler.SetWitnessService(svc)
	tx := &pb.AnyTx{Content: &pb.AnyTx_WitnessStakeTx{WitnessStakeTx: &pb.WitnessStakeTx{
		Base:   &pb.BaseMessage{TxId: "unstake_pending", FromAddress: address, ExecutedHeight: 11},
		Amount: "1",
		Op:     pb.OrderOp_REMOVE,
	}}}

	sv := vm.NewStateView(db.Get, db.Scan)
	ops, receipt, err := handler.DryRun(tx, sv)
	if err != nil || receipt == nil || receipt.Status != "SUCCEED" {
		t.Fatalf("exit request should allow old duties: receipt=%+v err=%v", receipt, err)
	}
	if len(ops) != 0 {
		t.Fatalf("direct state writes should not be replayed, got %d returned writes", len(ops))
	}
	info, err := svc.GetWitnessInfo(address)
	if err != nil {
		t.Fatalf("get witness: %v", err)
	}
	if info.Status != pb.WitnessStatus_WITNESS_UNSTAKING || info.ActiveCaseCount != 1 ||
		info.ReservedStakeAmount != "1000000000" || len(info.PendingTasks) != 1 {
		t.Fatalf("exit request erased old responsibility: %+v", info)
	}
	var persisted pb.WitnessInfo
	raw, _, _ := sv.Get(keys.KeyWitnessInfo(address))
	if err := proto.Unmarshal(raw, &persisted); err != nil || !proto.Equal(&persisted, info) {
		t.Fatalf("exit responsibility was not persisted: %+v err=%v", &persisted, err)
	}
	claimHandler := &vm.WitnessClaimStakeTxHandler{UnstakeLockBlocks: 1}
	claimHandler.SetWitnessService(svc)
	claimTx := &pb.AnyTx{Content: &pb.AnyTx_WitnessClaimStakeTx{WitnessClaimStakeTx: &pb.WitnessClaimStakeTx{
		Base: &pb.BaseMessage{TxId: "claim_old_case", FromAddress: address, ExecutedHeight: 100},
	}}}
	revision := sv.Revision()
	_, claimReceipt, claimErr := claimHandler.DryRun(claimTx, sv)
	if claimErr != nil || claimReceipt == nil || claimReceipt.Status != "FAILED" ||
		claimReceipt.Error != "witness has unfinished case responsibilities" || sv.Revision() != revision {
		t.Fatalf("old case did not block claim without mutations: receipt=%+v err=%v", claimReceipt, claimErr)
	}
	after, _ := svc.GetWitnessInfo(address)
	if !proto.Equal(after, info) || vm.GetBalance(sv, address, "FB").WitnessLockedBalance != stakeAmount {
		t.Fatal("failed claim changed old case responsibility or locked funds")
	}
}

func TestWitnessVoteRejectsSignerMismatch(t *testing.T) {
	db := NewMockDB()
	request := &pb.RechargeRequest{
		RequestId:         "request-signer-mismatch",
		Status:            pb.RechargeRequestStatus_RECHARGE_VOTING,
		DeadlineHeight:    200,
		SelectedWitnesses: []string{"fbc1victim", "fbc1witness2", "fbc1witness3"},
	}
	putWitnessSecurityState(t, db, keys.KeyRechargeRequest(request.RequestId), request)

	svc := witness.NewService(witness.DefaultConfig())
	svc.SetCurrentHeight(100)
	svc.LoadRequest(proto.Clone(request).(*pb.RechargeRequest))
	handler := &vm.WitnessVoteTxHandler{}
	handler.SetWitnessService(svc)
	tx := &pb.AnyTx{Content: &pb.AnyTx_WitnessVoteTx{WitnessVoteTx: &pb.WitnessVoteTx{
		Base: &pb.BaseMessage{TxId: "forged-witness-vote", FromAddress: "fbc1attacker", ExecutedHeight: 100},
		Vote: &pb.WitnessVote{RequestId: request.RequestId, WitnessAddress: "fbc1victim", VoteType: pb.WitnessVoteType_VOTE_PASS},
	}}}

	ops, receipt, err := handler.DryRun(tx, vm.NewStateView(db.Get, db.Scan))
	if err != nil {
		t.Fatalf("signer mismatch should be a business rejection: %v", err)
	}
	if receipt == nil || receipt.Status != "FAILED" || receipt.Error != "vote signer does not match witness address" {
		t.Fatalf("expected signer-mismatch failure, got receipt=%+v", receipt)
	}
	if len(ops) != 0 {
		t.Fatalf("forged vote must not produce writes, got %d", len(ops))
	}
	stored, err := svc.GetRequest(request.RequestId)
	if err != nil {
		t.Fatalf("get request: %v", err)
	}
	if len(stored.Votes) != 0 || stored.PassCount != 0 {
		t.Fatalf("forged vote mutated request: votes=%d pass=%d", len(stored.Votes), stored.PassCount)
	}
}

func TestWitnessVoteRoundRejectsStaleVoteAndAllowsOldWitnessToRevote(t *testing.T) {
	db := NewMockDB()
	request := &pb.RechargeRequest{
		RequestId:         "request-round-1",
		Status:            pb.RechargeRequestStatus_RECHARGE_VOTING,
		Round:             1,
		DeadlineHeight:    200,
		SelectedWitnesses: []string{"w1", "w2", "w3", "w4"},
	}
	putWitnessSecurityState(t, db, keys.KeyRechargeRequest(request.RequestId), request)
	putWitnessSecurityState(t, db, keys.KeyWitnessVote(request.RequestId, "w1"), &pb.WitnessVote{
		RequestId: request.RequestId, WitnessAddress: "w1", VoteType: pb.WitnessVoteType_VOTE_PASS, Round: 0,
	})

	newHandler := func() *vm.WitnessVoteTxHandler {
		svc := witness.NewService(witness.DefaultConfig())
		svc.LoadRequest(proto.Clone(request).(*pb.RechargeRequest))
		handler := &vm.WitnessVoteTxHandler{}
		handler.SetWitnessService(svc)
		return handler
	}
	newTx := func(id string, round uint32) *pb.AnyTx {
		return &pb.AnyTx{Content: &pb.AnyTx_WitnessVoteTx{WitnessVoteTx: &pb.WitnessVoteTx{
			Base: &pb.BaseMessage{TxId: id, FromAddress: "w1", ExecutedHeight: 100},
			Vote: &pb.WitnessVote{RequestId: request.RequestId, WitnessAddress: "w1", VoteType: pb.WitnessVoteType_VOTE_PASS, Round: round},
		}}}
	}

	if ops, receipt, err := newHandler().DryRun(newTx("stale-round", 0), vm.NewStateView(db.Get, db.Scan)); err != nil ||
		receipt == nil || receipt.Status != "FAILED" || receipt.Error != witness.ErrWitnessVoteRoundMismatch.Error() || len(ops) != 0 {
		t.Fatalf("stale vote result ops=%d receipt=%+v err=%v", len(ops), receipt, err)
	}
	if ops, receipt, err := newHandler().DryRun(newTx("current-round", 1), vm.NewStateView(db.Get, db.Scan)); err != nil ||
		receipt == nil || receipt.Status != "SUCCEED" || len(ops) == 0 {
		t.Fatalf("reused witness vote result ops=%d receipt=%+v err=%v", len(ops), receipt, err)
	}
}

func TestWitnessVotePersistsStatsAcrossRestart(t *testing.T) {
	db := NewMockDB()
	const address = "fbc1stats"
	putWitnessSecurityState(t, db, keys.KeyWitnessInfo(address), &pb.WitnessInfo{
		Address:       address,
		StakeAmount:   witness.DefaultConfig().MinStakeAmount,
		PendingReward: "777",
		Status:        pb.WitnessStatus_WITNESS_ACTIVE,
	})

	tests := []struct {
		requestID string
		voteType  pb.WitnessVoteType
		pass      uint64
		fail      uint64
		abstain   uint64
	}{
		{requestID: "request-stats-pass", voteType: pb.WitnessVoteType_VOTE_PASS, pass: 1},
		{requestID: "request-stats-fail", voteType: pb.WitnessVoteType_VOTE_FAIL, pass: 1, fail: 1},
		{requestID: "request-stats-abstain", voteType: pb.WitnessVoteType_VOTE_ABSTAIN, pass: 1, fail: 1, abstain: 1},
	}

	for i, tc := range tests {
		request := &pb.RechargeRequest{
			RequestId:         tc.requestID,
			Status:            pb.RechargeRequestStatus_RECHARGE_VOTING,
			Round:             1,
			DeadlineHeight:    200,
			SelectedWitnesses: []string{address, "w2", "w3", "w4"},
		}
		putWitnessSecurityState(t, db, keys.KeyRechargeRequest(tc.requestID), request)

		                                                                           
		svc := witness.NewService(witness.DefaultConfig())
		handler := &vm.WitnessVoteTxHandler{}
		handler.SetWitnessService(svc)
		tx := &pb.AnyTx{Content: &pb.AnyTx_WitnessVoteTx{WitnessVoteTx: &pb.WitnessVoteTx{
			Base: &pb.BaseMessage{TxId: tc.requestID + "-tx", FromAddress: address, ExecutedHeight: 100},
			Vote: &pb.WitnessVote{RequestId: tc.requestID, WitnessAddress: address, VoteType: tc.voteType, Round: 1},
		}}}

		ops, receipt, err := handler.DryRun(tx, vm.NewStateView(db.Get, db.Scan))
		if err != nil || receipt == nil || receipt.Status != "SUCCEED" {
			t.Fatalf("vote %d failed: receipt=%+v err=%v", i, receipt, err)
		}
		for _, op := range ops {
			if op.Del {
				delete(db.data, op.Key)
			} else {
				db.data[op.Key] = append([]byte(nil), op.Value...)
			}
		}

		var stored pb.WitnessInfo
		if err := proto.Unmarshal(db.data[keys.KeyWitnessInfo(address)], &stored); err != nil {
			t.Fatalf("unmarshal persisted witness stats: %v", err)
		}
		if stored.TotalWitnessCount != uint64(i+1) || stored.PassCount != tc.pass ||
			stored.FailCount != tc.fail || stored.AbstainCount != tc.abstain || stored.PendingReward != "777" {
			t.Fatalf("unexpected persisted witness stats after vote %d: %+v", i, &stored)
		}
	}

	                                                                             
	last := tests[len(tests)-1]
	svc := witness.NewService(witness.DefaultConfig())
	handler := &vm.WitnessVoteTxHandler{}
	handler.SetWitnessService(svc)
	duplicate := &pb.AnyTx{Content: &pb.AnyTx_WitnessVoteTx{WitnessVoteTx: &pb.WitnessVoteTx{
		Base: &pb.BaseMessage{TxId: "duplicate-stats-tx", FromAddress: address, ExecutedHeight: 101},
		Vote: &pb.WitnessVote{RequestId: last.requestID, WitnessAddress: address, VoteType: last.voteType, Round: 1},
	}}}
	ops, receipt, err := handler.DryRun(duplicate, vm.NewStateView(db.Get, db.Scan))
	if err != nil || receipt == nil || receipt.Status != "FAILED" || len(ops) != 0 {
		t.Fatalf("duplicate vote result ops=%d receipt=%+v err=%v", len(ops), receipt, err)
	}
	var stored pb.WitnessInfo
	if err := proto.Unmarshal(db.data[keys.KeyWitnessInfo(address)], &stored); err != nil {
		t.Fatalf("unmarshal witness stats after duplicate: %v", err)
	}
	if stored.TotalWitnessCount != 3 || stored.PassCount != 1 || stored.FailCount != 1 || stored.AbstainCount != 1 {
		t.Fatalf("duplicate vote changed persisted witness stats: %+v", &stored)
	}
}

func TestArbitrationVoteRejectsSignerMismatch(t *testing.T) {
	db := NewMockDB()
	challenge := &pb.ChallengeRecord{
		ChallengeId:        "challenge-signer-mismatch",
		RequestId:          "request-1",
		Status:             pb.ChallengeStatus_CHALLENGE_ACTIVE,
		DeadlineHeight:     200,
		ConsensusThreshold: 100,
		Arbitrators:        []string{"fbc1victim", "fbc1arbiter2", "fbc1arbiter3"},
	}
	putWitnessSecurityState(t, db, keys.KeyChallengeRecord(challenge.ChallengeId), challenge)

	svc := witness.NewService(witness.DefaultConfig())
	svc.SetCurrentHeight(100)
	svc.LoadChallenge(proto.Clone(challenge).(*pb.ChallengeRecord))
	handler := &vm.ArbitrationVoteTxHandler{}
	handler.SetWitnessService(svc)
	tx := &pb.AnyTx{Content: &pb.AnyTx_ArbitrationVoteTx{ArbitrationVoteTx: &pb.ArbitrationVoteTx{
		Base:        &pb.BaseMessage{TxId: "forged-arbitration-vote", FromAddress: "fbc1attacker", ExecutedHeight: 100},
		ChallengeId: challenge.ChallengeId,
		Vote:        &pb.WitnessVote{RequestId: challenge.RequestId, WitnessAddress: "fbc1victim", VoteType: pb.WitnessVoteType_VOTE_PASS},
	}}}

	ops, receipt, err := handler.DryRun(tx, vm.NewStateView(db.Get, db.Scan))
	if err != nil {
		t.Fatalf("signer mismatch should be a business rejection: %v", err)
	}
	if receipt == nil || receipt.Status != "FAILED" || receipt.Error != "vote signer does not match arbitrator address" {
		t.Fatalf("expected signer-mismatch failure, got receipt=%+v", receipt)
	}
	if len(ops) != 0 {
		t.Fatalf("forged arbitration vote must not produce writes, got %d", len(ops))
	}
	stored, err := svc.GetChallenge(challenge.ChallengeId)
	if err != nil {
		t.Fatalf("get challenge: %v", err)
	}
	if len(stored.ArbitrationVotes) != 0 || stored.PassCount != 0 {
		t.Fatalf("forged arbitration vote mutated challenge: votes=%d pass=%d", len(stored.ArbitrationVotes), stored.PassCount)
	}
}

func TestVoteHandlersRejectUnknownEnumWithoutWrites(t *testing.T) {
	const invalidType = pb.WitnessVoteType(127)
	cases := []struct {
		name   string
		tx     *pb.AnyTx
		dryRun func(*pb.AnyTx, vm.StateView) ([]vm.WriteOp, *vm.Receipt, error)
	}{
		{
			name: "witness vote",
			tx: &pb.AnyTx{Content: &pb.AnyTx_WitnessVoteTx{WitnessVoteTx: &pb.WitnessVoteTx{
				Base: &pb.BaseMessage{TxId: "invalid-witness-vote-enum", FromAddress: "witness1"},
				Vote: &pb.WitnessVote{RequestId: "request1", WitnessAddress: "witness1", VoteType: invalidType},
			}}},
			dryRun: (&vm.WitnessVoteTxHandler{}).DryRun,
		},
		{
			name: "arbitration vote",
			tx: &pb.AnyTx{Content: &pb.AnyTx_ArbitrationVoteTx{ArbitrationVoteTx: &pb.ArbitrationVoteTx{
				Base:        &pb.BaseMessage{TxId: "invalid-arbitration-vote-enum", FromAddress: "arb1"},
				ChallengeId: "challenge1",
				Vote:        &pb.WitnessVote{RequestId: "request1", WitnessAddress: "arb1", VoteType: invalidType},
			}}},
			dryRun: (&vm.ArbitrationVoteTxHandler{}).DryRun,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := NewMockDB()
			ops, receipt, err := tc.dryRun(tc.tx, vm.NewStateView(db.Get, db.Scan))
			if err != nil {
				t.Fatalf("unknown enum should be a business rejection: %v", err)
			}
			if receipt == nil || receipt.Status != "FAILED" || receipt.Error != "invalid vote type" {
				t.Fatalf("unexpected unknown-enum receipt: %+v", receipt)
			}
			if len(ops) != 0 {
				t.Fatalf("unknown enum produced %d writes", len(ops))
			}
			if len(db.data) != 0 {
				t.Fatalf("unknown enum mutated database: %+v", db.data)
			}
		})
	}
}
