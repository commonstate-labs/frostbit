package vm

import (
	"bytes"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"dex/keys"
	"dex/pb"
	"dex/rent"
	"dex/witness"
	"google.golang.org/protobuf/proto"
)

func settlementPut(t *testing.T, sv StateView, key string, value proto.Message) {
	t.Helper()
	b, e := proto.Marshal(value)
	if e != nil {
		t.Fatal(e)
	}
	sv.Set(key, b)
}
func settlementVote(a string, v pb.WitnessVoteType) *pb.WitnessVote {
	return &pb.WitnessVote{WitnessAddress: a, VoteType: v}
}
func settlementBaseline(sv StateView) (StateView, ReadThroughFn) {
	data := map[string][]byte{}
	for _, op := range sv.Diff() {
		if !op.Del {
			data[op.Key] = append([]byte(nil), op.Value...)
		}
	}
	read := func(key string) ([]byte, error) { return data[key], nil }
	scan := func(prefix string) (map[string][]byte, error) {
		out := map[string][]byte{}
		for key, value := range data {
			if strings.HasPrefix(key, prefix) {
				out[key] = value
			}
		}
		return out, nil
	}
	return NewStateView(read, scan), read
}

func settlementFixture(t *testing.T, success bool) (StateView, ReadThroughFn, *witness.ArbitrationVoteOutcome) {
	t.Helper()
	sv := newMemStateView()
	seedChallengeSettlementFBToken(t, sv, "10000000000")
	for _, a := range []string{"wrong", "good", "court"} {
		SetBalance(sv, a, FeeToken, &pb.TokenBalance{Balance: "200", WitnessLockedBalance: "3000000000"})
		settlementPut(t, sv, keys.KeyWitnessInfo(a), &pb.WitnessInfo{Address: a, StakeAmount: "3000000000", Status: pb.WitnessStatus_WITNESS_ACTIVE})
	}
	SetBalance(sv, "payer", FeeToken, &pb.TokenBalance{Balance: "1000"})
	req := &pb.RechargeRequest{RequestId: "case", RequesterAddress: "payer", RechargeFee: "101", Status: pb.RechargeRequestStatus_RECHARGE_CHALLENGED, ChallengeId: "challenge", OriginalPositions: []*pb.WitnessVote{settlementVote("wrong", pb.WitnessVoteType_VOTE_PASS), settlementVote("court", pb.WitnessVoteType_VOTE_FAIL)}}
	if err := collectWitnessServiceFee(sv, req, false); err != nil {
		t.Fatal(err)
	}
	assignments := []WitnessCaseAssignment{{"wrong", WitnessCaseRoleOriginal, WitnessCaseBondRaw}, {"wrong", WitnessCaseRoleOrdinary, "1000000003"}, {"good", WitnessCaseRoleOrdinary, WitnessCaseBondRaw}, {"court", WitnessCaseRoleOriginal, WitnessCaseBondRaw}, {"court", WitnessCaseRoleCourt, "0"}}
	if err := reserveWitnessCaseCollateral(sv, req, assignments, "1"); err != nil {
		t.Fatal(err)
	}
	settlementPut(t, sv, keys.KeyRechargeRequest(req.RequestId), req)
	ch := &pb.ChallengeRecord{ChallengeId: "challenge", RequestId: "case", Status: pb.ChallengeStatus_CHALLENGE_ACTIVE, StakeAmount: "103", OriginalVotes: req.OriginalPositions, OrdinaryVotes: []*pb.WitnessVote{settlementVote("wrong", pb.WitnessVoteType_VOTE_FAIL), settlementVote("good", pb.WitnessVoteType_VOTE_FAIL)}, FinalCourt: []string{"court"}, OrdinaryVotesFrozen: true, FinalVote: pb.WitnessVoteType_VOTE_FAIL, ChallengeSuccess: success}
	for _, entry := range []struct {
		a string
		n int64
	}{{"good", 101}, {"wrong", 1}, {"court", 1}} {
		tx := &pb.WitnessChallengeTx{Base: &pb.BaseMessage{TxId: entry.a, FromAddress: entry.a}, RequestId: req.RequestId, StakeAmount: fmt.Sprint(entry.n)}
		if err := witnessSettlementMove(sv, entry.a, big.NewInt(entry.n), true, "fixture_contribution", 1, ch.ChallengeId); err != nil {
			t.Fatal(err)
		}
		settlementPut(t, sv, keys.KeyChallengeContribution(ch.ChallengeId, entry.a), tx)
		if err := syncChallengeContributionRentClaim(sv, tx, ch.ChallengeId, big.NewInt(entry.n)); err != nil {
			t.Fatal(err)
		}
	}
	settlementPut(t, sv, keys.KeyChallengeRecord(ch.ChallengeId), ch)
	base, read := settlementBaseline(sv)
	req = proto.Clone(req).(*pb.RechargeRequest)
	ch = proto.Clone(ch).(*pb.ChallengeRecord)
	req.Status = pb.RechargeRequestStatus_RECHARGE_REJECTED
	ch.Status = pb.ChallengeStatus_CHALLENGE_FINALIZED
	ch.Finalized = true
	return base, read, &witness.ArbitrationVoteOutcome{Request: req, Challenge: ch}
}

func TestWitnessArbitrationSettlementSuccessConservationAndRoleExclusions(t *testing.T) {
	sv, read, out := settlementFixture(t, true)
	if err := applyArbitrationOutcomeToState(sv, out, 1, 0, 50); err != nil {
		t.Fatal(err)
	}
	for a, want := range map[string]string{"good": "900000253", "wrong": "200", "court": "251", "payer": "899"} {
		if got := GetBalance(sv, a, FeeToken).Balance; got != want {
			t.Fatalf("%s got %s want %s", a, got, want)
		}
	}
	if got := GetBalance(sv, "wrong", FeeToken).WitnessLockedBalance; got != "1999999997" {
		t.Fatal(got)
	}
	if got := GetBalance(sv, WitnessFeePoolAccount, FeeToken).Balance; got != "0" {
		t.Fatal(got)
	}
	if got := readChallengeSettlementFBTokenSupply(t, sv); got != "9900000000" {
		t.Fatal(got)
	}
	if !out.Challenge.Settled || !out.Request.FeeSettled {
		t.Fatal("missing settlement markers")
	}
	if rows, err := sv.Scan(keys.KeyChallengeContributionPrefix("challenge")); err != nil || len(rows) != 0 {
		t.Fatal("contribution not cleared", err)
	}
	AuditGlobalConservationDiff(sv.Diff(), 50, read)
}

func TestWitnessArbitrationSettlementFailureAwardsPrincipalAndActualFine(t *testing.T) {
	sv, read, out := settlementFixture(t, false)
	if err := applyArbitrationOutcomeToState(sv, out, 1, 0, 50); err != nil {
		t.Fatal(err)
	}
	                                                                           
	if got := GetBalance(sv, "good", FeeToken).Balance; got != "1000000255" {
		t.Fatal(got)
	}
	if got := GetBalance(sv, "wrong", FeeToken).Balance; got != "199" {
		t.Fatal(got)
	}
	if got := GetBalance(sv, "court", FeeToken).Balance; got != "250" {
		t.Fatal(got)
	}
	if got := readChallengeSettlementFBTokenSupply(t, sv); got != "10000000000" {
		t.Fatal(got)
	}
	AuditGlobalConservationDiff(sv.Diff(), 50, read)
}

func TestWitnessArbitrationSettlementLateFailureRollsBackMoneyAndPointers(t *testing.T) {
	sv, _, out := settlementFixture(t, true)
	                                                                       
	seedChallengeSettlementFBToken(t, sv, "1")
	before := map[string][]byte{}
	for _, key := range []string{keys.KeyBalance("wrong", FeeToken), keys.KeyWitnessInfo("wrong"), keys.KeyBalance(WitnessFeePoolAccount, FeeToken), keys.KeyRechargeRequest("case"), keys.KeyChallengeContribution("challenge", "good")} {
		b, _, _ := sv.Get(key)
		before[key] = b
	}
	original := proto.Clone(out.Request)
	if err := applyArbitrationOutcomeToState(sv, out, 1, 0, 50); err == nil {
		t.Fatal("underfunded burn accepted")
	}
	for key, want := range before {
		got, _, _ := sv.Get(key)
		if !bytes.Equal(got, want) {
			t.Fatalf("failed settlement changed %s", key)
		}
	}
	if !proto.Equal(original, out.Request) || out.Challenge.Settled {
		t.Fatal("failed settlement mutated input pointer")
	}
}

func TestWitnessArbitrationSettlementRejectsMissingContributionBacking(t *testing.T) {
	sv, _, out := settlementFixture(t, true)
	out.Challenge.StakeAmount = "104"
	if err := applyArbitrationOutcomeToState(sv, out, 1, 0, 50); err == nil {
		t.Fatal("unbacked principal accepted")
	}
	if GetBalance(sv, "wrong", FeeToken).WitnessLockedBalance != "3000000000" {
		t.Fatal("debited before validating principal")
	}
}

func TestWitnessArbitrationSettlementRepeatedOutcomePaysOnce(t *testing.T) {
	sv, _, out := settlementFixture(t, true)
	stale := &witness.ArbitrationVoteOutcome{Request: proto.Clone(out.Request).(*pb.RechargeRequest), Challenge: proto.Clone(out.Challenge).(*pb.ChallengeRecord)}
	if err := applyArbitrationOutcomeToState(sv, out, 1, 0, 50); err != nil {
		t.Fatal(err)
	}
	before := GetBalance(sv, "good", FeeToken).Balance
	if err := applyArbitrationOutcomeToState(sv, stale, 1, 0, 51); err != nil {
		t.Fatal(err)
	}
	if GetBalance(sv, "good", FeeToken).Balance != before {
		t.Fatal("replay paid twice")
	}
}

func TestWitnessServiceFeeRealPrepaymentNoChallengeAndReplay(t *testing.T) {
	sv := newMemStateView()
	SetBalance(sv, "payer", FeeToken, &pb.TokenBalance{Balance: "103"})
	req := &pb.RechargeRequest{RequestId: "fee", RequesterAddress: "payer", RechargeFee: "101", OriginalPositions: []*pb.WitnessVote{settlementVote("a", pb.WitnessVoteType_VOTE_PASS), settlementVote("b", pb.WitnessVoteType_VOTE_PASS)}}
	if err := collectWitnessServiceFee(sv, req, false); err != nil {
		t.Fatal(err)
	}
	if err := collectWitnessServiceFee(sv, req, false); err != nil {
		t.Fatal(err)
	}
	if GetBalance(sv, "payer", FeeToken).Balance != "2" || GetBalance(sv, WitnessFeePoolAccount, FeeToken).Balance != "101" {
		t.Fatal("fee was not escrowed once")
	}
	base, read := settlementBaseline(sv)
	if err := settleWitnessRequestFeesAndCollateral(base, req, pb.WitnessVoteType_VOTE_PASS); err != nil {
		t.Fatal(err)
	}
	if err := settleWitnessRequestFeesAndCollateral(base, req, pb.WitnessVoteType_VOTE_PASS); err != nil {
		t.Fatal(err)
	}
	if GetBalance(base, "a", FeeToken).Balance != "51" || GetBalance(base, "b", FeeToken).Balance != "50" {
		t.Fatal("fee split")
	}
	AuditGlobalConservationDiff(base.Diff(), 1, read)
}

func TestWitnessServiceFeeRejectsDecimalAndInsufficientSource(t *testing.T) {
	for _, fee := range []string{"1.1", "101"} {
		sv := newMemStateView()
		SetBalance(sv, "payer", FeeToken, &pb.TokenBalance{Balance: "100"})
		req := &pb.RechargeRequest{RequestId: "fee", RequesterAddress: "payer", RechargeFee: fee}
		if err := collectWitnessServiceFee(sv, req, false); err == nil {
			t.Fatalf("invalid fee %s accepted", fee)
		}
		if GetBalance(sv, "payer", FeeToken).Balance != "100" || req.ArbitrationProtocolVersion != 0 {
			t.Fatal("failed fee collection mutated state")
		}
	}
}

func TestWitnessRechargeMinimumFeeAppliesOnlyOnFirstCollection(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fee    string
		active bool
		accept bool
	}{
		{name: "legacy zero", fee: "0", accept: true},
		{name: "legacy below minimum", fee: "999999", accept: true},
		{name: "active empty", fee: "", active: true},
		{name: "active zero", fee: "0", active: true},
		{name: "active below minimum", fee: "999999", active: true},
		{name: "active exact minimum", fee: "1000000", active: true, accept: true},
		{name: "active above minimum", fee: "1000001", active: true, accept: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sv := newMemStateView()
			SetBalance(sv, "payer", FeeToken, &pb.TokenBalance{Balance: "2000000"})
			req := &pb.RechargeRequest{RequestId: "fee", RequesterAddress: "payer", RechargeFee: tc.fee}
			err := collectWitnessServiceFee(sv, req, tc.active)
			if (err == nil) != tc.accept {
				t.Fatalf("fee=%q active=%v err=%v", tc.fee, tc.active, err)
			}
			if !tc.accept {
				if got := GetBalance(sv, "payer", FeeToken).Balance; got != "2000000" {
					t.Fatalf("rejected fee changed payer balance: %s", got)
				}
				if _, exists, err := sv.Get(keys.KeyRechargeRequest("fee")); err != nil || exists {
					t.Fatalf("rejected fee persisted request: exists=%v err=%v", exists, err)
				}
			}
		})
	}

	sv := newMemStateView()
	SetBalance(sv, "payer", FeeToken, &pb.TokenBalance{Balance: "1"})
	legacy := &pb.RechargeRequest{RequestId: "legacy", RequesterAddress: "payer", RechargeFee: "0"}
	if err := collectWitnessServiceFee(sv, legacy, false); err != nil {
		t.Fatal(err)
	}
	if err := collectWitnessServiceFee(sv, legacy, true); err != nil {
		t.Fatalf("post-activation idempotent handling rejected legacy request: %v", err)
	}
	if got := GetBalance(sv, "payer", FeeToken).Balance; got != "1" || legacy.FeeEscrow != "0" {
		t.Fatalf("legacy request was charged again: balance=%s escrow=%s", got, legacy.FeeEscrow)
	}
	if err := settleWitnessRequestFeesAndCollateral(sv, legacy, pb.WitnessVoteType_VOTE_FAIL); err != nil {
		t.Fatalf("post-activation settlement rejected legacy escrow: %v", err)
	}
	if !legacy.FeeSettled || legacy.FeeEscrow != "0" {
		t.Fatalf("legacy request not settled: settled=%v escrow=%s", legacy.FeeSettled, legacy.FeeEscrow)
	}
}

type witnessFeeReadCountingView struct {
	StateView
	gets int
}

func (v *witnessFeeReadCountingView) Get(key string) ([]byte, bool, error) {
	v.gets++
	return v.StateView.Get(key)
}

func TestWitnessRechargeMinimumFeeRejectUsesOnlyCanonicalPointRead(t *testing.T) {
	sv := &witnessFeeReadCountingView{StateView: newMemStateView()}
	before := len(sv.Diff())
	req := &pb.RechargeRequest{RequestId: "under-minimum", RequesterAddress: "payer", RechargeFee: "999999"}
	if err := collectWitnessServiceFee(sv, req, true); err == nil {
		t.Fatal("under-minimum fee accepted")
	}
	if sv.gets != 1 || len(sv.Diff()) != before {
		t.Fatalf("rejected fee gets=%d diff=%d, want one existing canonical point read and no writes", sv.gets, len(sv.Diff()))
	}
}

func TestWitnessUnderfundedChallengeRefundPreservesFee(t *testing.T) {
	sv, read, out := settlementFixture(t, true)
	ch := proto.Clone(out.Challenge).(*pb.ChallengeRecord)
	ch.Finalized = false
	ch.Status = pb.ChallengeStatus_CHALLENGE_FUNDING
	if err := refundPendingChallengePrincipal(sv, ch, 50); err != nil {
		t.Fatal(err)
	}
	if err := setChallengeRecordWithTimeoutIndexes(sv, ch, 1, 0, 50); err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"good", "wrong", "court"} {
		if GetBalance(sv, a, FeeToken).Balance != "200" {
			t.Fatal("principal not refunded", a)
		}
	}
	if GetBalance(sv, WitnessFeePoolAccount, FeeToken).Balance != "101" {
		t.Fatal("funding refund consumed service fee")
	}
	if GetBalance(sv, "wrong", FeeToken).WitnessLockedBalance != "3000000000" {
		t.Fatal("funding refund slashed")
	}
	AuditGlobalConservationDiff(sv.Diff(), 50, read)
}

func TestWitnessArbitrationSettlementRentReclaimedContributionIsNotRecreated(t *testing.T) {
	sv, read, out := settlementFixture(t, true)
	cleaner := vmRentCleaner{sv: sv}
	if err := cleaner.sweepChallengeContribution("good", "challenge", "good", big.NewInt(101)); err != nil {
		t.Fatal(err)
	}
	                                                                      
	out.Challenge.StakeAmount = "2"
	if err := applyArbitrationOutcomeToState(sv, out, 1, 0, 50); err != nil {
		t.Fatal(err)
	}
	if got := GetBalance(sv, rent.RecycleFundAccount, FeeToken).Balance; got != "101" {
		t.Fatal(got)
	}
	if got := GetBalance(sv, "good", FeeToken).Balance; got != "450000150" {
		t.Fatal(got)
	}
	                                                                             
	                                                                                
	if got := readChallengeSettlementFBTokenSupply(t, sv); got != "9449999998" {
		t.Fatal(got)
	}
	AuditGlobalConservationDiff(sv.Diff(), 50, read)
}

func TestWitnessServiceFeeEmptyGroupBurnsRealEscrow(t *testing.T) {
	sv := newMemStateView()
	seedChallengeSettlementFBToken(t, sv, "101")
	SetBalance(sv, "payer", FeeToken, &pb.TokenBalance{Balance: "101"})
	req := &pb.RechargeRequest{RequestId: "emptyfee", RequesterAddress: "payer", RechargeFee: "101"}
	if err := collectWitnessServiceFee(sv, req, false); err != nil {
		t.Fatal(err)
	}
	base, read := settlementBaseline(sv)
	if err := settleWitnessRequestFeesAndCollateral(base, req, pb.WitnessVoteType_VOTE_FAIL); err != nil {
		t.Fatal(err)
	}
	if GetBalance(base, WitnessFeePoolAccount, FeeToken).Balance != "0" || readChallengeSettlementFBTokenSupply(t, base) != "0" {
		t.Fatal("empty fee group was not burned")
	}
	AuditGlobalConservationDiff(base.Diff(), 1, read)
}

func TestWitnessLegacySettlementCannotMintAndRollsBack(t *testing.T) {
	sv := newMemStateView()
	seedChallengeSettlementFBToken(t, sv, "1000")
	out := &witness.ArbitrationVoteOutcome{Challenge: &pb.ChallengeRecord{ChallengeId: "legacy", Finalized: true, StakeAmount: "1"}, ChallengerAddr: "claimant", ChallengerRefund: "2"}
	if _, err := applySlashAndRefundToState(sv, out); err == nil {
		t.Fatal("unbacked legacy reward minted")
	}
	if GetBalance(sv, "claimant", FeeToken).Balance != "0" || readChallengeSettlementFBTokenSupply(t, sv) != "1000" {
		t.Fatal("legacy mint rejection left partial writes")
	}
}
