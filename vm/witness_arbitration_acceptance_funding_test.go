package vm

import (
	"encoding/json"
	"fmt"
	"strconv"
	"testing"

	"dex/keys"
	"dex/pb"
	"dex/protocol"
	"dex/utils"
	"dex/witness"

	"google.golang.org/protobuf/proto"
)

func acceptanceFundingCheck(t *testing.T, label, expected, actual, unit string) {
	t.Helper()
	record, err := json.Marshal(struct {
		Label    string `json:"label"`
		Expected string `json:"expected"`
		Actual   string `json:"actual"`
		Unit     string `json:"unit"`
	}{label, expected, actual, unit})
	if err != nil {
		t.Fatal(err)
	}
	t.Log("DEX_ARBITRATION_CHECK " + string(record))
	if expected != actual {
		t.Fatalf("%s: expected %s, actual %s (%s)", label, expected, actual, unit)
	}
}

func acceptanceFundingPut(t *testing.T, sv StateView, key string, message proto.Message) {
	t.Helper()
	raw, err := proto.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(key, raw)
}

func acceptanceFundingRead(t *testing.T, sv StateView, key string, message proto.Message) {
	t.Helper()
	raw, exists, err := sv.Get(key)
	if err != nil || !exists {
		t.Fatalf("read %s: exists=%v err=%v", key, exists, err)
	}
	if err := proto.Unmarshal(raw, message); err != nil {
		t.Fatal(err)
	}
}

                                                                                     
                                                                                                            
                                                                                                        
func TestArbitrationAcceptanceUnderfundedRefund(t *testing.T) {
	const (
		requestID       = "acceptance-underfunded-request"
		challengeID     = "acceptance-underfunded-contribution"
		payer           = "acceptance-funding-payer"
		challenger      = "acceptance-funding-challenger"
		registration    = "10000000000"                                         
		serviceFee      = "3200000000"                                                             
		contribution    = "1000000000"                                                  
		initialFBSupply = "172000000000"
		externalDeposit = "12300"
	)
	cfg := witness.DefaultConfig()
	cfg.InitialWitnessCount = 16
	cfg.ChallengePeriodBlocks = 20
	svc := witness.NewService(cfg)
	t.Cleanup(svc.Stop)
	x := &Executor{WitnessService: svc}
	sv := newMemStateView()
	addresses := make([]string, 16)
	for i := range addresses {
		address := fmt.Sprintf("acceptance-funding-w%02d", i+1)
		addresses[i] = address
		acceptanceFundingPut(t, sv, keys.KeyAccount(address), &pb.Account{Address: address})
		acceptanceFundingPut(t, sv, keys.KeyWitnessInfo(address), &pb.WitnessInfo{
			Address: address, StakeAmount: registration, Status: pb.WitnessStatus_WITNESS_ACTIVE,
		})
		SetBalance(sv, address, FeeToken, &pb.TokenBalance{Balance: "0", WitnessLockedBalance: registration})
	}
	for address, amount := range map[string]string{payer: registration, challenger: "2000000000"} {
		acceptanceFundingPut(t, sv, keys.KeyAccount(address), &pb.Account{Address: address})
		SetBalance(sv, address, FeeToken, &pb.TokenBalance{Balance: amount})
	}
	acceptanceFundingPut(t, sv, keys.KeyToken(FeeToken), &pb.Token{Address: FeeToken, TotalSupply: initialFBSupply})
	putTestVaultConfig(t, sv, "eth", 1)
	putTestVaultState(t, sv, "eth", 0, VaultStatusActive)
	receiver := utils.GenerateTestIdentity(98716).Address
	sv, initialRead := settlementBaseline(sv)

	                                                                                          
	requestTx := &pb.AnyTx{Content: &pb.AnyTx_WitnessRequestTx{WitnessRequestTx: &pb.WitnessRequestTx{
		Base:        &pb.BaseMessage{TxId: requestID, FromAddress: payer, ExecutedHeight: 10, ChainId: accountRechargeTestChainID},
		NativeChain: "ETH", NativeTxHash: accountRechargeTestHash(requestID),
		NativeContract: "0x1f9840a85d5af5bf1d1762f925bdaddc4201f984",
		Amount:         externalDeposit, ReceiverAddress: receiver, RechargeFee: serviceFee,
		DepositProtocol: protocol.DepositProtocolVault,
	}}}
	ops, receipt, err := (&WitnessRequestTxHandler{witnessSvc: svc}).DryRun(requestTx, sv)
	if err != nil || receipt == nil || receipt.Status != "SUCCEED" || len(ops) != 0 {
		t.Fatalf("request handler: receipt=%+v writes=%d err=%v", receipt, len(ops), err)
	}
	acceptanceFundingCheck(t, "funding.fee.payer_after_prepayment", "6800000000", GetBalance(sv, payer, FeeToken).Balance, "FB_raw")
	acceptanceFundingCheck(t, "funding.requester_bond.held", "0", GetBalance(sv, WitnessRequesterBondPoolAccount, FeeToken).Balance, "FB_raw")
	acceptanceFundingCheck(t, "funding.fee.escrow_before_verdict", serviceFee, GetBalance(sv, WitnessFeePoolAccount, FeeToken).Balance, "FB_raw")
	req := loadRechargeRequestAtKey(sv, keys.KeyRechargeRequest(requestID))
	height := rechargeSelectionReadyHeight(req, cfg.BeaconMixBlocks)
	for h := req.SelectionHeight; h <= height; h++ {
		sv.Set(keys.KeyBlockHashByHeight(h), []byte(fmt.Sprintf("funding-beacon-%d", h)))
	}
	if err := x.applyPendingRechargeSelections(sv, height); err != nil {
		t.Fatal(err)
	}
	req = loadRechargeRequestAtKey(sv, keys.KeyRechargeRequest(requestID))
	acceptanceFundingCheck(t, "funding.original.committee_size", "16", strconv.Itoa(len(req.SelectedWitnesses)), "addresses")
	acceptanceFundingCheck(t, "funding.original.status", pb.RechargeRequestStatus_RECHARGE_VOTING.String(), req.Status.String(), "status")
	acceptanceFundingCheck(t, "funding.original.collateral_entries", "16", strconv.Itoa(len(req.CaseCollateral)), "addresses")
	byAddress := make(map[string]*pb.WitnessCaseCollateral, len(req.CaseCollateral))
	for _, bond := range req.CaseCollateral {
		if bond == nil || byAddress[bond.Address] != nil {
			t.Fatal("original collateral contains a missing or duplicate address")
		}
		byAddress[bond.Address] = bond
	}
	for _, address := range req.SelectedWitnesses {
		bond := byAddress[address]
		if bond == nil {
			t.Fatalf("selected original witness %s has no case collateral", address)
		}
		acceptanceFundingCheck(t, "funding.original.quote_basis."+address, contribution, bond.OriginalAmount, "FB_raw")
		acceptanceFundingCheck(t, "funding.original.case_reserved."+address, contribution, bond.ReservedAmount, "FB_raw")
		var info pb.WitnessInfo
		acceptanceFundingRead(t, sv, keys.KeyWitnessInfo(address), &info)
		acceptanceFundingCheck(t, "funding.original.account_reserved."+address, contribution, info.ReservedStakeAmount, "FB_raw")
		acceptanceFundingCheck(t, "funding.original.open_duty."+address, "1", strconv.FormatUint(info.ActiveCaseCount, 10), "cases")
	}

	                                                                                      
	for _, address := range req.SelectedWitnesses {
		voteTx := &pb.AnyTx{Content: &pb.AnyTx_WitnessVoteTx{WitnessVoteTx: &pb.WitnessVoteTx{
			Base: &pb.BaseMessage{TxId: "funding-vote-" + address, FromAddress: address, ExecutedHeight: height},
			Vote: &pb.WitnessVote{RequestId: requestID, WitnessAddress: address, VoteType: pb.WitnessVoteType_VOTE_PASS, Round: req.Round},
		}}}
		writes, receipt, err := (&WitnessVoteTxHandler{witnessSvc: svc}).DryRun(voteTx, sv)
		if err != nil || receipt == nil || receipt.Status != "SUCCEED" {
			t.Fatalf("original vote %s: receipt=%+v err=%v", address, receipt, err)
		}
		for _, op := range writes {
			if err := applyWriteOpWithRechargeIndexes(sv, op, cfg.BeaconMixBlocks, cfg.ChallengePeriodBlocks, height); err != nil {
				t.Fatal(err)
			}
		}
	}
	req = loadRechargeRequestAtKey(sv, keys.KeyRechargeRequest(requestID))
	acceptanceFundingCheck(t, "funding.original.pass_votes", "16", strconv.FormatUint(uint64(req.PassCount), 10), "votes")
	if req.VoteAcceptUntilHeight <= height {
		t.Fatal("expected a pending vote-grace due height")
	}
	if err := x.applyRechargeVoteGraceClosures(sv, req.VoteAcceptUntilHeight, cfg.BeaconMixBlocks); err != nil {
		t.Fatal(err)
	}
	req = loadRechargeRequestAtKey(sv, keys.KeyRechargeRequest(requestID))
	deadline := req.DeadlineHeight
	acceptanceFundingCheck(t, "funding.publicity.status", pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD.String(), req.Status.String(), "status")

	fundTx := &pb.AnyTx{Content: &pb.AnyTx_WitnessChallengeTx{WitnessChallengeTx: &pb.WitnessChallengeTx{
		Base:      &pb.BaseMessage{TxId: challengeID, FromAddress: challenger, ExecutedHeight: deadline},
		RequestId: requestID, StakeAmount: contribution,
	}}}
	ops, receipt, err = (&WitnessChallengeTxHandler{witnessSvc: svc}).DryRun(fundTx, sv)
	if err != nil || receipt == nil || receipt.Status != "SUCCEED" || len(ops) != 0 {
		t.Fatalf("funding handler: receipt=%+v writes=%d err=%v", receipt, len(ops), err)
	}
	ch := loadChallengeRecordAtKey(sv, keys.KeyChallengeRecord(challengeID))
	acceptanceFundingCheck(t, "funding.threshold", "1600000000", ch.RequiredStake, "FB_raw")
	acceptanceFundingCheck(t, "funding.contribution", contribution, ch.StakeAmount, "FB_raw")
	acceptanceFundingCheck(t, "funding.state_before_deadline", pb.ChallengeStatus_CHALLENGE_FUNDING.String(), ch.Status.String(), "status")
	_, dueExists, err := sv.Get(keys.KeyRechargeChallengeDue(deadline+1, requestID))
	if err != nil {
		t.Fatal(err)
	}
	acceptanceFundingCheck(t, "funding.publicity_due_index", "true", strconv.FormatBool(dueExists), "exists")

	                                                                                                   
	if err := x.applyRechargeChallengeFinalizations(sv, deadline, cfg.BeaconMixBlocks); err != nil {
		t.Fatal(err)
	}
	acceptanceFundingCheck(t, "funding.deadline.principal_not_refunded", contribution, GetBalance(sv, challenger, FeeToken).Balance, "FB_raw")
	acceptanceFundingCheck(t, "funding.deadline.deposit_not_credited", "0", GetBalance(sv, receiver, req.TokenAddress).Balance, "asset_raw")
	acceptanceFundingCheck(t, "funding.deadline.fee_still_escrowed", serviceFee, GetBalance(sv, WitnessFeePoolAccount, FeeToken).Balance, "FB_raw")
	if err := x.applyRechargeChallengeFinalizations(sv, deadline+1, cfg.BeaconMixBlocks); err != nil {
		t.Fatal(err)
	}
	closed := loadRechargeRequestAtKey(sv, keys.KeyRechargeRequest(requestID))
	ch = loadChallengeRecordAtKey(sv, keys.KeyChallengeRecord(challengeID))
	acceptanceFundingCheck(t, "funding.refund.challenger_balance", "2000000000", GetBalance(sv, challenger, FeeToken).Balance, "FB_raw")
	acceptanceFundingCheck(t, "funding.refund.remaining_principal", "0", ch.StakeAmount, "FB_raw")
	acceptanceFundingCheck(t, "funding.refund.settled", "true", strconv.FormatBool(ch.Settled), "boolean")
	acceptanceFundingCheck(t, "funding.request.final_status", pb.RechargeRequestStatus_RECHARGE_FINALIZED.String(), closed.Status.String(), "status")
	acceptanceFundingCheck(t, "funding.request.receiver_credit", externalDeposit, GetBalance(sv, receiver, req.TokenAddress).Balance, "asset_raw")
	acceptanceFundingCheck(t, "funding.fee.escrow_after_settlement", "0", GetBalance(sv, WitnessFeePoolAccount, FeeToken).Balance, "FB_raw")
	acceptanceFundingCheck(t, "funding.fee.settled", "true", strconv.FormatBool(closed.FeeSettled), "boolean")
	for _, address := range addresses {
		var info pb.WitnessInfo
		acceptanceFundingRead(t, sv, keys.KeyWitnessInfo(address), &info)
		acceptanceFundingCheck(t, "funding.fee.reward."+address, "200000000", GetBalance(sv, address, FeeToken).Balance, "FB_raw")
		acceptanceFundingCheck(t, "funding.collateral.released."+address, "0", info.ReservedStakeAmount, "FB_raw")
		acceptanceFundingCheck(t, "funding.duties.closed."+address, "0", strconv.FormatUint(info.ActiveCaseCount, 10), "cases")
		acceptanceFundingCheck(t, "funding.collateral.not_slashed."+address, registration, GetBalance(sv, address, FeeToken).WitnessLockedBalance, "FB_raw")
	}
	var token pb.Token
	acceptanceFundingRead(t, sv, keys.KeyToken(FeeToken), &token)
	acceptanceFundingCheck(t, "funding.FB_supply_unchanged", initialFBSupply, token.TotalSupply, "FB_raw")
	AuditGlobalConservationDiff(sv.Diff(), deadline+1, initialRead)
	acceptanceFundingCheck(t, "funding.global_conservation", "passed", "passed", "audit")

	beforeReplay := sv.Snapshot()
	for _, at := range []uint64{deadline + 1, deadline + 2} {
		if err := x.applyRechargeChallengeFinalizations(sv, at, cfg.BeaconMixBlocks); err != nil {
			t.Fatal(err)
		}
	}
	acceptanceFundingCheck(t, "funding.repeated_due.no_new_writes", strconv.Itoa(beforeReplay), strconv.Itoa(sv.Snapshot()), "state_writes")
	acceptanceFundingCheck(t, "funding.repeated_due.no_second_refund", "2000000000", GetBalance(sv, challenger, FeeToken).Balance, "FB_raw")
	acceptanceFundingCheck(t, "funding.repeated_due.no_second_credit", externalDeposit, GetBalance(sv, receiver, req.TokenAddress).Balance, "asset_raw")
}
