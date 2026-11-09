package vm

import (
	"dex/keys"
	"dex/pb"
	"errors"
	"math"
	"math/big"
	"reflect"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
)

const collateralTestMinStake = "3200000000"

func seedCaseCollateralWitness(t *testing.T, sv StateView, address, stake string) {
	t.Helper()
	info := &pb.WitnessInfo{Address: address, Status: pb.WitnessStatus_WITNESS_ACTIVE, StakeAmount: stake, ReservedStakeAmount: "0"}
	writeCaseCollateralWitness(t, sv, info)
	SetBalance(sv, address, FeeToken, &pb.TokenBalance{Balance: "123", WitnessLockedBalance: stake})
}

func writeCaseCollateralWitness(t *testing.T, sv StateView, info *pb.WitnessInfo) {
	t.Helper()
	data, err := proto.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	sv.Set(keys.KeyWitnessInfo(info.Address), data)
}

func readCaseCollateralWitness(t *testing.T, sv StateView, address string) *pb.WitnessInfo {
	t.Helper()
	raw, found, err := sv.Get(keys.KeyWitnessInfo(address))
	if err != nil || !found {
		t.Fatalf("read witness %s: found=%v err=%v", address, found, err)
	}
	var info pb.WitnessInfo
	if err := proto.Unmarshal(raw, &info); err != nil {
		t.Fatal(err)
	}
	return &info
}

func caseAssignment(address string, role uint32, amount string) WitnessCaseAssignment {
	return WitnessCaseAssignment{Address: address, Role: role, Amount: amount}
}

func mustReserveCase(t *testing.T, sv StateView, req *pb.RechargeRequest, assignments ...WitnessCaseAssignment) {
	t.Helper()
	if err := reserveWitnessCaseCollateral(sv, req, assignments, collateralTestMinStake); err != nil {
		t.Fatal(err)
	}
}

func TestWitnessCaseCollateralCommitteeFailureIsAtomic(t *testing.T) {
	sv := newMemStateView()
	seedCaseCollateralWitness(t, sv, "alice", "4000000000")
	seedCaseCollateralWitness(t, sv, "zed", "4000000000")
	first := &pb.RechargeRequest{RequestId: "first"}
	mustReserveCase(t, sv, first, caseAssignment("zed", WitnessCaseRoleOrdinary, "4000000000"))
	second := &pb.RechargeRequest{RequestId: "second"}
	before := sv.Diff()
	err := reserveWitnessCaseCollateral(sv, second, []WitnessCaseAssignment{
		caseAssignment("alice", WitnessCaseRoleOriginal, WitnessCaseBondRaw),
		caseAssignment("zed", WitnessCaseRoleOriginal, WitnessCaseBondRaw),
	}, collateralTestMinStake)
	if err == nil || !errors.Is(err, errWitnessCaseUnavailable) {
		t.Fatalf("expected capacity rejection, got %v", err)
	}
	if !reflect.DeepEqual(before, sv.Diff()) || len(second.CaseCollateral) != 0 {
		t.Fatal("partial committee reservation leaked")
	}
	if info := readCaseCollateralWitness(t, sv, "alice"); info.ActiveCaseCount != 0 || info.ReservedStakeAmount != "0" {
		t.Fatalf("alice modified: %+v", info)
	}
}

func TestWitnessCaseCollateralMergesRolesAndPreservesOriginalSnapshot(t *testing.T) {
	sv := newMemStateView()
	seedCaseCollateralWitness(t, sv, "alice", "4000000000")
	req := &pb.RechargeRequest{RequestId: "roles"}
	mustReserveCase(t, sv, req, caseAssignment("alice", WitnessCaseRoleOriginal, WitnessCaseBondRaw))
	mustReserveCase(t, sv, req,
		caseAssignment("alice", WitnessCaseRoleOrdinary, "2000000000"),
		caseAssignment("alice", WitnessCaseRoleCourt, "0"),
		caseAssignment("alice", WitnessCaseRoleOrdinary, WitnessCaseBondRaw))
	mustReserveCase(t, sv, req, caseAssignment("alice", WitnessCaseRoleOrdinary, "2000000000"))
	info := readCaseCollateralWitness(t, sv, "alice")
	if info.ActiveCaseCount != 1 || info.ReservedStakeAmount != "2000000000" {
		t.Fatalf("merged responsibility: %+v", info)
	}
	if len(req.CaseCollateral) != 1 || req.CaseCollateral[0].RoleMask != 7 || req.CaseCollateral[0].OriginalAmount != WitnessCaseBondRaw {
		t.Fatalf("role/original snapshot: %+v", req.CaseCollateral)
	}
	if balance := GetBalance(sv, "alice", FeeToken); balance.WitnessLockedBalance != "4000000000" || balance.Balance != "123" {
		t.Fatalf("reservation moved funds: %+v", balance)
	}
}

func TestWitnessCaseCollateralSlashCannotSpendOtherCaseAndIsIdempotent(t *testing.T) {
	sv := newMemStateView()
	seedCaseCollateralWitness(t, sv, "alice", "4000000000")
	first := &pb.RechargeRequest{RequestId: "first", FinalizeHeight: 100}
	second := &pb.RechargeRequest{RequestId: "second"}
	mustReserveCase(t, sv, first, caseAssignment("alice", WitnessCaseRoleOriginal, WitnessCaseBondRaw))
	mustReserveCase(t, sv, second, caseAssignment("alice", WitnessCaseRoleOriginal, WitnessCaseBondRaw))
	actual, err := slashWitnessCaseCollateral(sv, first, []string{"alice", "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if len(actual) != 1 || actual["alice"].String() != WitnessCaseBondRaw {
		t.Fatalf("actual=%v", actual)
	}
	info := readCaseCollateralWitness(t, sv, "alice")
	if info.StakeAmount != "3000000000" || info.ReservedStakeAmount != WitnessCaseBondRaw || info.ActiveCaseCount != 2 {
		t.Fatalf("cross-case state: %+v", info)
	}
	if second.CaseCollateral[0].ReservedAmount != WitnessCaseBondRaw {
		t.Fatal("other case reservation changed")
	}
	repeated, err := slashWitnessCaseCollateral(sv, first, []string{"alice"})
	if err != nil || len(repeated) != 0 {
		t.Fatalf("repeat: %v %v", repeated, err)
	}
	entries, err := NewLedger(sv).Query(LedgerFilter{Account: "alice", Reason: "witness_case_slash"})
	if err != nil || len(entries) != 1 || entries[0].From != BucketWitnessLocked || entries[0].Height != 100 {
		t.Fatalf("slash ledger: %+v %v", entries, err)
	}
	if err := releaseWitnessCaseCollateral(sv, first); err != nil {
		t.Fatal(err)
	}
	if err := releaseWitnessCaseCollateral(sv, first); err != nil {
		t.Fatal(err)
	}
	info = readCaseCollateralWitness(t, sv, "alice")
	if info.ActiveCaseCount != 1 || info.ReservedStakeAmount != WitnessCaseBondRaw || validateWitnessStakeClaim(info) == nil {
		t.Fatalf("released other-case duty: %+v", info)
	}
	if err := releaseWitnessCaseCollateral(sv, second); err != nil {
		t.Fatal(err)
	}
	if err := validateWitnessStakeClaim(readCaseCollateralWitness(t, sv, "alice")); err != nil {
		t.Fatal(err)
	}
}

func TestWitnessCaseCollateralCourtBlocksExitWithoutNewBond(t *testing.T) {
	sv := newMemStateView()
	seedCaseCollateralWitness(t, sv, "judge", collateralTestMinStake)
	req := &pb.RechargeRequest{RequestId: "court"}
	mustReserveCase(t, sv, req, caseAssignment("judge", WitnessCaseRoleCourt, "0"))
	info := readCaseCollateralWitness(t, sv, "judge")
	if info.ActiveCaseCount != 1 || info.ReservedStakeAmount != "0" || validateWitnessStakeClaim(info) == nil {
		t.Fatalf("court lacks exit lock: %+v", info)
	}
	info.Status = pb.WitnessStatus_WITNESS_UNSTAKING
	writeCaseCollateralWitness(t, sv, info)
	mustReserveCase(t, sv, req, caseAssignment("judge", WitnessCaseRoleCourt, "0"))
	if allowed, err := canReserveWitnessCaseAssignment(sv, &pb.RechargeRequest{RequestId: "new"}, caseAssignment("judge", WitnessCaseRoleCourt, "0"), collateralTestMinStake); err != nil || allowed {
		t.Fatalf("exiting judge accepted new case: %v %v", allowed, err)
	}
	if _, err := slashWitnessCaseCollateral(sv, req, []string{"judge"}); err == nil {
		t.Fatal("court-only vote was punished")
	}
	if err := releaseWitnessCaseCollateral(sv, req); err != nil {
		t.Fatal(err)
	}
	if err := validateWitnessStakeClaim(readCaseCollateralWitness(t, sv, "judge")); err != nil {
		t.Fatal(err)
	}
}

func TestWitnessCaseCollateralExitAllowsOldRoleButNotNewOrHigherLiability(t *testing.T) {
	sv := newMemStateView()
	seedCaseCollateralWitness(t, sv, "alice", "4000000000")
	req := &pb.RechargeRequest{RequestId: "existing"}
	mustReserveCase(t, sv, req, caseAssignment("alice", WitnessCaseRoleOrdinary, WitnessCaseBondRaw))
	info := readCaseCollateralWitness(t, sv, "alice")
	info.Status = pb.WitnessStatus_WITNESS_UNSTAKING
	writeCaseCollateralWitness(t, sv, info)
	mustReserveCase(t, sv, req, caseAssignment("alice", WitnessCaseRoleOrdinary, WitnessCaseBondRaw))
	for _, assignment := range []WitnessCaseAssignment{caseAssignment("alice", WitnessCaseRoleOrdinary, "2000000000"), caseAssignment("alice", WitnessCaseRoleCourt, "0")} {
		allowed, err := canReserveWitnessCaseAssignment(sv, req, assignment, collateralTestMinStake)
		if err != nil || allowed {
			t.Fatalf("exiting account accepted new liability: %v %v", allowed, err)
		}
	}
}

func TestWitnessCaseCollateralZeroStakeKeepsCourtDutyButRejectsNewSeat(t *testing.T) {
	sv := newMemStateView()
	seedCaseCollateralWitness(t, sv, "judge", collateralTestMinStake)
	court := &pb.RechargeRequest{RequestId: "old-court"}
	wrong := &pb.RechargeRequest{RequestId: "wrong"}
	mustReserveCase(t, sv, court, caseAssignment("judge", WitnessCaseRoleCourt, "0"))
	mustReserveCase(t, sv, wrong, caseAssignment("judge", WitnessCaseRoleOrdinary, collateralTestMinStake))
	if _, err := slashWitnessCaseCollateral(sv, wrong, []string{"judge"}); err != nil {
		t.Fatal(err)
	}
	mustReserveCase(t, sv, court, caseAssignment("judge", WitnessCaseRoleCourt, "0"))
	allowed, err := canReserveWitnessCaseAssignment(sv, &pb.RechargeRequest{RequestId: "new-court"}, caseAssignment("judge", WitnessCaseRoleCourt, "0"), collateralTestMinStake)
	if err != nil || allowed {
		t.Fatalf("zero stake new court accepted: %v %v", allowed, err)
	}
	info := readCaseCollateralWitness(t, sv, "judge")
	if info.StakeAmount != "0" || info.ActiveCaseCount != 2 || validateWitnessStakeClaim(info) == nil {
		t.Fatalf("old zero-stake duty lost: %+v", info)
	}
}

func TestWitnessCaseCollateralCourtDoesNotImmunizeBondedRole(t *testing.T) {
	sv := newMemStateView()
	seedCaseCollateralWitness(t, sv, "judge", collateralTestMinStake)
	req := &pb.RechargeRequest{RequestId: "dual-role"}
	mustReserveCase(t, sv, req, caseAssignment("judge", WitnessCaseRoleOriginal, WitnessCaseBondRaw), caseAssignment("judge", WitnessCaseRoleCourt, "0"))
	actual, err := slashWitnessCaseCollateral(sv, req, []string{"judge"})
	if err != nil || actual["judge"] == nil || actual["judge"].String() != WitnessCaseBondRaw {
		t.Fatalf("court immunity: %v %v", actual, err)
	}
	if info := readCaseCollateralWitness(t, sv, "judge"); info.ActiveCaseCount != 1 {
		t.Fatal("penalty released old duty")
	}
}

func TestWitnessCaseCollateralInvalidLastMemberNeverPartiallySlashes(t *testing.T) {
	sv := newMemStateView()
	seedCaseCollateralWitness(t, sv, "alice", collateralTestMinStake)
	req := &pb.RechargeRequest{RequestId: "case"}
	mustReserveCase(t, sv, req, caseAssignment("alice", WitnessCaseRoleOriginal, WitnessCaseBondRaw))
	before, beforeReq := sv.Diff(), proto.Clone(req)
	if _, err := slashWitnessCaseCollateral(sv, req, []string{"alice", "zed-not-member"}); err == nil {
		t.Fatal("nonmember accepted")
	}
	if !reflect.DeepEqual(before, sv.Diff()) || !proto.Equal(beforeReq, req) {
		t.Fatal("invalid later member partially slashed earlier member")
	}
}

func TestWitnessCaseCollateralRejectsFractionalAndUnbackedState(t *testing.T) {
	for _, raw := range []string{"0.5", "1000000000.1", "1e9", "-1", "+1000000000"} {
		t.Run(raw, func(t *testing.T) {
			sv := newMemStateView()
			seedCaseCollateralWitness(t, sv, "alice", collateralTestMinStake)
			req := &pb.RechargeRequest{RequestId: "invalid"}
			before := sv.Diff()
			if err := reserveWitnessCaseCollateral(sv, req, []WitnessCaseAssignment{caseAssignment("alice", WitnessCaseRoleOrdinary, raw)}, collateralTestMinStake); err == nil {
				t.Fatal("fractional amount accepted")
			}
			if !reflect.DeepEqual(before, sv.Diff()) {
				t.Fatal("invalid amount changed state")
			}
		})
	}
	sv := newMemStateView()
	seedCaseCollateralWitness(t, sv, "alice", collateralTestMinStake)
	req := &pb.RechargeRequest{RequestId: "backing"}
	mustReserveCase(t, sv, req, caseAssignment("alice", WitnessCaseRoleOriginal, WitnessCaseBondRaw))
	bal := GetBalance(sv, "alice", FeeToken)
	bal.WitnessLockedBalance = "900000000"
	SetBalance(sv, "alice", FeeToken, bal)
	before := sv.Diff()
	if _, err := slashWitnessCaseCollateral(sv, req, []string{"alice"}); err == nil {
		t.Fatal("clipped an unbacked debit")
	}
	if !reflect.DeepEqual(before, sv.Diff()) || req.CaseCollateral[0].SlashApplied {
		t.Fatal("unbacked debit mutated state")
	}
}

type failCaseCollateralLedgerView struct{ StateView }

func (v failCaseCollateralLedgerView) Scan(prefix string) (map[string][]byte, error) {
	if strings.HasPrefix(prefix, keys.KeyUserActivityPrefix("zed")) {
		return nil, errors.New("injected late ledger failure")
	}
	return v.StateView.Scan(prefix)
}

func TestWitnessCaseCollateralLedgerFailureRollsBackWholeCommittee(t *testing.T) {
	sv := newMemStateView()
	seedCaseCollateralWitness(t, sv, "alice", collateralTestMinStake)
	seedCaseCollateralWitness(t, sv, "zed", collateralTestMinStake)
	req := &pb.RechargeRequest{RequestId: "atomic-ledger", FinalizeHeight: 88}
	mustReserveCase(t, sv, req, caseAssignment("alice", WitnessCaseRoleOriginal, WitnessCaseBondRaw), caseAssignment("zed", WitnessCaseRoleOriginal, WitnessCaseBondRaw))
	before, beforeReq := sv.Diff(), proto.Clone(req)
	if _, err := slashWitnessCaseCollateral(failCaseCollateralLedgerView{sv}, req, []string{"alice", "zed"}); err == nil {
		t.Fatal("injected ledger failure did not reach caller")
	}
	if !reflect.DeepEqual(before, sv.Diff()) || !proto.Equal(beforeReq, req) {
		t.Fatal("late failure left balances, metadata, logs, or req mutated")
	}
}

func TestLedgerWitnessLockedBucketRoundTripAndStrictRead(t *testing.T) {
	sv := newMemStateView()
	SetBalance(sv, "alice", FeeToken, &pb.TokenBalance{Balance: "100", WitnessLockedBalance: "0", MinerLockedBalance: "7"})
	ledger := NewLedger(sv)
	for _, move := range []MoveRequest{
		{Account: "alice", Token: FeeToken, Amount: big.NewInt(40), From: BucketAvailable, To: BucketWitnessLocked, Reason: "witness_stake"},
		{Account: "alice", Token: FeeToken, Amount: big.NewInt(15), From: BucketWitnessLocked, To: BucketAvailable, Reason: "witness_claim"},
	} {
		if err := ledger.Move(move); err != nil {
			t.Fatal(err)
		}
	}
	bal := GetBalance(sv, "alice", FeeToken)
	if bal.Balance != "75" || bal.WitnessLockedBalance != "25" || bal.MinerLockedBalance != "7" {
		t.Fatalf("bucket mutation incorrect: %+v", bal)
	}
	bal.WitnessLockedBalance = "25.5"
	SetBalance(sv, "alice", FeeToken, bal)
	if err := ledger.Move(MoveRequest{Account: "alice", Token: FeeToken, Amount: big.NewInt(1), From: BucketWitnessLocked, To: BucketExternal, Reason: "bad_fraction"}); err == nil {
		t.Fatal("witness bucket truncated a fraction")
	}
}

func TestWitnessStakeLedgerKeepsExitAndOldCollateral(t *testing.T) {
	sv := newMemStateView()
	seedCaseCollateralWitness(t, sv, "alice", collateralTestMinStake)
	account, _ := proto.Marshal(&pb.Account{Address: "alice"})
	sv.Set(keys.KeyAccount("alice"), account)
	info := readCaseCollateralWitness(t, sv, "alice")
	info.Status, info.UnstakeHeight, info.ActiveCaseCount, info.ReservedStakeAmount = pb.WitnessStatus_WITNESS_UNSTAKING, 7, 1, WitnessCaseBondRaw
	writeCaseCollateralWitness(t, sv, info)
	SetBalance(sv, "alice", FeeToken, &pb.TokenBalance{Balance: "6400000000", WitnessLockedBalance: collateralTestMinStake})
	tx := &pb.AnyTx{Content: &pb.AnyTx_WitnessStakeTx{WitnessStakeTx: &pb.WitnessStakeTx{
		Base: &pb.BaseMessage{TxId: "topup-exiting", FromAddress: "alice", ExecutedHeight: 20}, Op: pb.OrderOp_ADD, Amount: collateralTestMinStake,
	}}}
	if ops, receipt, err := (&WitnessStakeTxHandler{}).DryRun(tx, sv); err != nil || receipt == nil || receipt.Status != "SUCCEED" || len(ops) != 0 {
		t.Fatalf("topup failed: ops=%d receipt=%+v err=%v", len(ops), receipt, err)
	}
	info = readCaseCollateralWitness(t, sv, "alice")
	if info.Status != pb.WitnessStatus_WITNESS_UNSTAKING || info.UnstakeHeight != 7 || info.ActiveCaseCount != 1 || info.ReservedStakeAmount != WitnessCaseBondRaw {
		t.Fatalf("topup reset old responsibility: %+v", info)
	}
	bal := GetBalance(sv, "alice", FeeToken)
	if bal.Balance != collateralTestMinStake || bal.WitnessLockedBalance != "6400000000" || info.StakeAmount != "6400000000" {
		t.Fatalf("topup did not conserve real funds: balance=%+v info=%+v", bal, info)
	}
	entries, err := NewLedger(sv).Query(LedgerFilter{Account: "alice", Reason: "witness_stake"})
	if err != nil || len(entries) != 1 || entries[0].From != BucketAvailable || entries[0].To != BucketWitnessLocked {
		t.Fatalf("topup ledger: %+v %v", entries, err)
	}
}

func TestWitnessStakeLedgerRejectsFractionsAndRollsBackLogFailure(t *testing.T) {
	sv := newMemStateView()
	seedCaseCollateralWitness(t, sv, "zed", collateralTestMinStake)
	account, _ := proto.Marshal(&pb.Account{Address: "zed"})
	sv.Set(keys.KeyAccount("zed"), account)
	SetBalance(sv, "zed", FeeToken, &pb.TokenBalance{Balance: "6400000000", WitnessLockedBalance: collateralTestMinStake})
	makeTx := func(amount string) *pb.AnyTx {
		return &pb.AnyTx{Content: &pb.AnyTx_WitnessStakeTx{WitnessStakeTx: &pb.WitnessStakeTx{
			Base: &pb.BaseMessage{TxId: "strict-topup", FromAddress: "zed", ExecutedHeight: 20}, Op: pb.OrderOp_ADD, Amount: amount,
		}}}
	}
	before := sv.Diff()
	for _, amount := range []string{"3200000000.5", "3.2e9", "+3200000000"} {
		_, receipt, _ := (&WitnessStakeTxHandler{}).DryRun(makeTx(amount), sv)
		if receipt == nil || receipt.Status != "FAILED" || !reflect.DeepEqual(before, sv.Diff()) {
			t.Fatalf("noninteger stake %s altered state", amount)
		}
	}
	_, receipt, err := (&WitnessStakeTxHandler{}).DryRun(makeTx(collateralTestMinStake), failCaseCollateralLedgerView{sv})
	if err == nil || receipt == nil || receipt.Status != "FAILED" || !reflect.DeepEqual(before, sv.Diff()) {
		t.Fatalf("late stake ledger failure not atomic: receipt=%+v err=%v", receipt, err)
	}
}

func TestWitnessClaimStakeLedgerConservesAndRejectsOverflowedLockDeadline(t *testing.T) {
	sv := newMemStateView()
	seedCaseCollateralWitness(t, sv, "alice", collateralTestMinStake)
	info := readCaseCollateralWitness(t, sv, "alice")
	info.Status, info.UnstakeHeight = pb.WitnessStatus_WITNESS_UNSTAKING, math.MaxUint64-2
	writeCaseCollateralWitness(t, sv, info)
	handler := &WitnessClaimStakeTxHandler{UnstakeLockBlocks: 10}
	makeTx := func(height uint64) *pb.AnyTx {
		return &pb.AnyTx{Content: &pb.AnyTx_WitnessClaimStakeTx{WitnessClaimStakeTx: &pb.WitnessClaimStakeTx{
			Base: &pb.BaseMessage{TxId: "claim-final", FromAddress: "alice", ExecutedHeight: height},
		}}}
	}
	before := sv.Diff()
	_, receipt, err := handler.DryRun(makeTx(math.MaxUint64), sv)
	if err != nil || receipt == nil || receipt.Status != "FAILED" || !reflect.DeepEqual(before, sv.Diff()) {
		t.Fatalf("overflowed lock deadline bypassed: %+v %v", receipt, err)
	}
	info.UnstakeHeight = 1
	writeCaseCollateralWitness(t, sv, info)
	if ops, receipt, err := handler.DryRun(makeTx(11), sv); err != nil || receipt == nil || receipt.Status != "SUCCEED" || len(ops) != 0 {
		t.Fatalf("claim: %+v %v", receipt, err)
	}
	bal := GetBalance(sv, "alice", FeeToken)
	info = readCaseCollateralWitness(t, sv, "alice")
	if bal.Balance != "3200000123" || bal.WitnessLockedBalance != "0" || info.StakeAmount != "0" || info.Status != pb.WitnessStatus_WITNESS_EXITED {
		t.Fatalf("claim failed fund conservation: %+v %+v", bal, info)
	}
	entries, err := NewLedger(sv).Query(LedgerFilter{Account: "alice", Reason: "witness_stake_claim"})
	if err != nil || len(entries) != 1 || entries[0].From != BucketWitnessLocked || entries[0].To != BucketAvailable {
		t.Fatalf("claim ledger: %+v %v", entries, err)
	}
}
