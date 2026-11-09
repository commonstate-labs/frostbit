package vm

import (
	"encoding/json"
	"fmt"
	"math/big"
	"testing"

	"dex/keys"
	"dex/pb"
	"dex/witness"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

                                                                               
                                                                                  
                                                                                
                                                                               
                                                                             
                                                                                 
                                                                             
                                                                          
func TestArbitrationAcceptanceChallengeSuccess(t *testing.T) {
	c := newArbitrationAcceptanceCase(t)
	c.decide(t, pb.WitnessVoteType_VOTE_FAIL)

	                                                                            
	                                                                             
	                                                                            
	                                                                      
	checkArbitrationAcceptance(t, "success.principal_refund", "1000000000", c.ledgerAmount(t, "challenger", "witness_challenge_principal_refund"), "FB_raw")
	checkArbitrationAcceptance(t, "success.contributor_pool_45pct", "2250000000", c.ledgerAmount(t, "challenger", "witness_challenge_contributor_reward"), "FB_raw")
	checkArbitrationAcceptance(t, "success.voter_pool_45pct", "2250000000", c.ledgerTotal(t, "witness_correct_vote_reward"), "FB_raw")
	checkArbitrationAcceptance(t, "success.failed_award_pool", "0", c.ledgerTotal(t, "witness_failed_challenge_award"), "FB_raw")
	checkArbitrationAcceptance(t, "success.challenger_balance", "4250000000", GetBalance(c.sv, "challenger", FeeToken).Balance, "FB_raw")
	for i, address := range c.ordinary[:3] {
		fee, balance := "34", "750100034"
		if i == 0 {
			fee, balance = "35", "750100035"
		}
		checkArbitrationAcceptance(t, "success."+address+".voter_reward", "750000000", c.ledgerAmount(t, address, "witness_correct_vote_reward"), "FB_raw")
		checkArbitrationAcceptance(t, "success."+address+".fee", fee, c.ledgerAmount(t, address, "witness_service_fee"), "FB_raw")
		checkArbitrationAcceptance(t, "success."+address+".balance", balance, GetBalance(c.sv, address, FeeToken).Balance, "FB_raw")
	}
	wrong := append(append([]string(nil), c.original...), "arb-d")
	for _, address := range wrong {
		checkArbitrationAcceptance(t, "success."+address+".balance", "100000", GetBalance(c.sv, address, FeeToken).Balance, "FB_raw")
		checkArbitrationAcceptance(t, "success."+address+".fee", "0", c.ledgerAmount(t, address, "witness_service_fee"), "FB_raw")
	}
	c.assertFinal(t, true, "5000000000", "500000000", "81500801000", wrong)
}

func TestArbitrationAcceptanceChallengeFailure(t *testing.T) {
	c := newArbitrationAcceptanceCase(t)
	c.decide(t, pb.WitnessVoteType_VOTE_PASS)

	                                                                            
	                                                                         
	                                                                            
	                                                                               
	checkArbitrationAcceptance(t, "failure.principal_refund", "0", c.ledgerAmount(t, "challenger", "witness_challenge_principal_refund"), "FB_raw")
	checkArbitrationAcceptance(t, "failure.opponent_pool_principal_plus_fine", "2000000000", c.ledgerTotal(t, "witness_failed_challenge_award"), "FB_raw")
	checkArbitrationAcceptance(t, "failure.contributor_pool", "0", c.ledgerTotal(t, "witness_challenge_contributor_reward"), "FB_raw")
	checkArbitrationAcceptance(t, "failure.voter_pool", "0", c.ledgerTotal(t, "witness_correct_vote_reward"), "FB_raw")
	checkArbitrationAcceptance(t, "failure.challenger_balance", "1000000000", GetBalance(c.sv, "challenger", FeeToken).Balance, "FB_raw")
	winners := []string{"arb-a", "arb-b", "arb-c", "original-a", "original-b", "original-c", "original-d"}
	for i, address := range winners {
		award, fee, balance := "285714285", "14", "285814299"
		if i < 5 {
			award, fee, balance = "285714286", "15", "285814301"
		}
		checkArbitrationAcceptance(t, "failure."+address+".opponent_award", award, c.ledgerAmount(t, address, "witness_failed_challenge_award"), "FB_raw")
		checkArbitrationAcceptance(t, "failure."+address+".fee", fee, c.ledgerAmount(t, address, "witness_service_fee"), "FB_raw")
		checkArbitrationAcceptance(t, "failure."+address+".balance", balance, GetBalance(c.sv, address, FeeToken).Balance, "FB_raw")
	}
	checkArbitrationAcceptance(t, "failure.arb-d.balance", "100000", GetBalance(c.sv, "arb-d", FeeToken).Balance, "FB_raw")
	checkArbitrationAcceptance(t, "failure.arb-d.fee", "0", c.ledgerAmount(t, "arb-d", "witness_service_fee"), "FB_raw")
	c.assertFinal(t, false, "1000000000", "0", "82000801000", []string{"arb-d"})
}

type arbitrationAcceptanceCase struct {
	sv                 StateView
	baseline           ReadThroughFn
	x                  *Executor
	request            *pb.RechargeRequest
	challenge          *pb.ChallengeRecord
	original, ordinary []string
	height             uint64
}

func newArbitrationAcceptanceCase(t *testing.T) *arbitrationAcceptanceCase {
	t.Helper()
	c := &arbitrationAcceptanceCase{
		sv:       newMemStateView(),
		original: []string{"original-a", "original-b", "original-c", "original-d"},
		ordinary: []string{"arb-a", "arb-b", "arb-c", "arb-d"},
	}
	                                                                              
	seedChallengeSettlementFBToken(t, c.sv, "82000801000")
	for _, address := range c.participants() {
		settlementPut(t, c.sv, keys.KeyAccount(address), &pb.Account{Address: address})
		settlementPut(t, c.sv, keys.KeyWitnessInfo(address), &pb.WitnessInfo{
			Address: address, StakeAmount: "10000000000", Status: pb.WitnessStatus_WITNESS_ACTIVE,
		})
		SetBalance(c.sv, address, FeeToken, &pb.TokenBalance{Balance: "100000", WitnessLockedBalance: "10000000000"})
	}
	for _, seed := range []struct{ address, amount string }{{"payer", "1000"}, {"challenger", "2000000000"}} {
		settlementPut(t, c.sv, keys.KeyAccount(seed.address), &pb.Account{Address: seed.address})
		SetBalance(c.sv, seed.address, FeeToken, &pb.TokenBalance{Balance: seed.amount})
	}
	c.request = seedAccountRechargeFinalizationTest(t, c.sv, "acceptance-request", pb.RechargeRequestStatus_RECHARGE_CHALLENGE_PERIOD)
	c.request.RequesterAddress, c.request.RechargeFee = "payer", "103"
	c.request.CreateHeight, c.request.DeadlineHeight, c.request.FinalizeHeight = 1, 100, 0
	c.request.PassCount = 4
	c.request.SelectedWitnesses = append([]string(nil), c.original...)
	for _, address := range c.original {
		c.request.OriginalPositions = append(c.request.OriginalPositions, &pb.WitnessVote{
			RequestId: c.request.RequestId, WitnessAddress: address, VoteType: pb.WitnessVoteType_VOTE_PASS,
		})
	}
	settlementPut(t, c.sv, keys.KeyRechargeRequest(c.request.RequestId), c.request)
	c.sv, c.baseline = settlementBaseline(c.sv)
	                                                                           
	require.NoError(t, collectWitnessServiceFee(c.sv, c.request, false))
	checkArbitrationAcceptance(t, "funding.payer_after_service_fee", "897", GetBalance(c.sv, "payer", FeeToken).Balance, "FB_raw")
	checkArbitrationAcceptance(t, "funding.service_fee_pool", "103", GetBalance(c.sv, WitnessFeePoolAccount, FeeToken).Balance, "FB_raw")
	checkArbitrationAcceptance(t, "funding.case_fee_earmark", "103", c.request.FeeEscrow, "FB_raw")
	assignments := make([]WitnessCaseAssignment, 0, len(c.original))
	for _, address := range c.original {
		assignments = append(assignments, WitnessCaseAssignment{Address: address, Role: WitnessCaseRoleOriginal, Amount: WitnessCaseBondRaw})
	}
	require.NoError(t, reserveWitnessCaseCollateral(c.sv, c.request, assignments, "3200000000"))
	                                                                        
	                                                                           
	for _, address := range c.original {
		var info pb.WitnessInfo
		c.decode(t, keys.KeyWitnessInfo(address), &info)
		info.Status, info.UnstakeHeight = pb.WitnessStatus_WITNESS_UNSTAKING, 2
		settlementPut(t, c.sv, keys.KeyWitnessInfo(address), &info)
	}
	cfg := witness.DefaultConfig()
	cfg.OrdinaryCommitteeSizes, cfg.OrdinaryThresholdBps = []uint32{4, 8, 16}, 7500
	svc := witness.NewService(cfg)
	t.Cleanup(svc.Stop)
	c.x = &Executor{WitnessService: svc}
	require.NoError(t, setRechargeRequestWithIndexes(c.sv, c.request, svc.BeaconMixBlocks()))
	fund := &pb.AnyTx{Content: &pb.AnyTx_WitnessChallengeTx{WitnessChallengeTx: &pb.WitnessChallengeTx{
		Base:      &pb.BaseMessage{TxId: "acceptance-challenge", FromAddress: "challenger", ExecutedHeight: 10},
		RequestId: c.request.RequestId, StakeAmount: "1000000000",
	}}}
	ops, receipt, err := (&WitnessChallengeTxHandler{witnessSvc: svc}).DryRun(fund, c.sv)
	require.NoError(t, err)
	require.NotNil(t, receipt)
	require.Equal(t, "SUCCEED", receipt.Status, receipt.Error)
	require.Empty(t, ops)
	c.challenge = loadChallengeRecordAtKey(c.sv, keys.KeyChallengeRecord("acceptance-challenge"))
	require.NotNil(t, c.challenge)
	require.Equal(t, pb.ChallengeStatus_CHALLENGE_PENDING_SELECTION, c.challenge.Status)
	checkArbitrationAcceptance(t, "funding.required_principal", "1000000000", c.challenge.RequiredStake, "FB_raw")
	checkArbitrationAcceptance(t, "funding.challenger_after_debit", "1000000000", GetBalance(c.sv, "challenger", FeeToken).Balance, "FB_raw")
	checkArbitrationAcceptance(t, "funding.challenge_escrow", "1000000000", c.challenge.StakeAmount, "FB_raw")
	c.height = challengeSelectionReadyHeight(c.challenge, svc.BeaconMixBlocks())
	require.NoError(t, c.x.applyPendingArbitrationSelections(c.sv, c.height))
	c.challenge = loadChallengeRecordAtKey(c.sv, keys.KeyChallengeRecord(c.challenge.ChallengeId))
	require.Equal(t, pb.ChallengeStatus_CHALLENGE_ACTIVE, c.challenge.Status)
	require.Equal(t, uint32(1), c.challenge.ArbitrationRound)
	require.ElementsMatch(t, c.ordinary, c.challenge.Arbitrators)
	checkArbitrationAcceptance(t, "selection.committee_size", "4", fmt.Sprint(len(c.challenge.Arbitrators)), "addresses")
	for _, address := range c.participants() {
		var info pb.WitnessInfo
		c.decode(t, keys.KeyWitnessInfo(address), &info)
		require.Equal(t, uint64(1), info.ActiveCaseCount, address)
		checkArbitrationAcceptance(t, "selection."+address+".reserved", "1000000000", info.ReservedStakeAmount, "FB_raw")
	}
	return c
}

func (c *arbitrationAcceptanceCase) decide(t *testing.T, finalVote pb.WitnessVoteType) {
	t.Helper()
	opposite := pb.WitnessVoteType_VOTE_PASS
	if finalVote == pb.WitnessVoteType_VOTE_PASS {
		opposite = pb.WitnessVoteType_VOTE_FAIL
	}
	lifecycleBallot(t, c.sv, c.x, c.challenge, "arb-d", opposite, c.height, true)
	for _, address := range c.ordinary[:2] {
		lifecycleBallot(t, c.sv, c.x, c.challenge, address, finalVote, c.height, true)
	}
	mid := loadChallengeRecordAtKey(c.sv, keys.KeyChallengeRecord(c.challenge.ChallengeId))
	require.False(t, mid.Finalized, "two agreeing votes out of four cannot settle")
	require.False(t, mid.Settled)
	checkArbitrationAcceptance(t, "quorum.before_third_vote_principal_held", "1000000000", mid.StakeAmount, "FB_raw")
	checkArbitrationAcceptance(t, "quorum.before_third_vote_fee_held", "103", GetBalance(c.sv, WitnessFeePoolAccount, FeeToken).Balance, "FB_raw")
	lifecycleBallot(t, c.sv, c.x, c.challenge, "arb-c", finalVote, c.height, true)
}

func (c *arbitrationAcceptanceCase) assertFinal(t *testing.T, success bool, fine, burn, supply string, wrong []string) {
	t.Helper()
	ch := loadChallengeRecordAtKey(c.sv, keys.KeyChallengeRecord(c.challenge.ChallengeId))
	req := loadRechargeRequestAtKey(c.sv, keys.KeyRechargeRequest(c.request.RequestId))
	require.True(t, ch.Finalized)
	require.True(t, ch.Settled)
	require.Equal(t, success, ch.ChallengeSuccess)
	require.True(t, req.FeeSettled)
	status, creditedAmount := pb.RechargeRequestStatus_RECHARGE_REJECTED, "0"
	if !success {
		status, creditedAmount = pb.RechargeRequestStatus_RECHARGE_FINALIZED, "123"
	}
	require.Equal(t, status, req.Status)
	checkArbitrationAcceptance(t, "final.challenge_escrow", "0", ch.StakeAmount, "FB_raw")
	checkArbitrationAcceptance(t, "final.case_fee_earmark", "0", req.FeeEscrow, "FB_raw")
	checkArbitrationAcceptance(t, "final.service_fee_pool", "0", GetBalance(c.sv, WitnessFeePoolAccount, FeeToken).Balance, "FB_raw")
	checkArbitrationAcceptance(t, "final.service_fee_paid", "103", c.ledgerTotal(t, "witness_service_fee"), "FB_raw")
	checkArbitrationAcceptance(t, "final.payer_balance", "897", GetBalance(c.sv, "payer", FeeToken).Balance, "FB_raw")
	checkArbitrationAcceptance(t, "final.actual_fines", fine, c.ledgerTotal(t, "witness_case_slash"), "FB_raw")
	actualSupply := readChallengeSettlementFBTokenSupply(t, c.sv)
	checkArbitrationAcceptance(t, "final.FB_total_supply", supply, actualSupply, "FB_raw")
	checkArbitrationAcceptance(t, "final.FB_burn", burn, new(big.Int).Sub(acceptanceInt(t, "82000801000"), acceptanceInt(t, actualSupply)).String(), "FB_raw")
	checkArbitrationAcceptance(t, "final.recharge_receiver_balance", creditedAmount, GetBalance(c.sv, c.request.ReceiverAddress, c.request.TokenAddress).Balance, "bridged_token_raw")
	wrongSet := make(map[string]bool, len(wrong))
	for _, address := range wrong {
		wrongSet[address] = true
	}
	assets := new(big.Int)
	for _, address := range c.participants() {
		var info pb.WitnessInfo
		c.decode(t, keys.KeyWitnessInfo(address), &info)
		stake := "10000000000"
		if wrongSet[address] {
			stake = "9000000000"
		}
		require.Equal(t, stake, info.StakeAmount, address)
		balance := GetBalance(c.sv, address, FeeToken)
		checkArbitrationAcceptance(t, "final."+address+".locked_stake", stake, balance.WitnessLockedBalance, "FB_raw")
		checkArbitrationAcceptance(t, "final."+address+".reserved", "0", info.ReservedStakeAmount, "FB_raw")
		checkArbitrationAcceptance(t, "final."+address+".active_cases", "0", fmt.Sprint(info.ActiveCaseCount), "cases")
		assets.Add(assets, acceptanceInt(t, balance.Balance))
		assets.Add(assets, acceptanceInt(t, balance.WitnessLockedBalance))
	}
	for _, address := range []string{"payer", "challenger", WitnessFeePoolAccount} {
		assets.Add(assets, acceptanceInt(t, GetBalance(c.sv, address, FeeToken).Balance))
	}
	checkArbitrationAcceptance(t, "final.accounted_FB_assets", supply, assets.String(), "FB_raw")
	var archivedRequest pb.RechargeRequest
	c.decode(t, keys.KeyArchiveRechargeRequest(c.request.RequestId), &archivedRequest)
	require.Len(t, archivedRequest.CaseCollateral, 8)
	for _, bond := range archivedRequest.CaseCollateral {
		require.True(t, bond.Released, bond.Address)
		require.Equal(t, "0", bond.ReservedAmount, bond.Address)
		require.Equal(t, wrongSet[bond.Address], bond.SlashApplied, bond.Address)
	}
	rows, err := c.sv.Scan(keys.KeyChallengeContributionPrefix(c.challenge.ChallengeId))
	require.NoError(t, err)
	require.Empty(t, rows, "terminal principal must not remain spendable in contributions")
	AuditGlobalConservationDiff(c.sv.Diff(), c.height, c.baseline)

	                                                                       
	                                                                                 
	before := c.sv.Snapshot()
	lifecycleBallot(t, c.sv, c.x, c.challenge, "arb-c", ch.FinalVote, c.height+1, false)
	var staleChallenge pb.ChallengeRecord
	c.decode(t, keys.KeyArchiveChallengeRecord(c.challenge.ChallengeId), &staleChallenge)
	staleChallenge.Settled = false
	require.NoError(t, applyArbitrationOutcomeToState(c.sv, &witness.ArbitrationVoteOutcome{
		Request: &archivedRequest, Challenge: &staleChallenge,
	}, c.x.WitnessService.BeaconMixBlocks(), 0, c.height+1))
	checkArbitrationAcceptance(t, "replay.state_writes", "0", fmt.Sprint(c.sv.Snapshot()-before), "writes")
	checkArbitrationAcceptance(t, "replay.service_fee_paid_once", "103", c.ledgerTotal(t, "witness_service_fee"), "FB_raw")
	checkArbitrationAcceptance(t, "replay.actual_fines_once", fine, c.ledgerTotal(t, "witness_case_slash"), "FB_raw")
	checkArbitrationAcceptance(t, "replay.FB_supply", supply, readChallengeSettlementFBTokenSupply(t, c.sv), "FB_raw")
}

func (c *arbitrationAcceptanceCase) participants() []string {
	return append(append([]string(nil), c.original...), c.ordinary...)
}

func (c *arbitrationAcceptanceCase) decode(t *testing.T, key string, value proto.Message) {
	t.Helper()
	raw, exists, err := c.sv.Get(key)
	require.NoError(t, err)
	require.True(t, exists, key)
	require.NoError(t, proto.Unmarshal(raw, value))
}

func (c *arbitrationAcceptanceCase) ledgerAmount(t *testing.T, address, reason string) string {
	t.Helper()
	entries, err := NewLedger(c.sv).Query(LedgerFilter{Account: address, Reason: reason})
	require.NoError(t, err)
	total := new(big.Int)
	for _, entry := range entries {
		require.Equal(t, FeeToken, entry.Token)
		total.Add(total, acceptanceInt(t, entry.Amount))
	}
	return total.String()
}

func (c *arbitrationAcceptanceCase) ledgerTotal(t *testing.T, reason string) string {
	t.Helper()
	total := new(big.Int)
	for _, address := range append(c.participants(), "payer", "challenger", WitnessFeePoolAccount) {
		total.Add(total, acceptanceInt(t, c.ledgerAmount(t, address, reason)))
	}
	return total.String()
}

func acceptanceInt(t *testing.T, raw string) *big.Int {
	t.Helper()
	amount, ok := new(big.Int).SetString(raw, 10)
	require.True(t, ok, "invalid integer %q", raw)
	return amount
}

func checkArbitrationAcceptance(t *testing.T, label, expected, actual, unit string) {
	t.Helper()
	record := struct {
		Label    string `json:"label"`
		Expected string `json:"expected"`
		Actual   string `json:"actual"`
		Unit     string `json:"unit"`
	}{label, expected, actual, unit}
	raw, err := json.Marshal(record)
	require.NoError(t, err)
	t.Logf("DEX_ARBITRATION_CHECK %s", raw)
	if actual != expected {
		t.Errorf("%s: got %s %s, want %s %s", label, actual, unit, expected, unit)
	}
}
