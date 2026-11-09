package vm

import (
	"dex/formal/itftrace"
	"dex/formal/refinement"
	"dex/future"
	"dex/keys"
	"dex/pb"
	"dex/rent"
	"math/big"
	"os"
	"reflect"
	"strconv"
	"testing"

	"google.golang.org/protobuf/proto"
)

const formalConservationToken = FeeToken

type formalConservationGhostState struct {
	insuranceCredits int64
	insuranceDebits  int64
}

func TestFormalGlobalConservationITFDifferential(t *testing.T) {
	path := os.Getenv("DEX_FORMAL_ITF_GLOBAL")
	if path == "" {
		t.Skip("DEX_FORMAL_ITF_GLOBAL is set by formal/run-go-differential.ps1")
	}
	trace, err := itftrace.Load(path)
	if err != nil {
		t.Fatalf("load Quint ITF trace: %v", err)
	}
	if len(trace.States) != 6 {
		t.Fatalf("global differential trace states=%d want=6", len(trace.States))
	}
	sv := NewMockStateView()
	ghost := formalConservationGhostState{}
	applyFormalConservationOps(sv, formalConservationDiff(t, trace.States[0], trace.States[0]))
	assertFormalConservationStateRelation(t, sv, trace.States[0], ghost, 0)

	for i := 1; i < len(trace.States); i++ {
		before := formalConservationStateFromView(t, sv, ghost)
		diff := formalConservationDiff(t, trace.States[i-1], trace.States[i])
		assertFormalConservationKernel(t, diff, i, trace.States[i].Action)
		if recovered := callGlobalAudit(diff, uint64(i)); recovered != nil {
			t.Fatalf("production audit rejected Quint transition %d action=%s: %v",
				i, trace.States[i].Action, recovered)
		}
		applyFormalConservationOps(sv, diff)
		after := formalConservationStateFromView(t, sv, ghost)
		insuranceDelta := after.InsuranceFund - before.InsuranceFund
		if insuranceDelta > 0 {
			ghost.insuranceCredits += insuranceDelta
		} else if insuranceDelta < 0 {
			ghost.insuranceDebits -= insuranceDelta
		}
		assertFormalConservationStateRelation(t, sv, trace.States[i], ghost, i)
	}
}

func applyFormalConservationOps(sv StateView, ops []WriteOp) {
	for _, op := range ops {
		if op.Del {
			sv.Del(op.Key)
		} else {
			sv.Set(op.Key, append([]byte(nil), op.Value...))
		}
	}
}

func assertFormalConservationStateRelation(t *testing.T, sv StateView, model itftrace.State, ghost formalConservationGhostState, step int) {
	t.Helper()
	expected, err := refinement.ConservationFromITF(model)
	if err != nil {
		t.Fatal(err)
	}
	actual := formalConservationStateFromView(t, sv, ghost)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("conservation state relation failed at step=%d action=%s\nactual:   %+v\nexpected: %+v",
			step, model.Action, actual, expected)
	}
}

func formalConservationStateFromView(t *testing.T, sv StateView, ghost formalConservationGhostState) refinement.ConservationState {
	t.Helper()
	pair := "BTC_" + formalConservationToken
	read := func(key string) []byte {
		data, exists, err := sv.Get(key)
		if err != nil {
			t.Fatalf("read normalized state key %s: %v", key, err)
		}
		if !exists {
			return nil
		}
		return data
	}
	readBalance := func(owner string) int64 {
		record, err := parseBalanceRecordForAudit(read(keys.KeyBalance(owner, formalConservationToken)))
		if err != nil {
			t.Fatalf("decode normalized balance %s: %v", owner, err)
		}
		return mustFormalBigInt64(t, balanceRecordTotalValue(record), "balance "+owner)
	}

	positionData := read(keys.KeyFuturePosition("formal-position"))
	positionToken, positionMargin := positionDeltaWithMarketQuotes(positionData, 1, map[string]string{pair: formalConservationToken})
	positionOpen := positionToken != "" && positionMargin.Sign() > 0
	challengeStake := activeChallengeStake(read(keys.KeyChallengeRecord("formal-challenge")))
	var token pb.Token
	if data := read(keys.KeyToken(formalConservationToken)); len(data) > 0 {
		if err := unmarshalProtoCompat(data, &token); err != nil {
			t.Fatalf("decode normalized token: %v", err)
		}
	}

	return refinement.ConservationState{
		Alice:            readBalance("formal-alice"),
		Bob:              readBalance("formal-bob"),
		RuntimeFeePool:   readBalance(RuntimeFeePoolAccount),
		RecycleFund:      readBalance(rent.RecycleFundAccount),
		PositionMargin:   mustFormalBigInt64(t, positionMargin, "position margin"),
		InsuranceFund:    mustFormalBigInt64(t, bytesBig(read(keys.KeyFutureInsuranceFund(pair))), "insurance fund"),
		ChallengeStake:   mustFormalBigInt64(t, challengeStake, "challenge stake"),
		TotalSupply:      mustFormalBigInt64(t, parseBigOrZero(token.TotalSupply), "total supply"),
		PositionOpen:     positionOpen,
		InsuranceCredits: ghost.insuranceCredits,
		InsuranceDebits:  ghost.insuranceDebits,
		ADLShortfall:     mustFormalBigInt64(t, bytesBig(read(keys.KeyFutureInsuranceShortfall(pair))), "ADL shortfall"),
	}
}

func mustFormalBigInt64(t *testing.T, value *big.Int, field string) int64 {
	t.Helper()
	if value == nil || !value.IsInt64() {
		t.Fatalf("normalized %s is not int64: %v", field, value)
	}
	return value.Int64()
}

func assertFormalConservationKernel(t *testing.T, diff []WriteOp, step int, action string) {
	t.Helper()
	futureQuotes := buildFutureMarketQuoteAuditIndex(diff, nil)
	totals := make(map[string]*big.Int)
	for _, op := range diff {
		plan := planConservationAuditOp(op.Key, futureQuotes)
		result := evaluateConservationAuditTransition(conservationAuditTransitionInput{
			Key:          op.Key,
			NewValue:     op.Value,
			OldValue:     op.OldValue,
			FutureQuotes: futureQuotes,
			plan:         plan,
		})
		if result.invalidBalanceRecord != "" {
			t.Fatalf("pure kernel rejected Quint transition %d action=%s: %s", step, action, result.invalidBalanceRecord)
		}
		for _, contribution := range result.contributions {
			addBig(totals, contribution.token, contribution.delta)
		}
	}
	if delta := totals[formalConservationToken]; delta != nil && delta.Sign() != 0 {
		t.Fatalf("pure kernel transition %d action=%s delta=%s", step, action, delta)
	}
}

func formalConservationDiff(t *testing.T, before, after itftrace.State) []WriteOp {
	t.Helper()
	pair := "BTC_" + formalConservationToken
	ops := make([]WriteOp, 0, 9)
	for _, bucket := range []struct {
		field string
		owner string
	}{
		{field: "alice", owner: "formal-alice"},
		{field: "bob", owner: "formal-bob"},
		{field: "runtimeFeePool", owner: RuntimeFeePoolAccount},
		{field: "recycleFund", owner: rent.RecycleFundAccount},
	} {
		oldAmount := mustFormalInt(t, before, bucket.field)
		newAmount := mustFormalInt(t, after, bucket.field)
		ops = append(ops, formalKnownWrite(
			keys.KeyBalance(bucket.owner, formalConservationToken),
			formalBalanceBytes(t, bucket.owner, newAmount),
			formalBalanceBytes(t, bucket.owner, oldAmount),
		))
	}

	oldMargin := mustFormalInt(t, before, "positionMargin")
	newMargin := mustFormalInt(t, after, "positionMargin")
	ops = append(ops, formalKnownWrite(
		keys.KeyFuturePosition("formal-position"),
		formalPositionBytes(t, pair, newMargin),
		formalPositionBytes(t, pair, oldMargin),
	))

	oldInsurance := mustFormalInt(t, before, "insuranceFund")
	newInsurance := mustFormalInt(t, after, "insuranceFund")
	ops = append(ops, formalKnownWrite(
		keys.KeyFutureInsuranceFund(pair),
		big.NewInt(newInsurance).Bytes(),
		big.NewInt(oldInsurance).Bytes(),
	))

	oldShortfall := mustFormalInt(t, before, "adlShortfall")
	newShortfall := mustFormalInt(t, after, "adlShortfall")
	ops = append(ops, formalKnownWrite(
		keys.KeyFutureInsuranceShortfall(pair),
		big.NewInt(newShortfall).Bytes(),
		big.NewInt(oldShortfall).Bytes(),
	))

	oldStake := mustFormalInt(t, before, "challengeStake")
	newStake := mustFormalInt(t, after, "challengeStake")
	ops = append(ops, formalKnownWrite(
		keys.KeyChallengeRecord("formal-challenge"),
		formalChallengeBytes(t, newStake),
		formalChallengeBytes(t, oldStake),
	))

	oldSupply := mustFormalInt(t, before, "totalSupply")
	newSupply := mustFormalInt(t, after, "totalSupply")
	ops = append(ops, formalKnownWrite(
		keys.KeyToken(formalConservationToken),
		formalTokenBytes(t, newSupply),
		formalTokenBytes(t, oldSupply),
	))
	return ops
}

func formalKnownWrite(key string, value, oldValue []byte) WriteOp {
	return WriteOp{
		Key:                   key,
		Value:                 value,
		Del:                   len(value) == 0,
		OldValue:              oldValue,
		OldExists:             len(oldValue) > 0,
		HasOldValue:           true,
		OldValueAuthoritative: true,
	}
}

func formalBalanceBytes(t *testing.T, owner string, amount int64) []byte {
	t.Helper()
	return formalMarshal(t, &pb.TokenBalanceRecord{
		Address: owner,
		Token:   formalConservationToken,
		Balance: &pb.TokenBalance{Balance: strconv.FormatInt(amount, 10)},
	})
}

func formalPositionBytes(t *testing.T, pair string, margin int64) []byte {
	t.Helper()
	if margin == 0 {
		return nil
	}
	return formalMarshal(t, &pb.FuturePositionState{
		PositionId: "formal-position",
		Owner:      "formal-alice",
		Pair:       pair,
		Side:       int32(future.Long),
		Margin:     strconv.FormatInt(margin, 10),
		Quantity:   "1",
		Status:     int32(future.StatusOpen),
	})
}

func formalChallengeBytes(t *testing.T, stake int64) []byte {
	t.Helper()
	if stake == 0 {
		return nil
	}
	return formalMarshal(t, &pb.ChallengeRecord{
		ChallengeId: "formal-challenge",
		StakeAmount: strconv.FormatInt(stake, 10),
		Status:      pb.ChallengeStatus_CHALLENGE_ACTIVE,
	})
}

func formalTokenBytes(t *testing.T, supply int64) []byte {
	t.Helper()
	return formalMarshal(t, &pb.Token{
		Address:     formalConservationToken,
		TotalSupply: strconv.FormatInt(supply, 10),
	})
}

func formalMarshal(t *testing.T, message proto.Message) []byte {
	t.Helper()
	data, err := proto.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mustFormalInt(t *testing.T, state itftrace.State, field string) int64 {
	t.Helper()
	value, err := state.Int(field)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func callGlobalAudit(diff []WriteOp, height uint64) (recovered any) {
	defer func() { recovered = recover() }()
	AuditGlobalConservationDiff(diff, height, nil)
	return nil
}
