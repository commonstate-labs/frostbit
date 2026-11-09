package vm

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
	"testing"

	"dex/keys"
	"dex/pb"
	"dex/protocol"
	"dex/rent"
	statedb "dex/stateDB"
	"dex/utils"
	"dex/witness"

	"google.golang.org/protobuf/proto"
)

                                                                             
                                                                              
func formalRewardFixture(t *testing.T, id string, gross int64, witnesses []string, receiverIsRequester bool) (StateView, ReadThroughFn, *pb.RechargeRequest) {
	t.Helper()
	sv, read, req := requesterBondFixture(t, id)
	req.Amount = fmt.Sprint(gross)
	req.RechargeFee = fmt.Sprint(protocol.MinWitnessRechargeFeeRaw)
	req.Status = pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD
	if receiverIsRequester {
		req.ReceiverAddress = req.RequesterAddress
	}
	for _, address := range witnesses {
		req.OriginalPositions = append(req.OriginalPositions, &pb.WitnessVote{WitnessAddress: address, VoteType: pb.WitnessVoteType_VOTE_PASS})
	}
	if err := persistRequesterBondState(sv, req); err != nil {
		t.Fatal(err)
	}
	if err := collectWitnessServiceFee(sv, req, true); err != nil {
		t.Fatal(err)
	}
	return sv, read, req
}

func formalRewardSupply(t *testing.T, sv StateView, asset string) string {
	t.Helper()
	raw, exists, err := sv.Get(keys.KeyToken(asset))
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		return "0"
	}
	var token pb.Token
	if err := proto.Unmarshal(raw, &token); err != nil {
		t.Fatal(err)
	}
	return token.TotalSupply
}

func formalRewardLedger(t *testing.T, sv StateView, req *pb.RechargeRequest) (string, string) {
	t.Helper()
	available, _, err := sv.Get(keys.KeyFrostVaultAvailableBalance(req.NativeChain, req.TokenAddress, req.VaultId))
	if err != nil {
		t.Fatal(err)
	}
	credited, _, err := sv.Get(keys.KeyFrostVaultFundsLedger(req.NativeChain, req.TokenAddress, req.VaultId))
	if err != nil {
		t.Fatal(err)
	}
	return string(available), string(credited)
}

func TestFormalRechargeRewardsGrossPayoutDifferential(t *testing.T) {
	witnessA := utils.GenerateTestIdentity(7105).Address
	witnessB := utils.GenerateTestIdentity(7106).Address
	for _, tc := range []struct {
		name      string
		gross     int64
		witnesses []string
		sameOwner bool
	}{
		{"tiny-zero-share", 9999, nil, false},
		{"one-unit-two-witnesses-owner-receiver-overlap", 10000, []string{witnessB, witnessA}, true},
		{"nineteen-thousand-one-witness", 19999, []string{witnessA}, false},
		{"twenty-thousand-two-witnesses", 20000, []string{witnessB, witnessA}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sv, read, req := formalRewardFixture(t, "formal_reward_"+tc.name, tc.gross, tc.witnesses, tc.sameOwner)
			share := tc.gross / 10000
			receiverNet := tc.gross - 2*share
			want := map[string]int64{req.ReceiverAddress: receiverNet}
			want[req.RequesterAddress] += share
			ordered := append([]string(nil), tc.witnesses...)
			sort.Strings(ordered)
			for i, address := range ordered {
				part := share / int64(len(ordered))
				if int64(i) < share%int64(len(ordered)) {
					part++
				}
				want[address] += part
			}
			if err := applyRechargeFinalized(sv, req, req.FinalizeHeight, 1); err != nil {
				t.Fatal(err)
			}
			var allocated int64
			for address, expected := range want {
				got := GetBalance(sv, address, req.TokenAddress).Balance
				if got != fmt.Sprint(expected) {
					t.Fatalf("recipient=%s balance=%s want=%d", address, got, expected)
				}
				attributionRaw, exists, err := sv.Get(keys.KeyRechargeRewardOwner(address, req.RequestId))
				if err != nil {
					t.Fatal(err)
				}
				if expected == 0 {
					if exists {
						t.Fatalf("zero-payout owner %s acquired attribution", address)
					}
				} else {
					if !exists {
						t.Fatalf("paid owner %s lacks attribution", address)
					}
					var row pb.RechargeRewardAttribution
					if err := proto.Unmarshal(attributionRaw, &row); err != nil {
						t.Fatal(err)
					}
					if row.RequestId != req.RequestId || row.TokenAddress != req.TokenAddress {
						t.Fatalf("wrong attribution identity for %s: %+v", address, &row)
					}
					parts := new(big.Int)
					for _, raw := range []string{row.ReceiverNetAmount, row.RequesterRewardAmount, row.WitnessRewardAmount} {
						if raw != "" {
							part, ok := new(big.Int).SetString(raw, 10)
							if !ok {
								t.Fatalf("invalid attribution %q", raw)
							}
							parts.Add(parts, part)
						}
					}
					if parts.Cmp(big.NewInt(expected)) != 0 {
						t.Fatalf("owner %s attribution sum=%s actual=%d", address, parts, expected)
					}
				}
				allocated += expected
			}
			if allocated != tc.gross {
				t.Fatalf("actual allocations sum=%d gross=%d", allocated, tc.gross)
			}
			if got := formalRewardSupply(t, sv, req.TokenAddress); got != fmt.Sprint(tc.gross) {
				t.Fatalf("supply=%s gross=%d", got, tc.gross)
			}
			available, credited := formalRewardLedger(t, sv, req)
			if available != fmt.Sprint(tc.gross) || credited != fmt.Sprint(tc.gross) {
				t.Fatalf("Vault available=%s credited=%s gross=%d", available, credited, tc.gross)
			}
			if _, exists, _ := sv.Get(keys.KeyRechargeCredited(req.RequestId)); !exists {
				t.Fatal("missing exact-once credit marker")
			}
			stored := requesterBondStored(t, sv, req.RequestId)
			if stored.GrossAmount != fmt.Sprint(tc.gross) || stored.ReceiverNetAmount != fmt.Sprint(receiverNet) ||
				stored.RequesterRewardAmount != fmt.Sprint(share) || stored.WitnessRewardAmount != fmt.Sprint(share) {
				t.Fatalf("persisted reward summary=%+v", stored)
			}
			if got := stored.RequesterBondStatus; got != pb.RequesterBondStatus_REQUESTER_BOND_RETURNED {
				t.Fatalf("bond status=%s", got)
			}
			AuditGlobalConservationDiff(sv.Diff(), req.FinalizeHeight, read)
			before := sv.Diff()
			if err := applyRechargeFinalized(sv, req, req.FinalizeHeight+1, 1); err != nil {
				t.Fatal(err)
			}
			if err := applyRechargeRejected(sv, req, 1); err != nil {
				t.Fatal(err)
			}
			if len(sv.Diff()) != len(before) {
				t.Fatalf("terminal replay added state writes: before=%d after=%d", len(before), len(sv.Diff()))
			}
		})
	}
}

func TestFormalRechargeRewardsFailClosedDifferential(t *testing.T) {
	witness := utils.GenerateTestIdentity(7105).Address
	for _, tc := range []struct {
		name   string
		mutate func(*pb.RechargeRequest)
	}{
		{"missing-policy", func(req *pb.RechargeRequest) { req.EconomicsPolicy = 0 }},
		{"wrong-policy", func(req *pb.RechargeRequest) { req.EconomicsPolicy = protocol.RechargeEconomicsLegacy }},
		{"unknown-policy", func(req *pb.RechargeRequest) { req.EconomicsPolicy = 99 }},
		{"missing-bond-amount", func(req *pb.RechargeRequest) { req.RequesterBondAmount = "" }},
		{"missing-bond-status", func(req *pb.RechargeRequest) { req.RequesterBondStatus = pb.RequesterBondStatus_REQUESTER_BOND_NONE }},
		{"missing-bond-both", func(req *pb.RechargeRequest) {
			req.RequesterBondAmount = ""
			req.RequesterBondStatus = pb.RequesterBondStatus_REQUESTER_BOND_NONE
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sv, _, req := formalRewardFixture(t, "formal_reward_invalid_"+tc.name, 10000, []string{witness}, false)
			stored := requesterBondStored(t, sv, req.RequestId)
			tc.mutate(stored)
			if err := persistRequesterBondState(sv, stored); err != nil {
				t.Fatal(err)
			}
			if err := applyRechargeFinalized(sv, req, req.FinalizeHeight, 1); err == nil {
				t.Fatal("invalid launch policy/bond credited")
			}
			failed := proto.Clone(req).(*pb.RechargeRequest)
			failed.Status = pb.RechargeRequestStatus_RECHARGE_REJECTED
			if err := applyRechargeRejected(sv, failed, 1); err == nil {
				t.Fatal("invalid launch policy/bond settled FAIL")
			}
			if got := formalRewardSupply(t, sv, req.TokenAddress); got != "0" {
				t.Fatalf("invalid request minted supply=%s", got)
			}
			available, credited := formalRewardLedger(t, sv, req)
			if available != "" || credited != "" {
				t.Fatalf("invalid request changed Vault available=%s credited=%s", available, credited)
			}
			if _, exists, _ := sv.Get(keys.KeyRechargeCredited(req.RequestId)); exists {
				t.Fatal("invalid request wrote credited marker")
			}
			if got := GetBalance(sv, WitnessRequesterBondPoolAccount, FeeToken).Balance; got != "1000000000" {
				t.Fatalf("invalid request moved bond pool=%s", got)
			}
		})
	}
	t.Run("trusted-chain-context-mismatch", func(t *testing.T) {
		sv, _, req := formalRewardFixture(t, "formal_reward_wrong_chain", 10000, []string{witness}, false)
		sv.(*overlayStateView).BindNetworkChainID(1)
		if err := applyRechargeFinalized(sv, req, req.FinalizeHeight, 1); err == nil {
			t.Fatal("launch policy credited under a legacy trusted chain ID")
		}
		if formalRewardSupply(t, sv, req.TokenAddress) != "0" {
			t.Fatal("cross-chain policy mismatch minted tokens")
		}
	})
	for _, gross := range []int64{10000, 20000} {
		t.Run(fmt.Sprintf("positive-share-no-witness-%d", gross), func(t *testing.T) {
			sv, _, req := formalRewardFixture(t, fmt.Sprintf("formal_reward_empty_%d", gross), gross, nil, false)
			if err := applyRechargeFinalized(sv, req, req.FinalizeHeight, 1); err == nil {
				t.Fatal("positive witness reward silently discarded")
			}
			if formalRewardSupply(t, sv, req.TokenAddress) != "0" || requesterBondStored(t, sv, req.RequestId).FeeSettled {
				t.Fatal("failed allocation left partial token or FB settlement")
			}
			if got := GetBalance(sv, WitnessFeePoolAccount, FeeToken).Balance; got != "1000000" {
				t.Fatalf("fee pool changed after rollback=%s", got)
			}
		})
	}
}

func TestFormalRechargeRewardsAttributionConflictRollsBackAllMoney(t *testing.T) {
	witnessAddr := utils.GenerateTestIdentity(7105).Address
	sv, _, req := formalRewardFixture(t, "formal_reward_attribution_conflict", 20000, []string{witnessAddr}, false)
	owner := req.ReceiverAddress
	sv.Set(keys.KeyRechargeRewardOwner(owner, req.RequestId), []byte("conflict"))
	feeBefore := GetBalance(sv, WitnessFeePoolAccount, FeeToken).Balance
	if err := applyRechargeFinalized(sv, req, req.FinalizeHeight, 1); err == nil {
		t.Fatal("preexisting reward attribution did not abort finalization")
	}
	if got := formalRewardSupply(t, sv, req.TokenAddress); got != "0" {
		t.Fatalf("failed finalization minted supply=%s", got)
	}
	available, credited := formalRewardLedger(t, sv, req)
	if available != "" || credited != "" {
		t.Fatalf("failed finalization changed Vault available=%s credited=%s", available, credited)
	}
	if got := GetBalance(sv, WitnessFeePoolAccount, FeeToken).Balance; got != feeBefore {
		t.Fatalf("failed finalization moved FB fee=%s before=%s", got, feeBefore)
	}
	if got := GetBalance(sv, WitnessRequesterBondPoolAccount, FeeToken).Balance; got != "1000000000" {
		t.Fatalf("failed finalization moved bond=%s", got)
	}
	if _, exists, _ := sv.Get(keys.KeyRechargeCredited(req.RequestId)); exists {
		t.Fatal("failed finalization wrote credited marker")
	}
}

func TestFormalRechargeRewardsColdAttributionLossCannotChangeMoney(t *testing.T) {
	witnessAddr := utils.GenerateTestIdentity(7105).Address
	sv, read, req := formalRewardFixture(t, "formal_reward_cold_index_loss", 20000, []string{witnessAddr}, false)
	if err := applyRechargeFinalized(sv, req, req.FinalizeHeight, 1); err != nil {
		t.Fatal(err)
	}
	ownerIndex := keys.KeyRechargeRewardOwner(req.ReceiverAddress, req.RequestId)
	if _, exists, err := sv.Get(ownerIndex); err != nil || !exists {
		t.Fatalf("expected launch owner attribution before loss: exists=%v err=%v", exists, err)
	}
	                                                                            
	                                                                   
	sv.Del(ownerIndex)
	if _, exists, err := sv.Get(ownerIndex); err != nil || exists {
		t.Fatalf("cold index deletion failed: exists=%v err=%v", exists, err)
	}
	if err := applyRechargeFinalized(sv, req, req.FinalizeHeight+1, 1); err != nil {
		t.Fatal(err)
	}
	if got := GetBalance(sv, req.ReceiverAddress, req.TokenAddress).Balance; got != "19996" {
		t.Fatalf("cold index loss changed receiver money=%s", got)
	}
	if got := GetBalance(sv, req.RequesterAddress, req.TokenAddress).Balance; got != "2" {
		t.Fatalf("cold index loss changed requester money=%s", got)
	}
	if got := GetBalance(sv, witnessAddr, req.TokenAddress).Balance; got != "2" {
		t.Fatalf("cold index loss changed witness money=%s", got)
	}
	if got := formalRewardSupply(t, sv, req.TokenAddress); got != "20000" {
		t.Fatalf("cold index loss changed supply=%s", got)
	}
	available, credited := formalRewardLedger(t, sv, req)
	if available != "20000" || credited != "20000" {
		t.Fatalf("cold index loss changed Vault available=%s credited=%s", available, credited)
	}
	stored := requesterBondStored(t, sv, req.RequestId)
	if stored.GrossAmount != "20000" || stored.ReceiverNetAmount != "19996" ||
		stored.RequesterRewardAmount != "2" || stored.WitnessRewardAmount != "2" {
		t.Fatalf("cold index loss changed canonical summary: %+v", stored)
	}
	if _, exists, err := sv.Get(keys.KeyRechargeCredited(req.RequestId)); err != nil || !exists {
		t.Fatalf("cold index loss removed credit marker: exists=%v err=%v", exists, err)
	}
	AuditGlobalConservationDiff(sv.Diff(), req.FinalizeHeight+1, read)
}

func TestFormalRechargeRewardsFinalFailAndRecoveredBond(t *testing.T) {
	witness := utils.GenerateTestIdentity(7105).Address
	t.Run("final-fail-zero-bridged-mint", func(t *testing.T) {
		sv, read, req := formalRewardFixture(t, "formal_reward_fail", 20000, []string{witness}, false)
		failed := proto.Clone(req).(*pb.RechargeRequest)
		failed.Status = pb.RechargeRequestStatus_RECHARGE_REJECTED
		if err := applyRechargeRejected(sv, failed, 1); err != nil {
			t.Fatal(err)
		}
		if formalRewardSupply(t, sv, req.TokenAddress) != "0" {
			t.Fatal("final FAIL minted bridged token")
		}
		if got := requesterBondStored(t, sv, req.RequestId).RequesterBondStatus; got != pb.RequesterBondStatus_REQUESTER_BOND_FORFEITED {
			t.Fatalf("final FAIL bond=%s", got)
		}
		AuditGlobalConservationDiff(sv.Diff(), req.FinalizeHeight, read)
	})
	t.Run("rent-recovered-bond-still-receives-bridge-reward", func(t *testing.T) {
		sv, read, req := formalRewardFixture(t, "formal_reward_recovered", 10000, []string{witness}, false)
		claim := keys.KeyAssetClaim(req.RequesterAddress, FeeToken, rentClaimRequesterBond, req.RequestId)
		if err := (vmRentCleaner{sv: sv}).PrepareAccountSweepClaimPage(req.RequesterAddress, 40, map[string][]byte{claim: []byte(req.RequesterBondAmount)}); err != nil {
			t.Fatal(err)
		}
		ownerFBBefore := GetBalance(sv, req.RequesterAddress, FeeToken).Balance
		if err := applyRechargeFinalized(sv, req, req.FinalizeHeight, 1); err != nil {
			t.Fatal(err)
		}
		if got := GetBalance(sv, req.RequesterAddress, FeeToken).Balance; got != ownerFBBefore {
			t.Fatalf("recovered bond refunded again: %s -> %s", ownerFBBefore, got)
		}
		if got := GetBalance(sv, req.RequesterAddress, req.TokenAddress).Balance; got != "1" {
			t.Fatalf("requester reward after rent=%s", got)
		}
		if got := GetBalance(sv, rent.RecycleFundAccount, FeeToken).Balance; got != "1000000000" {
			t.Fatalf("bond rent recycle=%s", got)
		}
		AuditGlobalConservationDiff(sv.Diff(), req.FinalizeHeight, read)
	})
}

func TestFormalRechargeRewardsFrozenArbitrationEligibility(t *testing.T) {
	wrong := utils.GenerateTestIdentity(7105).Address
	correct := utils.GenerateTestIdentity(7106).Address
	sv, read, req := formalRewardFixture(t, "formal_reward_frozen", 20000, nil, false)
	req.Status = pb.RechargeRequestStatus_RECHARGE_CHALLENGED
	req.OriginalPositions = []*pb.WitnessVote{{WitnessAddress: wrong, VoteType: pb.WitnessVoteType_VOTE_FAIL}}
	if err := persistRequesterBondState(sv, req); err != nil {
		t.Fatal(err)
	}
	if err := applyRechargeFinalized(sv, req, req.FinalizeHeight, 1); err == nil {
		t.Fatal("challenged PASS accepted without frozen arbitration eligibility")
	}
	if err := applyRechargeFinalized(sv, req, req.FinalizeHeight, 1, []string{correct, correct}); err != nil {
		t.Fatal(err)
	}
	if got := GetBalance(sv, wrong, req.TokenAddress).Balance; got != "0" {
		t.Fatalf("wrong original voter received reward=%s", got)
	}
	if got := GetBalance(sv, correct, req.TokenAddress).Balance; got != "2" {
		t.Fatalf("correct frozen voter reward=%s", got)
	}
	AuditGlobalConservationDiff(sv.Diff(), req.FinalizeHeight, read)
}

func TestFormalRechargeRewardsUnchallengedOutcomeUsesCanonicalOriginalVotes(t *testing.T) {
	witnessAddr := utils.GenerateTestIdentity(7105).Address
	sv, read, req := formalRewardFixture(t, "formal_reward_unchallenged_outcome", 10000, []string{witnessAddr}, false)
	final := proto.Clone(req).(*pb.RechargeRequest)
	final.Status = pb.RechargeRequestStatus_RECHARGE_FINALIZED
	if err := applyArbitrationOutcomeToState(sv, &witness.ArbitrationVoteOutcome{Request: final}, 1, 0, req.FinalizeHeight); err != nil {
		t.Fatal(err)
	}
	if got := GetBalance(sv, witnessAddr, req.TokenAddress).Balance; got != "1" {
		t.Fatalf("correct original witness reward=%s", got)
	}
	AuditGlobalConservationDiff(sv.Diff(), req.FinalizeHeight, read)
}

func TestFormalRechargeRewardsChallengedPassUsesFrozenCourtEligibility(t *testing.T) {
	sv, read, outcome := settlementFixture(t, true)
	sv.(*overlayStateView).BindNetworkChainID(51025)
	canonical := requesterBondStored(t, sv, outcome.Request.RequestId)
	asset := testBridgeAsset(t, "eth", "0x1f9840a85d5af5bf1d1762f925bdaddc4201f984")
	putTestVaultConfig(t, sv, "eth", 8)
	canonical.NativeChain, canonical.NativeTxHash = "eth", accountRechargeTestHash(canonical.RequestId)
	canonical.NativeContract, canonical.TokenAddress = asset.Contract, asset.ID
	canonical.Amount, canonical.ReceiverAddress = "20000", utils.GenerateTestIdentity(7103).Address
	canonical.DepositProtocol, canonical.VaultId = protocol.DepositProtocolVault, 7
	canonical.EconomicsPolicy = protocol.RechargeEconomicsLaunchV1
	sv.Set(keys.KeyRechargeEventPending(canonical.NativeChain, canonical.NativeTxHash, canonical.DepositEventIndex), []byte(canonical.RequestId))
	bond := big.NewInt(protocol.RequesterBondRaw)
	if err := witnessSettlementMove(sv, canonical.RequesterAddress, bond, false, "formal_fund_requester", 1, canonical.RequestId); err != nil {
		t.Fatal(err)
	}
	if _, err := adjustTokenTotalSupplyForAudit(sv, FeeToken, bond); err != nil {
		t.Fatal(err)
	}
	if err := witnessSettlementMove(sv, canonical.RequesterAddress, bond, true, "formal_lock_requester", 1, canonical.RequestId); err != nil {
		t.Fatal(err)
	}
	if err := witnessSettlementMove(sv, WitnessRequesterBondPoolAccount, bond, false, "formal_bond_pool", 1, canonical.RequestId); err != nil {
		t.Fatal(err)
	}
	if err := rent.SetClaim(sv, canonical.RequesterAddress, FeeToken, rentClaimRequesterBond, canonical.RequestId, bond); err != nil {
		t.Fatal(err)
	}
	canonical.RequesterBondAmount, canonical.RequesterBondStatus = bond.String(), pb.RequesterBondStatus_REQUESTER_BOND_HELD
	if err := persistRequesterBondState(sv, canonical); err != nil {
		t.Fatal(err)
	}
	final := outcome.Request
	final.NativeChain, final.NativeTxHash = canonical.NativeChain, canonical.NativeTxHash
	final.NativeContract, final.TokenAddress = canonical.NativeContract, canonical.TokenAddress
	final.Amount, final.ReceiverAddress = canonical.Amount, canonical.ReceiverAddress
	final.DepositProtocol, final.VaultId = canonical.DepositProtocol, canonical.VaultId
	final.Status = pb.RechargeRequestStatus_RECHARGE_FINALIZED
	                                                                          
	                                                                           
	final.EconomicsPolicy = 0
	final.RequesterBondAmount = ""
	final.RequesterBondStatus = pb.RequesterBondStatus_REQUESTER_BOND_NONE
	outcome.Challenge.FinalVote = pb.WitnessVoteType_VOTE_PASS
	outcome.Challenge.ArbitrationStage = pb.ArbitrationStage_ARBITRATION_FINAL_COURT
	outcome.Challenge.FinalCourt = []string{"right-court"}
	outcome.Challenge.ArbitrationVotes = []*pb.WitnessVote{settlementVote("right-court", pb.WitnessVoteType_VOTE_PASS)}
	if err := applyArbitrationOutcomeToState(sv, outcome, 1, 0, 50); err != nil {
		t.Fatal(err)
	}
	if got := GetBalance(sv, "right-court", asset.ID).Balance; got != "2" {
		t.Fatalf("correct frozen court reward=%s", got)
	}
	for _, wrong := range []string{"wrong", "good", "court"} {
		if got := GetBalance(sv, wrong, asset.ID).Balance; got != "0" {
			t.Fatalf("wrong frozen voter %s received bridge reward=%s", wrong, got)
		}
	}
	if got := GetBalance(sv, canonical.RequesterAddress, asset.ID).Balance; got != "2" {
		t.Fatalf("requester reward=%s", got)
	}
	if got := GetBalance(sv, canonical.ReceiverAddress, asset.ID).Balance; got != "19996" {
		t.Fatalf("receiver net=%s", got)
	}
	if got := formalRewardSupply(t, sv, asset.ID); got != "20000" {
		t.Fatalf("bridge supply=%s", got)
	}
	if got := requesterBondStored(t, sv, canonical.RequestId).EconomicsPolicy; got != protocol.RechargeEconomicsLaunchV1 {
		t.Fatalf("stale event overwrote canonical policy=%d", got)
	}
	AuditGlobalConservationDiff(sv.Diff(), 50, read)
}

func TestFormalRechargeRewardsLegacyChainUnchanged(t *testing.T) {
	sv := NewMockStateView()
	req := seedAccountRechargeFinalizationTest(t, sv, "formal_reward_legacy", pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD)
	req.Amount = "20000"
	req.EconomicsPolicy = protocol.RechargeEconomicsLegacy
	data, err := proto.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyRechargeRequest(req.RequestId), data)
	if err := applyRechargeFinalized(sv, req, req.FinalizeHeight, 1); err != nil {
		t.Fatal(err)
	}
	if got := GetBalance(sv, req.ReceiverAddress, req.TokenAddress).Balance; got != "20000" {
		t.Fatalf("legacy receiver balance=%s", got)
	}
	if got := GetBalance(sv, req.RequesterAddress, req.TokenAddress).Balance; got != "0" {
		t.Fatalf("legacy requester reward=%s", got)
	}
	stored := requesterBondStored(t, sv, req.RequestId)
	if stored.EconomicsPolicy != 0 || stored.GrossAmount != "" || stored.ReceiverNetAmount != "" ||
		stored.RequesterRewardAmount != "" || stored.WitnessRewardAmount != "" {
		t.Fatalf("legacy terminal acquired launch-only protobuf fields: %+v", stored)
	}
	for _, address := range []string{req.ReceiverAddress, req.RequesterAddress} {
		if row, exists, err := sv.Get(keys.KeyRechargeRewardOwner(address, req.RequestId)); err != nil || exists {
			t.Fatalf("legacy owner %s acquired launch-only reward index: exists=%v row=%q err=%v", address, exists, row, err)
		}
	}
}

func TestFormalRechargeRewardsSplitKernelConservesHugeGross(t *testing.T) {
	req := &pb.RechargeRequest{
		RequesterAddress: "a", ReceiverAddress: "a", EconomicsPolicy: protocol.RechargeEconomicsLaunchV1,
		OriginalPositions: []*pb.WitnessVote{{WitnessAddress: "a", VoteType: pb.WitnessVoteType_VOTE_PASS}, {WitnessAddress: "b", VoteType: pb.WitnessVoteType_VOTE_PASS}},
	}
	gross := new(big.Int).Lsh(big.NewInt(1), 192)
	plan, err := rechargeCreditPayouts(req, gross)
	if err != nil {
		t.Fatal(err)
	}
	wantShare := new(big.Int).Quo(gross, big.NewInt(10000))
	if want := new(big.Int).Sub(gross, new(big.Int).Mul(wantShare, big.NewInt(2))); plan.ReceiverNet.Cmp(want) != 0 {
		t.Fatalf("net=%s want=%s", plan.ReceiverNet, want)
	}
	total := new(big.Int)
	for _, amount := range plan.Payouts {
		total.Add(total, amount)
	}
	if total.Cmp(gross) != 0 {
		t.Fatalf("huge gross payout sum=%s want=%s", total, gross)
	}
}

func TestFormalRechargeRewardsStateViewCostIndependentOfOtherRequests(t *testing.T) {
	witnessAddr := utils.GenerateTestIdentity(7105).Address
	for _, n := range []int{63, 64, 65, 5000} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			base := make(map[string][]byte, n)
			noise, err := proto.Marshal(&pb.RechargeRequest{RequestId: "unrelated", Status: pb.RechargeRequestStatus_RECHARGE_PENDING_SELECTION})
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < n; i++ {
				base[keys.KeyRechargeRequest(fmt.Sprintf("unrelated-%06d", i))] = noise
			}
			reads, scans := 0, 0
			var scanPrefixes []string
			read := func(key string) ([]byte, error) {
				reads++
				if raw, ok := base[key]; ok {
					return raw, nil
				}
				return nil, statedb.ErrNotFound
			}
			scan := func(prefix string) (map[string][]byte, error) {
				scans++
				scanPrefixes = append(scanPrefixes, prefix)
				out := make(map[string][]byte)
				for key, raw := range base {
					if strings.HasPrefix(key, prefix) {
						out[key] = raw
					}
				}
				return out, nil
			}
			sv := NewStateView(read, scan)
			sv.(*overlayStateView).BindNetworkChainID(51025)
			for i := 0; i < n; i++ {
				sv.Del(keys.KeyRechargeRequest(fmt.Sprintf("unrelated-%06d", i)))
			}
			owner := utils.GenerateTestIdentity(7104).Address
			SetBalance(sv, owner, FeeToken, &pb.TokenBalance{Balance: "2000000000"})
			req := seedAccountRechargeFinalizationTest(t, sv, fmt.Sprintf("formal_reward_scale_%d", n), pb.RechargeRequestStatus_RECHARGE_PENDING_SELECTION)
			req.EconomicsPolicy = protocol.RechargeEconomicsLaunchV1
			req.Amount = "20000"
			req.RechargeFee = "1000000"
			req.OriginalPositions = []*pb.WitnessVote{{WitnessAddress: witnessAddr, VoteType: pb.WitnessVoteType_VOTE_PASS}}
			if err := persistRequesterBondState(sv, req); err != nil {
				t.Fatal(err)
			}
			if err := collectRequesterBond(sv, req); err != nil {
				t.Fatal(err)
			}
			if err := collectWitnessServiceFee(sv, req, true); err != nil {
				t.Fatal(err)
			}
			req.Status = pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD
			if err := persistRequesterBondState(sv, req); err != nil {
				t.Fatal(err)
			}
			beforeReads, beforeScans := reads, scans
			if err := applyRechargeFinalized(sv, req, req.FinalizeHeight, 1); err != nil {
				t.Fatal(err)
			}
			deltaReads, deltaScans := reads-beforeReads, scans-beforeScans
			for _, prefix := range scanPrefixes[beforeScans:] {
				if strings.HasPrefix(keys.KeyRechargeRequest("unrelated-000001"), prefix) {
					t.Fatalf("R=%d finalization scanned unrelated request prefix %q", n, prefix)
				}
			}
			if deltaScans > 8 || deltaReads > 120 {
				t.Fatalf("R=%d finalization base reads=%d prefix scans=%d; expected O(1) per fixed recipient count", n, deltaReads, deltaScans)
			}
			t.Logf("R=%d base reads=%d request-scoped prefix scans=%d", n, deltaReads, deltaScans)

			                                                               
			                                                            
			legacy := NewStateView(read, scan)
			legacy.(*overlayStateView).BindNetworkChainID(1)
			for i := 0; i < n; i++ {
				legacy.Del(keys.KeyRechargeRequest(fmt.Sprintf("unrelated-%06d", i)))
			}
			legacyReq := seedAccountRechargeFinalizationTest(t, legacy, fmt.Sprintf("formal_reward_legacy_scale_%d", n), pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD)
			legacyReq.Amount = "20000"
			if err := persistRequesterBondState(legacy, legacyReq); err != nil {
				t.Fatal(err)
			}
			legacyReadsBefore, legacyScansBefore := reads, scans
			if err := applyRechargeFinalized(legacy, legacyReq, legacyReq.FinalizeHeight, 1); err != nil {
				t.Fatal(err)
			}
			legacyReads, legacyScans := reads-legacyReadsBefore, scans-legacyScansBefore
			for _, prefix := range scanPrefixes[legacyScansBefore:] {
				if strings.HasPrefix(keys.KeyRechargeRequest("unrelated-000001"), prefix) {
					t.Fatalf("R=%d legacy finalization scanned unrelated request prefix %q", n, prefix)
				}
			}
			if legacyScans > 8 || legacyReads > 120 {
				t.Fatalf("R=%d legacy finalization base reads=%d prefix scans=%d", n, legacyReads, legacyScans)
			}
			t.Logf("R=%d legacy base reads=%d request-scoped prefix scans=%d; launch delta reads=%d scans=%d", n, legacyReads, legacyScans, deltaReads-legacyReads, deltaScans-legacyScans)
		})
	}
}
