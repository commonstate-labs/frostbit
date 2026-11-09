package vm

import (
	"dex/future"
	"dex/keys"
	"dex/pb"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
)

                                                                                                                           
func TestStrictAudit_BalanceDiffNegativeDeltaDiagnosticOnly(t *testing.T) {
	prev := StrictAuditEnabled()
	SetStrictAudit(true)
	defer SetStrictAudit(prev)

	addr := "alice"
	token := "USDT"
	balKey := keys.KeyBalance(addr, token)

	oldRec := &pb.TokenBalanceRecord{
		Address: addr, Token: token,
		Balance: &pb.TokenBalance{Balance: "1000", OrderFrozenBalance: "0", MinerLockedBalance: "0", WitnessLockedBalance: "0"},
	}
	oldBytes, _ := proto.Marshal(oldRec)
	newRec := &pb.TokenBalanceRecord{
		Address: addr, Token: token,
		Balance: &pb.TokenBalance{Balance: "300", OrderFrozenBalance: "0", MinerLockedBalance: "0", WitnessLockedBalance: "0"},
	}
	newBytes, _ := proto.Marshal(newRec)

	diff := []WriteOp{{Key: balKey, Value: newBytes, Category: "balance"}}
	readFn := func(key string) ([]byte, error) {
		if key == balKey {
			return oldBytes, nil
		}
		return nil, nil
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("balance-only diff should not panic before cross-system audit, got: %v", r)
		}
	}()
	AuditBalanceDiffConservation(diff, 1, readFn)
}

                                                                   
func TestStrictAuditEnv_ToggleAPI(t *testing.T) {
	prev := StrictAuditEnabled()
	defer SetStrictAudit(prev)

	SetStrictAudit(true)
	if !StrictAuditEnabled() {
		t.Fatal("expected strict enabled after SetStrictAudit(true)")
	}
	SetStrictAudit(false)
	if StrictAuditEnabled() {
		t.Fatal("expected strict disabled after SetStrictAudit(false)")
	}
}

func TestStrictAudit_GlobalDiffMarginLeakPanics(t *testing.T) {
	prev := StrictAuditEnabled()
	SetStrictAudit(true)
	defer SetStrictAudit(prev)

	pair := "BTC_USDT"
	oldPos := &pb.FuturePositionState{PositionId: "p1", Owner: "alice", Pair: pair, Side: int32(future.Short), Margin: "1000", Quantity: "1", Status: int32(future.StatusOpen)}
	newPos := &pb.FuturePositionState{PositionId: "p1", Owner: "alice", Pair: pair, Side: int32(future.Short), Margin: "900", Quantity: "1", Status: int32(future.StatusOpen)}
	oldBytes, _ := proto.Marshal(oldPos)
	newBytes, _ := proto.Marshal(newPos)
	posKey := keys.KeyFuturePosition("p1")
	diff := []WriteOp{{Key: posKey, Value: newBytes}}
	readFn := func(key string) ([]byte, error) {
		if key == posKey {
			return oldBytes, nil
		}
		return nil, nil
	}

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("margin decrease without matching balance or insurance credit must panic")
		}
	}()
	AuditGlobalConservationDiff(diff, 7, readFn)
}

func TestStrictAudit_GlobalDiffBalancedMarginToBalancePasses(t *testing.T) {
	prev := StrictAuditEnabled()
	SetStrictAudit(true)
	defer SetStrictAudit(prev)

	pair := "BTC_USDT"
	oldPos := &pb.FuturePositionState{PositionId: "p1", Owner: "alice", Pair: pair, Side: int32(future.Short), Margin: "1000", Quantity: "1", Status: int32(future.StatusOpen)}
	newPos := &pb.FuturePositionState{PositionId: "p1", Owner: "alice", Pair: pair, Side: int32(future.Short), Margin: "900", Quantity: "1", Status: int32(future.StatusOpen)}
	oldPosBytes, _ := proto.Marshal(oldPos)
	newPosBytes, _ := proto.Marshal(newPos)

	balKey := keys.KeyBalance("alice", "USDT")
	oldBal := &pb.TokenBalanceRecord{Address: "alice", Token: "USDT", Balance: &pb.TokenBalance{Balance: "0"}}
	newBal := &pb.TokenBalanceRecord{Address: "alice", Token: "USDT", Balance: &pb.TokenBalance{Balance: "100"}}
	oldBalBytes, _ := proto.Marshal(oldBal)
	newBalBytes, _ := proto.Marshal(newBal)

	posKey := keys.KeyFuturePosition("p1")
	diff := []WriteOp{{Key: balKey, Value: newBalBytes}, {Key: posKey, Value: newPosBytes}}
	readFn := func(key string) ([]byte, error) {
		switch key {
		case posKey:
			return oldPosBytes, nil
		case balKey:
			return oldBalBytes, nil
		default:
			return nil, nil
		}
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("balanced margin to balance transfer should pass, got panic: %v", r)
		}
	}()
	AuditGlobalConservationDiff(diff, 8, readFn)
}

func TestStrictAudit_GlobalDiffCoalescesDuplicateKeys(t *testing.T) {
	prev := StrictAuditEnabled()
	SetStrictAudit(true)
	defer SetStrictAudit(prev)

	pair := "BTC_USDT"
	oldPos := &pb.FuturePositionState{PositionId: "p1", Owner: "alice", Pair: pair, Side: int32(future.Long), Margin: "1000", Quantity: "1", Status: int32(future.StatusOpen)}
	midPos := &pb.FuturePositionState{PositionId: "p1", Owner: "alice", Pair: pair, Side: int32(future.Long), Margin: "800", Quantity: "1", Status: int32(future.StatusOpen)}
	finalPos := &pb.FuturePositionState{PositionId: "p1", Owner: "alice", Pair: pair, Side: int32(future.Long), Margin: "900", Quantity: "1", Status: int32(future.StatusOpen)}
	oldPosBytes, _ := proto.Marshal(oldPos)
	midPosBytes, _ := proto.Marshal(midPos)
	finalPosBytes, _ := proto.Marshal(finalPos)

	balKey := keys.KeyBalance("alice", "USDT")
	oldBal := &pb.TokenBalanceRecord{Address: "alice", Token: "USDT", Balance: &pb.TokenBalance{Balance: "0"}}
	newBal := &pb.TokenBalanceRecord{Address: "alice", Token: "USDT", Balance: &pb.TokenBalance{Balance: "100"}}
	oldBalBytes, _ := proto.Marshal(oldBal)
	newBalBytes, _ := proto.Marshal(newBal)

	posKey := keys.KeyFuturePosition("p1")
	diff := []WriteOp{{Key: posKey, Value: midPosBytes}, {Key: balKey, Value: newBalBytes}, {Key: posKey, Value: finalPosBytes}}
	readFn := func(key string) ([]byte, error) {
		switch key {
		case posKey:
			return oldPosBytes, nil
		case balKey:
			return oldBalBytes, nil
		default:
			return nil, nil
		}
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("duplicate key writes should be audited by final value only, got panic: %v", r)
		}
	}()
	AuditGlobalConservationDiff(diff, 11, readFn)
}

func TestStrictAudit_GlobalDiffChallengeStakeEscrowPasses(t *testing.T) {
	prev := StrictAuditEnabled()
	SetStrictAudit(true)
	defer SetStrictAudit(prev)

	balKey := keys.KeyBalance("challenger", FeeToken)
	oldBal := &pb.TokenBalanceRecord{Address: "challenger", Token: FeeToken, Balance: &pb.TokenBalance{Balance: "1000"}}
	newBal := &pb.TokenBalanceRecord{Address: "challenger", Token: FeeToken, Balance: &pb.TokenBalance{Balance: "700"}}
	oldBalBytes, _ := proto.Marshal(oldBal)
	newBalBytes, _ := proto.Marshal(newBal)

	challengeKey := keys.KeyChallengeRecord("c1")
	challenge := &pb.ChallengeRecord{ChallengeId: "c1", StakeAmount: "300", Status: pb.ChallengeStatus_CHALLENGE_PENDING_SELECTION}
	challengeBytes, _ := proto.Marshal(challenge)
	diff := []WriteOp{{Key: balKey, Value: newBalBytes}, {Key: challengeKey, Value: challengeBytes}}
	readFn := func(key string) ([]byte, error) {
		if key == balKey {
			return oldBalBytes, nil
		}
		return nil, nil
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("challenge stake escrow should balance available FB decrease, got panic: %v", r)
		}
	}()
	AuditGlobalConservationDiff(diff, 9, readFn)
}

func TestStrictAudit_GlobalDiffFinalizedChallengeRefundPasses(t *testing.T) {
	prev := StrictAuditEnabled()
	SetStrictAudit(true)
	defer SetStrictAudit(prev)

	balKey := keys.KeyBalance("challenger", FeeToken)
	oldBal := &pb.TokenBalanceRecord{Address: "challenger", Token: FeeToken, Balance: &pb.TokenBalance{Balance: "0"}}
	newBal := &pb.TokenBalanceRecord{Address: "challenger", Token: FeeToken, Balance: &pb.TokenBalance{Balance: "300"}}
	oldBalBytes, _ := proto.Marshal(oldBal)
	newBalBytes, _ := proto.Marshal(newBal)

	challengeKey := keys.KeyChallengeRecord("c1")
	oldChallenge := &pb.ChallengeRecord{ChallengeId: "c1", StakeAmount: "300", Status: pb.ChallengeStatus_CHALLENGE_ACTIVE}
	newChallenge := &pb.ChallengeRecord{ChallengeId: "c1", StakeAmount: "300", Status: pb.ChallengeStatus_CHALLENGE_FINALIZED, Finalized: true}
	oldChallengeBytes, _ := proto.Marshal(oldChallenge)
	newChallengeBytes, _ := proto.Marshal(newChallenge)
	diff := []WriteOp{{Key: balKey, Value: newBalBytes}, {Key: challengeKey, Value: newChallengeBytes}}
	readFn := func(key string) ([]byte, error) {
		switch key {
		case balKey:
			return oldBalBytes, nil
		case challengeKey:
			return oldChallengeBytes, nil
		default:
			return nil, nil
		}
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("finalized challenge refund should balance escrow release, got panic: %v", r)
		}
	}()
	AuditGlobalConservationDiff(diff, 10, readFn)
}

func TestGlobalDiffUsesWriteOpOldValueWithoutReadFn(t *testing.T) {
	prev := StrictAuditEnabled()
	SetStrictAudit(false)
	defer SetStrictAudit(prev)

	pair := "BTC_USDT"
	oldPos := &pb.FuturePositionState{PositionId: "p1", Owner: "alice", Pair: pair, Side: int32(future.Short), Margin: "1000", Quantity: "1", Status: int32(future.StatusOpen)}
	newPos := &pb.FuturePositionState{PositionId: "p1", Owner: "alice", Pair: pair, Side: int32(future.Short), Margin: "900", Quantity: "1", Status: int32(future.StatusOpen)}
	oldPosBytes := mustMarshalAudit(t, oldPos)
	newPosBytes := mustMarshalAudit(t, newPos)

	balKey := keys.KeyBalance("alice", "USDT")
	oldBal := &pb.TokenBalanceRecord{Address: "alice", Token: "USDT", Balance: &pb.TokenBalance{Balance: "0"}}
	newBal := &pb.TokenBalanceRecord{Address: "alice", Token: "USDT", Balance: &pb.TokenBalance{Balance: "100"}}
	oldBalBytes := mustMarshalAudit(t, oldBal)
	newBalBytes := mustMarshalAudit(t, newBal)

	posKey := keys.KeyFuturePosition("p1")
	diff := []WriteOp{
		{Key: balKey, Value: newBalBytes, OldValue: oldBalBytes, OldExists: true, HasOldValue: true, OldValueAuthoritative: true},
		{Key: posKey, Value: newPosBytes, OldValue: oldPosBytes, OldExists: true, HasOldValue: true, OldValueAuthoritative: true},
	}
	readFn := func(key string) ([]byte, error) {
		t.Fatalf("readFn should not be called when WriteOp old value is known: %s", key)
		return nil, nil
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("balanced diff using WriteOp old values should pass, got panic: %v", r)
		}
	}()
	AuditGlobalConservationDiff(diff, 12, readFn)
}

func TestGlobalDiffCoalescesDuplicateKeysKeepsFirstOldValue(t *testing.T) {
	prev := StrictAuditEnabled()
	SetStrictAudit(false)
	defer SetStrictAudit(prev)

	pair := "BTC_USDT"
	oldPos := &pb.FuturePositionState{PositionId: "p1", Owner: "alice", Pair: pair, Side: int32(future.Long), Margin: "1000", Quantity: "1", Status: int32(future.StatusOpen)}
	midPos := &pb.FuturePositionState{PositionId: "p1", Owner: "alice", Pair: pair, Side: int32(future.Long), Margin: "800", Quantity: "1", Status: int32(future.StatusOpen)}
	finalPos := &pb.FuturePositionState{PositionId: "p1", Owner: "alice", Pair: pair, Side: int32(future.Long), Margin: "900", Quantity: "1", Status: int32(future.StatusOpen)}
	oldPosBytes := mustMarshalAudit(t, oldPos)
	midPosBytes := mustMarshalAudit(t, midPos)
	finalPosBytes := mustMarshalAudit(t, finalPos)

	balKey := keys.KeyBalance("alice", "USDT")
	oldBal := &pb.TokenBalanceRecord{Address: "alice", Token: "USDT", Balance: &pb.TokenBalance{Balance: "0"}}
	newBal := &pb.TokenBalanceRecord{Address: "alice", Token: "USDT", Balance: &pb.TokenBalance{Balance: "100"}}
	oldBalBytes := mustMarshalAudit(t, oldBal)
	newBalBytes := mustMarshalAudit(t, newBal)

	posKey := keys.KeyFuturePosition("p1")
	diff := []WriteOp{
		{Key: posKey, Value: midPosBytes, OldValue: oldPosBytes, OldExists: true, HasOldValue: true, OldValueAuthoritative: true},
		{Key: balKey, Value: newBalBytes, OldValue: oldBalBytes, OldExists: true, HasOldValue: true, OldValueAuthoritative: true},
		{Key: posKey, Value: finalPosBytes},
	}
	readFn := func(key string) ([]byte, error) {
		t.Fatalf("readFn should not be called when coalesced WriteOp old value is known: %s", key)
		return nil, nil
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("duplicate key coalesce should preserve first old value, got panic: %v", r)
		}
	}()
	AuditGlobalConservationDiff(diff, 13, readFn)
}

func TestGlobalDiffCoalescesDuplicateKeysOverridesLaterOldValue(t *testing.T) {
	prev := StrictAuditEnabled()
	SetStrictAudit(false)
	defer SetStrictAudit(prev)

	balKey := keys.KeyBalance("alice", "USDT")
	oldBal := &pb.TokenBalanceRecord{Address: "alice", Token: "USDT", Balance: &pb.TokenBalance{Balance: "100"}}
	midBal := &pb.TokenBalanceRecord{Address: "alice", Token: "USDT", Balance: &pb.TokenBalance{Balance: "70"}}
	finalBal := &pb.TokenBalanceRecord{Address: "alice", Token: "USDT", Balance: &pb.TokenBalance{Balance: "0"}}
	oldToken := &pb.Token{Address: "USDT", TotalSupply: "100"}
	newToken := &pb.Token{Address: "USDT", TotalSupply: "0"}
	diff := []WriteOp{
		{Key: balKey, Value: mustMarshalAudit(t, midBal), OldValue: mustMarshalAudit(t, oldBal), OldExists: true, HasOldValue: true, OldValueAuthoritative: true},
		{Key: balKey, Value: mustMarshalAudit(t, finalBal), OldValue: mustMarshalAudit(t, midBal), OldExists: true, HasOldValue: true, OldValueAuthoritative: true},
		{Key: keys.KeyToken("USDT"), Value: mustMarshalAudit(t, newToken), OldValue: mustMarshalAudit(t, oldToken), OldExists: true, HasOldValue: true, OldValueAuthoritative: true},
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("duplicate key coalesce should use first old value even when last op has old value, got panic: %v", r)
		}
	}()
	AuditGlobalConservationDiff(diff, 19, nil)
}

func TestGlobalDiffUnbalancedPositivePanicsWithoutStrict(t *testing.T) {
	prev := StrictAuditEnabled()
	SetStrictAudit(false)
	defer SetStrictAudit(prev)

	balKey := keys.KeyBalance("alice", "USDT")
	oldBal := &pb.TokenBalanceRecord{Address: "alice", Token: "USDT", Balance: &pb.TokenBalance{Balance: "0"}}
	newBal := &pb.TokenBalanceRecord{Address: "alice", Token: "USDT", Balance: &pb.TokenBalance{Balance: "100"}}
	diff := []WriteOp{{Key: balKey, Value: mustMarshalAudit(t, newBal), OldValue: mustMarshalAudit(t, oldBal), OldExists: true, HasOldValue: true, OldValueAuthoritative: true}}

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("positive unbalanced balance delta must panic even when strict audit is off")
		}
	}()
	AuditGlobalConservationDiff(diff, 14, nil)
}

func TestGlobalDiffSupplyDeltaBalancesMint(t *testing.T) {
	prev := StrictAuditEnabled()
	SetStrictAudit(false)
	defer SetStrictAudit(prev)

	token := "USDT"
	balKey := keys.KeyBalance("alice", token)
	oldBal := &pb.TokenBalanceRecord{Address: "alice", Token: token, Balance: &pb.TokenBalance{Balance: "0"}}
	newBal := &pb.TokenBalanceRecord{Address: "alice", Token: token, Balance: &pb.TokenBalance{Balance: "100"}}
	oldToken := &pb.Token{Address: token, TotalSupply: "0"}
	newToken := &pb.Token{Address: token, TotalSupply: "100"}
	diff := []WriteOp{
		{Key: balKey, Value: mustMarshalAudit(t, newBal), OldValue: mustMarshalAudit(t, oldBal), OldExists: true, HasOldValue: true, OldValueAuthoritative: true},
		{Key: keys.KeyToken(token), Value: mustMarshalAudit(t, newToken), OldValue: mustMarshalAudit(t, oldToken), OldExists: true, HasOldValue: true, OldValueAuthoritative: true},
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("balance increase matched by supply increase should pass, got panic: %v", r)
		}
	}()
	AuditGlobalConservationDiff(diff, 15, nil)
}

func TestGlobalDiffSupplyDeltaBalancesMintWithUnderscoreToken(t *testing.T) {
	token := "br_0123456789abcdef0123"
	oldBal := &pb.TokenBalanceRecord{Address: "alice", Token: token, Balance: &pb.TokenBalance{Balance: "0"}}
	newBal := &pb.TokenBalanceRecord{Address: "alice", Token: token, Balance: &pb.TokenBalance{Balance: "100"}}
	oldToken := &pb.Token{Address: token, TotalSupply: "0"}
	newToken := &pb.Token{Address: token, TotalSupply: "100"}
	diff := []WriteOp{
		{Key: keys.KeyBalance("alice", token), Value: mustMarshalAudit(t, newBal), OldValue: mustMarshalAudit(t, oldBal), OldExists: true, HasOldValue: true, OldValueAuthoritative: true},
		{Key: keys.KeyToken(token), Value: mustMarshalAudit(t, newToken), OldValue: mustMarshalAudit(t, oldToken), OldExists: true, HasOldValue: true, OldValueAuthoritative: true},
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("underscore token mint should conserve funds, got panic: %v", r)
		}
	}()
	AuditGlobalConservationDiff(diff, 22, nil)
}

func TestGlobalDiffRejectsBalanceRecordTokenMismatch(t *testing.T) {
	oldBal := &pb.TokenBalanceRecord{Address: "alice", Token: "br_old", Balance: &pb.TokenBalance{Balance: "100"}}
	newBal := &pb.TokenBalanceRecord{Address: "alice", Token: "br_new", Balance: &pb.TokenBalance{Balance: "100"}}
	diff := []WriteOp{{
		Key: keys.KeyBalance("alice", "br_old"), Value: mustMarshalAudit(t, newBal),
		OldValue: mustMarshalAudit(t, oldBal), OldExists: true, HasOldValue: true, OldValueAuthoritative: true,
	}}

	defer func() {
		r := recover()
		if r == nil || !strings.Contains(fmt.Sprint(r), "token mismatch") {
			t.Fatalf("balance token mismatch must fail closed, got: %v", r)
		}
	}()
	AuditGlobalConservationDiff(diff, 23, nil)
}

func TestGlobalDiffBlockRewardAndFeeDistributionPasses(t *testing.T) {
	prev := StrictAuditEnabled()
	SetStrictAudit(false)
	defer SetStrictAudit(prev)

	userKey := keys.KeyBalance("user", FeeToken)
	minerKey := keys.KeyBalance("miner", FeeToken)
	oldUser := &pb.TokenBalanceRecord{Address: "user", Token: FeeToken, Balance: &pb.TokenBalance{Balance: "1000"}}
	newUser := &pb.TokenBalanceRecord{Address: "user", Token: FeeToken, Balance: &pb.TokenBalance{Balance: "900"}}
	oldMiner := &pb.TokenBalanceRecord{Address: "miner", Token: FeeToken, Balance: &pb.TokenBalance{Balance: "0"}}
	newMiner := &pb.TokenBalanceRecord{Address: "miner", Token: FeeToken, Balance: &pb.TokenBalance{Balance: "1100"}}
	oldToken := &pb.Token{Address: FeeToken, TotalSupply: "100000"}
	newToken := &pb.Token{Address: FeeToken, TotalSupply: "101000"}
	diff := []WriteOp{
		{Key: userKey, Value: mustMarshalAudit(t, newUser), OldValue: mustMarshalAudit(t, oldUser), OldExists: true, HasOldValue: true, OldValueAuthoritative: true},
		{Key: minerKey, Value: mustMarshalAudit(t, newMiner), OldValue: mustMarshalAudit(t, oldMiner), OldExists: true, HasOldValue: true, OldValueAuthoritative: true},
		{Key: keys.KeyToken(FeeToken), Value: mustMarshalAudit(t, newToken), OldValue: mustMarshalAudit(t, oldToken), OldExists: true, HasOldValue: true, OldValueAuthoritative: true},
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("block reward supply increase plus fee redistribution should pass, got panic: %v", r)
		}
	}()
	AuditGlobalConservationDiff(diff, 17, nil)
}

func TestGlobalDiffIncludesAllTokenBalanceBuckets(t *testing.T) {
	prev := StrictAuditEnabled()
	SetStrictAudit(false)
	defer SetStrictAudit(prev)

	token := "ARB.USDC"
	balKey := keys.KeyBalance("alice", token)
	oldBal := &pb.TokenBalanceRecord{Address: "alice", Token: token, Balance: &pb.TokenBalance{Balance: "329"}}
	newBal := &pb.TokenBalanceRecord{Address: "alice", Token: token, Balance: &pb.TokenBalance{
		Balance:               "0",
		LiquidLockedBalance:   "123",
		LeverageLockedBalance: "206",
	}}
	diff := []WriteOp{{Key: balKey, Value: mustMarshalAudit(t, newBal), OldValue: mustMarshalAudit(t, oldBal), OldExists: true, HasOldValue: true, OldValueAuthoritative: true}}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("moving balance into liquid/leverage buckets should conserve funds, got panic: %v", r)
		}
	}()
	AuditGlobalConservationDiff(diff, 18, nil)
}

func TestGlobalDiffDeleteUsesOldValue(t *testing.T) {
	prev := StrictAuditEnabled()
	SetStrictAudit(false)
	defer SetStrictAudit(prev)

	token := "USDT"
	balKey := keys.KeyBalance("alice", token)
	oldBal := &pb.TokenBalanceRecord{Address: "alice", Token: token, Balance: &pb.TokenBalance{Balance: "100"}}
	oldToken := &pb.Token{Address: token, TotalSupply: "100"}
	newToken := &pb.Token{Address: token, TotalSupply: "0"}
	diff := []WriteOp{
		{Key: balKey, Del: true, OldValue: mustMarshalAudit(t, oldBal), OldExists: true, HasOldValue: true, OldValueAuthoritative: true},
		{Key: keys.KeyToken(token), Value: mustMarshalAudit(t, newToken), OldValue: mustMarshalAudit(t, oldToken), OldExists: true, HasOldValue: true, OldValueAuthoritative: true},
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("balance delete matched by supply decrease should pass, got panic: %v", r)
		}
	}()
	AuditGlobalConservationDiff(diff, 16, nil)
}

func TestGlobalDiffParallelWorkerCount(t *testing.T) {
	prevProcs := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(prevProcs)

	if got := conservationAuditWorkerCount(2047); got != 1 {
		t.Fatalf("small audit should stay serial, got workers=%d", got)
	}
	if got := conservationAuditWorkerCount(4096); got < 2 {
		t.Fatalf("large audit should use parallel workers, got workers=%d", got)
	}
	if got := conservationAuditWorkerCount(8192); got != 4 {
		t.Fatalf("worker count should respect GOMAXPROCS and shard size, got workers=%d", got)
	}
}

func TestGlobalDiffParallelPathBalancedPasses(t *testing.T) {
	prevProcs := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(prevProcs)

	token := "USDT"
	diff := auditNoiseOps(4096)
	oldBal := &pb.TokenBalanceRecord{Address: "alice", Token: token, Balance: &pb.TokenBalance{Balance: "100"}}
	newBal := &pb.TokenBalanceRecord{Address: "alice", Token: token, Balance: &pb.TokenBalance{Balance: "0"}}
	oldToken := &pb.Token{Address: token, TotalSupply: "100"}
	newToken := &pb.Token{Address: token, TotalSupply: "0"}
	diff = append(diff,
		WriteOp{Key: keys.KeyBalance("alice", token), Value: mustMarshalAudit(t, newBal), OldValue: mustMarshalAudit(t, oldBal), OldExists: true, HasOldValue: true, OldValueAuthoritative: true},
		WriteOp{Key: keys.KeyToken(token), Value: mustMarshalAudit(t, newToken), OldValue: mustMarshalAudit(t, oldToken), OldExists: true, HasOldValue: true, OldValueAuthoritative: true},
	)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("balanced parallel audit should pass, got panic: %v", r)
		}
	}()
	stats := AuditGlobalConservationDiffWithStats(diff, 20, nil)
	if stats.CoalescedOps != len(diff) {
		t.Fatalf("coalesced ops = %d, want %d", stats.CoalescedOps, len(diff))
	}
	if stats.OtherOps != 4096 || stats.BalanceOps != 1 || stats.TokenOps != 1 {
		t.Fatalf("unexpected parallel stats: other=%d balance=%d token=%d", stats.OtherOps, stats.BalanceOps, stats.TokenOps)
	}
}

func TestGlobalDiffParallelPathUnbalancedPanics(t *testing.T) {
	prevProcs := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(prevProcs)

	token := "USDT"
	diff := auditNoiseOps(4096)
	oldBal := &pb.TokenBalanceRecord{Address: "alice", Token: token, Balance: &pb.TokenBalance{Balance: "0"}}
	newBal := &pb.TokenBalanceRecord{Address: "alice", Token: token, Balance: &pb.TokenBalance{Balance: "100"}}
	diff = append(diff, WriteOp{
		Key: keys.KeyBalance("alice", token), Value: mustMarshalAudit(t, newBal),
		OldValue: mustMarshalAudit(t, oldBal), OldExists: true, HasOldValue: true, OldValueAuthoritative: true,
	})

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("unbalanced parallel audit must still panic")
		}
	}()
	AuditGlobalConservationDiff(diff, 21, nil)
}

func TestGlobalDiffParallelReadFallbacksAreConcurrent(t *testing.T) {
	prevProcs := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(prevProcs)

	var inFlight atomic.Int64
	var maxInFlight atomic.Int64
	readFn := func(string) ([]byte, error) {
		cur := inFlight.Add(1)
		for {
			max := maxInFlight.Load()
			if cur <= max || maxInFlight.CompareAndSwap(max, cur) {
				break
			}
		}
		time.Sleep(200 * time.Microsecond)
		inFlight.Add(-1)
		return nil, nil
	}
	stats := AuditGlobalConservationDiffWithStats(auditZeroBalanceOps(t, 4096, "USDT"), 22, readFn)
	if stats.OldValueFallbacks != 4096 {
		t.Fatalf("old value fallbacks = %d, want 4096", stats.OldValueFallbacks)
	}
	if maxInFlight.Load() < 2 {
		t.Fatalf("read fallback did not run concurrently, max in-flight=%d", maxInFlight.Load())
	}
}

func TestGlobalDiffSkipsOldReadsForIrrelevantOps(t *testing.T) {
	var reads atomic.Int64
	readFn := func(string) ([]byte, error) {
		reads.Add(1)
		return nil, nil
	}
	stats := AuditGlobalConservationDiffWithStats(auditNoiseOps(4096), 23, readFn)
	if reads.Load() != 0 {
		t.Fatalf("unexpected old value reads for irrelevant keys: %d", reads.Load())
	}
	if stats.OtherOps != 4096 {
		t.Fatalf("other ops = %d, want 4096", stats.OtherOps)
	}
	if stats.OldValueFallbacks != 0 || stats.OldValueReadOps != 0 {
		t.Fatalf("irrelevant ops should not read old values: %+v", stats)
	}
}

func auditNoiseOps(n int) []WriteOp {
	ops := make([]WriteOp, n)
	for i := range ops {
		ops[i] = WriteOp{Key: "__audit_noise_" + strconv.Itoa(i), Value: []byte("x")}
	}
	return ops
}

func auditZeroBalanceOps(t *testing.T, n int, token string) []WriteOp {
	t.Helper()
	ops := make([]WriteOp, n)
	for i := range ops {
		owner := "audit_owner_" + strconv.Itoa(i)
		record := &pb.TokenBalanceRecord{Address: owner, Token: token, Balance: &pb.TokenBalance{Balance: "0"}}
		ops[i] = WriteOp{Key: keys.KeyBalance(owner, token), Value: mustMarshalAudit(t, record)}
	}
	return ops
}

func mustMarshalAudit(t *testing.T, msg proto.Message) []byte {
	t.Helper()
	data, err := proto.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
